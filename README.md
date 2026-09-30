# gdkit

`gdkit` is a Go 1.26 toolkit for static analysis and source processing of
Godot 4 GDScript. Its first tool, `gdkit arch`, enforces architectural
boundaries across a project.

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
3. Choose **Run workflow**, enter a semantic version such as `0.1.0`, and run it.

The workflow always releases the latest commit on the default branch. It runs
the tests, validates the version, creates the annotated `v0.1.0` tag, and
publishes the GitHub Release in the same job. A failed publishing attempt can be
retried with the same version as long as the tag still points to that commit.

The release contains macOS, Linux, and Windows archives for AMD64 and ARM64,
plus a SHA-256 checksum manifest. Release versions omit the leading `v`, so tag
`v0.1.0` is reported by the binary as `0.1.0`. Directly pushed `v*` tags remain
supported for automation and advanced use.
