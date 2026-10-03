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
GDKIT_CORPUS=/path/to/project go test -race ./lint -run TestCorpus

# Run the CLI from the checkout
go run ./cmd/gdkit arch check /path/to/godot-project
go run ./cmd/gdkit arch check --format json --show-edges .
go run ./cmd/gdkit lint check /path/to/godot-project
go run ./cmd/gdkit format check /path/to/godot-project
go run ./cmd/gdkit format check --diff /path/to/godot-project
go run ./cmd/gdkit format write /path/to/godot-project
go run ./cmd/gdkit uid check /path/to/godot-project
go run ./cmd/gdkit uid write --repair /path/to/godot-project

# Release packaging (same commands CI runs)
goreleaser check
goreleaser release --snapshot --clean
```

CI (`.github/workflows/ci.yml`) runs `go test -race`, `go vet`, `go build`, and a
GoReleaser snapshot on every pull request and every push to `main`. Releases are
cut by manually dispatching the **Release** workflow with `patch`/`minor`/`major`;
it computes the next version from the latest published GitHub Release and tags
the default branch.

## Architecture

`gdkit` is a static-analysis toolkit for Godot 4 GDScript. Twelve packages:

- `architecture/` — the dependency analyzer: layer and feature boundaries, cycles,
  and engine purity (the substance of `gdkit arch`).
- `lint/` — the linter: a registry of single-file rules over a `project.Snapshot`.
  Rule names are a public contract; they appear in JSON output, in config, and in
  inline ignore comments, so renaming one breaks user projects.
- `format/` — the formatter: drives gdparser's formatter over a `project.Snapshot`
  and verifies every rewrite before offering it. Verification covers the syntax
  tree (the reparsed output must keep it), the token stream (no token other than
  layout, parentheses, commas, and semicolons may appear, vanish, or change),
  and suppression placement (the code a lint suppression comment applies to
  must not change). It is pure except for `LoadConfig` and `Apply`; `Format`
  performs no I/O.
  `format.unsafe` and `source-parse` are its public diagnostic names and appear in
  JSON output.
- `uid/` — the `uid://` identifier codec and generator behind `gdkit uid`. It
  reproduces Godot's `ResourceUID` exactly, including the base-34 alphabet that
  omits `z` and `9` because Godot's own encoder is off by one and cannot be
  fixed; see the comment on `alphabet`. Ids are random, not derived from the
  path, so `Generator` takes an `io.Reader` and tests seed it. `Check` is pure
  and `Apply` is the only writer. `uid.missing`, `uid.malformed`, and
  `uid.duplicate` are its public diagnostic names.
- `project/` — discovery and parsing. The only package that reads a project from
  disk, so every tool agrees on scope and parses once. `Config.HonorIgnoreFile`
  is how lint, format, and uid share the root `.gdkitignore`; `architecture`
  leaves it off, because hiding a file would drop its `class_name` from the
  index. `Snapshot.UIDs` resolves an identifier to one path and covers every
  place Godot declares one — a `.uid` sidecar, a `.tscn` or `.tres` header, or a
  `.import` file — so a sidecar is not the only way a `uid://` load resolves; it
  loses malformed and duplicated sidecars, while `Snapshot.Sidecars` keeps every
  `.uid` file as read, which is what `uid` reports on.
- `internal/glob/` — the shared glob engine.
- `internal/versiongate/` — the `major.minor.patch` comparison behind
  `--minimum-version` and `minimum_gdkit_version`. Comparison is on the numeric
  triple only; prerelease and build metadata are ignored, so a GoReleaser snapshot
  of `0.2.1` satisfies a floor of `0.2.1`. `ParseRequirement` is deliberately
  stricter than `Parse`: a floor is hand-written, so a leading `v` or a prerelease
  suffix is an error, while a version a binary reports about itself is tolerated.
