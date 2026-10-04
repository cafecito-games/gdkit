# Writing the return type `lint` can decide

`require-return-type` reports a function with no `->` annotation and cannot
write one. This is the write path that writes `-> void` where gdkit can prove
that writing it changes nothing the engine checks, and refuses everywhere else.

`lint` has no writer today: `Check` is pure and the linter never touches disk.
So this adds a third `Apply` of the kind `format` and `generate` already have,
over `internal/atomicwrite`, with a verification oracle of its own — `format`
verifies that a rewrite did *not* change the syntax tree, and a lint fix
changes it on purpose.

The hard part is not the rewrite. It is deciding which functions can take the
annotation, and the answer is narrower than it looks.

## What the engine enforces

Every claim below was observed by running the probe against
`Godot Engine v4.7.2.stable` with `--headless --check-only --script`, which
reports script load failures on stderr. Nothing here is inferred from
documentation. Where a claim is not observed it is labelled unverified.

An annotation of `-> void` is checked in three directions.

**Upward, at the function's own inherited declaration.** This direction is
already closed, and that is the one piece of good news.

| Source | Observed |
| --- | --- |
| base `-> void`, descendant `-> int` | `Parse Error: The function signature doesn't match the parent. Parent signature is "get_thing() -> void".` |
| base `-> int`, descendant unannotated, body returns nothing | `Parse Error: Not all code paths return a value.` |
| base `-> Variant`, descendant unannotated, body returns nothing | `Parse Error: Not all code paths return a value.` |
| `func _can_drop_data(a, b): pass` on a `Control` | `Parse Error: Not all code paths return a value.` |
| `func _get(property): pass` on a `RefCounted` | `Parse Error: Not all code paths return a value.` |
| `func get_name(): pass` on a `Node` | `Parse Error: Not all code paths return a value.` |

So in a project that loads at all, an unannotated function whose body returns no
value has no ancestor declaration — GDScript or engine — that demands a value.
That holds for a `Variant` return as well as a concrete one: `_get` and
`_to_string` both refuse a body that falls off the end. A function
`require-return-type` reports is therefore never in conflict with something
above it, and no catalogue of engine virtuals is needed to know that.

**Downward, at descendants.** This direction is open, and the body of the
function being annotated has nothing to do with it.

| Source | Observed |
| --- | --- |
| base unannotated, descendant `-> int` | loads |
| base unannotated, descendant unannotated and returns a value | loads |
| base `-> void`, descendant `-> int` | `Parse Error: The function signature doesn't match the parent.` |
| base `-> void`, descendant unannotated and returns a value in one branch | `Parse Error: A void function cannot return a value.` |
| base `-> void` with a non-trivial body, descendant `-> int` | `Parse Error: The function signature doesn't match the parent.` |
| `@abstract func f() -> void`, implementation `-> int` | `Parse Error: The function signature doesn't match the parent.` |
| `@abstract func f()`, implementation `-> int` | loads |
| inner `class B extends A` overriding an unannotated `A.f` with `-> int` | loads |

Two consequences. The descendant need not be annotated for the write to break
it: an unannotated override that returns a value inherits the parent's return
type and is rejected, which is exactly the population of an untyped codebase.
And the base's body is irrelevant — a base with a real body is as constraining
as one that only says `pass`.

**Sideways, at call sites.** This direction is open too, and it is the one the
starting position for this work did not account for.

| Source | Observed |
| --- | --- |
| `func f(): pass` and `var x = f()` | loads |
| `func f() -> void: pass` and `var x = f()` | `Parse Error: Cannot get return value of call to "f()" because it returns "void".` |
| `func f() -> void: pass` and `print(f())` | same error |
| `func f() -> void: pass` and `return f()` | same error |
| cross-file, `var helper: Helper = Helper.new()` then `var x = helper.f()` | same error, reported on the caller |
| cross-file, untyped receiver `func caller(helper): var x = helper.f()` | loads |

Using a `void` call's value in any value position is a parse error. Before the
annotation the same code loads, because an unannotated function that falls off
the end returns `null`. The error is reported on the *caller's* file, which may
be any file in the project, and it is raised only when the receiver's type is
statically known: an untyped receiver is not checked, so the same write can
break one call site and silently change another.

**A void function may still `return` with no value.** `return` alone inside
`-> void` loads; `return null` is `Parse Error: A void function cannot return a
value.` So "no `return` carrying a value" is the right test, and `return null`
is not a candidate.

