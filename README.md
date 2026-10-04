# gdkit

**Static analysis and source processing for Godot 4 GDScript — architecture
boundaries, linting, formatting, and `uid://` identities in one binary.**

[![CI](https://img.shields.io/github/actions/workflow/status/cafecito-games/gdkit/ci.yml?branch=main&label=CI&logo=github)](https://github.com/cafecito-games/gdkit/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/cafecito-games/gdkit?label=release&logo=github)](https://github.com/cafecito-games/gdkit/releases/latest)
[![Go reference](https://img.shields.io/badge/go-reference-00ADD8?logo=go&logoColor=white)](https://pkg.go.dev/github.com/cafecito-games/gdkit)
[![Go version](https://img.shields.io/github/go-mod/go-version/cafecito-games/gdkit?logo=go&logoColor=white)](go.mod)
[![Godot 4](https://img.shields.io/badge/godot-4.x-478cbf?logo=godotengine&logoColor=white)](https://godotengine.org)
[![License](https://img.shields.io/github/license/cafecito-games/gdkit)](LICENSE)

`gdkit` is four tools over one project model. They share project discovery and
parse the same way, so every tool agrees on which files are in scope.

| Tool | What it does |
| --- | --- |
| [`gdkit arch`](#architecture-checks) | Enforces layer, feature, and engine-purity boundaries, and finds dependency cycles |
| [`gdkit lint`](#linting) | Reports 30 naming, structural, design, documentation, and formatting problems, and 7 more a project can opt in to |
| [`gdkit format`](#formatting) | Rewrites GDScript into one canonical style, verifying every rewrite first |
| [`gdkit uid`](#uid-sidecars) | Creates the `uid://` sidecars Godot would have created |
| [`gdkit gen`](#code-generation) | Generates `_to_string`, `equals`, and `deep_equals` into the classes that opt in |

The analyzer uses [`gdparser`](https://github.com/cafecito-games/gdparser) and
does not search source text with regular expressions. It parses GDScript,
indexes global `class_name` declarations, resolves semantic identifier and type
references, and inspects static `load()` and `preload()` calls. Comments and
string contents cannot accidentally create class dependencies.

## Quick start

```sh
brew install cafecito-games/tap/gdkit

cd /path/to/godot-project
gdkit arch check .      # layer, feature, and engine-purity boundaries
gdkit lint check .      # GDScript style and correctness
gdkit format check .    # canonical formatting; writes nothing
gdkit uid check .       # scripts with no uid:// identity
gdkit gen check .       # generated methods that are missing or out of date
```

Nothing needs configuring first: every tool runs with a built-in default policy.
`gdkit <tool> init` writes that policy to `.gdkit/` as an editable starting
point. Every check command accepts `--format json`, and exits `0` when clean,
`1` on findings, and `2` on a configuration, usage, or I/O failure — so each one
works as a CI gate as it stands. See
[Continuous integration](#continuous-integration).

## Install

Homebrew is the supported way to get a prebuilt binary on macOS and Linux:

```sh
brew install cafecito-games/tap/gdkit
```

Or build from source with the Go toolchain:

```sh
go install github.com/cafecito-games/gdkit/cmd/gdkit@latest
```

Or run it from a checkout:

```sh
go run ./cmd/gdkit arch check /path/to/godot-project
```

Releases carry macOS, Linux, and Windows archives for AMD64 and ARM64 with a
SHA-256 manifest.

## Contents

**Tools**

- [Architecture checks](#architecture-checks) — `gdkit arch`
- [Linting](#linting) — `gdkit lint`
- [Formatting](#formatting) — `gdkit format`
- [UID sidecars](#uid-sidecars) — `gdkit uid`
- [Code generation](#code-generation) — `gdkit gen`

**Across every tool**

- [Ignoring files with `.gdkitignore`](#ignoring-files-with-gdkitignore)
- [Continuous integration](#continuous-integration)
- [Pinning the gdkit version](#pinning-the-gdkit-version)
- [Machine-readable failures](#machine-readable-failures)
- [Version information](#version-information)

**Reference**

- [Architecture configuration](#architecture-configuration)
- [ADR-backed exceptions](#adr-backed-exceptions)
- [Library use](#library-use)
- [Development](#development) and [releasing](#releasing)

## Architecture checks

`gdkit arch check` classifies every `.gd` file into a layer and a feature,
builds the dependency graph from the parsed AST, and reports:

- duplicate `class_name` declarations and GDScript parse errors;
- GDScript files that have no layer and feature classification;
- class references and static resource loads that cross a forbidden boundary;
- missing project resources referenced by `load()` or `preload()`;
- dependency cycles, including cycles formed by a mix of class and script loads;
- `Node`, `Control`, `Node2D`, `SceneTree`, and `get_tree()` in domain or
  application code;
- engine globals including `Input`, `ProjectSettings`, `Time`, `FileAccess`,
  and `ResourceLoader` in those pure layers;
- signals in domain or application code, unless the file is an explicitly
  designated runtime boundary; and
- scene loading and node inspection from domain or application tests.

Static `res://` paths, paths relative to the current script, and `uid://` paths
are resolved. An identifier is discovered wherever Godot records it: a `.uid`
sidecar beside a script, the header of a `.tscn` or `.tres` file, or the
`.import` file beside an imported asset. Dynamic resource paths and
`user://` resources do not create project dependency edges.

Run without a configuration file to use the built-in conventions:

```sh
gdkit arch check .
gdkit arch check --format json .
gdkit arch check --show-edges .
gdkit arch check --minimum-version 0.3.0 .
```

Text output is one line per diagnostic, naming the rule in brackets, then a
summary:

```text
features/combat/presentation/health_panel.gd:5:16: combat/presentation may not depend on combat/infrastructure (features/combat/infrastructure/godot_health_reader.gd) [dependency.direction]
architecture check failed (1 diagnostics, 2 files, 1 dependencies)
```

JSON output carries the classified `files`, the deduplicated dependency
`edges`, and the `diagnostics`, each sorted by path, line, column, and rule, so
the output is stable between runs.

The command exits `0` when clean, `1` for architecture violations, and `2` for
configuration, usage, or I/O failures.

`--minimum-version` refuses to run unless the binary is at least the named
release. See [Pinning the gdkit version](#pinning-the-gdkit-version).

Classification rules, the dependency policy, and the built-in project layout are
in [Architecture configuration](#architecture-configuration); the documented
escape hatch is in [ADR-backed exceptions](#adr-backed-exceptions).

## Linting

`gdkit lint` reports 30 naming, structural, design, documentation, and
formatting problems in GDScript, with 7 further rules a project opts in to —
see [Rules that ship inert](#rules-that-ship-inert). Like the architecture
analyzer, it works from the parsed AST and shares its project discovery, so
both tools agree on which files are in scope.

```sh
gdkit lint check .
gdkit lint check --format json .
gdkit lint check --disable max-line-length,max-file-lines .
gdkit lint check --minimum-version 0.3.0 .
gdkit lint init .
```

Text output is one line per diagnostic, naming the rule in parentheses:

```text
player.gd:12: Error: Function name "DoThing" is not valid (function-name)
```

JSON output carries the same diagnostics with a 1-based rune `column` and, for
some rules, `end_line` and `end_column`. Diagnostics are sorted by path, line,
column, and rule, so the output is stable between runs.

The command exits `0` when clean, `1` when at least one `error` diagnostic was
reported, and `2` for configuration, usage, or I/O failures.

### Rules

Name rules check an identifier against a pattern from the configuration:

- `function-name`, `class-name`, `sub-class-name`, `signal-name`;
- `class-variable-name`, `class-load-variable-name`, `function-variable-name`,
  `function-preload-variable-name`, `function-argument-name`,
  `loop-variable-name`;
- `enum-name`, `enum-element-name`, `constant-name`, and `load-constant-name`.

Basic correctness rules:

- `duplicated-load`, `expression-not-assigned`, `unnecessary-pass`,
  `unused-argument`, and `comparison-with-itself`.

Structure rules:

- `class-definitions-order` checks the order of members against the configured
  slot order; and
- `no-else-return` and `no-elif-return` flag an `else` or `elif` that follows
  branches which return.

Static typing rules report a declaration that carries no static type
annotation. All six [ship inert](#rules-that-ship-inert), and
[Static typing rules](#static-typing-rules) describes each one:

- `require-return-type`, `require-argument-type`, `require-variable-type`,
  `require-typed-collection`, `require-signal-argument-type`, and
  `require-typed-loop-variable`.

Design limits:

- `max-returns`, `max-public-methods`, and `function-arguments-number`.

Documentation:

- `missing-docstring` requires a `##` documentation comment on public members.
  It reports nothing until `missing-docstring` in the configuration lists the
  member kinds to check.

Logging:

- `no-engine-logging` reports a call that writes a diagnostic message straight
  to Godot's output — `push_warning`, `push_error`, the `print` family,
  `print_stack`, `OS.alert` — so a project can require its own logger instead.
  It [ships inert](#rules-that-ship-inert).

Format rules:

- `max-file-lines`, `max-line-length`, `trailing-whitespace`, and
  `mixed-tabs-and-spaces`.

Two further rules are reported by the driver rather than by a rule:

- `source-parse` reports a file that does not parse. Rules cannot run on it.
- `unknown-ignore` reports a suppression comment that names a rule that does not
  exist, so a misspelled name cannot silently suppress nothing.

### Static typing rules

An untyped GDScript declaration is a `Variant`: the engine cannot check it,
cannot specialize it, and reports nothing when the wrong value is assigned to
it. Six rules report one. Each has a configuration key of the same name holding
a list of function-name globs it skips, and a Godot version its fix needs —
`godot_version` in the configuration decides whether that fix can be written at
all.

| Rule | Reports | Exempt list matches | Needs Godot |
| --- | --- | --- | --- |
| `require-return-type` | `func f():` with no `->` | the function being declared | 4.0 |
| `require-argument-type` | a parameter with no `: Type` and no `:=` default | the function being declared; for a lambda's parameter, the enclosing function | 4.0 |
| `require-variable-type` | `var x` or `var x = v`, at class or function scope, `@export` included | the enclosing function | 4.0 |
| `require-typed-collection` | an annotation of bare `Array` or `Dictionary`, and an empty `[]` or `{}` initializer on a declaration with no written type | the enclosing function | `Array[T]` 4.0, `Dictionary[K, V]` 4.4 |
| `require-signal-argument-type` | `signal s(arg)` with an untyped parameter | the signal's own name | 4.0 |
| `require-typed-loop-variable` | `for item in …` with no `: Type` | the enclosing function | 4.2 |

Three things satisfy every one of them, and all three are deliberate: an
explicit annotation; `:=` inference, which *is* static typing; and an explicit
`: Variant`, which is how a declaration opts out on purpose.

Four declarations are never reported as missing a *type*. A `const` carries no
annotation because GDScript types it from its value — its *element* type is
still reported, because typing a const from its value supplies no element type,
so `const ITEMS := []` is a bare `Array` like any other. A variadic `...args`
parameter collects whatever is passed into an `Array`, so "untyped" is not a
missing type. A lambda's return type is consumed where the lambda is written,
where an annotation is noise — a lambda's *parameters* are still checked,
because they are a contract its caller satisfies. And a property setter's
parameter cannot be checked at all: the parser exposes its name but no type,
and `set(value: int):` is itself a parse error, so there is nothing to report
and nothing a project asked to fix one could write.

Matching on the enclosing function is what makes `["_process"]` quiet a hot
loop's locals without quieting the file. A bare collection written in a signal's
payload is named for the signal, like the rest of the payload, because a signal
is not inside a function.

Three limits are worth knowing before a project reads a clean run as proof:

- **A collection is inferred only from an empty literal.** `var x := []` and
  `var x := {}` are reported, because an empty literal declares the collection
  and nothing else: the element type can only come from the author. A
  *populated* literal is not. `var x := [1, 2, 3]` is an untyped `Array` in
  Godot too, but naming its element type means typing every element and
  deciding what their common type is, which is expression inference this
  package does not have. Nor is any other initializer: `var x := build()` may
  well be a collection, and deciding that is the same problem. A declaration
  that carries a written annotation is reported from the annotation alone, so
  `var x: Array = []` is one finding and not two.
- **A finding whose fix the configured engine cannot parse is dropped, with no
  output at all.** There is no "your engine is too old" diagnostic, because
  telling a project to write a type it cannot parse is worse than saying
  nothing. So on `"godot_version": "4.3"` a bare `Dictionary` is not reported
  while a bare `Array` still is, and on anything below `4.2`
  `require-typed-loop-variable` reports nothing at all.
- **A class-scope declaration cannot be exempted by a list.** It has no
  enclosing function, so no glob can name it, not even `["*"]`; it is
  suppressed with a `# gdkit:ignore` comment like anything else. That includes a
  `for` loop inside a class-scope variable's initializer lambda. Code inside a
  property accessor is named for the property, which is the only name a pattern
  could use.

### Lint configuration

`gdkit lint init` writes `.gdkit/lint.json` with the default policy, and
existing files are preserved unless `--force` is supplied. Pass an alternate
file with `--config`, relative to the project root. `gdkit lint check` runs with
the defaults when no configuration file exists. Unknown keys, unknown rule
names, and patterns that do not compile are configuration errors.

Values are applied on top of the defaults, so an omitted field keeps its
default:

| Field | Default |
| --- | --- |
| `source_roots` | `["."]` |
| `exclude` | `[".git/**", ".godot/**", ".gdkit/**", "addons/**"]` |
| `godot_version` | `"4.7"` |
| `disable` | none |
| `enable` | none |
| `enable_new_rules` | `false` |
| `severity` | none; every rule is an `error` |
| `max-returns` | `6` |
| `max-public-methods` | `20` |
| `function-arguments-number` | `10` |
| `max-file-lines` | `1000` |
| `max-line-length` | `100` |
| `tab-characters` | `1` |
| each [static typing rule](#static-typing-rules)'s key | `[]`; no function is exempt |
| `missing-docstring` | `[]`; the rule is off |
| `no-engine-logging` | every engine output call; no logger named |

`disable` lists rules to turn off, and `--disable` takes the same names as a
comma-separated list in addition to the file. `exclude` uses the same glob
syntax as the architecture configuration. `gdkit lint check` also skips the
paths listed in [`.gdkitignore`](#ignoring-files-with-gdkitignore).

`enable` and `enable_new_rules` turn on rules that ship inert; see
[Rules that ship inert](#rules-that-ship-inert).

`godot_version` is the engine version the project targets, written as
`major.minor` or `major.minor.patch`. A rule whose fix needs newer syntax than
this reports nothing, so a project is never told to write an annotation its
engine cannot parse; the [static typing rules](#static-typing-rules) are the
rules this gates today. It defaults to the newest Godot gdkit knows, which is
safe only because every gated rule ships inert: a project that has not opted in
cannot be affected by the default, and a project on an older engine lowers this
one key instead of hunting for the right rule names.

```json
{
  "godot_version": "4.3",
  "enable": ["require-return-type", "require-argument-type", "require-variable-type"],
  "require-variable-type": ["_process", "_physics_process"]
}
```

`tab-characters` is a setting and not a rule. `max-line-length` expands each
tab to that many spaces before measuring a line.

`max-line-length` reports a long line only when a shorter form of it exists. A
wrap can go anywhere between two tokens but never inside one, so the narrowest a
line can be rewritten to is its indentation plus its widest token, and a line
already over the limit by that measure is left alone. A comment is measured by
its longest word, because prose wraps.

So a name from generated code or an addon that is longer than the limit on its
own, a `res://` path that deep, and a URL in a documentation comment are not
reported and need no suppression comment. Nothing shorter than that is exempt:
Godot's [style guide][styleguide] favors wrapping a long statement in
parentheses, and allows a backslash where parentheses do not fit, as in a match
pattern list — so a line holding several ordinary tokens does have a shorter
form, however unwieldy it looks. Binding a long name to a shorter local and
extracting a function are not counted, because every line is reducible under
those and the rule would never report anything.

[styleguide]: https://docs.godotengine.org/en/stable/tutorials/scripting/gdscript/gdscript_styleguide.html

Each name rule has a key of the same name holding a regular expression that must
match the whole identifier, for example:

```json
{
  "class-name": "([A-Z][a-z0-9]*)+",
  "signal-name": "[a-z][a-z0-9]*(_[a-z0-9]+)*",
  "enum-element-name": "[A-Z][A-Z0-9]*(_[A-Z0-9]+)*"
}
```

`missing-docstring` is a list of the member kinds that must carry a `##`
documentation comment, drawn from `class`, `func`, `signal`, `var`, `const`, and
`enum`. The list is empty by default, so the rule is opt-in and can be widened
one kind at a time:

```json
{ "missing-docstring": ["class", "signal", "func"] }
```

A member is public unless its name begins with an underscore, so Godot's
lifecycle callbacks and anything named `_like_this` are exempt, as is everything
inside a private inner class. `class` covers each inner `class`, and the script
itself when it declares a `class_name`; Godot reads the script's class comment
from the top of the file, ahead of every member. A static function is checked,
even though `max-public-methods` does not count one. A `#` comment does not
satisfy the rule, because Godot's generated class reference shows only `##`, and
neither does a `##` block separated from the member by a blank line.

`no-engine-logging` holds the calls to reject and, optionally, the name of the
logger the diagnostic should point at:

```json
{
  "enable": ["no-engine-logging"],
  "no-engine-logging": {
    "functions": ["push_warning", "push_error", "print", "OS.alert"],
    "logger": "Log"
  }
}
```

`functions` defaults to every call Godot offers for writing a diagnostic
message: `push_warning`, `push_error`, `print`, `prints`, `printt`, `printraw`,
`printerr`, `print_rich`, `print_debug`, `print_stack`, and `OS.alert`. Setting
it replaces that list, so a project can trim it to the calls it cares about or
add one of its own; an empty list silences the rule. A name may carry one
qualifier, as `OS.alert` does, and then only a call on that object is reported.
An unqualified name matches a bare call only, so a logger with a method of its
own named `push_error` or `print` is never reported — the rule matches the
function a call reaches, not the word. `logger` only changes the wording of the
diagnostic; with it unset the message says "use a logger abstraction instead".

`class-definitions-order` is a list of slot names that defaults to `tools`,
`classnames`, `extends`, `docstrings`, `signals`, `enums`, `consts`,
`staticvars`, `exports`, `pubvars`, `prvvars`, `onreadypubvars`,
`onreadyprvvars`, and `others`.

A rule's severity can be lowered to `warning`. Warnings are printed but do not
make the run fail:

```json
{
  "severity": {
    "max-line-length": "warning",
    "unused-argument": "warning"
  },
  "disable": ["max-file-lines"]
}
```

### Rules that ship inert

A gdkit upgrade must not change what an existing project's `lint check` reports
on unchanged configuration. So a new rule arrives **inert**: it is registered,
documented, and listed by `gdkit lint check --format json`, but it does not run
until the project asks for it.

```jsonc
{
  "version": 1,
  "enable": ["some-new-rule"],   // opt in to one
  "enable_new_rules": true       // or to every inert rule, now and later
}
```

`--enable` takes the same names as a comma-separated list, mirroring
`--disable`.

Three properties are worth knowing:

- **`disable` wins over `enable`.** An explicit "off" is the stronger
  statement, and a project listing a rule in both is most likely turning off
  something it opted in to earlier.
- **`enable` accepts any known rule name**, not only an inert one. Naming a
  rule that already runs does nothing. This is deliberate: when an inert rule
  graduates to running by default, every configuration that opted in keeps
  working instead of becoming a configuration error on upgrade.
- **`enable_new_rules` opts in to rules that do not exist yet.** It trades
  reproducibility across upgrades for always running the strictest policy gdkit
  knows, which is the right trade for some projects and the wrong one for a
  repository auditing a release.

Widening what an existing rule reports is the same event as adding a rule, from
a project's point of view, so it arrives the same way: as a new inert rule name
rather than as a quiet change to the rule already running.

Seven rules ship inert today: `no-engine-logging`, and the six
[static typing rules](#static-typing-rules) — `require-return-type`,
`require-argument-type`, `require-variable-type`, `require-typed-collection`,
`require-signal-argument-type`, and `require-typed-loop-variable`.

`missing-docstring` predates this mechanism and is inert through its own empty
`missing-docstring` list instead. That worked because the rule happens to be
configured by a list; most rules have no value to leave empty, which is why the
general mechanism exists.

### Suppressing diagnostics

Comments name one or more rules in a comma-separated list. `gdlint` is accepted
in place of `gdkit` in each directive, so existing suppression comments keep
working.

```gdscript
# gdkit:ignore = function-name, unused-argument
func DoThing(unused):
	pass

# gdkit:disable = max-line-length
# ... a region where long lines are fine ...
# gdkit:enable = max-line-length
```

- `# gdkit:ignore = rule-a, rule-b` applies to its own line and the line below.
- `# gdkit:disable = rule` applies from that line to the end of the file.
- `# gdkit:enable = rule` ends a disable.

Two behaviors are easy to trip on:

- The earliest `enable` for a rule ends every `disable` of that rule, including
  a `disable` that appears later in the file.
- The rule list runs to the end of the line, so a trailing comment becomes part
  of the last rule name. `# gdkit:ignore = function-name # note` suppresses
  nothing, and `unknown-ignore` reports `function-name # note` as an unknown
  rule.

### Severity and exit codes

Every rule is an error by default. Setting a rule's severity to `warning` in
`.gdkit/lint.json` reports it without failing the run, which is useful for a rule
a project is working toward rather than enforcing.

`gdkit lint check` exits `0` when clean or when the only diagnostics are
warnings, `1` when any error-severity diagnostic is reported, and `2` for
configuration, usage, or I/O failures.

## Formatting

`gdkit format` rewrites GDScript into one canonical style, which defaults to the
Godot GDScript style guide. It shares project discovery with the other tools and
prints each file from its parsed syntax tree.

```sh
gdkit format check .
gdkit format check --diff .
gdkit format check --format json .
gdkit format check --minimum-version 0.3.0 .
gdkit format write .
gdkit format init .
```

- `gdkit format check [--config path] [--format text|json] [--diff] [project-root]`
  reports the files that are not formatted and never writes to the project.
  `--diff` prints a unified diff after each file that would change; it cannot be
  combined with `--format json`.
- `gdkit format write [--config path] [--format text|json] [project-root]`
  rewrites those files in place.
- `gdkit format init [--force] [project-root]` writes `.gdkit/format.json` with
  the default style.

Running `gdkit format` without a subcommand runs `check`.

Text output names each file, then each diagnostic, then a summary:

```text
would reformat player.gd
broken.gd:3: Error: expected expression (source-parse)
format check failed (1 to reformat, 1 diagnostics)
```

`write` prints `reformatted player.gd` for each file it rewrote and ends with a
line such as `format write: 1 reformatted, 12 unchanged, 1 skipped`. JSON output
has the same shape for both commands: a `results` array with the `path` and
`changed` flag of every file that could be formatted, and a `diagnostics` array.
Both are sorted by path, so the output is stable between runs. `write` adds a
`written` array of the paths it replaced on disk, which lists the files written
before the failure when a run stops part-way.

`gdkit format check` exits `0` when every file is already formatted, `1` when a
file would change or has a diagnostic, and `2` for configuration, usage, or I/O
failures. `gdkit format write` exits `0` when every file is formatted once it
finishes, `1` when a file was skipped because of a diagnostic, and `2` for
configuration, usage, or I/O failures.

### Format diagnostics

A file with a diagnostic is left exactly as it is:

- `source-parse` reports a file that does not parse, so it cannot be formatted.
- `format.unsafe` reports a file whose formatted output would not keep the
  syntax tree or the tokens of its source, or would change the code a lint
  suppression comment applies to.

### Write safety

Before a file is reported as changed, its formatted output is parsed again and
compared structurally with the source. Only layout and the spellings the
configuration asks to normalize may differ. A file whose tree would change is
reported as `format.unsafe` and is never written. Writes are atomic and keep the
file mode: the new contents are written beside the file and renamed over it, so
an interrupted run leaves either the old file or the new one.

String literals, numbers, and comment text are compared without consulting the
formatter, so the check does not depend on the code it is checking. A string
may change only its quote character and the escaping of the quotes inside it;
every other escape must stay as written, as must every character of a string
that spans lines, including the spaces or tabs that end one of its lines.

The tokens of the output are then compared with the tokens of the source,
because the tree records only what the parser chose to keep. Line breaks,
indentation, parentheses, commas, and semicolons are left out, since the
formatter adds and removes them, and the same normalized spellings are allowed;
every other token must appear exactly as often after formatting as before. A
file whose tokens would change is reported as `format.unsafe`.

A rewrite is also refused as `format.unsafe` when it would change the code a
lint suppression comment applies to. A directive reaches lines rather than
syntax, so each one must stay the same directive, still trailing code or still
on a line of its own, with the same tokens on its line and on the line below it
before and after formatting. A comment that trails a block header, such as
`func f():  # gdlint:ignore = function-name`, stays on the header's line and is
accepted. The syntax tree is unchanged in every case below, but lint could
report something it did not report before, so the file is left alone:

- A comment that trails a line the formatter wraps or splits, such as a long
  call, `var a = 1; var b = 2  # gdlint:ignore = ...`, or a one-line
  `if x: pass  # gdlint:ignore = ...`, which leaves the comment on only one of
  the new lines.
- A comment whose line below is wrapped, joined, or separated from it by blank
  lines the formatter adds.

The rule is deliberately stricter than lint needs: wrapping the line below a
`disable` comment is refused although the directive would still cover it.
Formatting the affected lines by hand makes the file acceptable. A directive is
recognized the way lint recognizes it, by searching each raw line, so one
written inside a string literal counts; requoting that literal is allowed.

What `write` does and does not touch:

- CRLF line endings are rewritten as LF, and a UTF-8 byte order mark is dropped.
  A line break inside a string literal is kept as written.
- Symlinked files and directories are not discovered, so they are never
  rewritten.
- A rewrite replaces the file rather than editing it in place, so other hard
  links to it keep the old contents.
- A read-only file in a writable directory is replaced, and stays read-only.
- A file that changed on disk after it was read is not overwritten. The run
  stops there and exits `2`, with the files already written left in place.

### Format configuration

`gdkit format init` writes `.gdkit/format.json` with the default style, and an
existing file is preserved unless `--force` is supplied. Pass an alternate file
with `--config`, relative to the project root. Both commands run with the
defaults when no configuration file exists. Unknown keys, unknown values, and
exclude patterns that do not compile are configuration errors.

Values are applied on top of the defaults, so an omitted field keeps its
default:

| Field | Allowed values | Default |
| --- | --- | --- |
| `version` | `1` | `1` |
| `source_roots` | project-relative paths, at least one | `["."]` |
| `exclude` | glob patterns | `[".git/**", ".godot/**", ".gdkit/**", "addons/**"]` |
| `line_width` | `1` or more | `100` |
| `tab_width` | `1` or more | `4` |
| `indent` | `"tabs"`, `"spaces"` | `"tabs"` |
| `quote_style` | `"double"`, `"single"`, `"preserve"` | `"double"` |
| `comment_spacing` | `"normalize"`, `"preserve"` | `"normalize"` |
| `operators` | `"words"`, `"preserve"` | `"words"` |
| `numbers` | `"normalize"`, `"preserve"` | `"normalize"` |
| `trailing_commas` | `"when-broken"`, `"never"` | `"when-broken"` |
| `blank_lines.top_level` | `1` or more | `2` |
| `blank_lines.nested` | `1` or more | `1` |

- `line_width` is the column budget a line is kept within where possible. A
  line that has no place to break, such as a long name or string, stays long.
- `tab_width` is the columns a tab occupies when a line is measured, and the
  number of spaces per level when `indent` is `"spaces"`.
- `operators` set to `"words"` writes `and`, `or`, and `not` in place of `&&`,
  `||`, and `!`.
- `numbers` set to `"normalize"` rewrites literals such as `.5` and `0XFF` as
  `0.5` and `0xff`.
- `trailing_commas` set to `"when-broken"` adds a trailing comma to a list that
  spans several lines.
- `blank_lines.top_level` is the exact number of blank lines around top-level
  function and class declarations, and `blank_lines.nested` is the most
  consecutive blank lines kept anywhere else.
- `exclude` uses the same glob syntax as the architecture configuration.
  `gdkit format check` and `gdkit format write` also skip the paths listed in
  [`.gdkitignore`](#ignoring-files-with-gdkitignore).

## UID sidecars

Godot 4 gives every script a `uid://` identity and keeps it in a `.uid` file
beside the source, so a scene can reference the script by identity rather than
by path. The editor creates those sidecars while it scans the filesystem, which
means a script added without the editor open — by a generator, a merge, or a
`git mv` — has none until someone next opens the project. Until then, anything
that references it by `uid://` cannot resolve it, and `gdkit arch check`
reports a `resource.missing` error for the dangling reference.

`gdkit uid` creates the missing sidecars itself:

```sh
# Report scripts whose identity is missing or unusable, writing nothing
gdkit uid check /path/to/godot-project

# Create the sidecars Godot would have created
gdkit uid write /path/to/godot-project

# Refuse to run unless the binary is at least 0.3.0
gdkit uid check --minimum-version 0.3.0 /path/to/godot-project
```

`check` exits 1 when it finds a problem, so it works as a CI gate. `write`
creates every missing sidecar and exits 1 if it had to leave a problem behind.

Identifiers are generated exactly as Godot generates them: 63 random bits,
rendered in base 34 over the alphabet `abcdefghijklmnopqrstuvwxy012345678`.
That alphabet is missing `z` and `9` because Godot's own encoder is off by one,
a bug it
[cannot fix](https://github.com/godotengine/godot/issues/83843) without
invalidating every identifier ever written. A sidecar `gdkit` writes is
byte-identical in form to one the editor writes, down to the trailing newline.
An identifier is random rather than derived from the path, so two runs produce
different ones, and a generated identifier never collides with one already
present in the project.

### UID diagnostics

| Rule            | Meaning                                                          |
| --------------- | ---------------------------------------------------------------- |
| `uid.missing`   | The script has no `.uid` sidecar, so it has no stable identity.   |
| `uid.malformed` | The sidecar does not hold an identifier Godot could have written. |
| `uid.duplicate` | Two scripts' sidecars claim the same identifier.                  |

A malformed or duplicated sidecar is reported but not rewritten, because a new
identifier changes what every existing `uid://` reference to that script
resolves to and `gdkit` does not rewrite references. Pass `--repair` to reissue
them anyway:

```sh
gdkit uid write --repair /path/to/godot-project
```

With `--repair`, a malformed sidecar is replaced, and for a duplicated
identifier the first claimant in path order keeps it while the rest are
reissued. Check the result before committing it, and grep for the old
identifiers if anything else in the project might still point at them.

`gdkit uid` has no configuration file. It covers `.gd` files only — Godot also
writes sidecars for shaders, which `gdkit` does not parse and so does not speak
for — and it skips the paths listed in
[`.gdkitignore`](#ignoring-files-with-gdkitignore), so a vendored script is
left without an identity just as it is left unlinted.

## Code generation

`gdkit gen` writes boilerplate value-object methods into a class that asks for
them, inside a region it owns:

```gdscript
class_name Coordinate
extends RefCounted

# gdkit:generate = to_string, equals
var q: int
var r: int
var _cache: Dictionary  # gdkit:generate:ignore-field


# gdkit:generated:begin
func _to_string() -> String:
	return "Coordinate(q=%s, r=%s)" % [self.q, self.r]


func equals(p_other: Variant) -> bool:
	if not p_other is Object:
		return false
	if p_other.get_script() != get_script():
		return false
	return self.q == p_other.q and self.r == p_other.r


# gdkit:generated:end
```

```sh
gdkit gen check /path/to/godot-project          # report, write nothing
gdkit gen check --diff /path/to/godot-project   # show what would change
gdkit gen write /path/to/godot-project          # write the regions
gdkit gen init /path/to/godot-project           # write the default config
```

GDScript requires a class's methods to live in that class's one script file, so
there is no sibling-file equivalent of `go generate` here: the methods land in
your own script, between sentinels, and `gen` owns everything between them. Run
`gen write` on a clean tree and review the diff, as with `format write`.

### Opting in

| Form | Where | Meaning |
| --- | --- | --- |
| a `generate` entry in `.gdkit/generate.json` | configuration | opts matching files in |
| `# gdkit:generate = to_string, equals` | a class body, on its own line | opts this class in |
| `# gdkit:generate:ignore` | a class body, on its own line | opts this class out |
| `# gdkit:generate:ignore-field` | on, or above, a `var` | excludes that field |

Precedence is `ignore` > directive > configuration: an opt-out configuration can
override is not an opt-out. The class-level and field-level opt-outs are spelled
differently on purpose — one spelling for both would be ambiguous above a
class's first field.

A directive counts only when it is a comment in the class body itself. One
inside a function, or inside an inner class, does not opt anything in; a marker
on an inner class is reported, because an inner class cannot be generated for.

Fields are every member `var` the class declares, in declaration order. A
`const`, a `static var`, and an `@onready var` are never included — the last is
node wiring rather than state, and is null before `_ready`.

### What `equals` compares

The guard is script identity rather than `is <ClassName>`, for two reasons: it
works for a class that declares no `class_name`, and it is symmetric, so
`a.equals(b)` and `b.equals(a)` always agree. `is` answers true one way and
false the other across a subclass.

A subclass **composes** with its parent: the generated method calls
`super.equals(p_other)` before comparing its own fields, so every field in the
ancestry is compared by the class that declares it. Where that is not possible —
a base class with fields that has no `equals` of its own — the subclass is
refused rather than generating a comparison that silently ignores inherited
state. The diagnostic names the class that needs to opt in.

For the same reason a class is refused when a subclass elsewhere would inherit
its `equals` while adding fields of its own. Opting that subclass in clears it.

`equals` compares an object-valued field **by reference**, because that is what
`==` does to an `Object` in Godot 4. `Array` and `Dictionary` fields compare by
value, because that is what `==` does to those. For a class that holds another
value object, that is usually not the answer you want — use `deep_equals`.

### What `deep_equals` compares

`deep_equals` asks each value what it can do instead of comparing it with `==`:

```gdscript
if self.position != p_other.position:
	if self.position == null or p_other.position == null:
		return false
	if self.position is Object and self.position.has_method("deep_equals"):
		if not self.position.deep_equals(p_other.position):
			return false
	elif self.position is Object and self.position.has_method("equals"):
		if not self.position.equals(p_other.position):
			return false
	else:
		return false
```

So two distinct instances carrying equal values compare equal, which is the
whole point and what `equals` gets wrong. A field whose type has only a
hand-written `equals` is used rather than refused.

The method opens with `if self == p_other: return true` — the same instance is
strictly equal. That is also what makes the realistic recursive shapes
terminate: a self-reference, and a sub-object both sides share, are settled
without recursing.

What it will not do is compare two **independently built cyclic** graphs, where
no pair of instances is ever identical and the recursion never bottoms out. A
cyclic value object is pathological, so `gen` refuses the class instead:

```
node.gd:1: Error: deep_equals cannot be shown to terminate: this class's field
types form a cycle through node.gd (generate.unsupported)
```

That check reads declared field types, so a field with no type, an explicit
`Variant`, or a `:=`-inferred type is invisible to it; an untyped field in a
cycle will still exhaust the stack. A field typed `Array[Branch]` is **not** a
cycle — a container is handed to `==`, which Godot 4 evaluates by value, so the
generated code never recurses into its elements.

### Generation diagnostics

| Rule | Reports |
| --- | --- |
| `source-parse` | a file that could not be parsed |
| `class_name.duplicate` | two scripts claiming one `class_name` |
| `generate.stale` | a region that is missing or out of date |
| `generate.marker` | a malformed directive, an unknown generator name, or a second region in one class |
| `generate.conflict` | a method of the same name already declared with a different signature |
| `generate.unsupported` | a class `gen` refuses, with the reason: an unopened ancestor, a field-adding subclass, or a cyclic field-type graph |
| `generate.orphaned` | a region whose class no longer opts in |
| `generate.unsafe` | a rewrite that verification refused |

`equals` needs the whole inheritance graph to be visible, so a parse failure
*anywhere* in the project blocks it — an unreadable file could be a subclass
that adds fields, and nothing in the base names it. `to_string` does not depend
on the graph and is unaffected.

An orphaned region is **kept** and reported, not deleted: removing it is
`gen write --prune`, so editing a glob never destroys code as a side effect.

### Generation configuration

`.gdkit/generate.json`, written by `gdkit gen init`:

```json
{
  "version": 1,
  "source_roots": ["."],
  "exclude": [".git/**", ".godot/**", ".gdkit/**", "addons/**"],
  "generate": [
    { "paths": ["**/domain/value/*.gd"], "generators": ["to_string", "equals"] }
  ]
}
```

A file matching several entries gets the **union** of their generators, so
capability is added by adding a rule and reordering the list changes nothing.

`gen` reads `.gdkit/format.json` as well, and the region it writes is canonical
in your project's style — `gen write` introduces no new `format check` finding.
It does not reformat the rest of the file: every byte outside the region is left
exactly as it was.

## Ignoring files with .gdkitignore

A file named `.gdkitignore` at the project root lists paths that `gdkit lint`,
`gdkit format`, and `gdkit uid` skip, so third-party and generated code is
named once for every tool:

```gitignore
# Vendored plugins, wherever they sit in the tree
addons/
# Generated protocol code
*.pb.gd
```

`gdkit arch` does not read it. Hiding a file from the architecture analyzer
would remove its `class_name` from the index, and every reference to that class
would then be misreported. Use the architecture configuration's `exclude` to
change what `gdkit arch` analyzes.

A project without the file ignores nothing. Only the file at the project root is
read; a `.gdkitignore` in a subdirectory has no effect. A pattern that cannot be
parsed is a configuration error that names its line, and the command exits `2`.

The syntax is that of `.gitignore`:

- One pattern per line. Blank lines are skipped, and a line that starts with `#`
  is a comment. There are no trailing comments; write `\#` for a pattern that
  starts with a hash. Trailing spaces are dropped unless escaped with a
  backslash. CRLF line endings and a leading UTF-8 byte order mark are accepted.
- A leading `!` negates the pattern and re-includes what it matches. Write `\!`
  for a pattern that starts with an exclamation mark.
- A trailing `/` makes the pattern match directories only.
- A pattern with no other slash matches a name at any depth: `addons/` matches
  `addons` and `apps/editor/addons`, and `*.pb.gd` matches in every directory.
- A pattern with a leading or interior slash is anchored to the project root:
  `/client/protocol/`, `client/protocol/*.gd`.
- `*` and `?` do not cross `/`. `[abc]`, `[a-z]`, and `[!a]` are character
  classes, and a class never matches `/`. A leading `**/` matches in any
  directory, a trailing `/**` matches everything inside, and `/**/` matches
  zero or more directories. A backslash escapes the next character.
- Matching is case-sensitive, whatever the filesystem.
- Patterns are evaluated in order and the last one that matches decides.

The matcher differs from git in four ways:

- Matching is case-sensitive whatever the filesystem; git follows
  `core.ignoreCase`.
- `?` and a character class match one character. Git matches one byte, so the
  two disagree on names outside ASCII: `?.gd` matches `é.gd` here and not in
  git.
- A malformed pattern is an error with its line number, where git accepts it
  silently: an unterminated character class, a range that runs backwards
  (`[z-a]`), an unknown class name (`[[:word:]]`), a class that could only match
  `/` (`[/]`), a lone `!`, and a trailing lone backslash.
- A negated pattern can re-include something inside an ignored directory. A path
  is tested as itself and through each of its ancestor directories, and the last
  pattern that matches the path or any ancestor decides.

```gitignore
addons/
!addons/our_plugin/
```

This ignores every `addons` directory at any depth except the root-level
`addons/our_plugin/`, whose files are processed. The second pattern contains an
interior slash, so it is anchored to the project root and does not re-include
`apps/editor/addons/our_plugin/`.

`.gdkitignore` combines with each tool's `exclude` setting: a path is skipped
when `exclude` covers it or `.gdkitignore` ignores it. A negated pattern does
not override `exclude`, so re-including a directory under the root `addons/`
also requires removing `addons/**` from the default `exclude` of that tool.

## Continuous integration

Every check command is a gate as it stands: it writes nothing, exits non-zero on
findings, and sorts its output so two runs over the same source produce the same
bytes. A GitHub Actions job needs no wrapper:

```yaml
name: gdkit

on:
  push:
    branches: [main]
  pull_request:

jobs:
  gdkit:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - uses: actions/setup-go@v6
        with:
          go-version: "1.26.x"
      - run: go install github.com/cafecito-games/gdkit/cmd/gdkit@v0.4.0

      - run: gdkit arch check .
      - run: gdkit lint check .
        if: ${{ !cancelled() }}
      - run: gdkit format check --diff .
        if: ${{ !cancelled() }}
      - run: gdkit uid check .
        if: ${{ !cancelled() }}
```

Two details make this hold up over time:

- **Pin the binary.** Installing `@v0.4.0` rather than `@latest` keeps CI and
  every developer machine on one gdkit, so the two cannot reach different
  verdicts over the same source. Add
  [`--minimum-version`](#pinning-the-gdkit-version), or
  `minimum_gdkit_version` in the configuration, as defense in depth for the
  machines a pin does not reach.
- **`!cancelled()` reports everything once.** Without it the job stops at the
  first tool that fails, and a contributor fixes one class of finding per push.

Exit codes are the same for every check command:

| Exit | Meaning |
| --- | --- |
| `0` | clean — no findings, or `lint` findings that are all warnings |
| `1` | findings: a violation, a diagnostic, or a file that would be reformatted |
| `2` | a configuration, usage, or I/O failure; no report was produced |

For an annotating or reporting job, `--format json` carries the same
diagnostics with paths, 1-based rune columns, and rule names, and a failure
before the report is a single [error envelope on
stderr](#machine-readable-failures) while stdout stays empty — so a consumer can
tell "no report" from "an empty report".

`gdkit format write` and `gdkit uid write` are the two commands meant to run
outside CI, on a developer machine or in a job that opens a pull request. In a
gate, use their `check` form.

## Version information

Release binaries report their semantic version, commit hash, commit timestamp,
tree state, and Go toolchain. Text and machine-readable forms are available:

```sh
gdkit --version
gdkit version
gdkit version --format json
```

A tagged binary prints output similar to:

```text
gdkit version 1.2.3 (commit 0123456789ab), built 2026-09-30T12:00:00Z
```

GoReleaser injects authoritative release metadata through linker flags. Normal
`go build` installations fall back to the VCS metadata embedded by the Go
toolchain, so development binaries remain identifiable too.

## Machine-readable failures

With `--format json`, a run that fails before it can produce a report writes a
single error object to **stderr** and exits `2`. stdout stays empty, so a
consumer can tell "no report" from "an empty report".

```json
{
  "error": {
    "kind": "config.unknown_key",
    "message": "unknown key \"classifications[0].layre\" in architecture config",
    "path": ".gdkit/architecture.json",
    "key": "classifications[0].layre"
  }
}
```

`kind` is a public contract, like a rule name: it appears in output and a
consumer branches on it, so it is not renamed. `message` is the same text the
command writes in text mode, so the two modes never describe a failure
differently. `path` and `key` appear when the failure locates to a file or a
configuration key.

| Kind | Meaning |
| --- | --- |
| `config.read` | the configuration file could not be read |
| `config.parse` | the file is not well-formed JSON, or holds more than one JSON value |
| `config.unknown_key` | a key the schema does not define; `key` names it |
| `config.invalid` | the values do not validate |
| `config.version_floor` | `minimum_gdkit_version` is newer than this binary |
| `usage.arguments` | wrong arguments, or a flag combination that cannot be honored |
| `usage.format` | an unknown `--format` value |
| `usage.version_floor` | `--minimum-version` is not satisfied |
| `project.load` | the project could not be walked or read |
| `analysis.failed` | analysis itself failed |
| `output.write` | the report could not be written |
| `file.write` | a file the command was asked to create could not be written |

Three cases are deliberately **not** enveloped, because the envelope cannot be
promised for every exit-`2` path:

- **An unknown `--format` value**, which is reported as text. A consumer that
  misspelled the format cannot be assumed to parse the envelope it asked for by
  mistake.
- **An unknown command or subcommand, and a flag parse error**, which happen
  before `--format` has been read at all.
- **The `init` commands**, which have no `--format`.

`format write` and `uid write` are one further exception in the other
direction: a write that fails part-way still reports on stdout which files it
changed before failing, alongside the envelope on stderr, because that list is
what tells you the state the project is now in.

## Pinning the gdkit version

A project's configuration can depend on behavior a particular release
introduced. When CI runs one gdkit and a developer's machine has another, the
two can reach different verdicts over the same source — most awkwardly when the
older binary is the more permissive one.

**Pin the binary to prevent this.** A version pin is the mechanism that keeps
every machine on one gdkit; the gate described below only *detects* a mismatch
after the fact, and cannot help if nobody added it to the command. Pin with the
Go toolchain:

```sh
go run github.com/cafecito-games/gdkit/cmd/gdkit@v0.2.0 arch check .
```

A `Makefile` or `justfile` target that spells out the version keeps every
machine, and CI, on the same binary. A toolchain manager such as `mise` or
`asdf`, or a devcontainer image, pins it the same way for a team that installs
gdkit from Homebrew rather than through the Go toolchain.

**Gate on the version as defense in depth.** Where pinning is not in place — a
stale `brew install`, a contributor's older binary — a floor makes the run fail
closed rather than enforce weaker rules quietly. Every check and write command
accepts the flag:

```sh
gdkit arch check --minimum-version 0.2.0 .
```

The architecture configuration can carry the same requirement, which keeps it
beside the settings that needed it and applies to every invocation without each
caller remembering a flag:

```json
{
  "version": 1,
  "minimum_gdkit_version": "0.2.0"
}
```

Both forms are a **minimum**, not an exact match: a newer gdkit satisfies them,
so a patch release does not break every developer at once. Both are checked
before any configuration is read and before any source is parsed, so a binary
below the floor exits `2` with a message naming the required and detected
versions, and emits no report:

```text
gdkit: --minimum-version 0.3.0 requires gdkit 0.3.0 or newer, but this binary is 0.2.0
gdkit: .gdkit/architecture.json requires gdkit 0.3.0 or newer, but this binary is 0.2.0
```

The value is a `major.minor.patch` triple. A leading `v`, a missing component,
or a prerelease suffix is a configuration error rather than a silently accepted
approximation. On the detected side, a prerelease or build metadata suffix is
ignored, so a snapshot build of `0.2.1` satisfies a floor of `0.2.1`.

A development build — a local `go build` or `go run` with no release version —
satisfies no floor, because the point of the gate is to fail closed on a binary
nobody audited. Contributors who need to work inside a project that pins a
floor can set `GDKIT_ALLOW_DEV_VERSION=1`, which excuses development builds
only; it never excuses a release below the floor.

Both failure directions are covered. A gdkit older than `--minimum-version`
rejects the unknown flag, and a gdkit that predates `minimum_gdkit_version`
rejects it as an unknown configuration key, so neither can run with weaker
semantics than the configuration expects.

### What a release may change

A floor is only meaningful if gdkit says what a release is allowed to do:

- A release may **add** checks, but a new lint rule ships inert and does not
  change what a project reports until it opts in; see
  [Rules that ship inert](#rules-that-ship-inert). A fix that widens what an
  existing rule catches arrives the same way, as a new inert rule name.
- Enforcement may **not** become more permissive within a minor line. Removing
  a diagnostic, widening what a dependency rule allows, or changing what
  existing configuration syntax means requires a minor bump before 1.0.
- Rule names are a public contract and are not renamed; they appear in JSON
  output, in configuration, in allowlist exceptions, and in `# gdkit:ignore`
  comments.

So a floor guarantees the binary is no more permissive than the release the
project audited, which is the property a repository gate needs. The inert-by-
default rule above is what makes the stricter direction safe too: a project
that pins nothing still keeps its verdict across an upgrade.

Architecture checks do not yet have the inert-by-default mechanism, because no
release has needed to change one's semantics. Until they do, the floor is the
only guard there.

## Architecture configuration

Create editable starter files in a Godot project:

```sh
gdkit arch init /path/to/godot-project
```

This writes `.gdkit/architecture.json` and `.gdkit/allowlist.json`. Existing
files are preserved unless `--force` is supplied.

Classification rules are evaluated in order. `**` crosses directories, `*`
matches within one path segment, and `{feature}` captures one path segment for
the feature name. The built-in configuration recognizes layouts such as:

```text
features/combat/domain/health.gd
features/combat/application/ports/health_reader.gd
features/combat/application/read_models/health_view.gd
features/combat/presentation/health_panel.gd
features/combat/infrastructure/godot_health_reader.gd
features/combat/bootstrap/combat_composition_root.gd
shared/domain/result.gd
```

Its dependency rules express this policy:

| Source | Allowed targets |
| --- | --- |
| domain | same-feature and shared domain |
| application | same-feature/shared domain; application ports; shared application |
| presentation | same-feature/shared application read models and intents; same-feature/shared presentation |
| infrastructure | same-feature/shared domain; application ports; generated protocol infrastructure |
| bootstrap | every classified layer and feature |

Each entry in `dependencies` is an alternative allow rule. A dependency is
accepted when one complete rule matches it.

```json
{
  "from_layers": ["presentation"],
  "to_layers": ["application"],
  "to_features": ["same", "shared"],
  "to_paths": ["**/read_models/**", "**/intents/**", "**/shared/application/**"]
}
```

`"same"` means the source file's feature and `"*"` means any value. Optional
`from_features`, `from_paths`, `to_paths`, and `except_to_paths` fields can
narrow a rule. `from_paths` scopes the rule's *source* the way `to_paths`
scopes its target, using the same glob syntax; omitting it means any file in
`from_layers`, so adding the key can only make a rule match fewer
dependencies. That is what lets a hexagonal port be granted a narrow
permission without widening its whole layer:

```json
{
  "from_layers": ["application"],
  "from_paths": ["**/ports/**"],
  "to_layers": ["application"],
  "to_features": ["same", "shared"],
  "to_paths": ["**/read_models/**", "**/intents/**"]
}
```

Set `unclassified` to `"ignore"` only when incremental adoption is intentional.

The configuration is read strictly, because a rule you believe you wrote but
that is not in effect is worse than no rule at all:

- An unknown key anywhere in the file — at the top level, inside a
  `dependencies` rule, or inside a `classifications` entry — is a
  configuration error naming the key's full JSON path, such as
  `unknown key "classifications[11].layre" in architecture config`.
- A `classifications` entry must declare `pattern`, `layer`, and `feature`.
  There is no default layer; a missing one is a configuration error rather
  than a silent reclassification of the directory tree it matches.
- An optional `minimum_gdkit_version` declares the oldest gdkit release whose
  behavior this configuration was written against, as a `major.minor.patch`
  triple. An older binary fails rather than enforcing the rules it happens to
  understand; see [Pinning the gdkit version](#pinning-the-gdkit-version).
  `gdkit arch init` does not write the key, because a generated configuration
  must not pin itself to whichever binary generated it.

These all fail with exit code `2` before any file is analyzed, and with
`--format json` each reports a `kind` a consumer can branch on; see
[Machine-readable failures](#machine-readable-failures). The version floor is
reported ahead of the others: a configuration written for a newer gdkit
normally carries both the floor and the syntax that needed it, and naming the
unknown key would describe a typo instead of a binary that is too old.

Runtime boundaries and test discovery are path patterns:

```json
{
  "runtime_boundaries": ["**/runtime_boundaries/**"],
  "test_patterns": ["**/test/**", "**/tests/**", "**/*_test.gd"]
}
```

Pass an alternate configuration with `--config`, relative to the project root.

## ADR-backed exceptions

The allowlist is an escape hatch, not an undocumented bypass. Every exception
must have a reason and a project-relative ADR path that exists. An invalid,
missing, or expired ADR makes the allowlist itself fail and leaves the original
violation active.

```json
{
  "version": 1,
  "exceptions": [
    {
      "rule": "dependency.direction",
      "from": "features/inventory/presentation/legacy_panel.gd",
      "to": "features/inventory/infrastructure/legacy_gateway.gd",
      "symbol": "LegacyGateway",
      "reason": "Temporary seam while the gateway port is extracted",
      "adr": "docs/adr/0042-inventory-gateway-migration.md",
      "expires_on": "2027-03-31"
    }
  ]
}
```

`from` and `to` accept the same glob syntax as configuration paths. `to`,
`symbol`, and `expires_on` are optional; keeping them as specific as possible
prevents an exception from hiding unrelated violations.

## Library use

The analyzer is also a reusable Go package:

```go
config, err := architecture.LoadConfig(projectRoot, "")
analyzer, err := architecture.NewAnalyzer(projectRoot, config)
report, err := analyzer.Analyze()
```

`Report` contains deterministic file, edge, and diagnostic lists suitable for
editor integrations, CI annotations, graph export, or later code-transformation
tools.

## Development

```sh
gofmt -w .
go test -race ./...
go vet ./...
go build ./...
```

Validate the release configuration and build local snapshot artifacts:

```sh
goreleaser check
goreleaser release --snapshot --clean
```

## Releasing

Create a release entirely from GitHub:

1. Open the repository's **Actions** tab.
2. Select the **Release** workflow.
3. Choose **Run workflow**, select `patch`, `minor`, or `major`, and run it.

The workflow always releases the latest commit on the default branch. It runs
the tests, reads the latest published stable release, computes the next semantic
version, creates its annotated tag, and publishes the GitHub Release in the same
job. From `v0.4.2`, the choices produce `v0.4.3`, `v0.5.0`, or `v1.0.0`.
With no existing release, the calculation starts at `v0.0.0`.

Because the calculation uses the latest successfully published release, a
failed publishing attempt can be retried with the same increment as long as its
tag still points to the current default-branch commit.

The release contains macOS, Linux, and Windows archives for AMD64 and ARM64,
plus a SHA-256 checksum manifest. GoReleaser also updates the `gdkit` cask in
[cafecito-games/homebrew-tap](https://github.com/cafecito-games/homebrew-tap),
using the `HOMEBREW_TAP_TOKEN` secret to push to that repository. Release
versions omit the leading `v`, so tag `v0.1.0` is reported by the binary as
`0.1.0`. Directly pushed `v*` tags remain supported for automation and advanced
use.

## License

MIT — see [LICENSE](LICENSE). Copyright Cafecito Games LLC.
