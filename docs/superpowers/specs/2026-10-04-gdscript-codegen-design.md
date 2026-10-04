# GDScript code generation

A fifth gdkit tool, `gdkit gen`, that writes boilerplate value-object methods
into a class the project has opted in: `_to_string`, `equals`, and
`deep_equals`. It reports only on classes that opted in and on the types those
classes require, with one stated exception for parse failures, and it never
rewrites anything outside the region it owns.

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
deterministic but surprising, and it makes inserting a field above another field
silently change the meaning of a comment nobody touched. Distinct words cost
four characters and remove the question.

A comment directive rather than configuration alone, because the marker is then
visible in the script a reader is already looking at, travels with the file when
it moves, and appears in the diff that introduces it. A hand-written base class
(`extends GdkitValue`) was rejected because it spends GDScript's single
inheritance slot, forces an addon into the project, and cannot say *which*
helpers the class wants.

### Why not a wholly owned generated file

The `go generate` model — emit a file the tool owns outright and rewrite it
whole — is not available, because GDScript requires a class's methods to live in
that class's one script file. A sibling file cannot add a method to `Hex`.

A **generated base class** does get most of the way there, and was considered
seriously rather than dismissed: `hex.gd` declares
`extends "res://domain/hex.generated.gd"`, and the generated file holds the
methods and extends what `Hex` used to. It would delete the region extent rules,
byte-identity verification, the format oracle, `generate.unsafe`, the orphan
lifecycle and `--prune`, and most of `generate.conflict` — a hand-written
`equals` would simply override the generated one. It would also fix inherited
fields and thread the inheritance slot through rather than spending it, since
each generated base extends the real one.

It was rejected for what the generated bodies would have to look like. A base
class cannot statically reference a member declared in its subclass — GDScript
rejects the identifier — so every field access would become `get("q")`. That
costs static type checking inside the generated code and replaces direct member
access with a dynamic property lookup in `_to_string` and `equals`, which are
exactly the methods that run on every `print()` and every comparison. It also
requires the generated files to be committed, since a fresh clone would not open
in Godot until `gen write` had run, and each new `.gd` file needs a `.uid`
sidecar, putting `gen` and `uid` in each other's way.

The in-file region keeps `self.q`: type-checked, direct, and adding nothing to
the filesystem or to Godot's global class list. The cost is this document's
length, which is paid once.

A free-function helper (`HexEquality.equals(a, b)`) would keep full static typing
in a wholly owned file, but `_to_string` must be a method on the class for
`str()` and `print()` to use it, so that model cannot deliver one of the three
generators at all.

### Grammar

The directive reuses the shape of the three suppression directives in
`internal/suppression` — `#`, `gdkit` or `gdlint`, `:`, the directive word, `=`,
a comma-separated list — but it is parsed by `generate/marker.go`, not by that
package, and it is strict where that package is lax.

`internal/suppression` lets a rule list run to the end of the line because
gdlint does, which is why a trailing remark silently breaks `# gdkit:ignore`. A
gdkit-original directive owes gdlint nothing, so each item in a `generate` list
must be a known generator name and anything else is a `generate.marker`
diagnostic naming it.

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

The sentinels carry no checksum. A region whose content is not what the
generator would emit is simply out of date — `gen check` reports it,
`gen check --diff` shows it, `gen write` rewrites it — exactly as `format`
treats a file that is not canonical. Git is the safety net, and `gen write` is
documented the way `format write` is: run it on a clean tree. A checksummed
region that refused to be clobbered was considered and rejected: it puts a hash
comment in every generated region, churning the diff on metadata, to protect
against editing code whose own first line says it is generated.

#### Region extent

The region is a byte span, and it must be defined exactly, because whether a
canonical file stays canonical turns on it. The span runs from the first byte of
the line holding `# gdkit:generated:begin` to the newline ending the line
holding `# gdkit:generated:end`, **extended backwards over every immediately
preceding blank line**. Trailing blank lines after the end sentinel are *not*
owned: they belong to the gap before whatever follows, which the following
statement's own formatting governs.