**An override of a void engine virtual already behaves as `-> void` whether or
not the annotation is written.**

| Source | Observed |
| --- | --- |
| `extends Node`, `func _ready(): pass`, and `var x = _ready()` | `Parse Error: Cannot get return value of call to "_ready()" because it returns "void".` |
| same for `_process`, and the same across files through a typed receiver | same error |
| GDScript base `extends Node` with unannotated `_ready`, descendant `_ready() -> int` | `Parse Error: The function signature doesn't match the parent. Parent signature is "_ready() -> void".` |
| `extends Node`, `func my_own_thing(): pass`, and `var x = my_own_thing()` | loads |

This is the shape of the safe case, and it generalises: **writing `-> void` is a
no-op for the engine's checks exactly when the function's inherited declaration
is already `-> void`.** Both hazardous directions are already closed by the
ancestor, so nothing downstream can newly break. The last row shows the
contrast — a name with no inherited declaration is unconstrained today, and
annotating it narrows it.

**Unverified.** How a broken call site behaves when it is reached only at
runtime through `load()`, and whether exporting a project compiles every script
and so surfaces the error before shipping, were not tested. The probe observed
the error at script load, under `--check-only`.

## The hazard, and what it means for this work

`-> void` is **not** safely decidable from a single file. The starting position
for this work held that the hazard was limited to base classes written to be
overridden, and that skipping a function whose body does nothing — only `pass`,
or a docstring and `pass` — would remove most of it. The probes refute both
halves:

- the body of the function being annotated has no bearing on whether a
  descendant may return a value (the base-with-a-real-body row above), so
  "does nothing" is a guess about the author's intention, not a soundness
  argument;
- `@abstract func f()` has no body at all, so the heuristic does not even name
  the clearest case of a declaration written to be overridden;
- the hazard is not confined to descendants. A call site that uses the value is
  broken just as hard, and gdkit cannot find every such call site without
  resolving receiver types.

What survives from the starting position is the posture: refuse rather than
write. Applied here, that means the fix is **project-wide**, not single-file,
and conservative where it cannot be exact.

### The conditions a candidate must satisfy

`lint check` stays single-file and pure. The rule records a candidate; the
fixer, which holds the whole snapshot, decides. A candidate is written only
when all five hold.

1. **The function is not abstract.** `FunctionDeclaration.Abstract` is true for
   `@abstract func f()`, which declares a contract for implementations to
   satisfy and carries no body. Annotating it constrains every implementation
   (the `@abstract ... -> void` row above). This is a soundness exclusion, not
   a heuristic.
