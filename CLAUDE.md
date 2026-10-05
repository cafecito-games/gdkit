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

# Refresh the verified official Godot 4.7 engine schema
go run ./internal/semantic/engineschema/cmd/generate \
  -input /path/to/extension_api.json \
  -output internal/semantic/engineschema/data/godot_4_7.json.gz \
  -source-commit ed1daf0bf \
  -raw-sha256 d0e4c08c03b165156dabe6bfb6a906baf0069189f62035341230a246c86d6986
# The command prints every pinned digest/size. When changing the input, follow
# internal/semantic/engineschema/data/README.md's complete refresh checklist.

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

`gdkit` is a static-analysis toolkit for Godot 4 GDScript. Its packages are:

- `architecture/` — the dependency analyzer: layer and feature boundaries, cycles,
  and engine purity (the substance of `gdkit arch`).
- `lint/` — the linter: a registry of single-file rules over a `project.Snapshot`.
  Rule names are a public contract; they appear in JSON output, in config, and in
  inline ignore comments, so renaming one breaks user projects. `godot_version`
  decides whether a typing rule's fix can be written at all, and a finding whose
  annotation the configured engine cannot parse is **dropped silently**: there is
  no "engine too old" diagnostic, because telling a project to write a type it
  cannot parse is worse than saying nothing. So a bare `Dictionary` is not
  reported below `4.4` and `require-typed-loop-variable` reports nothing below
  `4.2`, and a maintainer asking why a bare `Dictionary` is quiet is asking about
  this key. `Context` carries a `compiledConfig` — every name pattern, every
  exempt glob, and the parsed engine version, compiled once by `validate` — so no
  rule compiles or parses anything per file. An enabled rule that implements
  `EngineSchemaRule` receives the immutable selected engine through `Context`.
  With no explicit `extension_api`, that capability is the sole trigger for
  lazily loading a bundled table; ordinary rules never load or version-gate on
  semantic data. An explicit override is project-root-relative, validated once
  at startup, and replaces the embedded table wholesale.
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
  and `Apply` is the only writer. `uid.missing`, `uid.malformed`,
  `uid.duplicate`, `uid.dangling`, and `uid.crossed` are its public diagnostic
  names.
  It speaks for every identity Godot declares, not only a `.gd` sidecar, so
  `uid.malformed` covers a `.tscn`/`.tres` header and a `.import` line too —
  but `uid.missing` stays `.gd`-only, because gdkit does not know which other
  files Godot would have given a sidecar. `uid.duplicate` stays scoped to
  scripts as well: a scene and a script that collide are reported from the
  reference side, as `uid.crossed`, which names the line that loads the wrong
  file.
  `Check` plans the writes as well as reporting them, in an unexported
  `Report.work`, because the identity table it resolves against lives there and
  a report decoded from JSON must not be writable. `Apply` reads and verifies
  every line it will touch before it writes anything, so a reference that moved
  under the run abandons it rather than splitting it; a reference rewrite needs
  no `--repair`, because the `path=` beside it is the authority and is what
  Godot already falls back to, while reissuing a *declaration* does, because
  that changes what every reference resolves to including one gdkit cannot see.
  A reissue moves the references to the old value in the same run, and `path=`
  is the stronger attribution: an `[ext_resource]` follows the file it names
  however many others hold the same text, while a pathless reference is
  attributed by text alone and so is left alone when the text is shared —
  which a duplicated value always is, so a pathless reference to a reissued
  duplicate keeps resolving to the first claimant, which is what it already
  did.
- `project/` — discovery and parsing. The only package that reads a project from
  disk, so every tool agrees on scope and parses once. `Config.HonorIgnoreFile`
  is how lint, format, and uid share the root `.gdkitignore`; `architecture`
  leaves it off, because hiding a file would drop its `class_name` from the
  index. `Config.Selection` is the third option, for a tool that must index
  more than it writes: the universe is walked and parsed unfiltered while
  `Snapshot.Selected` names the subset the caller acts on. `generate` uses it;
  the other four pass `Selection: nil`, which selects everything.
  `Snapshot.Autoloads` holds the manifest's `[autoload]` table, because Godot
  resolves an autoload identifier as a project global while analysing a base
  class, so `extends SomeAutoload` is a real inheritance edge. `Snapshot.UIDs` resolves an identifier to one path and covers every
  place Godot declares one — a `.uid` sidecar, a `.tscn` or `.tres` header, or a
  `.import` file — so a sidecar is not the only way a `uid://` load resolves; it
  loses malformed and duplicated sidecars, while `Snapshot.Sidecars` keeps every
  `.uid` file as read, which is what `uid` reports on.
  `Config.Identities` is the fourth option, and only `uid` sets it:
  `Snapshot.Claims` is every declaration of a `uid://` identity, including the
  mechanism and the line it sits on, and `Snapshot.References` is every use of
  one. It is opt-in because collecting the references means reading every
  `.tscn` and `.tres` through rather than only its header, and because it
  prunes nothing: an ignored directory is walked so a hidden file's claim is
  still indexed, carrying `Claim.Ignored`, since dropping a claimant is what
  would make every reference to it look dangling. A reference inside an ignored
  path is not collected at all, because nothing reports or rewrites one.
