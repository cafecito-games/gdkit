# GDScript code generation

A fifth gdkit tool, `gdkit gen`, that writes boilerplate value-object methods
into a class the project has opted in: `_to_string`, `equals`, and
`deep_equals`. It reports only on classes that opted in and on the types those
classes require, and it never rewrites anything outside the region it owns.

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
| `# gdkit:generate:ignore-field` | on, or directly above, a `var` | excludes that field |

Precedence is `ignore` > directive > config, which is lint's `disable` >
`enable` rule applied to the same problem for the same reason: an opt-out that
can be overridden by configuration is not an opt-out.

The class-level and field-level opt-outs are spelled **differently on purpose**.
One spelling for both is ambiguous and no precedence rule can recover the
author's intent, because a standalone comment directly above the first `var` of
a class satisfies the definition of both at once:

```gdscript
# gdkit:generate = equals

# gdkit:generate:ignore     <- the class? or just q?
var q: int
```

Resolving that by position — "class-level unless a `var` follows" — is
deterministic but surprising, and it makes inserting a field above another
field silently change the meaning of a comment nobody touched. Distinct words
cost four characters and remove the question.

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
followed by `=`, so none of the three `generate` forms matches any of its
patterns. In particular `lint`'s `unknown-ignore`, which reports only names
found *inside* a recognised list, passes over them silently. A test pins this,
because a future change to that regex could quietly turn every marker in every
adopting project into an `unknown-ignore` finding.

### The region

Generated methods live between sentinels:

```gdscript
# gdkit:generated:begin
func _to_string() -> String:
	return "Hex(q=%s, r=%s)" % [self.q, self.r]
# gdkit:generated:end
```

The region stays exactly where it already is in the file. `gen check` does not
care about its position, so a placement the author chose is never churned. When
no region exists, it is inserted at the end of the enclosing class body. A file
holds at most one region per class; a second is a `generate.marker` diagnostic.

**The region owns the blank lines on both sides of itself**, up to and including
the newline that ends the preceding statement and the one that begins the
following statement. Without that, the region cannot satisfy the project's
`blank_lines.top_level` setting, because spacing around a function depends on
what sits next to it — and that spacing would then be the one thing neither
`gen` nor `format` owned. Those bytes are part of the region for every purpose
in this document: they are emitted, compared, and verified as region content.

The sentinels carry no checksum. A region whose content is not what the
generator would emit is simply out of date — `gen check` reports it,
`gen check --diff` shows it, `gen write` rewrites it — exactly as `format`
treats a file that is not canonical. Git is the safety net, and `gen write` is
documented the way `format write` is: run it on a clean tree. A checksummed
region that refused to be clobbered was considered and rejected: it puts a hash
comment in every generated region, churning the diff on metadata, to protect
against editing code whose own first line says it is generated.

#### Orphaned regions

A region whose class no longer opts in — it gained an `ignore`, stopped matching
configuration, or dropped its last generator — is **retained**, and reported as
`generate.orphaned`. It is removed only by `gen write --prune`.

Neither alternative is acceptable as a default. Silently keeping it leaves
methods in the file that the class has explicitly opted out of, with nothing
saying so. Silently deleting it makes `gen write` destroy code as a side effect
of an unrelated edit to a glob, which is beyond what "the tool owns this region"
licenses. Retaining it with a diagnostic says what is true and leaves the
destructive step to a flag the author typed on purpose.

## Pipeline

`generate` is a whole-project analysis, closer to `architecture` than to `lint`.
This is not a stylistic choice. To emit `origin.deep_equals(p_other.origin)` for
`var origin: Coordinate`, the generator must know that `Coordinate` is a project
class rather than an engine type, and that `Coordinate` will have a
`deep_equals` — without the second fact the emitted call is a runtime crash.

### Discovery is split in two, and the split is the load-bearing part

The semantic index must span the **whole project**, while generation targets are
**filtered**. Conflating the two is unsound, and `architecture` already carries
the comment explaining why (`architecture/analyzer.go:61`): a file hidden from
the walk takes its `class_name` out of the index, and references to it then
produce false results. `project.Load` really does drop ignored and excluded
scripts from the snapshot, so for `generate` the failure would be worse than a
false positive — absence from the index is indistinguishable from "this type is
a primitive or engine-owned", and the generator would emit `q != p_other.q` for
a field whose type is a project class with value semantics.