2. **No `return` in the function's own body carries a value.** The walk is
   specified under [Which returns belong to the function](#which-returns-belong-to-the-function).
3. **The inheritance graph over the whole project is complete for this class.**
   No ancestor or descendant with an unresolved base, no inheritance cycle, no
   file that failed to parse. `generate`'s index already demotes every
   inheritance-sensitive answer on exactly these grounds, for exactly this
   reason: an unresolved base means the class could have an unseen descendant.
4. **No descendant redeclares the name with a non-void return type or with a
   value-returning body.** This is decidable from the graph plus one fact per
   method, and it closes the downward direction exactly.
5. **No call expression anywhere in the project whose callee's final name
   component is this function's name appears in a value position.** A value
   position is anything other than a whole expression statement, or an `await`
   whose result is itself discarded as a statement.

Condition 5 is a deliberate over-approximation. Deciding it exactly needs the
receiver's type, which means expression inference, which `lint` does not have
and the typed-rules design already closed the door on. Matching on the method
name alone refuses more than it must — a candidate named `update` is refused
because some unrelated class's `update()` is read for its value somewhere — and
it never writes where the exact answer would have refused. The refusal
diagnostic names the call site that caused it, so an over-refusal is
diagnosable rather than mysterious.

Conditions 3 through 5 are what make this sound, and together they subsume the
engine-virtual case without a catalogue of virtuals: `func _ready():` passes
because descendants that override `_ready` return nothing and because `_ready`
is not read for its value. That matters, because `generate` deliberately
refused to grow a catalogue of builtin names and this work should not
reintroduce one.

### The index, and why it must ignore `.gdkitignore`

Conditions 3 through 5 are questions about the universe, not about the selected
files. A descendant hidden by `.gdkitignore` is still a descendant, and a call
site in an ignored file still stops the project loading.

`project.Config.Selection` exists for this: the universe is walked and parsed
unfiltered while `Snapshot.Selected` names the subset the caller acts on.
`generate` already uses it. So `lint fix` loads with
`Selection: &project.Selection{SourceRoots: …, Exclude: …, HonorIgnoreFile: true}`
and an unfiltered `Config`, and `Linter.Lint` iterates `snapshot.Selected`
rather than `snapshot.Paths`. `Selected` is `Paths` when `Selection` is nil, so
that change is a no-op for `lint check`, `lint`'s own tests, and every other
caller.

### Open question: the cost of base resolution

Resolving `extends` to another indexed class is the fiddly part of condition 3,
and `generate/index.go` already solves it for a `class_name`, a `res://` path, a
`preload`-aliased constant, an inner class, and an autoload name that Godot
resolves as a project global. Writing that resolution a second time inside
`lint` is precisely the drift `internal/atomicwrite` and `internal/glob` exist
to prevent.

**Recommendation:** lift the base resolution and the parent/child edges out of
`generate` into an internal package both packages use, and count that
extraction as part of the cost of this work rather than pretending the fixer
can do without it. `lint` must not import `generate`.

**Alternative, if that extraction is judged too large to carry here:** the
first cut refuses any candidate in a class that any other class in the project
extends, directly or transitively, which needs the edges but no per-method
facts. It over-refuses — every method of every base class is refused, including
`_ready` — and it never writes unsoundly. The decision is which of those two
the first release ships, and it is the maintainer's: the second is cheaper and
leaves most of the value on the table.

## Explicitly out of scope: any return type but `void`

A body whose every `return` carries a literal of one type is tempting and is
out, and the engine says why.

| Source | Observed |
| --- | --- |
| `func f(flag): if flag: return 1` | loads |
| `func f(flag) -> int: if flag: return 1` | `Parse Error: Not all code paths return a value.` |

An unannotated function that returns a value from one branch and falls off the
end of another is legal and returns `null` on that path. Annotating it `-> int`
is rejected, because a declared return type makes the engine demand totality.
Inferring `int` therefore requires reachability analysis over the body, and
getting it wrong does not produce a wrong annotation — it produces a project
that does not load. The door is closed here, deliberately.

A related shape can never be annotated at all: `func f(flag): if flag: return` …
`return 1` mixes a bare `return` with a value-returning one, which loads
unannotated, is refused by `-> void` ("A void function cannot return a value")
and refused by `-> int` ("Not all code paths return a value"). It is not a
defect in this design; it is code that has to change before any annotation
fits.

## Which returns belong to the function

`ast.Children` descends into a `LambdaExpression`'s body, so a walk that does
not stop will attribute a lambda's `return` to the enclosing function. The
probe confirms it: over

```gdscript
func with_lambda():
	var f = func(): return 1
	var g = func inner(x):
		if x:
			return x
		return 0
	f.call()
	g.call(1)
```

an `ast.Inspect` over the function's body statements that returns `true`
everywhere counts three value-returning returns; one that stops at
`*ast.LambdaExpression` counts none. The correct answer is none: the function
returns nothing and is a `-> void` candidate. PR #35 shipped a bug where lambda
sites escaped a collector's walk, so this is the known-dangerous part of the
traversal.

The walk starts at each statement of `FunctionDeclaration.Body` — not at the
declaration node — and returns `false` from `*ast.LambdaExpression`,
`*ast.FunctionDeclaration`, and `*ast.ClassDeclaration`.

Where a `return` can appear, and who owns it:

| Site | Belongs to the function |
| --- | --- |
| a statement of the body | yes |
| inside an `if`, `elif`, or `else` branch | yes |
| inside a `while` or `for` body | yes |
| inside a `match` case body, at any nesting | yes |
| nested inside any combination of the above | yes |
| inside a lambda in the body, named or anonymous, inline or indented | no |
| inside a lambda in a *parameter default* | no, and the walk never reaches it: parameter defaults are children of the declaration, not of its body statements |
| inside a method of an inner class declared in the body | no |
| inside a property accessor of a class-scope variable | no, and the walk never reaches one: accessors hang off `VariableDeclaration`, which is not in any function's body |

The probe confirms the first five rows together (a function with returns nested
through `if`/`elif`/`while`/`for`/`match` yields three, all of them bare) and
the parameter-default row (zero, with and without the stop).

An accessor body has no return type annotation of its own, so it is not a site
this work can act on either way.

## Where the edit is recorded

`Diagnostic` gains one unexported field:

```go
// fix is the edit that would satisfy this finding, or nil when the rule
// cannot write one. It is unexported deliberately: Diagnostic is serialized
// to JSON and that output is a public contract, so gdkit does not commit to a
// wire format for fixes before the shape of fixes is settled. Every
// registered rule lives in this package, so every rule that needs it can
// reach it. Promoting it later is additive.
type fix struct {
	// anchor is the byte offset of the "func" keyword of the declaration to
	// annotate, which identifies the site exactly.
	anchor int
	// returnType is the annotation to write.
	returnType string
}
```

Unexported is right for the reason the starting position gives, and it is also
load-bearing in a second way: `Diagnostic` travels by value through the driver,
which copies the whole struct when it stamps `Rule`, so the payload survives
without anything being threaded, and `encoding/json` cannot reach it.

**One refinement of the starting position.** The payload carries the site's
anchor rather than a finished byte range and replacement string. Computing the
range requires lexing the file (see below), and a rule that lexes per file
cuts against the design `Context` was built for, where nothing is compiled or
parsed per file. The fixer must lex each file it changes anyway, for the
verification oracle, so the range is computed there — once. What the starting
position was protecting against is a second place that *decides which functions
to annotate*, and that decision stays entirely in the rule: the fixer reads the
anchor and never re-derives a candidate.

A rule that cannot write a fix leaves `fix` nil, which is every rule but this
one.

## Locating the insertion point

There is no span for the colon that ends a function header, so the anchor is
resolved by lexing. gdkit already lexes in `format/tokens.go`, so this is an
established mechanism rather than a new one, and it is not a regular expression
over source text.

From the token whose start offset is the anchor, scan forward tracking bracket
depth across `(`/`)`, `[`/`]`, and `{`/`}`. The parameter list's closing `)` is
the `)` that returns depth to zero. The header colon is the first `:` at depth
zero after it; an abstract declaration has no colon and the scan ends at the
newline. The edit **replaces** the bytes between the end of that `)` and the
start of that `:` with `" -> void"`.

Replacing rather than inserting is what makes `func f() :` come out as
`func f() -> void:` instead of `func f()  -> void:`. **The fix is refused when
those bytes are not all spaces and tabs**, which keeps `func f() \` followed by
a line of indentation and a colon from being silently joined into one line.

Depth tracking is what keeps a colon that is not the header's from being
chosen. The probe ran this over twelve headers:

| Header | Result |
| --- | --- |
| `func f():` | `func f() -> void:` |
| `func f() :` | `func f() -> void:` |
| `static func f(a, b):` | `static func f(a, b) -> void:` |
| `func f(a: int = 1, b := 2):` | parameter type and inferred default untouched |
| `func f(a = {"x": 1}):` | the dictionary's colon is at depth 2 and is skipped |
| `func f(x: Dictionary[String, int]):` | the bracketed type's commas and colon are skipped |
| a parameter list broken over four lines with a trailing comma | `) -> void:` on the closing line |
| `func f(a = func(): return 1):` | the lambda's colon is at depth 1 and is skipped |
| `func f():  # why` | `func f() -> void:  # why` |
| `func f(): pass` | `func f() -> void: pass` |
| `@abstract func f()` | `@abstract func f() -> void` — never reached, abstract is refused |
| `func f() \` + newline + `:` | refused, the bytes between are not spaces and tabs |

## Building the changed file

All the fixes for one file are applied in a single pass, and the file is
verified once.

Sort a file's fixes by the start of their byte range, ascending, and build the
output by copying the span before each range, writing `" -> void"`, and
continuing from the end of the range. Equivalently one may apply them in
descending order in place; ascending with a copy is specified because it makes
the offsets of the original the only offsets in play, and offset arithmetic
against a buffer being mutated is how this kind of code corrupts files.

**Two fixes whose ranges intersect are a refusal for the whole file**, not a
best effort. One function header holds at most one fix, so this cannot arise
from this rule; it is asserted rather than assumed because the consequence of
being wrong is a mangled file, and a later fixable rule will hit it.

## Verification

The output is refused unless all four hold.

1. **It parses.** `gdparser.ParseFile` on the new bytes returns no error.
2. **Its token sequence is the input's with exactly the expected tokens
   inserted.** Lex both; compare token type and lexeme pairwise, in order,
   including the layout tokens. The only difference permitted is, at each
   fixed header, the insertion of `->` followed by the identifier `void`
   between that header's `)` and `:`. Any other difference anywhere is a
   refusal.
3. **Each fixed function's return type is now `void`, and no other function's
   changed.** Read from the reparsed tree: the function at each anchor has
   `ReturnType == "void"`, every other `FunctionDeclaration` has the
   `ReturnType` it had, and the number of declarations is unchanged.
4. **Nothing moved under a suppression comment.** The line count is identical,
   and the list of suppression directives — kind, rule names, whether the
   directive stands alone, and line number — is identical before and after.

Condition 2 is a stronger oracle than `format`'s, and it can be because the
edit is weaker. `format` reorders tokens, drops redundant parentheses, and
renormalizes numbers and quotes, so it can only compare multisets with the
parentheses, commas, and semicolons left out. This edit inserts two tokens on
one line and changes nothing else, so an exact sequence comparison over every
token is available, and nothing is excused.

Condition 2 also makes a separate tree-equality check unnecessary, which is why
condition 3 is as narrow as it is. The parse tree is a function of the token
stream; two files with the same token sequence apart from an inserted return
type, both of which parse, cannot differ in the tree anywhere but at that
return type. `format` needs its tree comparison because its token comparison is
a multiset and is allowed to skip tokens; here the sequence comparison carries
the weight, and condition 3 pins the one difference that is meant to be there.

Condition 4 holds structurally — the inserted text contains no newline, so no
line's number changes and no directive moves — and is checked anyway, because
`internal/suppression` finds directives by searching raw lines and the check
costs one pass. It is the analogue of `format/suppression.go`'s
`movedSuppression`, not a copy of it: `movedSuppression` also refuses a change
to the *tokens* on a directive's line, which this fix makes on purpose when it
annotates a header that a directive covers. The invariant that matters is that
a directive keeps reaching the same lines, and it does.

A refused file is reported and left alone. Other files in the same run are
still written: a refusal is a statement about one file, and withholding the
rest would make a single unusual header block an entire project's fixes.

## Writing

`internal/atomicwrite.Replace`, with a prefix of `.gdkit-lint-*`, exactly as
`format.Apply` and `generate.Apply` use it — the write beside and rename, and
the re-read before the rename that refuses a target edited while the run was
computing. `lint.Apply` is the only writer in the package and does nothing but
write; the plan it writes is computed purely.

Mirroring `format`, the split is `Linter.Plan(snapshot, report) FixReport`,
which is pure, and `lint.Apply(snapshot, FixReport) ([]string, error)`, which
writes and returns the project-relative paths written in order, stopping at the
first failure and returning what it already wrote alongside the error. `--diff`
needs the plan and not the write, which is the reason for the split.

## Diagnostics the fixer reports

Two names, both public contract, neither a rule:

- `lint.unsafe` — a file whose fixes were refused, and why. The message names
  the condition that failed: the verification oracle, an intersecting pair of
  ranges, a header whose `)` and `:` are separated by something other than
  spaces and tabs.
- `lint.undecidable` — a candidate dropped because the project-wide conditions
  were not satisfied, naming what stopped it: the descendant that overrides it
  with a value, the call site that reads its value, or the unresolved base that
  made the graph incomplete.

They are spelled with a dot, like `format.unsafe` and `generate.unsafe`, and
not with a dash, like the rule names `source-parse` and `unknown-ignore`. That
is deliberate: a dashed name is a rule, which means `--disable` accepts it,
`register` reserves it, and a suppression comment can silence it. A refusal to
write is not something a project should be able to silence, and `register`'s
panic on a reserved name is the mechanism that keeps the two namespaces apart.
A consumer that writes `lint.unsafe` into a suppression comment gets
`unknown-ignore`, which is the right answer.

`lint.undecidable` is reported only under a flag, because on a project of any
size the list is long and reporting it unasked would bury the fixes. See the
open question on `--explain`.

## Command surface

```
gdkit lint fix [flags] [project-root]
```

**Open question: the subcommand name.** Every other write in gdkit is
`<tool> write` — `format write`, `uid write`, `gen write` — and
`lint write` would match that without a thought.

**Recommendation: `lint fix`**, which is what issue #36 calls it. The three
existing `write` subcommands write something the tool computes in full: the
formatted file, the sidecar Godot would have written, the region the generator
owns. This one writes a subset of findings and leaves the rest, the subset it
writes shrinks as the conditions get stricter, and `lint check`'s verdict is
unaffected by which findings happen to be fixable. `fix` says that; `write`
promises more than it does. It is also the universal spelling for this
operation in every other linter a user has met. The maintainer may still prefer
the local convention over the global one, and that is a reasonable call to make
differently.

### Flags

| Flag | Meaning |
| --- | --- |
| `--config` | as every other command |
| `--format text\|json` | as every other command |
| `--enable`, `--disable` | as `lint check`, since the rules that can be fixed ship inert |
| `--minimum-version` | as every other command |
| `--diff` | compute the plan, print a unified diff per file, write nothing |
| `--explain` | also report `lint.undecidable` for every candidate that was dropped |

`--diff` is part of the first cut, not a later addition. A write path that
changes meaning rather than layout and cannot be previewed is one a user has to
either trust completely or not use, and `format check --diff` already
establishes both the flag and the `internal/textdiff` machinery behind it. As
in `format`, `--diff` cannot be combined with `--format json`; the JSON plan
carries the same information in a form a consumer can read.

### Exit codes

| Code | When |
| --- | --- |
| `0` | every planned fix was written, or `--diff` had nothing to show |
| `1` | a fix was refused (`lint.unsafe`), or `--diff` would change something |
| `2` | configuration, usage, or I/O failure, including a target that changed on disk |

Findings that are not fixable do **not** affect the exit code. That is the
question the starting position raises about a run where some findings are
fixable and some are not, and the answer is that `lint fix` reports on writes
and `lint check` reports on findings. A `lint fix` run that annotates four
functions and leaves ninety unannotated has done its whole job and exits `0`;
the ninety are what the next `lint check` reports, with the same exit `1` it
reported before. Conflating the two would make `lint fix` unusable in a
pipeline that runs it before `lint check`.

`--diff` exiting `1` when something would change mirrors `format check`, so a
CI job can assert that no fix is outstanding.

Under `--format json` an exit-`2` failure is a JSON envelope on stderr with
stdout left empty, `--format` is validated before anything else that can fail,
and an unknown `--format` value is reported as text — the conventions are
`cmd/gdkit`'s and this command inherits all of them.

### Output

Text output lists the files written, one per line, then the refusals:

```
annotated player.gd (2 functions)
annotated ui/menu.gd (1 function)
res://odd.gd:12: Error: refused to write a return type: the header's colon is not on the line of its parameter list (lint.unsafe)
lint fix: 3 return types written in 2 files, 1 file refused
```

**The list of files written is printed even when the run fails part-way**, as
`format write` and `uid write` do, and the summary line is omitted in that
case. That list is what tells a caller what state the project is in, and it is
the only thing that does.

JSON output is the plan, the refusals, and `written`, following
`cmd/gdkit/format.go`'s `writeReport`: the report type embedded in a wrapper
that adds the paths actually replaced, in path order, including the ones
written before a failure.

A run with nothing to do prints which rules `lint fix` can act on and whether
they are enabled, rather than only a count of zero. A silent `0` from a command
whose job is to annotate reads as "the project is annotated", and the section
on what this does not do exists because it is not.

## Interaction with inertness

`require-return-type` ships inert through `lint.PendingRule`. **`lint fix` must
not act on a rule the project has not enabled**, and in this design it cannot:
the fixer runs the same `Linter`, built from the same config, so a rule that
`newLinter` left out of `enabled` produces no findings and therefore no fixes.
The property falls out of the architecture rather than needing a policy, which
is the right place for it to come from.

It is also the right answer on its own terms. `PendingRule` exists so that
upgrading gdkit cannot change what an existing project reports on unchanged
configuration. Writing into a project's source on the strength of a rule it
never opted into would be a strictly larger violation of that promise than
reporting would. A project opts in the way it already does — `enable` in
`.gdkit/lint.json`, `enable_new_rules`, or `--enable require-return-type` on
the command line, which `lint check` already accepts.

The per-rule exempt list is a related but separate lever and is not the one to
reach for here: it suppresses the *finding*, so a project that exempts
`_ready` to quiet the report also loses the fix. Nothing in this design changes
that, and the conditions above are what decide whether a fix is written.

## Interaction with the engine floor

`-> void` is Godot 4.0 syntax. `require-return-type`'s floor in `lint`'s typing
collector is already `godot40`, the lowest value the collector has, so
`godot_version` can never drop a `require-return-type` finding on any 4.x
engine and cannot gate this fix. Said plainly: **there is no version gate on
this one.**

The one configuration that does silence it is a `godot_version` below `4.0`,
which `versiongate.ParseEngineVersion` accepts as a number. Then
`Context.supports(godot40)` is false, the rule reports nothing, and there is
nothing to fix. That needs no special case: no findings means no fixes, and a
project that tells gdkit it is on Godot 3 is right to get neither.

## Interaction with suppressions

**A suppressed finding is never fixed, and that is structural rather than
checked.** `Linter.Lint` drops a diagnostic that `suppressions.silences`
covers before it reaches the report, so a suppressed finding never carries its
`fix` payload out of the driver. This is the strongest argument for the
starting position's second conclusion: because the edit travels on the
`Diagnostic`, suppression filtering applies to fixes for free, where a second
pass that re-derived the edits would have had to reimplement it.

In the other direction — whether the write moves a suppression's target —
`format/suppression.go` answers the question by refusing any rewrite that
changes the tokens on a directive's line or on the line below it. This fix
changes the tokens on the header's line on purpose, so that rule cannot be
imported wholesale. What `format`'s rule is protecting is that a directive goes
on reaching the same code, and it does here: the inserted text contains no
newline, so no line's number changes, no directive moves, and nothing moves out
from under or into one. Verification condition 4 pins exactly that.

One consequence is worth stating because it looks like a bug and is not. A
`# gdkit:ignore = max-line-length` written above a function header goes on
covering that header after the fix lengthens it, so a header the fix pushes
past the configured line length stays silenced. That is the directive doing its
job.