The leading gap the region emits depends on its neighbour, and these are the
rules gdparser's `blankLineGaps` actually applies rather than an approximation
of them:

| Position | Leading gap emitted |
| --- | --- |
| region is the first statement in the class body | none |
| preceded by a major declaration (`func`, `class`) | `blank_lines.top_level` |
| preceded by any other statement (`var`, `const`, `signal`) | `blank_lines.top_level` |
| region is at end of file | no trailing blank lines; file ends with one newline after the end sentinel |

The second and third rows agree, and that is not an accident worth glossing
over: gdparser surrounds a *major declaration* with `top_level`, and
`commentRunStart` attributes the gap to the top of a comment run that documents
a declaration. The begin sentinel is such a run, sitting above a `func`, so the
gap gdparser wants before the sentinel is `top_level` regardless of what
precedes it. The clamp to `blank_lines.nested` applies only between statements
that are not major declarations, which the region never is.

This is why owning the *leading* gap and not the trailing one is correct rather
than arbitrary: the leading gap is attributed to the region by the formatter, and
the trailing gap is attributed to the statement after it.

#### Orphaned regions

A region whose class no longer opts in — it gained an `ignore`, stopped matching
configuration, or dropped its last generator — is **retained**, and reported as
`generate.orphaned`. It is removed only by `gen write --prune`.

Neither alternative is acceptable as a default. Silently keeping it leaves
methods in the file that the class has explicitly opted out of, with nothing
saying so. Silently deleting it makes `gen write` destroy code as a side effect
of an unrelated edit to a glob, which is beyond what "the tool owns this region"
licenses.

**Orphan detection scans the whole semantic universe, not just targets**, and is
the one lifecycle question target filtering does not get to answer. Otherwise
adding a path to `exclude` would make its region invisible to both the
diagnostic and `--prune`, so the most likely way to orphan a region would also
be the way to hide it. `--prune` will not write to a file outside the selection,
and says so: the diagnostic names the file and states that it must re-enter the
selection to be pruned. Abandoning ownership silently is the thing being avoided.

## Pipeline

`generate` is a whole-project analysis, closer to `architecture` than to `lint`.
This is not a stylistic choice. To emit `origin.deep_equals(p_other.origin)` for
`var origin: Coordinate`, the generator must know that `Coordinate` is a project
class rather than an engine type, and that `Coordinate` will *actually* have a
`deep_equals` — without the second fact the emitted call is a runtime crash.

### Universe and selection

The semantic index must span the whole project while generation targets are
filtered, and `architecture` already carries the comment explaining why
(`architecture/analyzer.go:61`): a file hidden from the walk takes its
`class_name` out of the index, and references to it then produce false results.
For `generate` the failure is worse — absence from the index is
indistinguishable from "engine-owned", so a field typed as a hidden project
class would get identity comparison.

`generate` must not implement this filtering itself. `project` is documented as
the only package that reads a project from disk, and `.gdkitignore` is read from
disk, so a `generate/target.go` opening it would break that invariant, while a
`Check` that does not read it cannot identify its own targets. The capability
belongs in `project`:

```go
// Selection narrows which parsed scripts a tool acts on. Scripts outside it
// are still walked, parsed, and present in the snapshot.
type Selection struct {
	SourceRoots     []string
	Exclude         []string
	HonorIgnoreFile bool
}

type Config struct {
	Root string
	// SourceRoots, Exclude, and HonorIgnoreFile describe the universe: what
	// is walked and parsed at all.
	SourceRoots     []string
	Exclude         []string
	HonorIgnoreFile bool
	// Selection, when non-nil, narrows what the caller acts on without
	// narrowing the universe.
	Selection *Selection
}

type Snapshot struct {
	// ...
	// Selected is the subset of Paths that Config.Selection admits, sorted.
	// It equals Paths when Selection is nil.
	Selected []string
}
```

`generate` loads the universe with no exclusions and puts its `source_roots`,
`exclude`, and `.gdkitignore` in `Selection`. One load, one parse per file, and
no second package reading the project. The existing four tools pass
`Selection: nil` and are unaffected.

Only a selected script may opt in, and only a selected script may carry a
diagnostic — with the parse-failure exception below.