So:

- **The index** is built from a `project.Load` with `Exclude` empty and
  `HonorIgnoreFile` false, rooted at the project root. It sees everything.
- **The targets** are the subset of that snapshot passing `source_roots`,
  `exclude`, and `.gdkitignore`. Only a target can opt in, and only a target can
  carry a diagnostic.

`generate` applies those three filters itself, with `internal/glob` and
`internal/ignore`, rather than taking a second filtered `project.Load` — one
load, two views, one parse per file. The cost is that a short amount of
`project`'s own filtering logic is restated in `generate/target.go`; the
alternative is parsing the project twice, which is worse, or a new
`project.Config` mode, which is more invasive than this tool justifies.

A declared type that resolves to nothing even in the unfiltered index is a
`generate.unsupported` diagnostic on the class that referred to it. It is never
assumed to be an engine type.

### Order

`Check()` runs a fixed sequence, and everything downstream depends on the
ordering:

1. **Load configuration** — `.gdkit/generate.json`, and also `format`'s
   configuration, because step 7 needs the project's `indent`, `line_width`,
   `quote_style`, and `blank_lines`.
2. **Load and partition** — the unfiltered snapshot, then the target subset.
3. **Resolve opt-in** — configuration globs, then the in-file directive, then
   `ignore`, over targets only. Yields the requested set of
   (script, class, generators).
4. **Index** — over the **whole** snapshot: `class_name` to script, the
   inheritance edge each class declares, and each class's declared methods with
   their staticness and arity.
5. **Plan methods** — the method set each class *will* have: what it declares,
   union what step 3 requested for it.
6. **Select fields** — per requested class, in declaration order, with the
   per-field opt-out applied.
7. **Emit and canonicalise** — each requested generator produces its method
   text; `deep_equals` consults step 5. The region, including the blank lines it
   owns, is formatted in isolation with the project's `format` configuration and
   spliced into the file.
8. **Verify** — the candidate must reparse, and every byte outside the region
   must be identical to the source.
9. **Sort** — by path, then line, column, rule.

**Step 5 must precede step 7, and that ordering is what makes a fresh adoption
work at all.** If `A` holds a `B` and both classes newly opt into `deep_equals`,
then at step 4 neither declares one yet. Consulting step 4 would refuse `A`,
refuse `B` for the same reason if it holds an `A`, and leave a pair of mutually
referencing classes that can never be generated for a first time. Consulting the
*planned* set resolves both.

No iteration is required to reach that set, and it is worth being precise about
why: opting a class in never opts another class in, so the planned set is a
single union of declared and requested methods and a second pass could not add
to it. A cycle between two requested classes is therefore fine.

Step 4 records staticness and arity because a method *name* is not enough. A
`static func equals(a, b)` and a `func equals(p_other: Variant) -> bool` are
both "a class declaring `equals`"; only the second can be the target of
`origin.equals(…)`. A declared method that does not match the shape a generator
would emit does not satisfy the plan, and if the class is a requested one it is
a `generate.conflict`.

### Formatting and the full-file invariant

Step 7 formats the region alone rather than the spliced file, and the
distinction is load-bearing. Formatting the whole file would let gdparser
reformat code *outside* the region whenever the file was not already canonical,
which breaks step 8 and silently turns `gen write` into a partial `format write`.
Formatting the region alone is only sound because the region sits at indent 0,
which is the honest reason v1 is limited to top-level classes: an inner class's
region would need re-indentation after formatting, and that is a separate
problem.

Isolated formatting cannot *by itself* establish that a canonical file stays
canonical, because gdparser's blank-line handling depends on neighbouring
statements, which an isolated snippet cannot see. That gap is closed by the
region owning its boundary whitespace: the emitted region carries exactly
`blank_lines.top_level` blank lines before its first function and after its
last, so the spacing that depends on context is produced deliberately rather
than inherited from a formatter run that lacked the context.

Because that is an argument and not a proof, it is also checked. A test runs
the project's full-file formatter over every generated fixture as a **read-only
oracle** and asserts it reports no change. The claim being tested is the precise
one: `gen write` introduces no new `format check` finding. It does not make a
file canonical, and a file that was not canonical stays exactly as uncanonical
as it was.

