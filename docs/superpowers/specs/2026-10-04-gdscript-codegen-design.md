# GDScript code generation

A fifth gdkit tool, `gdkit gen`, that writes boilerplate value-object methods
into a class the project has opted in: `_to_string`, `equals`, and
`deep_equals`. Generation only: it never reports on code it was not asked to
generate, and it never rewrites anything outside the region it owns.

GDScript has no partial classes, no mixins, and no user-defined annotations — an
`@gdkit_value` is a compile error in Godot, not an extension point. Generated
methods therefore have to land inside the user's own script. That single language
fact is what makes this tool shaped like `format` (a verified in-place rewriter)
rather than like a conventional out-of-band generator, and it decides most of
what follows.

## The marker

A class opts in through a standalone comment directive in its body, with path
globs in configuration as a second route and a per-file override that beats
both.

| Form | Scope | Meaning |
| --- | --- | --- |
| a `generate` entry in `.gdkit/generate.json` | path glob | opts matching files in, with that entry's generator list |
| `# gdkit:generate = to_string, equals` | class body, standalone | opts this class in; replaces any config-supplied list |
| `# gdkit:generate:ignore` | class body, standalone | opts this class out |
| `# gdkit:generate:ignore` | on, or directly above, a `var` | excludes that field |

Precedence is `ignore` > directive > config, which is lint's `disable` >
`enable` rule applied to the same problem for the same reason: an opt-out that
can be overridden by configuration is not an opt-out.

A comment directive rather than configuration alone, because the marker is then
visible in the script a reader is already looking at, travels with the file when
it moves, and appears in the diff that introduces it. Configuration alone was
rejected on that last point — a reader of `Coordinate.gd` would have no way to
know its methods are generated until they reached the sentinel. A base class
(`extends GdkitValue`) was rejected because it spends GDScript's single
inheritance slot, forces an addon into the project, and cannot say *which*
helpers the class wants.

### Grammar

The directive reuses the shape of the three suppression directives in
`internal/suppression` — `#`, `gdkit` or `gdlint`, `:`, the directive word, `=`,
a comma-separated list — but it is parsed by `generate/marker.go`, not by that
package, and it is strict where that package is lax.

`internal/suppression` lets a rule list run to the end of the line because
gdlint does, which is why a trailing remark silently breaks `# gdkit:ignore`.
A gdkit-original directive owes gdlint nothing, so each item in a `generate`
list must be a known generator name and anything else is a `generate.marker`
diagnostic naming it. A trailing remark fails loudly rather than quietly doing
nothing.

No change to `internal/suppression` is needed, and none is wanted.
`directivePattern` requires the literal word `ignore`, `disable`, or `enable`
followed by `=`, so neither `# gdkit:generate = …` nor
`# gdkit:generate:ignore` matches any of the three patterns. In particular
`lint`'s `unknown-ignore`, which reports only names found *inside* a recognised
list, passes over both silently. A test pins this, because a future change to
that regex could quietly turn every marker in every adopting project into an
`unknown-ignore` finding.

### The region

Generated methods live between sentinels:

```gdscript
# gdkit:generated:begin
func _to_string() -> String:
	return "Hex(q=%s, r=%s)" % [q, r]
# gdkit:generated:end
```

The region stays exactly where it already is in the file. `gen check` does not
care about its position, so a placement the author chose is never churned. When
no region exists, it is inserted at the end of the enclosing class body. A file
holds at most one region per class; a second is a `generate.marker` diagnostic.

The sentinels carry no checksum. A region whose content is not what the
generator would emit is simply out of date — `gen check` reports it,
`gen check --diff` shows it, `gen write` overwrites it — exactly as `format`
treats a file that is not canonical. Git is the safety net, and `gen write` is
documented the way `format write` is: run it on a clean tree. A checksummed
region that refused to be clobbered was considered and rejected: it puts a hash
comment in every generated region, churning the diff on metadata, to protect
against editing code whose own first line says it is generated.

## Pipeline

`generate` is a whole-project analysis, closer to `architecture` than to `lint`.
This is not a stylistic choice. To emit `origin.deep_equals(p_other.origin)` for
`var origin: Coordinate`, the generator must know that `Coordinate` is a project
class rather than an engine type, and that `Coordinate` actually declares a
`deep_equals` — without the second fact the emitted call is a runtime crash.
Both are answerable statically from the snapshot, because a hand-written
`deep_equals` and a generated one are equally visible in the AST.