`internal/suppression` finds directives by searching raw lines, so a directive
can live inside a string literal. The fix writes only between a `)` and a `:`
in a function header, which is never inside a literal, so it can neither create
nor destroy a directive; condition 4 confirms it rather than assuming it.

## Interaction with the formatter

Inserting eight characters can push a header past the configured line length,
and the probe shows gdparser's formatter does not wrap a long function header:
a 103-column header under `GodotStyle()`, whose `LineWidth` is 100, comes back
unchanged. So `gdkit format write` will not relieve a header this fix
lengthens, and the new `max-line-length` finding, if any, is the project's to
resolve by renaming or by raising the limit.

Nothing else about the output is unformatted: `func f() -> void:` is the
canonical form, and the one header shape where the edit is not a pure
insertion — `func f() :` — is normalized toward it.

## Idempotence

A second run writes nothing, for the same reason the suppression property
holds: the rule reports only a function with no `->`, and after the first run
the function has one. There is no marker to maintain and no region to own,
which is what distinguishes this from `generate`.

The test for it is a run over the output of a run, asserting that the planned
fix count is zero and the bytes are byte-identical. Tested, not assumed: a
future fixable rule whose fix does not satisfy its own check would be a loop,
and this is where that gets caught.

## Testing

The conventions are `lint`'s: throwaway projects built with
`writeProject(t, t.TempDir(), …)`, assertions on rule names and positions
rather than on formatted text, and a satisfied case asserted as explicitly as a
violating one. A passing test is not the evidence here; the mutation that turns
it red is. Each test below is named with the mutation it exists to catch.

