# Typed-GDScript lint rules

Six `lint` rules that report GDScript declarations carrying no static type
annotation. Detection only: no rewriting, no inference of what a missing
annotation should have been. Those are separate specs, named under
[Explicitly out of scope](#explicitly-out-of-scope).

Static typing is Godot's documented correctness and performance win, and nothing
enforces it project-wide today. `gdlint` has no equivalent rules, so these six
names are gdkit-original; adding them keeps gdkit a superset of gdlint rather
than diverging from it.

## Rules

All six ship inert through `lint.PendingRule`, so upgrading gdkit cannot change
an existing project's verdict on unchanged configuration. `PendingSince()` names
the release that introduces them.

| Rule | Reports | Minimum Godot version for the fix |
| --- | --- | --- |
| `require-return-type` | `func f():` with no `->` | 4.0 |
| `require-argument-type` | a parameter with no `: Type` and no `:=` default | 4.0 |
| `require-variable-type` | `var x` or `var x = v`, class or function scope, `@export` included | 4.0 |
| `require-typed-collection` | an annotation of bare `Array` or `Dictionary` | `Array[T]` 4.0, `Dictionary[K, V]` 4.4 |
| `require-signal-argument-type` | `signal s(arg)` with an untyped parameter | 4.0 |
| `require-typed-loop-variable` | `for item in …` with no `: Type` | 4.2 |

A site is satisfied by any of three things, and all three are deliberate:

- an explicit annotation;
- `:=` inference, which *is* static typing — `ast.Parameter.Inferred` and the
  variable declaration's `Inferred` report it;
- an explicit `: Variant`, which is how a declaration opts out on purpose.

Never reported:

- `const`, which GDScript types from its value, so it is already typed;
- a variadic `...args` parameter, which is a `Variant` array by construction;
- a lambda's return type (see [Lambdas](#lambdas)).

## Approach

One collector, six rules keyed by name — the shape `lint/rules_name.go` already
uses, where a single `nameRule{rule: string}` type and one `collectNames` walk
back all fourteen name rules. A reader who knows that file needs no new idea to
read this one.

The alternative considered and rejected was six self-contained rules with six
AST walks. It restates "what counts as typed" six times, which is how the six
drift apart; the shared collector gives that definition one home, and the
version gate and exempt lists then apply uniformly without being reimplemented
per rule.

Promoting typing sites into `lint.Context` as a per-file pre-pass shared with
future rules was also rejected for now. `Context` carries configuration only,
and this spec does not need the invasiveness. It is worth revisiting if the
Godot performance rules later want the same traversal.

### The collector

`collectTypingSites(script)` walks once, mirroring `nameCollector`: the class
body yields class-scope variables, signals, and functions; a function scope
yields locals, `for` headers, and nested lambdas.

```go
type typingSite struct {
    rule      string      // which rule governs this site
    name      string      // the declared name, for the message
    enclosing string      // function or signal name, for the exempt list
    typeName  string      // the written annotation, "" when absent
    inferred  bool        // written with ":="
    floor     string      // minimum godot_version for the fix to be writable
    span      token.Span  // what to underline
}
```

`floor` belongs to the site rather than to the rule because
`require-typed-collection` needs both values: a bare `Array` is fixable at 4.0,
a bare `Dictionary` only at 4.4. This is the only rule in gdkit whose
applicability varies per finding, and it is pinned by a test for that reason.

Everything the collector reads already exists in `gdparser` v0.1.5 —
`FunctionDeclaration.ReturnType` and `ReturnTypeSpan`, `Parameter.Type`,
`Parameter.Inferred` and `Parameter.Variadic`, and the variable declaration's
`Type`, `TypeSpan`, and `Inferred`. The `Type` fields are raw strings, so a bare
`Array` is distinguished from `Array[int]` by the string's shape. This spec
needs no parser change.

### Reported position

At the declared name, except for `require-typed-collection`, where the written
annotation *is* the defect and the site reports its `TypeSpan`. A missing `->`
has no span of its own, so reporting at the name is what lets a missing return
type land somewhere a reader and an editor annotation can both use. The name
rules already report at the name, so this is consistent rather than novel.

`EndLine` and `EndColumn` come from the span end; columns go through the
existing `runeColumn`, which counts runes rather than bytes.

### Messages

In the voice the existing rules use (`Function name "%s" is not valid`):

```
Function "move" has no return type                     (require-return-type)
Argument "data" of function "apply" has no type        (require-argument-type)
Variable "items" has no type                           (require-variable-type)
Array has no element type; write Array[T]              (require-typed-collection)
Argument "amount" of signal "damaged" has no type      (require-signal-argument-type)
Loop variable "item" has no type                       (require-typed-loop-variable)
```

### Lambdas

A lambda's parameters are checked by `require-argument-type`; its return type is
never checked. A lambda's parameters are a contract its caller has to satisfy,
while its return value is consumed at the point the lambda is written, where an
annotation is noise. The enclosing function's name is what a lambda site matches
an exempt list against.

### Known limitation: only written annotations are examined

`var x := []` infers an untyped `Array`, and `require-typed-collection` does not
report it. Catching it needs expression inference, which this package does not
have and should not grow: a single-file linter that starts inferring types is
the first step toward a semantic analyzer, which belongs in a package of its own
if it is ever built. The rule checks annotations only, and the README says so.

## Configuration

```json
{
  "godot_version": "4.7",

  "require-return-type": ["_ready", "_process"],
  "require-argument-type": [],
  "require-variable-type": [],
  "require-typed-collection": [],
  "require-signal-argument-type": [],
  "require-typed-loop-variable": []
}
```

### `godot_version`

A `major.minor.patch` string, default `"4.7"`, parsed by
`internal/versiongate.ParseRequirement` rather than `Parse`. `ParseRequirement`
is the stricter of the two precisely because its input is hand-written, which
this value is: a leading `v` or a prerelease suffix is an error here, not
something to tolerate. `Config.Validate()` parses it, so a typo is a
configuration failure carrying a `failure.Kind` — exit `2` — rather than a rule
that quietly stops firing.

Defaulting to the newest version is safe only because these rules ship inert: a
project that has not opted in cannot be affected by the default, and a project
on an older engine lowers one key instead of hunting for the right rule name.

### Per-rule exempt lists

Each rule's key holds function-name glob patterns the rule skips, matched with
`internal/glob` — `*` and `?`, with no path segments to cross. What the pattern
matches depends on the rule:

| Rule | Pattern matches |
| --- | --- |
| `require-return-type`, `require-argument-type` | the function being declared |
| `require-variable-type`, `require-typed-collection`, `require-typed-loop-variable` | the enclosing function, so `["_process"]` quiets a hot loop's locals without quieting the file |
| `require-signal-argument-type` | the signal's own name |

A class-scope declaration has no enclosing function and is therefore never
exempted by a list; it is suppressed with a `# gdkit:ignore` comment like
anything else. Every list defaults empty, and `Validate()` compiles every
pattern so a malformed glob fails as configuration rather than silently matching
nothing.

### The list-semantics collision, accepted deliberately

These six rules take list-shaped config whose empty value means *no
exemptions*, while `missing-docstring`'s empty list means *the rule is off*.
Same shape, opposite meaning.

The alternative was `{"exempt": [...]}` objects, which are self-describing but
heavier and break the flat one-key-per-rule convention the config file has
today. The flat lists win because `missing-docstring` is the documented oddity —
it predates `PendingRule` and is inert through its own empty list only because
that rule happened to be configured by a list — while these six are the regular
case. Inertness is `PendingRule`'s job and never a list's. The README and the
struct field comments state this explicitly, because a reader comparing the two
keys will otherwise assume they work alike.

## Testing

Four layers, following the conventions already in `lint/`.

**Per-rule tables** in `lint/rules_typing_test.go` over small inline sources,
asserting rule name, line, and column. Satisfied cases are asserted as
explicitly as violating ones: `:=` inference, an explicit `: Variant`, a
`const`, and a variadic parameter must each produce nothing. "The rule fired on
something it shouldn't" is the failure mode that reaches users as noise, so it
is tested directly rather than inferred from the absence of a failure.

**Fixtures** in `lint/testdata/typing/*.gd` with a `typingFixtureExpectations`
group, run under a config that enables all six — mirroring how
`testdata/logging` and `loggingFixtureExpectations` cover the inert
`no-engine-logging`. This is required, not optional:
`TestFixturesExerciseEveryRule` fails the build when a registered rule fires
nowhere, and these six fire nowhere under the default configuration by
construction.

**Version-gate tests.** The gate's job is to make a rule report nothing, which
is indistinguishable from the rule being broken, so the gate is pinned from both
sides:

- `require-typed-loop-variable` is silent at `godot_version: "4.1"` and fires at
  `"4.2"`;
- `require-typed-collection` reports a bare `Array` but not a bare `Dictionary`
  at `"4.3"`, and both at `"4.4"`.

**`TestPendingRulesAreExactlyTheInertOnes`** gains all six names. The test exists
so a rule cannot start or stop shipping inert unnoticed, and six new inert rules
are exactly the event it watches for.

Plus `Validate()` tests for a malformed `godot_version` and a malformed exempt
glob, both asserting a `failure.Kind` rather than a message.

## Documentation

- The README's lint rule reference gains all six, with their config keys, their
  satisfied-by cases, and their version floors.
- The two counts of "30 … problems" in the README become 36.
- `godot_version` is documented in the lint configuration table with its
  default, and the reason the default is the newest version.
- The `.gdkit/lint.json` written by `gdkit lint init` gains `godot_version`. It
  does not gain the six rule keys: `init` writes the default policy, the default
  policy exempts nothing, and writing six empty lists would imply the rules run.

## Explicitly out of scope

Each is its own spec.

- **`lint --fix` for return types.** Inferring that a function with no `return`
  statement should be `-> void`, and rewriting it. Needs a rewrite-and-verify
  story — `format`'s verifier rejects a rewrite that adds a token, which adding
  `-> void` does — and needs the boundary of the safely-decidable subset
  established, including the cross-file cases (overriding a parent that declares
  a return type, and Godot virtual methods whose return type the engine
  mandates). `typingSite` already carries `span` and `typeName`, which is where
  a suggested replacement hangs; no speculative fields are added now.
- **The Variant-return census.** Listing every function whose effective return
  is Variant, as a signal of under-modeled code. This is a census rather than a
  set of findings, and nothing in gdkit emits a non-`Diagnostic` report today,
  so it needs an output shape of its own.
- **Godot performance rules** (`get_node` in `_process`, unfreed `Node.new()`,
  `load` where `preload` works). They need a call-context notion `lint` does not
  have, and carry false-positive risk these rules do not.