### Type resolution needs a builtin catalogue

"Not in the project `class_name` index" does not mean "engine-owned", and it
does not mean "project class" either: engine types are absent from that index
too, so `Vector2` and a misspelled `Coordinat` are indistinguishable by it
alone. Resolution is therefore against two catalogues in order:

1. **Builtin Variant value types** — an embedded list in `generate/builtin.go`.
   A field of one compares with `==`, which is correct including for `Array` and
   `Dictionary`, since Godot 4 compares those by value.
2. **The project `class_name` index.**

The builtin set is **not** stable across Godot 4, and an earlier draft of this
spec asserted that it was. `PackedVector4Array` is absent from the Godot 4.0
`Variant.Type` enum and present by 4.6. The list is therefore the **union** of
every builtin introduced anywhere in Godot 4, which needs no version
configuration to be sound: a type that does not exist in the engine version a
project targets cannot appear in that project's source, so a union can only be
over-permissive about names that nobody can write. A per-version list would buy
nothing and would need a `godot_version` key to consult.

`Object` and `Variant` are in the enum but are **not** in this list. `Object` is
the very thing whose reference semantics justify refusing a `Node`-typed field,
so classifying it as a comparable builtin would contradict that rule; a field
declared `Object` has no value equality to generate. `Variant` is handled by the
untyped row of the dispatch table, since that is what it means.

A type in neither is **refused** with `generate.unsupported` naming it. That
rejects a field typed as a native engine class, such as `Node` or `Texture2D`:
a native engine object has reference semantics and no value equality to
generate, so a class holding one is not the kind of class this tool can compare
structurally. The field-level opt-out is the escape hatch, and the diagnostic
names it.

**This applies to `deep_equals` alone.** Resolution exists to decide whether to
recurse, and only `deep_equals` recurses. `_to_string` formats any value with
`%s`, and `equals` compares any value with `==`; neither needs to know what a
field's type is, so neither refuses a field for having an unresolvable one. A
class holding a `Node` can still generate `_to_string` and `equals` — the
`equals` will compare that field by reference, which is the correct semantic for
a node. Only a generator that claims to compare *structurally* owes the reader a
refusal when it cannot.

A full native-class catalogue — roughly 800 names, versioned per Godot release —
is what it would take to distinguish `Texture2D` from a typo, and it buys only a
better *message* for a field that is refused either way. It is out of scope.

### Order

`Check` runs a fixed sequence, and everything downstream depends on the
ordering:

1. **Load and partition** — the universe, and the selection within it.
2. **Resolve opt-in** — configuration globs, then the in-file directive, then
   `ignore`, over selected scripts only. Yields the *requested* set of
   (script, class, generators).
3. **Index** — over the whole universe: `class_name` to script, the inheritance
   edge each class declares, each class's declared methods with their staticness
   and arity, and **each class's selectable fields**.
4. **Resolve capabilities** — compute, to a fixed point, which
   (class, signature) pairs are *realizable*.
5. **Emit and canonicalise** — each requested generator produces its method
   text, consulting step 4. The region, including the leading gap it owns, is
   formatted in isolation with the project's `format` configuration and spliced
   into the file.
6. **Verify** — the candidate must reparse, and every byte outside the region
   must be identical to the source.
7. **Sort** — by path, then line, column, rule.

**Field selection is part of indexing, not a later step for requested classes
only.** Capability resolution and both inheritance rules ask whether some
*other* class — an ancestor, or an unrequested descendant — declares a
selectable field, so that question has to be answerable for every parsed class
before step 4 runs. An earlier draft selected fields after resolving
capabilities and only for requested classes, which made the fixed point depend
on information the pipeline had not computed yet. `# gdkit:generate:ignore-field`
is therefore read for every class in the universe, including classes that never
opt in.

### Realizable, not merely planned

A method a class *requested* is not a method it will *have*. If `B` requests
`deep_equals` and then loses its candidate — a conflict, an unsupported field,
a refused inheritance — then a `deep_equals` in `A` that called `B.deep_equals`
would compile and crash. "Requested" is an intention; emission may only depend
on a capability that survives every blocker.