**The walk.**

- A function with a lambda that returns a value is annotated. Red when the walk
  stops returning `false` at `*ast.LambdaExpression` — the exact bug PR #35
  shipped.
- A function with returns nested through `if`, `elif`, `else`, `while`, `for`,
  and `match` is not annotated. Red when the walk stops descending into any one
  of those, which is the failure a single flat case would miss; the table has a
  row per construct so the diagnosis names the construct.
- A function whose parameter default is a value-returning lambda is annotated.
  Red when the walk starts at the declaration node instead of at its body
  statements.
- A function containing an inner class whose method returns a value is
  annotated. Red when the walk does not stop at `*ast.ClassDeclaration`.
- A function with a bare `return` is annotated; one with `return null` is not.
  Red when the test is the presence of a `return` rather than the presence of a
  value, which is the mutation that writes `-> void` onto code the engine then
  refuses.

**The conditions.**

- An `@abstract func f()` is never annotated. Red when `Abstract` is not
  checked. The declaration has no body, so a "body does nothing" test does not
  catch it and the assertion has to be its own.
- A base whose descendant in another file overrides the name with `-> int` is
  not annotated; the same with a descendant that is unannotated and returns a
  value is not annotated either. Red when condition 4 looks at the
  descendant's annotation rather than at its body as well.