`Check()` runs a fixed sequence, and everything downstream depends on the
ordering:

1. **Load configuration** — `.gdkit/generate.json`, and also `format`'s
   configuration, because step 6 needs the project's `indent`, `line_width`,
   `quote_style`, and `blank_lines`.
2. **Index** — one pass over every script, recording `class_name` to path and
   the set of methods each class declares. A duplicate `class_name` is a
   `class_name.duplicate` diagnostic here, as in `architecture`.
3. **Resolve opt-in** — configuration globs, then the in-file directive, then
   `ignore`. Yields the set of (script, class, generators).
4. **Select fields** — per class, in declaration order, with the per-field
   opt-out applied.
5. **Emit** — each requested generator produces its method text. `deep_equals`
   consults the index from step 2.
6. **Canonicalise** — format the emitted region **in isolation**, as a
   standalone GDScript source, with the project's `format` configuration, then
   splice the result into the file.
7. **Verify** — the candidate must reparse, and every byte outside the region
   must be identical to the source.
8. **Sort** — `Report.sort()`, by path, then line, column, rule.

Step 6 formats the region alone rather than the spliced file, and the
distinction is load-bearing. Formatting the whole file would let gdparser
reformat code *outside* the region whenever the file was not already canonical,
which breaks the invariant in step 7 and silently turns `gen write` into a
partial `format write`. Formatting the region alone is only sound because the
region sits at indent 0, which is the honest reason v1 is limited to top-level
classes: an inner class's region would need re-indentation after formatting, and
that is a separate problem.

The consequence to document, and the one to state carefully: `gen write` does
**not** make a file canonical, and it does not introduce any new `format check`
finding either. A file that was already format-clean stays clean. A file that
was not stays exactly as unclean as it was.

### Verification

`format`'s safety net cannot be reused. Its whole value is the promise that a
rewrite changes nothing but layout, enforced by comparing the token stream — and
a generator changes the token stream by definition. Extending `format.unsafe` to
carve out an exception would weaken the one invariant that makes `format write`
trustworthy.

The replacement is tighter and cheaper. Given source `S` with its region at
`[a, b)` and candidate `C` with its region at `[a', b')`:

- `S[:a]` equals `C[:a']`, and `S[b:]` equals `C[b':]`;
- `C` reparses;
- the region in `C` is byte-for-byte what step 6 produced.

A candidate that fails any of these is refused with `generate.unsafe` and the
file is left untouched, the way `format` refuses rather than rewriting badly.

## Package shape

A new top-level `generate/` package and a `gdkit gen` subcommand, mirroring
`format` and `uid`: a pure `Check`, a single `Apply` writer, its own
configuration, its own diagnostic names, and `cmd/gdkit` holding only flags,
output, and exit codes. `project.Config.HonorIgnoreFile` is set, so `generate`
shares the root `.gdkitignore` with `lint`, `format`, and `uid`.

```
generate/
  config.go      Config, DefaultConfig, LoadConfig, Validate
  marker.go      the directive grammar and the region sentinels
  fields.go      member var selection
  index.go       class_name to script, and each class's declared methods
  generator.go   the registry and the Generator interface
  emit_*.go      one file per generator
  check.go       Check(snapshot) Report — pure, no I/O
  verify.go      the outside-the-region invariant
  apply.go       Apply(snapshot, report) ([]string, error) — the only writer
  model.go       Result, Diagnostic, Report, rule name constants
```

A generation stage inside `format` was rejected for the verification reason
above. A `lint` rule reporting staleness alongside a separate writer was also
rejected: it splits one concept across two packages, and `lint` would need the
whole field model and emitter in order to know what the correct text is — all of
the work, in the package that is not allowed to write.

### The registry

```go
type Generator interface {
	// Name is the directive and configuration spelling: "to_string".
	Name() string
	// Methods are the GDScript methods it emits, for conflict detection.
	Methods() []string
	// Emit returns the method text, unformatted.
	Emit(class Class, index *Index) (string, []Diagnostic)
}
```

