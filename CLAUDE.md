# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```sh
gofmt -w .
go test -race ./...
go vet ./...
go build ./...

# One test or one package
go test -race ./architecture -run TestAnalyzerRejectsDirectionCyclesAndEngineAccess
go test -race ./lint -run TestName
go test -race ./lint -run TestDifferential              # needs gdlint on PATH
GDKIT_CORPUS=/path/to/project go test -race ./lint -run TestCorpus
GDKIT_PARITY_CORPUS=/path/to/project go test -race ./lint -run TestDifferentialCorpus

# Run the CLI from the checkout
go run ./cmd/gdkit arch check /path/to/godot-project
go run ./cmd/gdkit arch check --format json --show-edges .
go run ./cmd/gdkit lint check /path/to/godot-project

# Release packaging (same commands CI runs)
goreleaser check
goreleaser release --snapshot --clean
```

CI (`.github/workflows/ci.yml`) runs `go test -race`, `go vet`, `go build`, and a
GoReleaser snapshot on every push and pull request. Releases are cut by manually
dispatching the **Release** workflow with `patch`/`minor`/`major`; it computes the
next version from the latest published GitHub Release and tags the default branch.

## Architecture

`gdkit` is a static-analysis toolkit for Godot 4 GDScript. Six packages:

- `architecture/` — the dependency analyzer: layer and feature boundaries, cycles,
  and engine purity (the substance of `gdkit arch`).
- `lint/` — the linter: a registry of single-file rules over a `project.Snapshot`.
  Rule names are a public contract; they appear in JSON output, in config, and in
  inline ignore comments, so renaming one breaks user projects.
- `project/` — discovery and parsing. The only package that reads a project from
  disk, so both tools agree on scope and parse once.
- `internal/glob/` — the shared glob engine.
- `cmd/gdkit/` — flag parsing, output formatting, and exit codes only. It holds no
  analysis logic; it loads a `Config`, builds an `Analyzer` or `Linter`, and prints
  a `Report`.
- `internal/buildinfo/` — version metadata, injected by GoReleaser `-ldflags` and
  falling back to the Go toolchain's embedded VCS settings for local builds.

`gdkit lint` is meant to match `gdlint` from godot-gdscript-toolkit. Parity is
verified by the differential test (`TestDifferential`), and gdlint's own source is
the reference whenever a rule's behavior is in question. `tab-characters` is a
configuration value used by `max-line-length`, not a rule; `source-parse` and
`unknown-ignore` are reported by the driver rather than by a registered rule.

### Analysis pipeline

`Analyzer.Analyze()` (`architecture/analyzer.go`) runs a fixed sequence, and
everything downstream depends on the ordering:

1. **Load allowlist** — invalid exceptions become `allowlist.adr` diagnostics and are
   dropped, so a broken allowlist never suppresses the violation it was meant to cover.
2. **Discover** — walk `SourceRoots`, skipping `Exclude` matches, collecting `.gd`
   files and building a `uid://` → path map from `.uid` sidecars.
3. **Parse and index** — one `gdparser.ParseFile` per file; record `class_name`
   declarations into a global map and flag duplicates. Parse failures and unclassified
   files become diagnostics here.
4. **Inspect** — one `ast.Inspect` walk per file emits dependency `Edge`s (class
   references and static `load`/`preload`/`ResourceLoader.load` string literals) and
   per-file purity diagnostics (engine types, `get_tree()`, signals, test rules).
5. **Check** — `checkDependencies` and `checkCycles` (Tarjan SCC) in
   `architecture/checks.go` run over the deduplicated edge set.
6. **Suppress and sort** — allowlist filtering, then `Report.sort()`.

`Report` output must be deterministic: file, edge, and diagnostic lists are sorted by
path/line/column/rule so JSON output is diffable and usable for CI annotations.

### Key invariants

- **No regex over source text.** Dependencies come only from the parsed AST. The
  README promises comments and strings cannot create edges, and
  `TestAnalyzerIndexesReferencesAndIgnoresCommentsAndStrings` guards it.
- **Shadowing is resolved before edges are added.** `collectShadows` precomputes
  `(name, span)` ranges for locals, parameters, and member declarations;
  `isShadowed` suppresses both the edge and the engine-purity diagnostic for a
  shadowed identifier. A parameter named `InfraStore` is not a dependency on the
  class `InfraStore`.
- **Purity is derived from classification, not from paths at the check site.** A file
  is "pure" when its layer is `domain` or `application`; that single flag drives the
  `engine.reference`, `engine.tree`, `engine.signal`, `test.scene_load`, and
  `test.node_inspection` rules. `RuntimeBoundaries` is the only signal escape hatch.
- **Resource resolution distinguishes project dependencies from non-dependencies.**
  `resolveResource` returns `(target, isProjectResource)`. `user://`, other schemes,
  dynamic (non-literal) paths, and paths escaping the root are not project resources
  and create no edge and no diagnostic. Unresolvable `uid://` and missing `res://`
  targets are `resource.missing` errors.
- **Only `.gd` targets participate in cycle detection**; scene and resource edges are
  still reported but cannot form a cycle.

### Configuration model

`DefaultConfig()` in `architecture/config.go` is both the zero-config policy and the
content written by `gdkit arch init`. Config files are unmarshalled *onto* the
defaults, so omitted fields inherit the built-in policy.

- **Classification** rules are evaluated in order; first match wins. `{feature}`
  captures exactly one path segment and must appear once in the pattern when the
  rule's `feature` is `{feature}`.
- **Dependencies** are alternative *allow* rules — a dependency is permitted when one
  complete rule matches it. Add capability by adding a rule, never by loosening an
  existing one. `"same"` means the source file's own feature; `"*"` means any.
- **Patterns** are a custom glob (`architecture/pattern.go`) compiled to regex and
  cached: `**/` crosses directories, `*` and `?` stay within a segment, `{feature}`
  captures a segment. It is not `path/filepath.Match`.
- `Config.Validate()` runs from both `LoadConfig` and `NewAnalyzer`, and compiles
  every pattern in the config so bad globs fail as configuration errors, not silently.

### Diagnostic rules and exit codes

Rule names (`dependency.direction`, `dependency.cycle`, `classification.missing`,
`class_name.duplicate`, `source.parse`, `resource.missing`, `engine.reference`,
`engine.tree`, `engine.signal`, `test.scene_load`, `test.node_inspection`,
`allowlist.adr`) are the public contract: they appear in JSON output and are what
allowlist exceptions match on. Renaming one breaks existing project allowlists.

Exit codes: `0` clean, `1` architecture violations, `2` configuration/usage/IO
failure. `cmd/gdkit` returns `2` from every error path before analysis completes.

## Conventions

- Tests build throwaway Godot projects with `writeProject(t, t.TempDir(), …)` and run
  `analyzeDefault`, asserting on diagnostic rule names and edges rather than on
  formatted text. New checks belong in that style.
- Adding a check means: emit the `Diagnostic` with a stable `Rule`, `Symbol`, and
  `Target` (the allowlist matches on all three), and document it in the README's
  `arch check` list.