- The same, with the descendant as an inner class in the same file. Red when
  the index holds only top-level classes.
- The same, with the descendant hidden by `.gdkitignore`. Red when the fixer
  loads with `HonorIgnoreFile: true` on `Config` instead of on `Selection`,
  which is the mutation that makes the ignore file silently unsound.
- A class whose base does not resolve is not annotated. Red when condition 3 is
  dropped, which is the mutation that writes on an incomplete graph.
- A function whose value is read in another file is not annotated; the same
  function called as a bare statement is annotated. Red when condition 5 is
  dropped, and red in the other direction when it refuses every call rather
  than every value-position call.
- `func _ready():` on a class with subclasses that override `_ready` without
  returning anything **is** annotated. Red when the conditions are tightened
  into "refuse any class with a descendant", and it is the test that proves the
  design delivers the case the issue is about.

**The header anchor.** A table over the twelve headers in the anchor section,
asserting the output bytes. Red when depth tracking is dropped (the dictionary
default and the bracketed parameter type both produce a mangled header), when
the edit inserts rather than replaces (`func f() :` gains a double space), and
when the continuation header is not refused.

**Multiple fixes in one file.** A file with four annotatable functions,
asserting all four annotations and the exact bytes. Red when the fixes are
applied in ascending order against a buffer being mutated, which shifts every
range after the first. A file with one annotatable function among three that
are not is the companion: red when the fixer annotates by position in the
report rather than by anchor.