So step 4 computes `realizable(class, signature)` as a **greatest** fixed point:

- seed it optimistically — every declared-and-compatible method, plus every
  requested generator, is assumed realizable;
- repeatedly demote any **generated** pair whose own emission needs a provider
  that is not realizable, or whose class carries a blocking diagnostic;
- stop when a pass demotes nothing.

A declared, compatible method is **immutable** and can never be demoted. It
already exists in the file; nothing this run does can take it away. Conflating
the two would let a blocked generator erase a perfectly good hand-written
provider and collapse every dependent that was relying on it, so the set tracks
provenance — `declared` or `generated` — and only the latter is demotable.

The set only ever shrinks, so this terminates. Starting optimistically rather
than pessimistically is what makes a cycle work: `A` and `B` holding each other
and both newly requesting `deep_equals` are realizable together, because neither
is demoted by anything other than the other's absence. A cycle survives exactly
when every member survives on its own merits — which is the correct answer, and
the one a least fixed point would get wrong by refusing both.

This supersedes an earlier draft of this spec that computed a single union of
declared and requested methods. The union is right about opt-in, which never
cascades, and wrong about failure, which does.

### Effective providers, not nearest requests

Both inheritance questions are answered by one operation. For a class `C` and a
signature `S`, `provider(C, S)` is the nearest class in `C`'s ancestry,
including `C` itself, that declares a compatible `S` or has `S` realizable.

**Dispatching a field.** A field typed `C` recurses when `provider(C, S)`
exists, not when `C` itself provides `S`. This is what makes a zero-field
subclass work: `C extends B`, `C` adds nothing, `B` generates `deep_equals`, so
`provider(C, deep_equals)` is `B` and a field typed `C` recurses correctly. An
earlier draft refused that case while calling it harmless.

**Composing with the ancestor.** A generated comparison that looked only at the
fields its own class declares would be unsound in both directions, and script
identity does not save it. Two `SubHex` instances answer the same
`get_script()`, so a `SubHex` inheriting `Hex.equals` passes the guard and
ignores every field `SubHex` added; and a `SubHex` that generates its *own*
`equals` would compare only `r` and ignore the `q` it inherited. An earlier
draft of this spec claimed that opting the subclass in cleared the problem,
which was simply wrong — it relocates it.

A generated method therefore **composes with its ancestor's provider** before
comparing its own fields:

```gdscript
func equals(p_other: Variant) -> bool:
	if not (p_other is Object):
		return false
	if p_other.get_script() != get_script():
		return false
	if not super.equals(p_other):
		return false
	return self.r == p_other.r
```

`super.equals` is emitted only when `provider(parent, S)` exists. The ancestor's
own guard passes, because both operands really are the same script. This closes
inherited fields rather than declaring them out of scope: every field in the
ancestry is compared, each by the class that declares it.

What remains is the case with no ancestor to compose with:

> A requested class `R` generating signature `S` is refused with
> `generate.unsupported` when `R`'s ancestry declares a selectable field and
> `provider(parent(R), S)` does not exist. The message names the nearest
> ancestor that would need to opt in.

And the mirror of it, for a descendant that does not opt in at all:

> `R` is refused when some descendant `D` has `provider(D, S) == R` and at least
> one class on the path from `R` (exclusive) to `D` (inclusive) declares a
> selectable field.

Three consequences of the descendant rule are deliberate:

- **Descendants are transitive, not direct.** A grandchild that adds fields and
  inherits `R`'s method is as unsound as a child that does.
- **An override is a barrier.** If an intermediate `B` provides `S` itself then
  `provider(D, S)` is `B`, not `R`, so `R` is not refused for `D`'s sake — and
  `B`, which now composes with `R`, is checked against its own descendants by
  the same rule.
- **A field-free descendant is not a problem**, because it adds no state an
  inherited comparison could miss.

Both diagnostics land on `R`, the class whose generated method would be unsound,
rather than on an ancestor or descendant that may not be selected at all.

### Parse failures are universe-wide, and this is the one scope exception