Generators emit in a fixed registry order, not in the order the directive lists
them, so the region's content depends only on the class and not on how the
marker was written. Adding a future generator is a new `emit_*.go` and a
registry entry.

## What is generated

### Field selection

Every member `var` the class declares, in declaration order. Never a `const`
(GDScript types it from its value and it is not instance state), never a
`static var`, and never an `@onready var` — that is node wiring, not state, and
reading one before `_ready` is a null dereference. A field carrying the
per-field opt-out is excluded.

A property with accessors is included by name. The generated code reads `q`,
which runs the getter, as hand-written code would.

### `_to_string`

```gdscript
func _to_string() -> String:
	return "Hex(q=%s, r=%s)" % [q, r]
```

The name is the `class_name` when the script declares one, otherwise the file's
base name without `.gd`. A class with no selected fields emits `"Hex()"`.

There is no configuration for this format. A project that wants a different one
hand-writes `_to_string` and does not opt into the `to_string` generator, which
is a better outcome than a style option nobody can change later without
rewriting every region in every adopting project.

### `equals`

```gdscript
func equals(p_other: Variant) -> bool:
	if not (p_other is Object):
		return false
	if p_other.get_script() != get_script():
		return false
	return q == p_other.q and r == p_other.r
```

Field-wise `==` behind a script-identity guard. `is <ClassName>` was rejected on
two counts. It cannot name a class that declares no `class_name`, nor an inner
class, so those classes could not opt in at all; and it makes equality
asymmetric across a subclass — `Hex.new().equals(SubHex.new())` is true while
the reverse is false. Script identity is symmetric, which is the property a
value object needs.

The long `and` chain is emitted on one line and wrapped by step 6 to the
project's `line_width`.

### `deep_equals`

`equals` is enough for every field except an object-valued one. Godot 4 compares
`Array` and `Dictionary` **by value**, not by reference, so a field-wise `==`
already handles containers of primitives correctly. Objects still compare by
reference, which means a plain `==` on `var origin: Coordinate` silently
compares identity — the exact bug a value object exists to prevent. That is
`deep_equals`'s entire job. gdkit is Godot 4 only, so this semantic holds for
every version `godot_version` can name and nothing here is version-gated.

Dispatch per field, decided from `ast.VariableDeclaration.Type` against the
step-2 index:

| Declared type | Emitted | Diagnostic |
| --- | --- | --- |
| primitive or engine value type | `q != p_other.q` | — |
| project class declaring `deep_equals` | `not origin.deep_equals(p_other.origin)` | — |
| project class with no `deep_equals` | nothing; the class is refused | `generate.unsupported` |
| `Array[T]`, `T` a project class | size check, then element-wise recursion | — |
| `Dictionary`, or an untyped container | `==` | `generate.untyped` |
| absent, `Variant`, or `:=` inferred | runtime `has_method("deep_equals")` fallback | `generate.untyped` |

```gdscript
func deep_equals(p_other: Variant) -> bool:
	if not (p_other is Object):
		return false
	if p_other.get_script() != get_script():
		return false
	if q != p_other.q:
		return false
	if not origin.deep_equals(p_other.origin):
		return false
	if tags.size() != p_other.tags.size():
		return false
	for i in tags.size():
		if not tags[i].deep_equals(p_other.tags[i]):
			return false
	return true
```

`deep_equals` emits one `if` per field rather than an `and` chain, because the
comparison differs per field and a chain of mixed call and operator forms is
unreadable at any width.

The last row of the table is the one to expect in practice. `var origin :=
Coordinate.new()` is statically typed as far as Godot is concerned, but
`ast.VariableDeclaration.Type` is empty and the node carries only
`Inferred: true` — the inferred type is not in the tree. So the ergonomic `:=`,
which is a natural way to write exactly these classes, lands in the runtime
fallback.

## Diagnostics

Rule names are a public contract, as they are in every other gdkit tool: they
appear in JSON output and in configuration. Two names are reused rather than
coined: `source-parse`, as `lint` and `format` spell it — note that
`architecture` spells its own `source.parse`, and `generate` belongs to the
former family — and `class_name.duplicate`, from `architecture`.