- `generate/` — the code generator behind `gdkit gen`: writes `_to_string`,
  `equals`, and `deep_equals` into a class that opted in, inside a
  sentinel-delimited region it owns. Unlike `lint` it is a whole-project analysis, because `equals` composes
  with an ancestor's implementation and is refused when a descendant would
  inherit an unsound one, so both answers need the entire inheritance graph.
  `Check` is pure and `Apply` is the only writer. `generate.stale`,
  `generate.marker`, `generate.conflict`, `generate.unsupported`,
  `generate.orphaned`, and `generate.unsafe` are its public diagnostic names.
  `deep_equals` dispatches at runtime — `x is Object and x.has_method(...)` —
  rather than resolving each field's type statically. That is deliberate and
  deletes three mechanisms an earlier design needed: a catalogue of builtin
  Variant names, capability resolution over field edges, and a severity field
  for an untyped-field warning. It also uses a field type that has only a
  hand-written `equals`, which the static design refused. `is Object` precedes
  `has_method` because `has_method` is declared on `Object`, so calling it on an
  `int` is a runtime error rather than `false`.
  Recursion terminates on the realistic shapes through `if self == p_other`,
  which settles a self-reference and a shared sub-object without recursing. Two
  independently built cyclic graphs cannot terminate that way, so
  `Index.FieldTypeCycle` refuses the class rather than the generator threading a
  visited set through a helper method. `fieldTypeTarget` deliberately does not
  follow an element type inside `Array[T]`: a container is handed to `==`, which
  Godot 4 evaluates by value, so the emitted code never recurses into elements
  and following it would refuse cycles the code cannot reach.
- `internal/atomicwrite/` — the write-beside-and-rename replacement shared by
  `format`, `generate`, and `uid`, including the re-read before the rename that
  keeps a concurrent edit from being lost.
- `internal/glob/` — the shared glob engine.
- `internal/versiongate/` — the `major.minor.patch` comparison behind
  `--minimum-version` and `minimum_gdkit_version`. Comparison is on the numeric
  triple only; prerelease and build metadata are ignored, so a GoReleaser snapshot
  of `0.2.1` satisfies a floor of `0.2.1`. `ParseRequirement` is deliberately
  stricter than `Parse`: a floor is hand-written, so a leading `v` or a prerelease
  suffix is an error, while a version a binary reports about itself is tolerated.
  `ParseEngineVersion` is `ParseRequirement` with an optional patch, and it exists
  because `lint.json`'s `godot_version` is hand-written too and wants that same
  strictness — but nobody writes `4.7.0`, so `4.7` has to be accepted.
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
- `internal/failure/` — the machine-readable `kind` of a failure, carried from
  the package that detected it to the command that reports it. Only the
  detecting package knows whether a config failed to be read, to parse, to name
  known keys, to satisfy the floor, or to validate, and `--format json`
  consumers branch on the difference. `Kind` values are a public contract like
  rule names. `cmd/gdkit` passes a fallback kind at each call site, so a failure
  from a package that does not label one yet still reports something stable.
- `internal/buildinfo/` — version metadata, injected by GoReleaser `-ldflags` and
  falling back to the Go toolchain's embedded VCS settings for local builds.
- `internal/semantic/` — the immutable resolved type vocabulary and engine
  symbol index. It has no parsing or diagnostic policy; `AssignableTo` is
  three-valued, and `Indeterminate` means analysis was inconclusive so
  consumers stay silent.
- `internal/semantic/engineschema/` — the explicit Godot extension-API model,
  deterministic artifact generator, validator, and lazy exact-minor registry.
  Built-in support starts at `4.7`, using the official 4.7.2 artifact; there is
  no nearest/newest fallback and no embedded 4.0–4.6 history. Raw overrides are
  wholesale and numeric-version-matched. Reads are capped at 64 MiB, container
  spellings at 32 levels, and each inheritance chain at 256 in-schema classes;
  the inheritance limit does not cap the schema's total class count. Every
  retained row is validated before the immutable `semantic.Engine` is
  published, and generated records/digests contain semantic identity and
  provenance but no paths or timestamps.
- `internal/semanticsource/` — the adapter from `project.Snapshot` to the
  semantic analyzer's source boundary. It exposes the full `Snapshot.Paths`
  universe rather than the filtered `Selected` action subset, never reparses
  files, and resolves only static script targets present in the snapshot.