Diagnostics are otherwise confined to selected scripts. Parse failures are not,
and the reason is that inheritance is a **reverse** dependency. An excluded,
unparseable script may extend a requested base class and add fields. Nothing in
the base names that subclass, so there is no referrer to hang a
`generate.unsupported` on, and the missing edge does not look like missing
information — it looks like a clean project. The refusal rule above would
silently fail to fire.

So: when any generator whose soundness depends on the inheritance graph is
requested — `equals` and `deep_equals` both do — **every** parse failure in the
universe is a blocking `source-parse` diagnostic for the run, selected or not.

The consequence is real and worth stating plainly: a project with one
unparseable script anywhere cannot generate equality methods until it parses.
The alternative is generating a method that is quietly wrong whenever the file
that would have proved it wrong is also the file that cannot be read.

### Formatting and the full-file invariant

Step 6 formats the region alone rather than the spliced file. Formatting the
whole file would let gdparser reformat code *outside* the region whenever the
file was not already canonical, which breaks step 7 and silently turns
`gen write` into a partial `format write`. Formatting the region alone is only
sound because the region sits at indent 0, which is the honest reason v1 is
limited to top-level classes: an inner class's region would need re-indentation
after formatting.

The gap that isolated formatting cannot see — spacing that depends on
neighbours — is closed by the extent rules above, which reproduce
`blankLineGaps`'s actual behaviour rather than assuming it.

Because that is an argument about another package's code, it is checked rather
than trusted. The full-file formatter runs as a **read-only oracle** on every
candidate whose source file was already format-clean, in `gen check` and
`gen write` alike, not only over test fixtures. A candidate the oracle would
change is refused with `generate.unsafe`. That turns the invariant from a claim
in a document into a precondition of writing, which is the standard `format`
already sets by refusing rather than rewriting badly.

The claim, stated precisely: `gen write` introduces no new `format check`
finding. It does not make a file canonical, and a file that was not canonical
stays exactly as uncanonical as it was.

### Verification

`format`'s safety net cannot be reused. Its value is the promise that a rewrite
changes nothing but layout, enforced by comparing the token stream — and a
generator changes the token stream by definition. Extending `format.unsafe` to
carve out an exception would weaken the one invariant that makes `format write`
trustworthy.

The replacement is tighter and cheaper. Given source `S` with its region at
`[a, b)` and candidate `C` with its region at `[a', b')`, both spans as defined
under Region extent:

- `S[:a]` equals `C[:a']`, and `S[b:]` equals `C[b':]`;
- `C` reparses;
- the region in `C` is byte-for-byte what step 6 produced;
- if `S` was format-clean, the oracle reports no change on `C`.

A candidate failing any of these is refused with `generate.unsafe` and the file
is left untouched.

## The plan model

`Check` returns a plan, and the two commands read it differently. Without this,
`generate.stale` being an error is incoherent: a stale region is both the thing
`check` must fail on and the thing `write` exists to fix.

```go
func New(config Config, formatting format.Config) (*Generator, error)

// Check is pure: it performs no I/O.
func (g *Generator) Check(snapshot *project.Snapshot) Plan

type Candidate struct {
	Path     string
	Class    string
	Contents []byte // the whole file, region spliced
	Region   Span   // in Contents, including the owned leading gap
	Changed  bool
}

type Plan struct {
	Candidates  []Candidate
	Diagnostics []Diagnostic
}
```

- **`gen check`** reports every `Changed` candidate as `generate.stale`, plus
  every diagnostic. Exit 1 if either is non-empty at error severity.
- **`gen write`** applies every `Changed` candidate. Staleness is not a failure
  for `write` — fixing it is the point. It exits 1 only when a blocking
  diagnostic remains, 2 on an I/O failure, and prints what it changed either
  way.

A blocking diagnostic suppresses its own class's candidate, and — through step 4
— the candidate of anything that depended on the capability it cost. It does not
stop unrelated classes from being generated.

## Package shape