| Rule | Reports | Severity |
| --- | --- | --- |
| `source-parse` | a file that could not be parsed | error |
| `class_name.duplicate` | two scripts declaring one `class_name` | error |
| `generate.stale` | a region that is missing or out of date | error |
| `generate.marker` | a malformed directive, an unknown generator name, or a second region in one class | error |
| `generate.conflict` | the class already declares, outside the region, a method a requested generator emits | error |
| `generate.unsupported` | a class the generator refuses: an inner class, or a field whose type it cannot compare | error |
| `generate.unsafe` | verification refused the splice | error |
| `generate.untyped` | `deep_equals` fell back to a runtime dispatch | warning |

`generate.conflict` matters more than its size suggests: a hand-written
`_to_string` outside the region plus a generated one inside is a duplicate
function declaration, which is a Godot compile error. The step-2 index detects
it for free.

`generate.untyped` is the only warning, so `Diagnostic` carries a severity and
`Report` grows a `HasErrors()` alongside `HasDiagnostics()`: a warning prints
but does not take the run to exit 1. `format` has no severity and `lint` has a
configurable one; this is neither. It is hardcoded, because a project able to
downgrade `generate.unsupported` to a warning would be asking for code that does
not compile.

The simpler alternative, considered and not taken: drop the diagnostic entirely
and emit a comment naming the untyped field *inside* the region, where the
reader who needs it is already looking. That needs no severity concept at all.
It was rejected because a fallback is something CI should be able to see, and a
comment inside a generated region is invisible to `--format json`.

## Configuration

`.gdkit/generate.json`, written by `gdkit gen init`:

```json
{
  "version": 1,
  "source_roots": ["."],
  "exclude": [],
  "generate": []
}
```

A `generate` entry names path globs and the generators they opt in:

```json
{
  "generate": [
    {
      "paths": ["**/domain/value/*.gd"],
      "generators": ["to_string", "equals", "deep_equals"]
    }
  ]
}
```

Unknown keys are rejected at every depth, naming the offending key's full JSON
path, as `architecture` does. `minimum_gdkit_version` is supported and checked
before the unknown-key walk, for the reason recorded in the repository's
`CLAUDE.md`: a configuration written for a newer gdkit carries both the floor
and the syntax that needed it.

Globs use `internal/glob`, the engine the other tools share.

## Command surface

```sh
gdkit gen check [--config path] [--format text|json] [--diff] [project-root]
gdkit gen write [--config path] [--format text|json] [project-root]
gdkit gen init  [--force] [project-root]
```

Bare `gdkit gen` runs `check`. Both `check` and `write` take
`--minimum-version`. `--diff` cannot be combined with `--format json`, as in
`format check`.

Exit `0` clean, `1` findings, `2` configuration, usage, or I/O failure. Under
`--format json` an exit-2 failure is an envelope on stderr with stdout empty.
`gen write` prints the files it changed on stdout even when a write fails
part-way, because that list is what tells the caller the state the project is
in.

The subcommand is `gen` rather than `generate`, following `arch`.

## Testing

Tests build throwaway Godot projects with `writeProject(t, t.TempDir(), …)` and
assert on diagnostic rule names, per the repository conventions.

One deliberate departure: these tests must also assert on emitted text, because
the text is the unit under test. Golden fixtures per generator, one directory
per case.

Three invariants carry the most weight and each gets a test over the full
fixture corpus:

- `gen write` is idempotent — a second run changes nothing;
- `gen write` introduces no new `format check` finding;
- `gen write` followed by `gen check` is clean.

A test pins that `lint` reports nothing on a file carrying a marker, so a future
change to `internal/suppression`'s regex cannot turn every marker in every
adopting project into an `unknown-ignore` finding.

## Explicitly out of scope

- **Inner classes.** A marker on an inner `class` is `generate.unsupported`.
  Its region would need re-indentation after isolated formatting.
- **Inherited fields.** A class generates from the fields it declares. Reaching
  into a base class across files is resolvable from the snapshot but is a
  separate design.
- **A `hash` generator.** The obvious fourth member of this family, and the one
  with real subtlety: Godot's `hash()` on an object is identity-based, so a
  value-object hash has to be composed by hand and must agree with `equals` or
  a `Dictionary` keyed on these objects breaks. It deserves its own spec.
- **Rewriting a `_to_string` the project hand-wrote.** `generate.conflict`
  reports it; nothing migrates it.
- **A `_to_string` format option.** One documented format.
