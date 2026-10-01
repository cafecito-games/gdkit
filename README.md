# gdkit

`gdkit` is a Go 1.26 toolkit for static analysis and source processing of
Godot 4 GDScript. `gdkit arch` enforces architectural boundaries across a
project, and `gdkit lint` checks GDScript style and correctness.

The analyzer uses [`gdparser`](https://github.com/cafecito-games/gdparser) and
does not search source text with regular expressions. It parses GDScript,
indexes global `class_name` declarations, resolves semantic identifier and type
references, and inspects static `load()` and `preload()` calls. Comments and
string contents cannot accidentally create class dependencies.

## Install

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
with discoverable `.uid` sidecars are resolved. Dynamic resource paths and
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
syntax as the architecture configuration.

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
plus a SHA-256 checksum manifest. Release versions omit the leading `v`, so tag
`v0.1.0` is reported by the binary as `0.1.0`. Directly pushed `v*` tags remain
supported for automation and advanced use.