```
generate/
  config.go      Config, DefaultConfig, LoadConfig, Validate
  marker.go      the directive grammar and the region sentinels
  region.go      region extent and the owned leading gap
  fields.go      member var selection
  builtin.go     the 39 builtin Variant type names
  index.go       class_name, inheritance, declared method signatures
  capability.go  provider() and the realizable fixed point
  generator.go   the registry and the Generator interface
  emit_*.go      one file per generator
  check.go       New, Check — pure, no I/O
  verify.go      the outside-the-region invariant and the format oracle
  apply.go       Apply(snapshot, plan) ([]string, error) — the only writer
  model.go       Diagnostic, Plan, Report, rule name constants
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
	// Signatures are the methods it emits.
	Signatures() []Signature
	// NeedsInheritanceGraph reports that soundness depends on the descendant
	// set, which is what makes a universe-wide parse failure blocking.
	NeedsInheritanceGraph() bool
	Emit(class Class, index *Index, capabilities *Capabilities) (string, []Diagnostic)
}
```

Generators emit in a fixed registry order, not in the order the directive lists
them, so the region's content depends only on the class and not on how the
marker was written.

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
name the public signature wants. The repository already resolves shadowing
before drawing conclusions about an identifier
(`architecture/collectShadows`); this is the same hazard from the emitting side.

### `_to_string`

```gdscript
func _to_string() -> String:
	return "Hex(q=%s, r=%s)" % [self.q, self.r]
```

The name is the `class_name` when the script declares one, otherwise the file's
base name without `.gd`. A class with no selected fields emits `"Hex()"`.

`to_string` does not depend on the inheritance graph: an inherited `_to_string`
that names too few fields prints an incomplete value, which is wrong output and
not a wrong answer, and Godot's own `str()` on a subclass is no better. It
therefore does not make universe-wide parse failures blocking, and
`NeedsInheritanceGraph` returns false for it.

There is no configuration for this format. A project that wants a different one
hand-writes `_to_string` and does not opt into the `to_string` generator, which
is better than a style option nobody can change later without rewriting every
region in every adopting project.

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
two counts: it cannot name a class that declares no `class_name`, nor an inner
class, so those classes could not opt in at all; and it makes equality
asymmetric across a subclass — `Hex.new().equals(SubHex.new())` is true while
the reverse is false. Script identity is symmetric, which is the property a
value object needs. It is not sufficient on its own, which is what the
descendant refusal rule above exists for.

The long `and` chain is emitted on one line and wrapped by step 6 to the
project's `line_width`.

### `deep_equals`

`equals` is enough for every field except an object-valued one. Godot 4 compares
`Array` and `Dictionary` **by value**, not by reference, so a field-wise `==`
already handles containers of builtins correctly. Objects still compare by
reference, which means a plain `==` on `var origin: Coordinate` silently
compares identity — the exact bug a value object exists to prevent. gdkit is
Godot 4 only, so this holds for every supported version and nothing here is
version-gated.

Dispatch per field:

| Declared type | Emitted | Diagnostic |
| --- | --- | --- |
| a builtin Variant type | `self.q != p_other.q` | — |
| project class with `provider(C, deep_equals)` | null-aware recursion | — |
| project class with no provider | nothing; the class is refused | `generate.unsupported` |
| `Array[T]`, `T` a project class with a provider | size check, then null-aware element-wise recursion | — |
| `Dictionary`, or an untyped container | `==` | `generate.untyped` |
| a type in neither catalogue | nothing; the class is refused | `generate.unsupported` |
| absent, `Variant`, or `:=` inferred | runtime `has_method` fallback | `generate.untyped` |

An object-valued field can be `null`, and `null.deep_equals(…)` is a runtime
error, so every recursion is null-aware. A recursion can also **not terminate**,
which is a separate and worse problem: a self-referential field, or a live
`A → B → A` object graph, makes a naive structural comparison descend forever.
The realizability fixed point does not help — it proves the method exists, not
that the walk it performs is finite.

So the generator emits **two** methods. `deep_equals` is the public entry point
and holds the visited set; `_gdkit_deep_equals` does the work and is what
recursion calls. Keeping the set out of the public signature is what lets
`a.deep_equals(b)` stay a one-argument call:

```gdscript
func deep_equals(p_other: Variant) -> bool:
	return _gdkit_deep_equals(p_other, {})


func _gdkit_deep_equals(p_other: Variant, __gdkit_seen: Dictionary) -> bool:
	if not (p_other is Object):
		return false
	if p_other.get_script() != get_script():
		return false
	var __gdkit_key := [get_instance_id(), p_other.get_instance_id()]
	if __gdkit_seen.has(__gdkit_key):
		return true
	__gdkit_seen[__gdkit_key] = true
	if not super._gdkit_deep_equals(p_other, __gdkit_seen):
		return false
	if self.q != p_other.q:
		return false
	if (self.origin == null) != (p_other.origin == null):
		return false
	if self.origin != null and not self.origin._gdkit_deep_equals(p_other.origin, __gdkit_seen):
		return false
	if self.tags.size() != p_other.tags.size():
		return false
	for __gdkit_index in self.tags.size():
		var __gdkit_left: Variant = self.tags[__gdkit_index]
		var __gdkit_right: Variant = p_other.tags[__gdkit_index]
		if (__gdkit_left == null) != (__gdkit_right == null):
			return false
		if __gdkit_left != null and not __gdkit_left._gdkit_deep_equals(__gdkit_right, __gdkit_seen):
			return false
	return true
```

Returning `true` on a revisited pair is the coinductive answer — a pair already
under comparison is assumed equal unless something else proves it unequal —
which is the standard treatment for bisimulation on cyclic structures and the
only one that terminates without declaring every cyclic graph unequal.

The visited set is keyed on the pair of instance IDs, so an `Array` is used as a
`Dictionary` key, which Godot 4 hashes by value. The `super` call participates
in the recursion, so the set threads through the ancestry too.

One consequence for the provider rules: recursion requires
`_gdkit_deep_equals`, not `deep_equals`. A hand-written `deep_equals` therefore
satisfies a top-level call but **cannot** be a provider for a field's recursion,
and a class holding a field of that type is refused with
`generate.unsupported`. The signature a provider must offer is named in the
diagnostic, because the fix is to let gdkit generate the pair.

One `if` per field rather than an `and` chain, because the comparison differs
per field and a chain of mixed call and operator forms is unreadable at any
width.

The last row of the table is the one to expect in practice. `var origin :=
Coordinate.new()` is statically typed as far as Godot is concerned, but
`ast.VariableDeclaration.Type` is empty and the node carries only
`Inferred: true` — the inferred type is not in the tree. So the ergonomic `:=`,
a natural way to write exactly these classes, lands in the runtime fallback.

## Diagnostics

Rule names are a public contract: they appear in JSON output and in
configuration. Two are reused rather than coined: `source-parse`, as `lint` and
`format` spell it — `architecture` spells its own `source.parse`, and `generate`
belongs to the former family — and `class_name.duplicate`, from `architecture`.

| Rule | Reports | Severity |
| --- | --- | --- |
| `source-parse` | a file that could not be parsed; universe-wide when an inheritance-sensitive generator is requested, otherwise selected files only | error |
| `class_name.duplicate` | two scripts claiming one `class_name` that a requested class needs to resolve | error |
| `generate.stale` | a region that is missing or out of date (`check` only) | error |
| `generate.marker` | a malformed directive, an unknown generator name, or a second region in one class | error |
| `generate.conflict` | the class declares, outside the region, a method with the same name as one a requested generator emits, in a shape that is not compatible with it | error |
| `generate.unsupported` | an inner class; a field type in neither catalogue; a field type with no provider for the signature; or a class whose descendant would inherit an unsound method | error |
| `generate.orphaned` | a region whose class no longer opts in, anywhere in the universe | error |
| `generate.unsafe` | verification or the format oracle refused the splice | error |
| `generate.untyped` | `deep_equals` fell back to a runtime dispatch | warning |

**`generate.conflict` fires on the name, and compatibility only decides
satisfaction.** GDScript has no overloading: a `static func equals(a, b)` in a
class that requests `equals` cannot coexist with a generated `func equals(
p_other: Variant) -> bool`, because the second declaration is a duplicate and
the file will not compile. So an existing method whose signature is compatible
*satisfies* the request and nothing is emitted; an existing method with the same
name and an incompatible signature is a conflict. An earlier draft of this spec
said signature matching would keep a `static func equals(a, b)` from being
reported, which was wrong in the only way that matters — it would have emitted a
file Godot refuses to load.