### Verification

`format`'s safety net cannot be reused. Its whole value is the promise that a
rewrite changes nothing but layout, enforced by comparing the token stream — and
a generator changes the token stream by definition. Extending `format.unsafe` to
carve out an exception would weaken the one invariant that makes `format write`
trustworthy.

The replacement is tighter and cheaper. Given source `S` with its region at
`[a, b)` and candidate `C` with its region at `[a', b')`, where both spans
include the blank lines the region owns:

- `S[:a]` equals `C[:a']`, and `S[b:]` equals `C[b':]`;
- `C` reparses;
- the region in `C` is byte-for-byte what step 7 produced.

A candidate that fails any of these is refused with `generate.unsafe` and the
file is left untouched, the way `format` refuses rather than rewriting badly.

## The plan model

`Check` does not return "findings" that `write` then has to reverse-engineer.
It returns a plan, and the two commands read it differently. Without this,
`generate.stale` being an error is incoherent: a stale region is both the thing
`check` must fail on and the thing `write` exists to fix.

```go
// Candidate is a file generate can rewrite, and the contents to write.
type Candidate struct {
	Path     string
	Class    string
	Contents []byte // the whole file, region spliced
	Region   Span   // in Contents, including owned blank lines
	Changed  bool   // Contents differs from the source
}

// Plan is the outcome of a run. A class with a blocking diagnostic
// contributes no candidate.
type Plan struct {
	Candidates  []Candidate
	Diagnostics []Diagnostic
}
```

- **`gen check`** reports every `Changed` candidate as `generate.stale`, plus
  every diagnostic. Exit 1 if either is non-empty at error severity.
- **`gen write`** applies every `Changed` candidate and reports what it wrote.
  Staleness is not a failure for `write` — fixing it is the point. It exits 1
  only when a blocking diagnostic remains, or 2 on an I/O failure, and it prints
  the files it changed on stdout either way.

A blocking diagnostic suppresses only its own class's candidate. One unparseable
or conflicted class does not stop the rest of the project from being generated.

## Package shape

A new top-level `generate/` package and a `gdkit gen` subcommand, mirroring
`format` and `uid`: a pure `Check`, a single `Apply` writer, its own
configuration, its own diagnostic names, and `cmd/gdkit` holding only flags,
output, and exit codes.

```
generate/
  config.go      Config, DefaultConfig, LoadConfig, Validate
  target.go      source_roots, exclude, and .gdkitignore over the snapshot
  marker.go      the directive grammar and the region sentinels
  fields.go      member var selection
  index.go       class_name, inheritance, and declared method shapes
  plan.go        Candidate, Plan, and the planned-method set
  generator.go   the registry and the Generator interface
  emit_*.go      one file per generator
  check.go       Check(snapshot) Plan — pure, no I/O
  verify.go      the outside-the-region invariant
  apply.go       Apply(snapshot, plan) ([]string, error) — the only writer
  model.go       Diagnostic, Report, rule name constants
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
	// Signatures are the methods it emits, with the shapes that satisfy the
	// plan and that a declaration elsewhere would conflict with.
	Signatures() []Signature
	// Emit returns the method text, unformatted.
	Emit(class Class, index *Index, planned MethodSet) (string, []Diagnostic)
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
reading one before `_ready` is a null dereference. A field carrying
`# gdkit:generate:ignore-field` is excluded.

A property with accessors is included by name. The generated code reads
`self.q`, which runs the getter, as hand-written code would.

### Identifiers in emitted code

Every field reference is written `self.<name>`, and every generated local is
prefixed `__gdkit_`. Both are collision avoidance, and neither is optional:

- a field named `p_other` is shadowed by the parameter, so an unqualified
  `p_other` in the body would read the argument rather than the field;
- a field named `i` collides with a loop variable.

`self.p_other` and `p_other.p_other` are unambiguous, so the parameter keeps the
name `p_other` that the public signature wants. The repository already resolves
shadowing before drawing conclusions about an identifier
(`architecture/collectShadows`); this is the same hazard faced from the emitting
side.

### `_to_string`