- `internal/ignore/` — the gitignore-style matcher behind `.gdkitignore`. It
  differs from git in four documented ways: a negated pattern can re-include a
  path inside an ignored directory; matching is case-sensitive whatever the
  filesystem; `?` and character classes match one character, not one byte; and
  a malformed pattern is an error naming its line where git accepts it silently.
- `internal/suppression/` — the lint suppression directive grammar, shared by
  `lint`, which obeys the comments, and `format`, which must not change what
  they cover.
- `internal/textdiff/` — the unified diff behind `gdkit format check --diff`.
- `cmd/gdkit/` — flag parsing, output formatting, and exit codes only. It holds no
  analysis logic; it loads a `Config`, builds an `Analyzer`, `Linter`, or `Formatter`,
  and prints a `Report`.
- `internal/buildinfo/` — version metadata, injected by GoReleaser `-ldflags` and
  falling back to the Go toolchain's embedded VCS settings for local builds.

`tab-characters` is a configuration value used by `max-line-length`, not a rule.
`source-parse` and `unknown-ignore` are reported by the driver rather than by a
registered rule. Several rules encode deliberately unusual behavior — token-based
rather than scope-based name counting in `unused-argument`, token-stream
comparison in `comparison-with-itself`, annotation re-pairing in
`class-definitions-order`, and a static function that `missing-docstring`
checks while `max-public-methods` does not count it. The comments in `lint/`
record why; do not "simplify" them without reading those.

### Analysis pipeline

`Analyzer.Analyze()` (`architecture/analyzer.go`) runs a fixed sequence, and
everything downstream depends on the ordering:

1. **Load allowlist** — invalid exceptions become `allowlist.adr` diagnostics and are
   dropped, so a broken allowlist never suppresses the violation it was meant to cover.
2. **Discover** — walk `SourceRoots`, skipping `Exclude` matches, collecting `.gd`
   files and building a `uid://` → path map from `.uid` sidecars, `.tscn` and
   `.tres` headers, and `.import` files.
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
defaults, so omitted fields inherit the built-in policy — but every list field is
cleared before decoding and restored afterwards only when the file omits its key.
`encoding/json` decodes an array element onto whatever the slice already holds at
that index, so without that a declared rule would silently inherit fields from the
default rule sitting at the same position.

- **Classification** rules are evaluated in order; first match wins. `{feature}`
  captures exactly one path segment and must appear once in the pattern when the
  rule's `feature` is `{feature}`. `pattern`, `layer`, and `feature` are all
  required: there is no default layer, because defaulting one reclassifies a whole
  directory tree and the diagnostics then describe a layering nobody configured.
- **Dependencies** are alternative *allow* rules — a dependency is permitted when one
  complete rule matches it. Add capability by adding a rule, never by loosening an
  existing one. `"same"` means the source file's own feature; `"*"` means any.
  `from_paths` and `to_paths` scope a rule's source and target; an omitted list means
  any file the rule's layers and features already allow, so either key can only
  narrow the rule it appears on.
- **Unknown keys are rejected** at every level. `checkUnknownKeys` walks the raw
  document against `Config`'s own reflected shape and names the offending key's full
  JSON path (`classifications[11].layre`), because `DisallowUnknownFields` reports
  the key alone, which does not locate a typo in a large config.
- **`minimum_gdkit_version` is checked before `checkUnknownKeys` and before
  `Validate()`, and the ordering is load-bearing.** `checkMinimumVersion` decodes
  the key with a non-strict `json.Unmarshal` of its own for exactly that reason: a
  config written for a newer gdkit carries both the floor and the syntax that needed
  it, so checking later would report `unknown key "..."` and point the reader at a
  typo instead of a binary that is too old. `TestLoadConfigReportsTheMinimumVersionBeforeAnUnknownKey`
  guards it. The development-build bypass (`GDKIT_ALLOW_DEV_VERSION`) is read from
  the environment inside `LoadConfig` because the check has to live there to order
  correctly; `detectedVersion` is a package variable so tests can load a config as
  an arbitrary release would.
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