**The oracle.** The verifier is tested through an injected plan that makes a
wrong edit, the way `format`'s `verifyTree` is injected through the `Formatter`
struct. A plan that writes `-> void` at the wrong offset, one that writes it
twice, and one that deletes a token must each be refused with `lint.unsafe`.
Red when the token comparison is a multiset instead of a sequence — the
double-write case passes a multiset check if the counts happen to match — and
red when the comparison skips parentheses or commas the way `format`'s does.

**Suppressions.** A finding suppressed by `# gdkit:ignore = require-return-type`
on the line above the header is not fixed. Red when the fixer collects
candidates by walking the rules itself rather than by reading the report the
driver already filtered.

**Inertness.** `lint fix` on a project that has not enabled
`require-return-type` writes nothing, and the same project with
`--enable require-return-type` writes. Red when the fixer builds its linter
from the registry instead of from the configured rule set.

**Idempotence.** As described above.

**The command.** Exit `0` with unfixable findings left over; exit `1` from
`--diff` when something would change; exit `1` from a refusal; exit `2` from a
target that changed on disk, with the paths written before it still listed on
stdout. The last is the one that matters: red when the failure path returns
before printing, which is the convention `format write` and `uid write` already
follow and the reason a caller can tell what state the project is in.

## What this does not do