```gdscript
func _to_string() -> String:
	return "Hex(q=%s, r=%s)" % [self.q, self.r]
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
	return self.q == p_other.q and self.r == p_other.r
```

Field-wise `==` behind a script-identity guard. `is <ClassName>` was rejected on
two counts. It cannot name a class that declares no `class_name`, nor an inner
class, so those classes could not opt in at all; and it makes equality
asymmetric across a subclass — `Hex.new().equals(SubHex.new())` is true while
the reverse is false. Script identity is symmetric, which is the property a
value object needs.

The long `and` chain is emitted on one line and wrapped by step 7 to the
project's `line_width`.

#### Inheritance is refused, not approximated

Script identity makes equality symmetric but it does **not** make an inherited
`equals` correct, and the difference matters. Two `SubHex` instances both answer
the same `get_script()`, so a base `equals` inherited by `SubHex` passes its own
guard and then compares only the fields `Hex` declared. Since this spec
generates from declared fields only, `SubHex`'s own state is ignored and
equality wrongly returns true.

A guard cannot fix this: the method has no static handle on "the script I was
generated into" without a self-referential `preload`. What *can* fix it is the
index. Step 4 records inheritance edges across the whole project, so:

> A requested class that is extended by a project class which declares at least
> one selectable field of its own, and which neither declares nor plans an
> override of the generated method, is refused with `generate.unsupported`. The
> message names the subclass.

The diagnostic lands on the requested base class, not on the subclass, because
the base is the class whose generated method would be unsound and the subclass
may not be a target at all. Opting the subclass in clears it: the subclass then
generates its own override. A subclass that adds no selectable fields is
harmless and is not reported.

### `deep_equals`

`equals` is enough for every field except an object-valued one. Godot 4 compares
`Array` and `Dictionary` **by value**, not by reference, so a field-wise `==`
already handles containers of primitives correctly. Objects still compare by
reference, which means a plain `==` on `var origin: Coordinate` silently
compares identity — the exact bug a value object exists to prevent. That is
`deep_equals`'s entire job. gdkit is Godot 4 only, so this semantic holds for
every version `godot_version` can name and nothing here is version-gated.

Dispatch per field, decided from `ast.VariableDeclaration.Type` against the
step-4 index and the step-5 plan:

| Declared type | Emitted | Diagnostic |
| --- | --- | --- |
| primitive or engine value type | `self.q != p_other.q` | — |
| project class that plans `deep_equals` | null-aware recursion | — |
| project class that does not plan `deep_equals` | nothing; the class is refused | `generate.unsupported` |
| `Array[T]`, `T` a project class planning `deep_equals` | size check, then null-aware element-wise recursion | — |
| `Dictionary`, or an untyped container | `==` | `generate.untyped` |
| a type that resolves to nothing in the index | nothing; the class is refused | `generate.unsupported` |
| absent, `Variant`, or `:=` inferred | runtime `has_method("deep_equals")` fallback | `generate.untyped` |

An object-valued field can be `null`, and `null.deep_equals(…)` is a runtime
error, so every recursion is null-aware:

```gdscript
func deep_equals(p_other: Variant) -> bool:
	if not (p_other is Object):
		return false
	if p_other.get_script() != get_script():
		return false
	if self.q != p_other.q:
		return false
	if (self.origin == null) != (p_other.origin == null):
		return false
	if self.origin != null and not self.origin.deep_equals(p_other.origin):
		return false
	if self.tags.size() != p_other.tags.size():
		return false
	for __gdkit_index in self.tags.size():
		var __gdkit_left: Variant = self.tags[__gdkit_index]
		var __gdkit_right: Variant = p_other.tags[__gdkit_index]
		if (__gdkit_left == null) != (__gdkit_right == null):
			return false
		if __gdkit_left != null and not __gdkit_left.deep_equals(__gdkit_right):
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
| `source-parse` | a **target** file that could not be parsed | error |
| `class_name.duplicate` | two scripts claiming one `class_name`, where that name is one a requested class needs to resolve | error |
| `generate.stale` | a region that is missing or out of date (`check` only) | error |
| `generate.marker` | a malformed directive, an unknown generator name, or a second region in one class | error |
| `generate.conflict` | the class declares, outside the region, a method a requested generator emits, in a shape that cannot stand in for it | error |
| `generate.unsupported` | a class the generator refuses: an inner class, an unresolvable field type, a field type that does not plan the method, or an extending class that would inherit an unsound method | error |
| `generate.orphaned` | a region whose class no longer opts in | error |
| `generate.unsafe` | verification refused the splice | error |
| `generate.untyped` | `deep_equals` fell back to a runtime dispatch | warning |

Diagnostics are scoped to targets and to the types requested classes require, so
a parse failure or a duplicate `class_name` in an unrelated corner of the project
is not reported. The consequence is deliberate: an unparseable non-target that
*would* have declared a type a requested class needs surfaces as
`generate.unsupported` on the requested class, naming the type it could not
resolve. That is the honest signal — the generator cannot see the type, and
which file was supposed to declare it is a guess.

`generate.conflict` matters more than its size suggests: a hand-written
`_to_string` outside the region plus a generated one inside is a duplicate
function declaration, which is a Godot compile error. The step-4 index detects
it, and matching on signature rather than name is what keeps it from firing on a
`static func equals(a, b)` that is not the same method at all.

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
  "exclude": [".git/**", ".godot/**", ".gdkit/**", "addons/**"],
  "generate": []
}
```

