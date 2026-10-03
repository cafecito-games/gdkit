# gdkit

`gdkit` is a Go 1.26 toolkit for static analysis and source processing of
Godot 4 GDScript. `gdkit arch` enforces architectural boundaries across a
project, `gdkit lint` checks GDScript style and correctness, `gdkit format`
rewrites GDScript into one canonical style, and `gdkit uid` gives a script the
`uid://` identity Godot would have given it.

The analyzer uses [`gdparser`](https://github.com/cafecito-games/gdparser) and
does not search source text with regular expressions. It parses GDScript,
indexes global `class_name` declarations, resolves semantic identifier and type
references, and inspects static `load()` and `preload()` calls. Comments and
string contents cannot accidentally create class dependencies.

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

## Architecture checks

`gdkit arch check` currently reports:

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
```

The command exits `0` when clean, `1` for architecture violations, and `2` for
configuration, usage, or I/O failures.

## Linting

`gdkit lint` reports 29 naming, structural, design, and formatting problems in
GDScript. Like the architecture analyzer, it works from the parsed AST and shares
its project discovery, so both tools agree on which files are in scope.

```sh
gdkit lint check .
gdkit lint check --format json .
gdkit lint check --disable max-line-length,max-file-lines .
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

Design limits:

- `max-returns`, `max-public-methods`, and `function-arguments-number`.

Format rules:

- `max-file-lines`, `max-line-length`, `trailing-whitespace`, and
  `mixed-tabs-and-spaces`.

Two further rules are reported by the driver rather than by a rule:

- `source-parse` reports a file that does not parse. Rules cannot run on it.
- `unknown-ignore` reports a suppression comment that names a rule that does not
  exist, so a misspelled name cannot silently suppress nothing.

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
| `disable` | none |
| `severity` | none; every rule is an `error` |
| `max-returns` | `6` |
| `max-public-methods` | `20` |
| `function-arguments-number` | `10` |
| `max-file-lines` | `1000` |
| `max-line-length` | `100` |
| `tab-characters` | `1` |

`disable` lists rules to turn off, and `--disable` takes the same names as a
comma-separated list in addition to the file. `exclude` uses the same glob
syntax as the architecture configuration. `gdkit lint check` also skips the
paths listed in [`.gdkitignore`](#ignoring-files-with-gdkitignore).

`tab-characters` is a setting and not a rule. `max-line-length` expands each
tab to that many spaces before measuring a line.

Each name rule has a key of the same name holding a regular expression that must
match the whole identifier, for example:

```json
{
  "class-name": "([A-Z][a-z0-9]*)+",
  "signal-name": "[a-z][a-z0-9]*(_[a-z0-9]+)*",
  "enum-element-name": "[A-Z][A-Z0-9]*(_[A-Z0-9]+)*"
}
```

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
  suppression comment applies to, and a file with a one-line class body that
  holds several members.

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

A file is refused as `format.unsafe` before it is formatted when it holds a
one-line class body with more than one member, such as
`class A: var v = 1; var u = 2`. Godot ends that body at its first member, so
`u` belongs to the enclosing script, while the parser reads both into `A`
([gdparser#75](https://github.com/cafecito-games/gdparser/issues/75)) and a
rewrite would move `u` into the class. Writing the class as a block, or the
second member on its own line, makes the file acceptable.

Two layouts are currently refused as `format.unsafe` because the formatted
output parses back to a different tree
([gdparser#77](https://github.com/cafecito-games/gdparser/issues/77)): a lambda
whose one-line body is a compound statement, such as
`func(): if a: return 1`, and an annotation on the same line as `class_name`,
such as `@abstract class_name X extends Node`. Writing the lambda body as a
block, or the annotation on its own line, avoids both.

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

## Configuration

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
`from_features`, `to_paths`, and `except_to_paths` fields can narrow a rule.
Set `unclassified` to `"ignore"` only when incremental adoption is intentional.

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
using the `HOMEBREW_TAP_TOKEN` secret to push to that repository. Release versions omit the leading `v`, so tag
`v0.1.0` is reported by the binary as `0.1.0`. Directly pushed `v*` tags remain
supported for automation and advanced use.