Stated plainly, because a clean `lint fix` run is not a fully annotated
project and the README has to say so:

- It writes `-> void` and no other type. A function that returns a value keeps
  no annotation, and most functions in an untyped codebase return values.
- It refuses every function it cannot prove safe, and the conditions are
  conservative by design. Condition 5 in particular refuses on a name
  collision with an unrelated class's method, so a function that would have
  been safe is left alone.
- It annotates nothing but a return type. The other five typing rules report
  and do not write.
- A clean run therefore means "gdkit wrote every `-> void` it could prove", not
  "this project is typed". `lint check` remains the measure of what is left,
  and it will still report plenty.
- Nothing here tells a project which of its functions *should* return a value.
  A function that returns nothing because it was never finished is annotated
  `-> void` as readily as one that is void by design.

## Explicitly out of scope

- **Any return type but `void`.** Argued above, with the engine's totality
  requirement as the reason.
- **Fixes for the other five typing rules.** `require-argument-type` and
  `require-variable-type` need the type of a value, which is inference.
  `require-typed-collection` needs the element type. `lint` does not do
  inference and this work does not start it.
- **An engine-virtual catalogue.** The conditions above make one unnecessary,
  and `generate` already declined to build its analogue. If a later design
  wants `-> void` on a virtual that conditions 4 and 5 refuse, the catalogue is
  the thing to reach for, and it should be generated from the engine rather
  than written by hand.
- **Removing a wrong annotation.** Nothing here deletes or changes an existing
  `->`.