`generate.untyped` is the only warning, so `Diagnostic` carries a severity and
`Report` grows `HasErrors()` alongside `HasDiagnostics()`: a warning prints but
does not take the run to exit 1. It is hardcoded, because a project able to
downgrade `generate.unsupported` to a warning would be asking for code that does
not compile.

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

**A file matching several entries gets the union of their generators**, which is
the grain of the codebase: `architecture`'s `dependencies` are alternative allow
rules, where capability is added by adding a rule. Union is also
order-independent, so reordering the list cannot change the output.

`source_roots`, `exclude`, and `.gdkitignore` populate `project.Selection`. None
narrows the universe. There is no `godot_version` key. The builtin catalogue is a
union over all of Godot 4 rather than a per-version list, for the reason given
under Type resolution, and every other semantic this design relies on is
constant across Godot 4.

Unknown keys are rejected at every depth, naming the offending key's full JSON
path, as `architecture` does. `minimum_gdkit_version` is supported and checked
before the unknown-key walk, for the reason recorded in `CLAUDE.md`.

Globs use `internal/glob`.

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
assert on diagnostic rule names, per the repository conventions. One deliberate
departure: they must also assert on emitted text, because the text is the unit
under test. Golden fixtures per generator.

Invariants, each over the full fixture corpus:

- `gen write` is idempotent;
- the full-file formatter, as a read-only oracle, reports no change on any
  candidate whose source was format-clean;
- `gen write` followed by `gen check` is clean;
- removing a region and regenerating returns the same bytes.

Cases that exist because an earlier draft of this design got them wrong, each
with a named test:

- two classes holding each other, both newly requesting `deep_equals`, which
  must generate rather than deadlock;
- the same pair where one is separately blocked, where **both** must be refused
  rather than one emitting a call to a method that will not exist;
- a field named `p_other`, and an `Array` field named `i`;
- a field typed as a class `.gdkitignore` hides, which must still resolve;
- a `null` object field, and a typed array holding a `null`;
- a base class with a field-adding **grandchild** that does not opt in, refused;
- the same graph with an intermediate class that overrides, **not** refused;
- a zero-field subclass used as a field type, which must recurse via its
  ancestor's provider rather than being refused;
- a `static func equals(a, b)` in a requested class, which must be a conflict;
- a class that stops opting in while its region remains;
- an orphaned region in an excluded file, which must still be reported;
- an unparseable file outside the selection, which must block `equals` but not
  `to_string`;
- a field typed `Vector2`, which must compare rather than be refused; one
  typed `PackedVector4Array`, which must compare although it postdates 4.0; one
  typed `Object`, which must be refused; and one typed `Coordinat`, which must
  be refused;
- a self-referential field, and a live `A` to `B` to `A` graph, which must
  terminate and compare equal;
- a subclass that generates `equals` while its base owns fields, which must
  compose via `super` and compare both;
- a requested subclass whose base has no provider, which must be refused;
- a class with a hand-written `deep_equals` used as another class's field type,
  which must be refused for lacking `_gdkit_deep_equals`;
- a blocked generator alongside a hand-written provider of the same signature,
  where the hand-written one must survive.

A test pins that `lint` reports nothing on a file carrying any of the three
marker forms.

## Explicitly out of scope

- **Inner classes.** A marker on an inner `class` is `generate.unsupported`.
- **A full native-class catalogue.** Roughly 800 versioned names, to improve a
  message for a field that is refused either way.
- **A `hash` generator.** The obvious fourth member of this family and the one
  with real subtlety: Godot's `hash()` on an object is identity-based, so a
  value-object hash must be composed by hand and must agree with `equals` or a
  `Dictionary` keyed on these objects breaks. Its own spec.
- **Rewriting a hand-written `_to_string`.** `generate.conflict` reports it;
  nothing migrates it.
- **A `_to_string` format option.** One documented format.