A rule may **ship inert**: if it implements `lint.PendingRule` it does not run
until a project names it in `enable` or sets `enable_new_rules`. This exists so
an upgrade cannot change what an existing project reports on unchanged
configuration, which is also what makes the README's "a release may become
stricter" promise safe. `disable` wins over `enable`, and `enable` deliberately
accepts any known rule name rather than only an inert one, so a config that
opted in keeps working after the rule graduates to running by default.
Pending-ness is an optional interface rather than a parallel registry map so a
test can inject one through `newLinter`. Seven rules ship inert —
`no-engine-logging` and the six `require-*` typing rules — and
`TestPendingRulesAreExactlyTheInertOnes` pins that set so a rule cannot start or
stop shipping inert unnoticed. `missing-docstring` predates the
mechanism and is inert through its own empty list, which only worked because
that rule is configured by a list.

`tab-characters` is a configuration value used by `max-line-length`, not a rule.
`source-parse` and `unknown-ignore` are reported by the driver rather than by a
registered rule. Several rules encode deliberately unusual behavior — token-based
rather than scope-based name counting in `unused-argument`, token-stream
comparison in `comparison-with-itself`, annotation re-pairing in
`class-definitions-order`, a static function that `missing-docstring` checks
while `max-public-methods` does not count it, and callee-shape rather than name
matching in `no-engine-logging`, so a project logger with a method named
`push_error` is not mistaken for the engine's function. The comments in `lint/`
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
- **`checkSingleValue` runs first, and it is what makes the two whole-document
  checks sound.** `checkMinimumVersion` and `checkUnknownKeys` both use
  `json.Unmarshal`, which requires the file to be exactly one JSON value and which
  they skip when it is not; `json.Decoder.Decode` reads only the first value and
  never looks at what follows. Without this a config followed by anything else
  loads with neither check applied, so a declared version floor silently does not
  apply and the run passes. `Decoder.More` cannot stand in for the check — it
  reports whether another array or object element follows, so it answers false for
  a trailing `}` or `]`; the test table covers those.
  `TestLoadConfigRejectsContentAfterTheTopLevelObject` guards it.
- **Patterns** are a custom glob (`architecture/pattern.go`) compiled to regex and
  cached: `**/` crosses directories, `*` and `?` stay within a segment, `{feature}`
  captures a segment. It is not `path/filepath.Match`.
- `Config.Validate()` runs from both `LoadConfig` and `NewAnalyzer`, and compiles
  every pattern in the config so bad globs fail as configuration errors, not silently.

### Capability resolution in `generate`

Two things an earlier design got wrong, recorded so they are not reintroduced.

- **`provider` stops at the nearest declaration of a method *name*, not the
  nearest compatible one.** GDScript's runtime lookup does not walk past an
  incompatible override, so neither may the analysis: given `A` with a good
  `equals`, `B extends A` declaring `equals(a, b)`, and `C extends B` emitting
  `super.equals(p_other)`, the call reaches `B`'s and fails.
  Nearest-compatible-ancestor would have reported `A` and emitted it anyway.
- **Resolution must iterate.** It is a monotone demotion to stability, not a
  parents-first walk, because the descendant refusal rule points *up* the
  hierarchy: demoting `B` can move `provider(C)` from `B` to `A` and force `A`
  down after a single pass had already settled it. Seeding is optimistic so a
  first adoption across a hierarchy can start at all — a newly requested parent
  declares nothing yet and is a *virtual* provider.

`canonicalise` formats the region with its sentinels rather than wrapping them
around formatted bodies. Both sentinels are comment runs, and gdparser's
`blankLineGaps` attributes a `top_level` gap to a comment run, so the canonical
form carries blank lines before the *end* sentinel too. Predicting that and
wrapping afterwards produced a region the full-file format oracle rejected.

### Diagnostic rules and exit codes

Rule names (`dependency.direction`, `dependency.cycle`, `classification.missing`,
`class_name.duplicate`, `source.parse`, `resource.missing`, `engine.reference`,
`engine.tree`, `engine.signal`, `test.scene_load`, `test.node_inspection`,
`allowlist.adr`) are the public contract: they appear in JSON output and are what
allowlist exceptions match on. Renaming one breaks existing project allowlists.

Exit codes: `0` clean, `1` architecture violations, `2` configuration/usage/IO
failure. `cmd/gdkit` returns `2` from every error path before analysis completes.

Under `--format json` an exit-`2` failure is a JSON envelope on **stderr**, not
prose, and stdout stays empty so a consumer can tell "no report" from "an empty
report". `--format` is therefore validated before anything else in the command
can fail, because its value decides how every later failure is reported; an
unknown value is itself reported as text, since a caller who misspelled the
format cannot be assumed to parse an envelope. Unknown commands and flag parse
errors precede `--format` entirely and stay prose, as do the `init` commands,
which have no `--format`. `format write` and `uid write` still print the list of
files they changed on stdout when a write fails part-way, because that list is
what tells the caller the state the project is in.

## Conventions

- Tests build throwaway Godot projects with `writeProject(t, t.TempDir(), …)` and run
  `analyzeDefault`, asserting on diagnostic rule names and edges rather than on
  formatted text. New checks belong in that style.
- Adding a check means: emit the `Diagnostic` with a stable `Rule`, `Symbol`, and
  `Target` (the allowlist matches on all three), and document it in the README's
  `arch check` list.