The `exclude` default matches `format.DefaultConfig()` exactly rather than being
empty. An empty default would make `addons/**` a generation target, so a glob
someone wrote for their own code could start rewriting a third-party addon.

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

**A file matching several entries gets the union of their generators.** Union
rather than first-match, because that is the grain of the codebase:
`architecture`'s `dependencies` are alternative allow rules, where capability is
added by adding a rule. Union is also order-independent, so reordering the list
cannot change the output.

`source_roots`, `exclude`, and `.gdkitignore` select **targets only**. None of
them narrows the semantic index, for the reason given under Discovery.

Unknown keys are rejected at every depth, naming the offending key's full JSON
path, as `architecture` does. `minimum_gdkit_version` is supported and checked
before the unknown-key walk, for the reason recorded in the repository's
`CLAUDE.md`: a configuration written for a newer gdkit carries both the floor
and the syntax that needed it.

Globs use `internal/glob`, the engine the other tools share.

## Command surface

```sh
gdkit gen check [--config path] [--format text|json] [--diff] [project-root]
gdkit gen write [--config path] [--format text|json] [--prune] [project-root]
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

Four invariants carry the most weight and each gets a test over the full fixture
corpus:

- `gen write` is idempotent — a second run changes nothing;
- the project's full-file formatter, run as a read-only oracle over every
  generated fixture, reports no change;
- `gen write` followed by `gen check` is clean;
- a file whose region is removed and regenerated returns to the same bytes.

Cases that exist because they were wrong in an earlier draft of this design, and
each of which gets a named test:

- two classes that hold each other and both newly opt into `deep_equals`, which
  must generate rather than deadlock;
- a field named `p_other`, and an `Array` field named `i`;
- a field typed as a class that `.gdkitignore` hides, which must still resolve;
- a `null` object field and a typed array holding a `null`;
- a base class with a field-adding subclass that does not opt in, which must be
  refused;
- a `static func equals(a, b)`, which must not satisfy the plan and must not be
  reported as a conflict in the wrong class;
- a class that stops opting in while its region remains.

A test pins that `lint` reports nothing on a file carrying any of the three
marker forms, so a future change to `internal/suppression`'s regex cannot turn
every marker in every adopting project into an `unknown-ignore` finding.

## Explicitly out of scope

- **Inner classes.** A marker on an inner `class` is `generate.unsupported`.
  Its region would need re-indentation after isolated formatting.
- **Inherited fields.** A class generates from the fields it declares. Reaching
  into a base class across files is resolvable from the index but is a separate
  design; until it exists, the inheritance refusal above keeps the gap from
  producing wrong code.
- **A `hash` generator.** The obvious fourth member of this family, and the one
  with real subtlety: Godot's `hash()` on an object is identity-based, so a
  value-object hash has to be composed by hand and must agree with `equals` or
  a `Dictionary` keyed on these objects breaks. It deserves its own spec.
- **Rewriting a `_to_string` the project hand-wrote.** `generate.conflict`
  reports it; nothing migrates it.
- **A `_to_string` format option.** One documented format.
