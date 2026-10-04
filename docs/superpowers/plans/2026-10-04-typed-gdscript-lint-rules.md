# Typed-GDScript Lint Rules Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add six inert `gdkit lint` rules that report GDScript declarations carrying no static type annotation.

**Architecture:** One `collectTypingSites` walk per file produces a slice of violations; a single `typingRule{rule: string}` type backs all six rules by filtering that slice, exactly as `nameRule`/`collectNames` backs the fourteen name rules. Configuration gains a `godot_version` floor and one exempt-pattern list per rule, both compiled once by `Config.validate()` into a new `compiledConfig` that `lint.Context` carries.

**Tech Stack:** Go, `github.com/cafecito-games/gdparser` v0.1.5 (`ast`, `token`), `internal/versiongate`, `internal/glob`.

**Spec:** `docs/superpowers/specs/2026-10-04-typed-gdscript-lint-rules-design.md`

---

## Background the engineer needs

Read these before Task 1. They are short and every task depends on them.

- **`lint/rules_name.go`** — the pattern this plan copies. One `nameRule{rule: string}` type registered once per rule name, backed by one `nameCollector` walk. `runeColumn` (line 70) converts the parser's byte column into the rune column diagnostics use; every new rule calls it.
- **`lint/linter.go`** — `Rule`, `PendingRule`, `Context`, `register`, `newLinter`. A rule returns diagnostics with only `Message`, `Line`, `Column`, `EndLine`, `EndColumn` set; the driver stamps `Rule`, `Path`, and `Severity`.
- **`lint/config.go`** — `Config`, `DefaultConfig`, `LoadConfig`, `Validate`/`validate`.
- **`lint/rules_logging.go`** — the only rule shipping inert today. Copy its `PendingSince()` shape.

Two project rules that bite in this work:

1. **A rule name is a permanent public contract.** It appears in JSON output, in config files, and in users' `# gdkit:ignore` comments. The six names in this plan are final.
2. **Two tests fail the moment a new rule registers.** `TestPendingRulesAreExactlyTheInertOnes` pins the exact set of inert rules, and `TestFixturesExerciseEveryRule` fails when a registered rule fires in no fixture. Every task that registers a rule therefore **must** update both in the same commit, or it lands a red build. Each task below says so explicitly.

Commands, from the repo root:

```sh
gofmt -w .
go vet ./...
go test -race ./lint/... ./internal/versiongate/...
```

## File structure

| File | Responsibility | Task |
| --- | --- | --- |
| `lint/config.go` | `compiledConfig`; `godot_version` and the six exempt lists; their validation | 1, 2, 3 |
| `lint/linter.go` | `Context` carries `compiledConfig`; `Pattern`, `supports`, `exempt` accessors | 1, 2, 3 |
| `internal/versiongate/versiongate.go` | `ParseEngineVersion` — a hand-written engine version with an optional patch | 2 |
| `lint/rules_typing.go` | the collector, `typingRule`, and all six registrations | 4, 5, 6 |
| `lint/rules_typing_test.go` | per-rule tables, including the satisfied cases | 4, 5, 6, 7 |
| `lint/testdata/typing/*.gd` | fixtures, one file per rule group | 4, 5, 6 |
| `lint/rules_fixture_test.go` | `typingFixtureConfig`, `typingFixtureExpectations`, the group list | 4, 5, 6 |
| `lint/pending_test.go` | the pinned inert-rule set | 4, 5, 6 |
| `README.md` | the six rules, `godot_version`, the rule count | 8 |

`lint/rules_typing.go` holds all six rules because they share one collector and one rule type; splitting them across files would separate a rule from the walk that feeds it. It stays comparable in size to `rules_name.go`, which carries fourteen.

---

### Task 1: `compiledConfig`

**Goal:** Move the compiled name patterns into a struct that `Context` carries, so later tasks have somewhere to put the parsed Godot version and the compiled exempt globs.

**Files:**
- Modify: `lint/config.go` (the `validate` method)
- Modify: `lint/linter.go:36-42` (`Context` and `Pattern`), `lint/linter.go:158` (`newLinter`)

**Acceptance Criteria:**
- [ ] `Config.validate()` returns `(*compiledConfig, error)`
- [ ] `Context` holds a `*compiledConfig` instead of a `patterns` map
- [ ] `Context.Pattern` returns `nil` rather than panicking on a zero `Context`
- [ ] No behavior change: the whole existing `lint` suite passes untouched

**Verify:** `go test -race ./lint/...` → `ok` with no test file edited in this task

**Steps:**

- [ ] **Step 1: Confirm the suite is green before refactoring**

```sh
go test -race ./lint/...
```

Expected: `ok  github.com/cafecito-games/gdkit/lint`. This task is a pure refactor, so the existing suite *is* its test — there is no new test to write first. If it is already red, stop and report.

- [ ] **Step 2: Add `compiledConfig` in `lint/config.go`**

Put it immediately above the `validate` method:

```go
// compiledConfig holds everything validate compiles once, so no rule compiles
// anything per file. Context carries it; a rule reaches it through an accessor
// rather than reading the fields, which keeps the compiled forms private.
type compiledConfig struct {
	patterns map[string]*regexp.Regexp
}
```

- [ ] **Step 3: Change `validate` to return it**

Change the signature and the three `return nil, …` paths stay as they are (they already return `nil` for the first value). Only the success path changes:

```go
// validate is Validate, also returning the compiled configuration so a caller
// that needs it does not compile twice.
func (c Config) validate() (*compiledConfig, error) {
```

and at the end of the function, replace `return patterns, nil` with:

```go
	return &compiledConfig{patterns: patterns}, nil
```

`Validate()` itself needs no change — it already discards the first return value.

- [ ] **Step 4: Change `Context` in `lint/linter.go`**

Replace the `Context` struct and `Pattern` method with:

```go
// Context gives a rule the project and its resolved configuration.
type Context struct {
	Config Config

	compiled *compiledConfig
}

// Pattern returns the compiled, anchored pattern for a name rule.
func (c *Context) Pattern(rule string) *regexp.Regexp {
	if c.compiled == nil {
		return nil
	}
	return c.compiled.patterns[rule]
}
```

The nil guard matters: a test may build a `Context` by hand, and a nil map read is fine while a nil pointer dereference is not.

- [ ] **Step 5: Change `newLinter`**

In `lint/linter.go`, rename the variable and pass it through:

```go
	compiled, err := config.validate()
	if err != nil {
		return nil, err
	}
```

and in the returned struct literal:

```go
		context:  Context{Config: config, compiled: compiled},
```

- [ ] **Step 6: Verify nothing changed**

```sh
gofmt -w . && go vet ./... && go test -race ./lint/...
```

Expected: PASS, with no test file modified.

- [ ] **Step 7: Commit**

```bash
git add lint/config.go lint/linter.go
git commit -m "Hold the lint configuration's compiled forms in one struct

Context carried a bare pattern map, which leaves nowhere to put the
compiled forms the typing rules need. compiledConfig gives validate one
return value to grow, and Context reaches it through accessors so the
compiled forms stay private to the package."
```

---

### Task 2: `godot_version`

**Goal:** A configured engine version, parsed once, that a rule can compare a floor against — so a rule never tells a project to write syntax its engine cannot parse.

**Files:**
- Modify: `internal/versiongate/versiongate.go` (add `ParseEngineVersion`)
- Modify: `internal/versiongate/versiongate_test.go`
- Modify: `lint/config.go` (`Config`, `DefaultConfig`, `validate`, `compiledConfig`)
- Modify: `lint/linter.go` (add `Context.supports`)
- Modify: `lint/config_test.go`

**Acceptance Criteria:**
- [ ] `versiongate.ParseEngineVersion` accepts `"4.7"` and `"4.7.0"` and rejects `"4"`, `"v4.7"`, `"4.7.0-beta1"`, `"4.7."`, and `""`
- [ ] `Config.GodotVersion` defaults to `"4.7"` and is written by `gdkit lint init`
- [ ] A malformed `godot_version` fails `Validate()` with a message naming the key
- [ ] `Context.supports(floor)` is true when the configured version is at least `floor`

**Verify:** `go test -race ./internal/versiongate/... ./lint/...` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing versiongate test**

Append to `internal/versiongate/versiongate_test.go`:

```go
// ParseEngineVersion exists because neither existing entry point fits a Godot
// version written by hand: ParseRequirement demands all three components, and
// nobody writes "4.7.0", while Parse tolerates a leading "v" and a prerelease
// suffix because it reads what a binary reports about itself.
func TestParseEngineVersion(t *testing.T) {
	tests := []struct {
		value   string
		want    Version
		wantErr bool
	}{
		{value: "4.7", want: Version{Major: 4, Minor: 7}},
		{value: "4.7.0", want: Version{Major: 4, Minor: 7}},
		{value: "4.7.1", want: Version{Major: 4, Minor: 7, Patch: 1}},
		{value: "4.0", want: Version{Major: 4}},
		{value: "4", wantErr: true},
		{value: "v4.7", wantErr: true},
		{value: "4.7.0-beta1", wantErr: true},
		{value: "4.7.", wantErr: true},
		{value: "", wantErr: true},
		{value: "4.x", wantErr: true},
	}
	for _, test := range tests {
		got, err := ParseEngineVersion(test.value)
		if test.wantErr {
			if err == nil {
				t.Errorf("ParseEngineVersion(%q) = %v, want an error", test.value, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseEngineVersion(%q) = %v", test.value, err)
			continue
		}
		if got != test.want {
			t.Errorf("ParseEngineVersion(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```sh
go test -race ./internal/versiongate/ -run TestParseEngineVersion
```

Expected: FAIL — `undefined: ParseEngineVersion`.

- [ ] **Step 3: Implement it**

Add to `internal/versiongate/versiongate.go`, below `ParseRequirement`:

```go
// ParseEngineVersion reads a Godot engine version a project writes by hand into
// its configuration. It is ParseRequirement with an optional patch component,
// because an engine version is written "4.7" far more often than "4.7.0"; a
// leading "v" or a prerelease suffix is still a mistake worth naming.
func ParseEngineVersion(value string) (Version, error) {
	if strings.Count(value, ".") == 1 {
		return parseTriple(value+".0", value)
	}
	return parseTriple(value, value)
}
```

Appending `.0` before parsing rather than special-casing the component count keeps the strict per-component rules: `"v4.7"` becomes `"v4.7.0"`, whose first component still fails `decimal`, and the error names the original `"v4.7"`.

- [ ] **Step 4: Run it to verify it passes**

```sh
go test -race ./internal/versiongate/ -run TestParseEngineVersion
```

Expected: PASS.

- [ ] **Step 5: Write the failing lint config test**

Append to `lint/config_test.go`:

```go
func TestDefaultConfigTargetsTheNewestGodot(t *testing.T) {
	if got := DefaultConfig().GodotVersion; got != "4.7" {
		t.Fatalf("GodotVersion = %q, want \"4.7\"", got)
	}
}

func TestValidateRejectsAMalformedGodotVersion(t *testing.T) {
	config := DefaultConfig()
	config.GodotVersion = "v4.7"
	err := config.Validate()
	if err == nil || !strings.Contains(err.Error(), "godot_version") {
		t.Fatalf("Validate() = %v, want an error naming godot_version", err)
	}
}

// supports is what a version-gated rule asks before reporting, so a project is
// never told to write a type annotation its engine cannot parse.
func TestContextSupportsComparesTheConfiguredVersion(t *testing.T) {
	tests := []struct {
		configured string
		floor      versiongate.Version
		want       bool
	}{
		{configured: "4.7", floor: versiongate.Version{Major: 4}, want: true},
		{configured: "4.7", floor: versiongate.Version{Major: 4, Minor: 4}, want: true},
		{configured: "4.3", floor: versiongate.Version{Major: 4, Minor: 4}, want: false},
		{configured: "4.4", floor: versiongate.Version{Major: 4, Minor: 4}, want: true},
		{configured: "4.1", floor: versiongate.Version{Major: 4, Minor: 2}, want: false},
	}
	for _, test := range tests {
		config := DefaultConfig()
		config.GodotVersion = test.configured
		compiled, err := config.validate()
		if err != nil {
			t.Fatalf("validate() = %v", err)
		}
		context := Context{Config: config, compiled: compiled}
		if got := context.supports(test.floor); got != test.want {
			t.Errorf("godot_version %q supports(%v) = %t, want %t", test.configured, test.floor, got, test.want)
		}
	}
}
```

Add `"strings"` and `"github.com/cafecito-games/gdkit/internal/versiongate"` to that file's imports if they are not already there.

- [ ] **Step 6: Run it to verify it fails**

```sh
go test -race ./lint/ -run 'GodotVersion|ContextSupports'
```

Expected: FAIL — `config.GodotVersion undefined` and `context.supports undefined`.

- [ ] **Step 7: Add the config field**

In `lint/config.go`, add to `Config` immediately after `Exclude`:

```go
	// GodotVersion is the engine version the project targets, written as
	// "major.minor" or "major.minor.patch". A rule whose fix needs newer syntax
	// than this reports nothing, so a project is never told to write a type
	// annotation its engine cannot parse.
	//
	// It defaults to the newest Godot gdkit knows, which is safe only because
	// every version-gated rule ships inert: a project that has not opted in
	// cannot be affected by the default, and a project on an older engine
	// lowers this one key instead of hunting for the right rule name.
	GodotVersion string `json:"godot_version"`
```

The tag carries no `omitempty`, so `gdkit lint init` writes the key — it is the one new key a project is expected to set.

- [ ] **Step 8: Default it, compile it, expose it**

In `DefaultConfig()`, add after `Exclude`:

```go
		GodotVersion: "4.7",
```

In `compiledConfig`, add the field:

```go
type compiledConfig struct {
	patterns     map[string]*regexp.Regexp
	godotVersion versiongate.Version
}
```

In `validate()`, before the `return`, parse it:

```go
	godotVersion, err := versiongate.ParseEngineVersion(c.GodotVersion)
	if err != nil {
		return nil, fmt.Errorf("godot_version: %w", err)
	}
```

and carry it:

```go
	return &compiledConfig{patterns: patterns, godotVersion: godotVersion}, nil
```

Add `"github.com/cafecito-games/gdkit/internal/versiongate"` to `lint/config.go`'s imports.

Note that `validate` already assigns `err` from `compileNamePatterns`; reuse the variable with `=` rather than `:=` if the compiler objects.

- [ ] **Step 9: Add the `Context` accessor**

In `lint/linter.go`, below `Pattern`:

```go
// supports reports whether the project's configured Godot version is at least
// floor. A typing site names the version that first accepts the annotation it
// wants, so a site is dropped rather than reported when the engine is older.
func (c *Context) supports(floor versiongate.Version) bool {
	if c.compiled == nil {
		return false
	}
	return !c.compiled.godotVersion.Less(floor)
}
```

Add `"github.com/cafecito-games/gdkit/internal/versiongate"` to `lint/linter.go`'s imports.

- [ ] **Step 10: Run the tests to verify they pass**

```sh
gofmt -w . && go vet ./... && go test -race ./internal/versiongate/... ./lint/...
```

Expected: PASS. `go test -race ./...` should also pass — `cmd/gdkit`'s `lint init` golden output, if it pins the written JSON, now includes `"godot_version": "4.7"`. If a `cmd/gdkit` test fails on that, update its expected output; that is the correct new behavior.

- [ ] **Step 11: Commit**

```bash
git add internal/versiongate lint/config.go lint/linter.go lint/config_test.go
git commit -m "Give lint the Godot version the project targets

A typing rule must not tell a project to write an annotation its engine
cannot parse, so it needs the engine version. godot_version defaults to
the newest Godot gdkit knows, which is safe only because every rule that
reads it ships inert.

ParseEngineVersion is new because neither existing entry point fits a
hand-written engine version: ParseRequirement demands all three
components and nobody writes 4.7.0, while Parse tolerates a leading v
and a prerelease suffix that are mistakes in a config file."
```

---

### Task 3: Per-rule exempt lists

**Goal:** Each of the six rules takes a list of function-name globs it skips, compiled once.

**Files:**
- Modify: `lint/config.go` (`Config`, `exemptPatterns`, `validate`, `compiledConfig`)
- Modify: `lint/linter.go` (add `Context.exempt`)
- Modify: `lint/config_test.go`

**Acceptance Criteria:**
- [ ] Six `[]string` config keys exist, named exactly after their rules, all `omitempty`
- [ ] `gdkit lint init` does **not** write them (the default policy exempts nothing, and six empty lists would imply the rules run)
- [ ] Every pattern is compiled by `Validate()`. `internal/glob.Compile` cannot fail — it `QuoteMeta`s every literal, so `[` is a literal bracket — so its error path is unreachable and gets no test. Check the error anyway; do not write a test that cannot pass.
- [ ] `Context.exempt(rule, name)` matches `internal/glob` patterns, and is false for an empty `name`

**Verify:** `go test -race ./lint/ -run Exempt` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test**

Append to `lint/config_test.go`:

```go
// An empty exempt list means "no exemptions". This is the opposite of
// missing-docstring, whose empty list turns that rule off; inertness is
// PendingRule's job and never a list's.
func TestContextExemptMatchesGlobPatterns(t *testing.T) {
	config := DefaultConfig()
	config.RequireReturnType = []string{"_ready", "_on_*"}
	compiled, err := config.validate()
	if err != nil {
		t.Fatalf("validate() = %v", err)
	}
	context := Context{Config: config, compiled: compiled}

	tests := []struct {
		rule string
		name string
		want bool
	}{
		{rule: "require-return-type", name: "_ready", want: true},
		{rule: "require-return-type", name: "_on_button_pressed", want: true},
		{rule: "require-return-type", name: "_process", want: false},
		{rule: "require-return-type", name: "", want: false},
		// A pattern on one rule never exempts another.
		{rule: "require-argument-type", name: "_ready", want: false},
	}
	for _, test := range tests {
		if got := context.exempt(test.rule, test.name); got != test.want {
			t.Errorf("exempt(%q, %q) = %t, want %t", test.rule, test.name, got, test.want)
		}
	}
}
```

There is deliberately no malformed-pattern test: `glob.Compile` cannot fail, so there is no malformed pattern to write one with.

- [ ] **Step 2: Run it to verify it fails**

```sh
go test -race ./lint/ -run Exempt
```

Expected: FAIL — `config.RequireReturnType undefined`.

- [ ] **Step 3: Add the config fields**

In `lint/config.go`, add to `Config` after `ClassDefinitionsOrder`:

```go
	// The six fields below hold function-name glob patterns the matching typing
	// rule skips. An empty list means no exemptions: the rules are inert until
	// a project enables them, which is PendingRule's job and never a list's.
	// MissingDocstring below is the exception, not the pattern — its empty list
	// turns that rule off, because it predates PendingRule.
	//
	// What the pattern matches depends on the rule. For require-return-type and
	// require-argument-type it is the function being declared. For
	// require-variable-type, require-typed-collection, and
	// require-typed-loop-variable it is the enclosing function, so ["_process"]
	// quiets a hot loop's locals without quieting the file; a class-scope
	// declaration has no enclosing function and is never exempted by a list.
	// For require-signal-argument-type it is the signal's own name.
	RequireReturnType         []string `json:"require-return-type,omitempty"`
	RequireArgumentType       []string `json:"require-argument-type,omitempty"`
	RequireVariableType       []string `json:"require-variable-type,omitempty"`
	RequireTypedCollection    []string `json:"require-typed-collection,omitempty"`
	RequireSignalArgumentType []string `json:"require-signal-argument-type,omitempty"`
	RequireTypedLoopVariable  []string `json:"require-typed-loop-variable,omitempty"`
```

Do **not** add them to `DefaultConfig()`. A nil slice with `omitempty` is omitted by `json.MarshalIndent`, which is what keeps `gdkit lint init` from writing six keys that imply the rules run.

- [ ] **Step 4: Map and compile them**

Add below `namePatterns()` in `lint/config.go`:

```go
// exemptPatterns maps each typing rule to its configured exempt patterns.
func (c Config) exemptPatterns() map[string][]string {
	return map[string][]string{
		"require-return-type":          c.RequireReturnType,
		"require-argument-type":        c.RequireArgumentType,
		"require-variable-type":        c.RequireVariableType,
		"require-typed-collection":     c.RequireTypedCollection,
		"require-signal-argument-type": c.RequireSignalArgumentType,
		"require-typed-loop-variable":  c.RequireTypedLoopVariable,
	}
}

// compileExemptPatterns compiles every exempt pattern once, so no rule compiles
// one per file.
func (c Config) compileExemptPatterns() (map[string][]glob.Pattern, error) {
	compiled := make(map[string][]glob.Pattern)
	for rule, patterns := range c.exemptPatterns() {
		for _, pattern := range patterns {
			parsed, err := glob.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("%s exempt pattern %q: %w", rule, pattern, err)
			}
			compiled[rule] = append(compiled[rule], parsed)
		}
	}
	return compiled, nil
}
```

In `compiledConfig`, add:

```go
	exempt map[string][]glob.Pattern
```

In `validate()`, before the `return`:

```go
	exempt, err := c.compileExemptPatterns()
	if err != nil {
		return nil, err
	}
```

and carry it in the returned literal:

```go
	return &compiledConfig{patterns: patterns, godotVersion: godotVersion, exempt: exempt}, nil
```

`glob` is already imported by `lint/config.go`.

- [ ] **Step 5: Add the `Context` accessor**

In `lint/linter.go`, below `supports`:

```go
// exempt reports whether name matches one of the rule's exempt patterns. A site
// with no such name — a class-scope declaration, which has no enclosing
// function — is never exempt, and is suppressed with a comment instead.
func (c *Context) exempt(rule, name string) bool {
	if c.compiled == nil || name == "" {
		return false
	}
	for _, pattern := range c.compiled.exempt[rule] {
		if matched, _ := pattern.Match(name); matched {
			return true
		}
	}
	return false
}
```

`glob.Pattern.Match` returns `(bool, map[string]string)`; the second value is the `{feature}`-style captures, which this use has none of.

- [ ] **Step 6: Run the tests to verify they pass**

```sh
gofmt -w . && go vet ./... && go test -race ./lint/...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add lint/config.go lint/linter.go lint/config_test.go
git commit -m "Let a project exempt functions from the typing rules

Each typing rule takes function-name globs it skips, so a team that does
not want a return type on every _ready writes one pattern rather than
disabling the rule. An empty list means no exemptions; init does not
write the keys, because six empty lists would imply the rules run."
```

---

### Task 4: The collector, `require-return-type`, and `require-argument-type`

**Goal:** The shared walk, the rule type behind all six, and the two rules that govern a signature.

**Files:**
- Create: `lint/rules_typing.go`, `lint/rules_typing_test.go`, `lint/testdata/typing/signatures.gd`
- Modify: `lint/rules_fixture_test.go`, `lint/pending_test.go`

**Acceptance Criteria:**
- [ ] `func f():` reports `require-return-type` at the function's name
- [ ] An untyped parameter reports `require-argument-type` at the parameter's name
- [ ] `-> void`, `: int`, `:= 0`, `: Variant`, and a variadic `...args` each report nothing
- [ ] A lambda's parameters are checked; a lambda's return type is never checked
- [ ] Both rules are inert under `DefaultConfig()`
- [ ] `TestPendingRulesAreExactlyTheInertOnes` and `TestFixturesExerciseEveryRule` pass

**Verify:** `go test -race ./lint/...` → `ok`

**Steps:**

- [ ] **Step 1: Find the release these rules ship in**

```sh
gh release list --limit 3
```

Use the next **minor** after the newest published release as `PendingSince()`. This plan writes `"0.5.0"`; if `gh` shows the newest release is not `0.4.x`, use the correct value consistently in every task below.

- [ ] **Step 2: Write the failing test**

Create `lint/rules_typing_test.go`:

```go
package lint

import (
	"strings"
	"testing"
)

// typingConfig enables every typing rule and nothing else. The rules ship
// inert, so a test that does not enable them asserts nothing.
//
// It opts in by name rather than with EnableNewRules, which would also enable
// no-engine-logging and make a fixture holding a print() report a rule this
// group is not about. Deriving the names from PendingRuleNames keeps the helper
// correct as each task registers another rule.
func typingConfig() Config {
	config := DefaultConfig()
	for _, name := range PendingRuleNames() {
		if strings.HasPrefix(name, "require-") {
			config.Enable = append(config.Enable, name)
		}
	}
	return config
}

func TestRequireReturnTypeReportsAnUnannotatedFunction(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-return-type", `
func move():
	pass
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want one diagnostic", found)
	}
	if found[0].Line != 2 || found[0].Column != 6 {
		t.Errorf("reported at %d:%d, want 2:6 (the function's name)", found[0].Line, found[0].Column)
	}
	if found[0].Message != `Function "move" has no return type` {
		t.Errorf("message = %q", found[0].Message)
	}
}

// The satisfied cases are asserted as explicitly as the violating one: a rule
// that fires where it should not reaches users as noise.
func TestRequireReturnTypeAcceptsAnAnnotatedFunction(t *testing.T) {
	sources := map[string]string{
		"void":    "func move() -> void:\n\tpass\n",
		"a type":  "func move() -> int:\n\treturn 1\n",
		"Variant": "func move() -> Variant:\n\treturn 1\n",
	}
	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			if found := lintSourceWithConfig(t, typingConfig(), "require-return-type", source); len(found) != 0 {
				t.Fatalf("got %v, want none", found)
			}
		})
	}
}

// A lambda's parameters are a contract its caller satisfies; its return value is
// consumed where the lambda is written, so the return type is not checked.
func TestRequireReturnTypeIgnoresALambda(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-return-type", `
func build() -> void:
	var double := func(value: int): return value * 2
	double.call(1)
`)
	if len(found) != 0 {
		t.Fatalf("got %v, want none", found)
	}
}

func TestRequireArgumentTypeReportsAnUntypedParameter(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-argument-type", `
func apply(data, amount: int) -> void:
	print(data, amount)
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want one diagnostic", found)
	}
	if found[0].Line != 2 || found[0].Column != 12 {
		t.Errorf("reported at %d:%d, want 2:12 (the parameter's name)", found[0].Line, found[0].Column)
	}
	if found[0].Message != `Argument "data" of function "apply" has no type` {
		t.Errorf("message = %q", found[0].Message)
	}
}

func TestRequireArgumentTypeAcceptsTypedAndInferredAndVariadic(t *testing.T) {
	sources := map[string]string{
		"annotated": "func apply(data: int) -> void:\n\tprint(data)\n",
		"inferred":  "func apply(data := 0) -> void:\n\tprint(data)\n",
		"Variant":   "func apply(data: Variant) -> void:\n\tprint(data)\n",
		"variadic":  "func apply(...rest) -> void:\n\tprint(rest)\n",
	}
	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			if found := lintSourceWithConfig(t, typingConfig(), "require-argument-type", source); len(found) != 0 {
				t.Fatalf("got %v, want none", found)
			}
		})
	}
}

func TestRequireArgumentTypeChecksALambdaParameter(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-argument-type", `
func build() -> void:
	var double := func(value): return value * 2
	double.call(1)
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want one diagnostic", found)
	}
	if found[0].Message != `Argument "value" of lambda has no type` {
		t.Errorf("message = %q", found[0].Message)
	}
}

func TestTypingRulesAreInertByDefault(t *testing.T) {
	report := lintProject(t, DefaultConfig(), map[string]string{"a.gd": "func move():\n\tpass\n"})
	for _, diagnostic := range report.Diagnostics {
		t.Errorf("an inert typing rule fired: %v", diagnostic)
	}
}

func TestRequireReturnTypeHonorsAnExemptPattern(t *testing.T) {
	config := typingConfig()
	config.RequireReturnType = []string{"_ready"}
	found := lintSourceWithConfig(t, config, "require-return-type", `
func _ready():
	pass

func move():
	pass
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want only the unexempted function", found)
	}
	if found[0].Line != 5 {
		t.Errorf("reported line %d, want 5", found[0].Line)
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

```sh
go test -race ./lint/ -run 'RequireReturnType|RequireArgumentType|TypingRules'
```

Expected: FAIL — `enable names unknown rule "require-return-type"` is *not* what you should see, because `EnableNewRules` does not validate names; expect the diagnostics simply to be absent, e.g. `got [], want one diagnostic`.

- [ ] **Step 4: Create `lint/rules_typing.go`**

```go
package lint

import (
	"fmt"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"

	"github.com/cafecito-games/gdkit/internal/versiongate"
	"github.com/cafecito-games/gdkit/project"
)

// typingRuleNames is every rule backed by the typing collector. Registering
// from one list keeps the set and the collector in one file, the way
// nameMessages does for the name rules.
var typingRuleNames = []string{
	"require-return-type",
	"require-argument-type",
}

func init() {
	for _, rule := range typingRuleNames {
		register(typingRule{rule: rule})
	}
}

// The Godot versions that first accept each typed form. They are values rather
// than strings so a site compares without parsing anything per file.
var (
	godot40 = versiongate.Version{Major: 4}
)

// typingRule reports a declaration that carries no static type annotation.
// Static typing is Godot's documented correctness and performance win, and an
// untyped declaration is a Variant: the engine cannot check it, cannot
// specialize it, and reports nothing when the wrong thing is assigned to it.
//
// All of the typing rules share this one type and one collector, the way the
// fourteen name rules share nameRule and collectNames. They ship inert, as a
// new rule must, so an upgrade cannot change what an existing project reports
// on unchanged configuration.
type typingRule struct{ rule string }

func (r typingRule) Name() string { return r.rule }

func (r typingRule) PendingSince() string { return "0.5.0" }

func (r typingRule) Check(context *Context, script *project.Script) []Diagnostic {
	var found []Diagnostic
	for _, site := range collectTypingSites(script) {
		if site.rule != r.rule {
			continue
		}
		// A site whose fix needs newer syntax than the project's engine is
		// dropped: telling a project to write a type it cannot parse is worse
		// than saying nothing.
		if !context.supports(site.floor) {
			continue
		}
		if context.exempt(r.rule, site.enclosing) {
			continue
		}
		start, end := site.span.Start, site.span.End
		found = append(found, Diagnostic{
			Message:   site.message,
			Line:      start.Line,
			Column:    runeColumn(script, start),
			EndLine:   end.Line,
			EndColumn: runeColumn(script, end),
		})
	}
	return found
}

// typingSite is one declaration that is missing a type. The collector emits a
// site only for a violation, so a rule never re-decides what counts as typed.
type typingSite struct {
	// rule is the rule that governs the site.
	rule string
	// message is the rendered diagnostic.
	message string
	// enclosing is the name an exempt pattern matches: the function being
	// declared, the function a site sits inside, or a signal's own name. It is
	// empty for a class-scope declaration, which no list can exempt.
	enclosing string
	// floor is the Godot version that first accepts the annotation the site
	// wants. It belongs to the site rather than the rule because
	// require-typed-collection needs two: Array[T] is 4.0 and
	// Dictionary[K, V] is 4.4.
	floor versiongate.Version
	// span is what to underline.
	span token.Span
}

// annotated reports whether a declaration carries a static type. ":=" inference
// is static typing, and an explicit Variant is a deliberate opt-out, so both
// satisfy every typing rule.
func annotated(typeName string, inferred bool) bool {
	return inferred || typeName != ""
}

type typingCollector struct {
	script *project.Script
	found  []typingSite
}

// collectTypingSites walks a file once and returns every declaration missing a
// type. Class bodies and function bodies are walked separately, as in
// collectNames: parameters and property accessors are not ast.Nodes, so they
// are reached from their parent declaration rather than through ast.Inspect.
func collectTypingSites(script *project.Script) []typingSite {
	if script.File == nil {
		return nil
	}
	collector := &typingCollector{script: script}
	collector.classBody(script.File.Statements)
	return collector.found
}

func (c *typingCollector) add(site typingSite) {
	c.found = append(c.found, site)
}

func (c *typingCollector) classBody(statements []ast.Statement) {
	for _, statement := range statements {
		switch declaration := statement.(type) {
		case *ast.ClassDeclaration:
			c.classBody(declaration.Body)
		case *ast.FunctionDeclaration:
			c.function(declaration)
		}
	}
}

// function records a function's signature and walks its body. An abstract
// function has no body but still declares a signature its callers rely on.
func (c *typingCollector) function(declaration *ast.FunctionDeclaration) {
	if !annotated(declaration.ReturnType, false) {
		c.add(typingSite{
			rule:      "require-return-type",
			message:   fmt.Sprintf("Function %q has no return type", declaration.Name),
			enclosing: declaration.Name,
			floor:     godot40,
			span:      declaration.NameSpan,
		})
	}
	c.parameters("require-argument-type", declaration.Parameters, declaration.Name,
		fmt.Sprintf("function %q", declaration.Name))
	c.functionScope(declaration.Name, declaration.Body)
}

// parameters records every parameter with no type. A variadic parameter is a
// Variant array by construction, so there is nothing to annotate.
func (c *typingCollector) parameters(rule string, parameters []ast.Parameter, enclosing, owner string) {
	for _, parameter := range parameters {
		if parameter.Variadic || annotated(parameter.Type, parameter.Inferred) {
			continue
		}
		c.add(typingSite{
			rule:      rule,
			message:   fmt.Sprintf("Argument %q of %s has no type", parameter.Name, owner),
			enclosing: enclosing,
			floor:     godot40,
			span:      parameter.NameSpan,
		})
	}
}

func (c *typingCollector) functionScope(enclosing string, statements []ast.Statement) {
	for _, statement := range statements {
		c.inspect(enclosing, statement)
	}
}

// inspect walks a subtree that executes inside a function. The enclosing
// function's name travels with it, because that is what an exempt pattern
// matches for a site inside a function.
func (c *typingCollector) inspect(enclosing string, node ast.Node) {
	if node == nil {
		return
	}
	ast.Inspect(node, func(node ast.Node) bool {
		switch declaration := node.(type) {
		case *ast.LambdaExpression:
			// A lambda's parameters are a contract its caller satisfies, so
			// they are checked. Its return value is consumed where the lambda
			// is written, where an annotation is noise, so ReturnType is
			// deliberately not checked even though the parser exposes it.
			c.parameters("require-argument-type", declaration.Parameters, enclosing, c.lambdaOwner(declaration))
		}
		return true
	})
}

// lambdaOwner names a lambda in a diagnostic. A lambda may carry a name, which
// Godot reports in a stack trace and a reader will recognize.
func (c *typingCollector) lambdaOwner(declaration *ast.LambdaExpression) string {
	if declaration.Name == "" {
		return "lambda"
	}
	return fmt.Sprintf("lambda %q", declaration.Name)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

```sh
gofmt -w . && go test -race ./lint/ -run 'RequireReturnType|RequireArgumentType|TypingRules'
```

Expected: PASS. Columns are the most likely failure: `lintSourceWithConfig` writes the source with its leading newline intact, so the `func` line is line 2 and `runeColumn` counts from 1. If a column assertion fails, print the diagnostic and correct the *test's* expectation to the real name position — do not change the reported span, which is deliberately the name.

- [ ] **Step 6: Update the pinned inert-rule set**

`TestPendingRulesAreExactlyTheInertOnes` in `lint/pending_test.go` now fails. `PendingRuleNames()` is sorted, so change `want` to:

```go
	want := []string{"no-engine-logging", "require-argument-type", "require-return-type"}
```

- [ ] **Step 7: Add the fixture**

Create `lint/testdata/typing/signatures.gd`:

```gdscript
extends Node


func _ready():
	var speed := compute(1, 2.0)
	print(speed)


func compute(first, second: float) -> float:
	return first + second


func typed(value: int) -> int:
	var double := func(amount): return amount * 2
	return double.call(value)
```

- [ ] **Step 8: Wire the fixture group**

In `lint/rules_fixture_test.go`, add the config and expectations beside the logging ones:

```go
// typingFixtureConfig enables every typing rule. They ship inert, so they need
// a fixture group of their own. It reuses typingConfig so the fixture group and
// the per-rule tests cannot drift into enabling different sets.
func typingFixtureConfig() Config {
	return typingConfig()
}

// typingFixtureExpectations is the complete set of diagnostics the files in
// testdata/typing must produce once the typing rules are enabled.
var typingFixtureExpectations = map[string][]fixtureFinding{
	"signatures.gd": {
		{4, "require-return-type"},
		// "first" only: "second" is annotated.
		{9, "require-argument-type"},
		// The lambda's parameter, not its missing return type.
		{14, "require-argument-type"},
	},
}
```

Add the runner beside `TestLoggingFixtureDiagnostics`:

```go
// TestTypingFixtureDiagnostics is TestFixtureDiagnostics for the fixtures that
// only produce diagnostics once the typing rules are enabled.
func TestTypingFixtureDiagnostics(t *testing.T) {
	checkFixtures(t, lintFixtures(t, "typing", typingFixtureConfig()), typingFixtureExpectations)
}
```

And add the group to the slice in `TestFixturesExerciseEveryRule`:

```go
	for _, group := range []map[string][]fixtureFinding{
		fixtureExpectations, docstringFixtureExpectations,
		loggingFixtureExpectations, typingFixtureExpectations,
	} {
```

- [ ] **Step 9: Verify the whole suite**

```sh
gofmt -w . && go vet ./... && go test -race ./...
```

Expected: PASS. If `TestTypingFixtureDiagnostics` reports a line you did not expect, read the fixture: expectations are exact, and an unexpected diagnostic fails exactly as a missing one does.

- [ ] **Step 10: Commit**

```bash
git add lint/rules_typing.go lint/rules_typing_test.go lint/testdata/typing lint/rules_fixture_test.go lint/pending_test.go
git commit -m "Report functions and parameters with no type annotation

An untyped declaration is a Variant: the engine cannot check it, cannot
specialize it, and reports nothing when the wrong thing is assigned to
it. require-return-type and require-argument-type report the two sites
that make up a signature, which is the boundary other code relies on.

One collector backs both, as collectNames backs the name rules. A
lambda's parameters are checked and its return type is not: the
parameters are a contract its caller satisfies, while the return value
is consumed where the lambda is written."
```

---

### Task 5: `require-variable-type` and `require-typed-collection`

**Goal:** Variables, at class and function scope, and the bare `Array`/`Dictionary` annotation — the rule with two version floors.

**Files:**
- Modify: `lint/rules_typing.go`, `lint/rules_typing_test.go`, `lint/rules_fixture_test.go`, `lint/pending_test.go`
- Create: `lint/testdata/typing/variables.gd`

**Acceptance Criteria:**
- [ ] `var x` and `var x = 1` report `require-variable-type` at the name, at both class and function scope
- [ ] `@export var x = 1` reports; `const X = 1` never reports
- [ ] `var x: Array` reports `require-typed-collection` at the annotation, not at the name
- [ ] `var x: Array[int]` and `var x: Dictionary[String, int]` report nothing
- [ ] A bare `Dictionary` reports at `godot_version: "4.4"` and not at `"4.3"`, while a bare `Array` reports at both
- [ ] A bare `Array` in a parameter or a return type also reports

**Verify:** `go test -race ./lint/...` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing test**

Append to `lint/rules_typing_test.go`:

```go
func TestRequireVariableTypeReportsAtBothScopes(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-variable-type", `
var health = 100
var typed: int = 100
const LIMIT = 10

func move() -> void:
	var speed = 1.0
	var inferred := 1.0
	print(health, typed, LIMIT, speed, inferred)
`)
	if len(found) != 2 {
		t.Fatalf("got %v, want the two untyped variables", found)
	}
	if found[0].Line != 2 || found[1].Line != 7 {
		t.Errorf("reported lines %d and %d, want 2 and 7", found[0].Line, found[1].Line)
	}
	if found[0].Message != `Variable "health" has no type` {
		t.Errorf("message = %q", found[0].Message)
	}
}

// An @export is still a Variant; the editor infers the exported type from the
// assigned value, which is not the same as the variable carrying one.
func TestRequireVariableTypeReportsAnUntypedExport(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-variable-type", `
@export var speed = 1.0
@export var typed: float = 1.0
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want one diagnostic", found)
	}
	if found[0].Line != 2 {
		t.Errorf("reported line %d, want 2", found[0].Line)
	}
}

// GDScript types a const from its value, so it is already statically typed.
func TestRequireVariableTypeIgnoresAConstant(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-variable-type", `
const LIMIT = 10

func move() -> void:
	const LOCAL = 2
	print(LIMIT, LOCAL)
`)
	if len(found) != 0 {
		t.Fatalf("got %v, want none", found)
	}
}

func TestRequireTypedCollectionReportsABareAnnotation(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
var items: Array = []
var typed: Array[int] = []
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want one diagnostic", found)
	}
	if found[0].Line != 2 || found[0].Column != 12 {
		t.Errorf("reported at %d:%d, want 2:12 (the annotation)", found[0].Line, found[0].Column)
	}
	if found[0].Message != "Array has no element type; write Array[T]" {
		t.Errorf("message = %q", found[0].Message)
	}
}

// The rule is the only one in gdkit whose applicability varies per finding:
// Array[T] is 4.0 and Dictionary[K, V] is 4.4, so one engine version accepts the
// fix for one and not the other.
func TestRequireTypedCollectionGatesDictionarySeparately(t *testing.T) {
	source := `
var items: Array = []
var lookup: Dictionary = {}
`
	tests := []struct {
		version string
		want    []int
	}{
		{version: "4.3", want: []int{2}},
		{version: "4.4", want: []int{2, 3}},
	}
	for _, test := range tests {
		config := typingConfig()
		config.GodotVersion = test.version
		found := lintSourceWithConfig(t, config, "require-typed-collection", source)
		if len(found) != len(test.want) {
			t.Fatalf("godot_version %q: got %v, want %d diagnostics", test.version, found, len(test.want))
		}
		for index, line := range test.want {
			if found[index].Line != line {
				t.Errorf("godot_version %q: diagnostic %d on line %d, want %d", test.version, index, found[index].Line, line)
			}
		}
	}
}

func TestRequireTypedCollectionChecksSignaturesToo(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-collection", `
func pick(from: Array) -> Array:
	return from
`)
	if len(found) != 2 {
		t.Fatalf("got %v, want the parameter and the return type", found)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```sh
go test -race ./lint/ -run 'RequireVariableType|RequireTypedCollection'
```

Expected: FAIL — no diagnostics, because neither rule is registered.

- [ ] **Step 3: Register the two rules and add the 4.4 floor**

In `lint/rules_typing.go`, extend `typingRuleNames`:

```go
var typingRuleNames = []string{
	"require-return-type",
	"require-argument-type",
	"require-variable-type",
	"require-typed-collection",
}
```

and the version block:

```go
var (
	godot40 = versiongate.Version{Major: 4}
	godot44 = versiongate.Version{Major: 4, Minor: 4}
)
```

- [ ] **Step 4: Collect variables and collection annotations**

Add the `VariableDeclaration` case to `classBody`:

```go
		case *ast.VariableDeclaration:
			c.variable(declaration, "")
```

Add the same case to the `inspect` switch, so a local is collected too:

```go
		case *ast.VariableDeclaration:
			c.variable(declaration, enclosing)
```

`ast.Inspect` visits a declaration's own subtree, so a local variable inside a lambda body is reached by the same walk.

Add the two methods:

```go
// variable records a variable with no type and walks its accessors. A constant
// is never recorded: GDScript types a const from its value, so it is already
// statically typed. An @export is recorded like any other variable — the editor
// infers the exported type from the assigned value, which is not the same as
// the variable carrying one.
//
// enclosing is empty for a class-scope declaration, which no exempt list can
// reach; inside an accessor body it is the property's own name, which is what a
// reader would write a pattern for.
func (c *typingCollector) variable(declaration *ast.VariableDeclaration, enclosing string) {
	if !declaration.Constant && !annotated(declaration.Type, declaration.Inferred) {
		c.add(typingSite{
			rule:      "require-variable-type",
			message:   fmt.Sprintf("Variable %q has no type", declaration.Name),
			enclosing: enclosing,
			floor:     godot40,
			span:      declaration.NameSpan,
		})
	}
	c.collection(declaration.Type, declaration.TypeSpan, enclosing)
	c.functionScope(declaration.Name, declaration.Getter)
	if declaration.Setter != nil {
		c.functionScope(declaration.Name, declaration.Setter.Body)
	}
}

// parameterizedForms names the typed form each bare collection wants. They
// differ in arity, so the message cannot be built from the type name alone.
var parameterizedForms = map[string]string{
	"Array":      "Array[T]",
	"Dictionary": "Dictionary[K, V]",
}

// collection records a bare Array or Dictionary annotation, wherever it is
// written: a variable, a parameter, a return type, or a loop variable.
//
// Only a written annotation is examined. "var x := []" infers an untyped Array,
// and catching that needs expression inference this package does not have and
// should not grow — a single-file linter that starts inferring types is the
// first step toward a semantic analyzer, which belongs in its own package.
func (c *typingCollector) collection(typeName string, span token.Span, enclosing string) {
	var floor versiongate.Version
	switch typeName {
	case "Array":
		floor = godot40
	case "Dictionary":
		// Dictionary[K, V] does not exist before Godot 4.4, so on an older
		// engine there is no fix to recommend.
		floor = godot44
	default:
		return
	}
	c.add(typingSite{
		rule:      "require-typed-collection",
		message:   fmt.Sprintf("%s has no element type; write %s", typeName, parameterizedForms[typeName]),
		enclosing: enclosing,
		floor:     floor,
		span:      span,
	})
}
```

- [ ] **Step 5: Check signature annotations for bare collections**

In `function`, after the return-type block, add:

```go
	c.collection(declaration.ReturnType, declaration.ReturnTypeSpan, declaration.Name)
```

In `parameters`, replace the `continue` on an annotated parameter so the annotation is still examined:

```go
	for _, parameter := range parameters {
		if parameter.Variadic {
			continue
		}
		c.collection(parameter.Type, parameter.TypeSpan, enclosing)
		if annotated(parameter.Type, parameter.Inferred) {
			continue
		}
		c.add(typingSite{
			rule:      rule,
			message:   fmt.Sprintf("Argument %q of %s has no type", parameter.Name, owner),
			enclosing: enclosing,
			floor:     godot40,
			span:      parameter.NameSpan,
		})
	}
```

- [ ] **Step 6: Run the tests to verify they pass**

```sh
gofmt -w . && go test -race ./lint/ -run 'RequireVariableType|RequireTypedCollection'
```

Expected: PASS.

- [ ] **Step 7: Update the pinned set**

In `lint/pending_test.go`, `want` becomes (sorted):

```go
	want := []string{
		"no-engine-logging", "require-argument-type", "require-return-type",
		"require-typed-collection", "require-variable-type",
	}
```

- [ ] **Step 8: Add the fixture**

Create `lint/testdata/typing/variables.gd`:

```gdscript
extends Node

const LIMIT := 10

@export var speed = 1.0

var items: Array = []
var lookup: Dictionary[String, int] = {}


func tally() -> int:
	var total = 0
	for value in items:
		total += value
	return total + LIMIT + int(speed) + lookup.size()
```

Add to `typingFixtureExpectations` in `lint/rules_fixture_test.go`:

```go
	"variables.gd": {
		{5, "require-variable-type"},
		{7, "require-typed-collection"},
		// "items" is annotated, so only its bare Array is reported.
		{12, "require-variable-type"},
	},
```

The `for` on line 13 produces nothing yet; Task 6 adds its expectation.

- [ ] **Step 9: Verify the whole suite**

```sh
gofmt -w . && go vet ./... && go test -race ./...
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add lint/rules_typing.go lint/rules_typing_test.go lint/testdata/typing lint/rules_fixture_test.go lint/pending_test.go
git commit -m "Report variables and bare collections with no type

require-variable-type covers class and function scope, including an
@export, whose exported type the editor infers from the value without
the variable itself carrying one. A const is never reported: GDScript
types it from its value.

require-typed-collection carries two floors rather than one, because
Array[T] is Godot 4.0 and Dictionary[K, V] is 4.4, so one engine version
accepts the fix for one and not the other. The floor therefore lives on
the site rather than on the rule. Only written annotations are examined;
var x := [] infers an untyped Array that this rule deliberately misses."
```

---

### Task 6: `require-signal-argument-type` and `require-typed-loop-variable`

**Goal:** The last two sites: a signal's payload and a `for` header.

**Files:**
- Modify: `lint/rules_typing.go`, `lint/rules_typing_test.go`, `lint/rules_fixture_test.go`, `lint/pending_test.go`, `lint/testdata/typing/variables.gd`
- Create: `lint/testdata/typing/signals.gd`

**Acceptance Criteria:**
- [ ] `signal damaged(amount)` reports at the parameter, with a message naming the signal
- [ ] `signal damaged(amount: int)` reports nothing
- [ ] `for item in items` reports `require-typed-loop-variable` at the loop variable
- [ ] `for item: Thing in items` reports nothing
- [ ] The loop rule is silent at `godot_version: "4.1"` and reports at `"4.2"`
- [ ] A signal's exempt list matches the **signal's** name, not an enclosing function

**Verify:** `go test -race ./lint/...` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing test**

Append to `lint/rules_typing_test.go`:

```go
func TestRequireSignalArgumentTypeReportsAnUntypedPayload(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-signal-argument-type", `
signal damaged(amount)
signal healed(amount: int)
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want one diagnostic", found)
	}
	if found[0].Line != 2 {
		t.Errorf("reported line %d, want 2", found[0].Line)
	}
	if found[0].Message != `Argument "amount" of signal "damaged" has no type` {
		t.Errorf("message = %q", found[0].Message)
	}
}

// A signal has no enclosing function, so its exempt list matches its own name.
func TestRequireSignalArgumentTypeExemptsBySignalName(t *testing.T) {
	config := typingConfig()
	config.RequireSignalArgumentType = []string{"damaged"}
	found := lintSourceWithConfig(t, config, "require-signal-argument-type", `
signal damaged(amount)
signal healed(amount)
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want only the unexempted signal", found)
	}
	if found[0].Line != 3 {
		t.Errorf("reported line %d, want 3", found[0].Line)
	}
}

func TestRequireTypedLoopVariableReportsAnUntypedHeader(t *testing.T) {
	found := lintSourceWithConfig(t, typingConfig(), "require-typed-loop-variable", `
func tally(items: Array[int]) -> int:
	var total := 0
	for value in items:
		total += value
	for typed: int in items:
		total += typed
	return total
`)
	if len(found) != 1 {
		t.Fatalf("got %v, want one diagnostic", found)
	}
	if found[0].Line != 4 {
		t.Errorf("reported line %d, want 4", found[0].Line)
	}
	if found[0].Message != `Loop variable "value" has no type` {
		t.Errorf("message = %q", found[0].Message)
	}
}

// A typed loop variable does not exist before Godot 4.2, so on an older engine
// there is no fix to recommend and the rule says nothing.
func TestRequireTypedLoopVariableIsGatedAt42(t *testing.T) {
	source := `
func tally(items: Array[int]) -> int:
	var total := 0
	for value in items:
		total += value
	return total
`
	for version, want := range map[string]int{"4.1": 0, "4.2": 1} {
		config := typingConfig()
		config.GodotVersion = version
		found := lintSourceWithConfig(t, config, "require-typed-loop-variable", source)
		if len(found) != want {
			t.Errorf("godot_version %q: got %v, want %d diagnostics", version, found, want)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```sh
go test -race ./lint/ -run 'RequireSignalArgumentType|RequireTypedLoopVariable'
```

Expected: FAIL — no diagnostics.

- [ ] **Step 3: Register the rules and add the 4.2 floor**

In `lint/rules_typing.go`, `typingRuleNames` becomes the full set:

```go
var typingRuleNames = []string{
	"require-return-type",
	"require-argument-type",
	"require-variable-type",
	"require-typed-collection",
	"require-signal-argument-type",
	"require-typed-loop-variable",
}
```

and the version block:

```go
var (
	godot40 = versiongate.Version{Major: 4}
	godot42 = versiongate.Version{Major: 4, Minor: 2}
	godot44 = versiongate.Version{Major: 4, Minor: 4}
)
```

- [ ] **Step 4: Collect the two sites**

Add the signal case to `classBody`:

```go
		case *ast.SignalDeclaration:
			c.signal(declaration)
```

and the method:

```go
// signal records a signal's untyped parameters. A signal's payload is the
// boundary an untyped value travels furthest from: the emitter and every
// receiver are written apart, and nothing checks the type between them.
//
// A signal has no enclosing function, so its own name is what an exempt
// pattern matches.
func (c *typingCollector) signal(declaration *ast.SignalDeclaration) {
	c.parameters("require-signal-argument-type", declaration.Parameters, declaration.Name,
		fmt.Sprintf("signal %q", declaration.Name))
}
```

Add the loop case to the `inspect` switch:

```go
		case *ast.ForStatement:
			if !annotated(declaration.Type, false) {
				c.add(typingSite{
					rule:      "require-typed-loop-variable",
					message:   fmt.Sprintf("Loop variable %q has no type", declaration.Variable),
					enclosing: enclosing,
					floor:     godot42,
					span:      declaration.VariableSpan,
				})
			}
			c.collection(declaration.Type, declaration.TypeSpan, enclosing)
```

- [ ] **Step 5: Run the tests to verify they pass**

```sh
gofmt -w . && go test -race ./lint/ -run 'RequireSignalArgumentType|RequireTypedLoopVariable'
```

Expected: PASS.

- [ ] **Step 6: Update the pinned set to its final value**

In `lint/pending_test.go`:

```go
	want := []string{
		"no-engine-logging", "require-argument-type", "require-return-type",
		"require-signal-argument-type", "require-typed-collection",
		"require-typed-loop-variable", "require-variable-type",
	}
```

- [ ] **Step 7: Add the signal fixture and finish the variables one**

Create `lint/testdata/typing/signals.gd`:

```gdscript
extends Node

signal damaged(amount)
signal healed(amount: int, source: Node)


func _on_hit() -> void:
	damaged.emit(1)
	healed.emit(1, self)
```

Add to `typingFixtureExpectations`:

```go
	"signals.gd": {
		{3, "require-signal-argument-type"},
	},
```

And add the `for` on line 13 of `variables.gd` to that file's expectations, keeping them in line order:

```go
	"variables.gd": {
		{5, "require-variable-type"},
		{7, "require-typed-collection"},
		// "items" is annotated, so only its bare Array is reported.
		{12, "require-variable-type"},
		{13, "require-typed-loop-variable"},
	},
```

- [ ] **Step 8: Verify the whole suite**

```sh
gofmt -w . && go vet ./... && go test -race ./...
```

Expected: PASS, with `TestFixturesExerciseEveryRule` now satisfied for all six rules.

- [ ] **Step 9: Commit**

```bash
git add lint/rules_typing.go lint/rules_typing_test.go lint/testdata/typing lint/rules_fixture_test.go lint/pending_test.go
git commit -m "Report signal payloads and loop variables with no type

A signal's payload is the boundary an untyped value travels furthest
from: the emitter and every receiver are written apart and nothing checks
the type between them, so the exempt list matches the signal's own name
rather than an enclosing function.

A typed loop variable is Godot 4.2, so the rule is gated: on an older
engine there is no fix to recommend and it says nothing."
```

---

### Task 7: Cross-rule version-gate test

**Goal:** Pin that the gate makes rules silent for the right reason, since "reports nothing" is also what a broken rule does.

**Files:**
- Modify: `lint/rules_typing_test.go`

**Acceptance Criteria:**
- [ ] One test asserts that a 4.0 project gets the 4.0 rules and neither gated rule
- [ ] The same source at 4.7 produces every rule, proving the silence was the gate and not a dead traversal

**Verify:** `go test -race ./lint/ -run TypingVersionGate` → PASS

**Steps:**

- [ ] **Step 1: Write the test**

Append to `lint/rules_typing_test.go`:

```go
// The version gate's job is to make a rule report nothing, which is exactly
// what a rule whose traversal is broken does. Asserting both sides of the gate
// on one source is what tells the two apart: every rule must fire on the newest
// engine, and only the ungated ones on the oldest.
func TestTypingVersionGateSilencesOnlyTheGatedRules(t *testing.T) {
	source := `
signal damaged(amount)

var items: Array = []
var lookup: Dictionary = {}


func reset():
	items.clear()


func tally(start) -> int:
	var total = start
	for value in items:
		total += value
	return total + lookup.size()
`
	tests := []struct {
		version string
		want    []string
	}{
		{
			version: "4.0",
			want: []string{
				"require-argument-type",
				"require-return-type",
				"require-typed-collection",
				"require-variable-type",
			},
		},
		{
			version: "4.7",
			want: []string{
				"require-argument-type",
				"require-return-type",
				"require-signal-argument-type",
				"require-typed-collection",
				"require-typed-loop-variable",
				"require-variable-type",
			},
		},
	}
	for _, test := range tests {
		config := typingConfig()
		config.GodotVersion = test.version
		report := lintProject(t, config, map[string]string{"a.gd": source})
		seen := make(map[string]bool)
		for _, diagnostic := range report.Diagnostics {
			seen[diagnostic.Rule] = true
		}
		var got []string
		for rule := range seen {
			got = append(got, rule)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("godot_version %q fired %v, want %v", test.version, got, test.want)
		}
	}
}
```

Add `"reflect"` and `"sort"` to the file's imports.

Every rule has a site in that source: `reset` is unannotated (`require-return-type`), `tally`'s `start` is untyped (`require-argument-type`), `total` is untyped (`require-variable-type`), `items` and `lookup` are bare collections (`require-typed-collection`), `damaged`'s parameter is untyped (`require-signal-argument-type`), and the `for` header is untyped (`require-typed-loop-variable`). The two lists may therefore differ only by the two gated rules.

- [ ] **Step 2: Run it**

```sh
go test -race ./lint/ -run TypingVersionGate
```

Expected: PASS. If it fails, the failure names exactly which rule set each version produced. The property the test exists for is that the 4.0 list is the 4.7 list minus `require-signal-argument-type` and `require-typed-loop-variable`; if anything else differs, that is a bug in a rule, not in the test.

- [ ] **Step 3: Commit**

```bash
git add lint/rules_typing_test.go
git commit -m "Pin both sides of the typing rules' version gate

A gated rule reports nothing, which is also what a rule with a broken
traversal does. Asserting the same source on the oldest and the newest
engine tells them apart: the two lists may differ only by the two gated
rules."
```

---

### Task 8: Documentation

**Goal:** The README describes the six rules, `godot_version`, and the new rule count, so a user can find and configure them.

**Files:**
- Modify: `README.md` (the `gdkit lint` row at line 19, the sentence at line 152, the lint rule reference, the lint configuration table)

**Acceptance Criteria:**
- [ ] Both "30 … problems" counts read 36
- [ ] Each of the six rules appears with what it reports, what satisfies it, its exempt key, and its version floor
- [ ] `godot_version` appears in the lint configuration table with its default and the reason the default is the newest version
- [ ] The README states that the six rules ship inert and how to enable them
- [ ] The `require-typed-collection` blind spot (`var x := []`) is documented

**Verify:** `grep -c "require-return-type" README.md` → at least 1, and `grep "30 naming" README.md` → no output

**Steps:**

- [ ] **Step 1: Confirm the counts to change**

```sh
grep -n "30 naming\|reports 30" README.md
```

Expected: the two lines named in **Files**. Both become 36 — the six new rules bring the registry from 30 to 36.

- [ ] **Step 2: Update the counts**

```sh
sed -i '' 's/Reports 30 naming/Reports 36 naming/; s/reports 30 naming/reports 36 naming/' README.md
grep -n "36 naming" README.md
```

Expected: two lines. If `sed` matched neither, open the file and edit the sentences by hand — the wording may differ slightly from this plan.

- [ ] **Step 3: Document the rules**

In the lint rule reference section, add a subsection. Write it in the README's existing voice — what the rule reports, then why:

```markdown
#### Static typing

Six rules require static type annotations. An untyped GDScript declaration is a
`Variant`: the engine cannot check it, cannot specialize it, and reports nothing
when the wrong thing is assigned to it. Godot itself warns about an unannotated
function return (`UNTYPED_DECLARATION`); these rules make that a check you can
gate CI on, and extend it to the sites the engine does not warn about. All six
**ship inert** — name them in `enable`, or set `enable_new_rules`, to turn them
on.

| Rule | Reports | Needs Godot |
| --- | --- | --- |
| `require-return-type` | `func f():` with no `->` | 4.0 |
| `require-argument-type` | a parameter with no type and no `:=` default | 4.0 |
| `require-variable-type` | `var x` or `var x = v`, at class or function scope, `@export` included | 4.0 |
| `require-typed-collection` | an annotation of bare `Array` or `Dictionary` | `Array[T]` 4.0, `Dictionary[K, V]` 4.4 |
| `require-signal-argument-type` | `signal s(arg)` with an untyped parameter | 4.0 |
| `require-typed-loop-variable` | `for item in …` with no `: Type` | 4.2 |

A declaration satisfies these rules three ways: an explicit annotation, `:=`
inference, or an explicit `: Variant`, which opts out on purpose. A `const` is
never reported — GDScript types it from its value — and neither is a variadic
`...args` parameter, which is a `Variant` array by construction. A lambda's
parameters are checked; a lambda's return type is not.

A rule says nothing when `godot_version` is older than the version its fix
needs, so a project is never told to write an annotation its engine cannot
parse.

`require-typed-collection` examines written annotations only. `var x := []`
infers an untyped `Array` and is not reported: finding that needs expression
inference, which the linter does not do.

Each rule's config key holds function-name globs it skips:

```json
{
  "enable": ["require-return-type", "require-argument-type"],
  "require-return-type": ["_ready", "_on_*"]
}
```

The pattern matches the function being declared for `require-return-type` and
`require-argument-type`, the enclosing function for `require-variable-type`,
`require-typed-collection`, and `require-typed-loop-variable`, and the signal's
own name for `require-signal-argument-type`. A class-scope declaration has no
enclosing function and is exempted with a `# gdkit:ignore` comment instead.
```

- [ ] **Step 4: Document `godot_version`**

Add a row to the lint configuration table, beside `source_roots` and `exclude`:

```markdown
| `godot_version` | `"4.7"` |
```

and a paragraph below the table:

```markdown
`godot_version` is the engine version the project targets, written as
`major.minor` or `major.minor.patch`. A rule whose fix needs newer syntax says
nothing on an older engine. It defaults to the newest Godot gdkit knows, which
is safe because every rule that reads it ships inert: a project that has not
enabled one cannot be affected by the default, and a project on an older engine
lowers this one key.
```

- [ ] **Step 5: Verify**

```sh
grep -n "require-return-type\|godot_version" README.md | head
grep -n "30 naming" README.md
```

Expected: the first prints several lines; the second prints nothing.

- [ ] **Step 6: Full verification before the final commit**

```sh
gofmt -w . && go vet ./... && go build ./... && go test -race ./...
```

Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add README.md
git commit -m "Document the typing rules and godot_version

Records what satisfies each rule, which name an exempt pattern matches
per rule, the version floors, and the one case
require-typed-collection deliberately misses."
```

---

## Follow-on work, deliberately not in this plan

Each is its own spec, named in the design document. The findings below come from
a feasibility investigation against the Godot 4.7 source and a local editor
binary; they are recorded here so the next spec does not re-derive them.

**`lint fix` for return types.** Confirmed: `format`'s verifier rejects the
rewrite twice over — `comparedFields` (`format/tree.go:44-60`) compares
`FunctionDeclaration.ReturnType` verbatim, and `significantTokens`
(`format/tokens.go:115-116`) exempts only layout, parens, commas, and
semicolons, so the added `->` and `void` appear as new token keys. Reusable
unchanged: `movedSuppression` (`format/suppression.go:97-117`), which is exactly
the check an additive fix needs, and `replaceFile` (`format/apply.go:45-79`).
The tree and token checks want *inverted* forms: assert the only tree delta is
the one `ReturnType`, and the only token delta is `{-> +1, void +1}`. Do not
route through `gdformat.FileWithOptions`, which renders whole files; splice bytes
at the first depth-0 `token.Colon` found by lexing from `KeywordSpan`.

Three hazards, in order of how much they should worry us:

1. **Some paths return a value** — visible in-file, and the reason to require
   that *no* `ReturnStatement` carries a value.
2. **Overriding a non-void parent** — largely self-policing, because an
   unannotated override already adopts the parent's return type
   (`gdscript_analyzer.cpp:1881-1894`), so such a function with no return
   already fails to compile. But that check is `#ifdef TOOLS_ENABLED`, so it is
   editor-only. A bundled native-virtual table is still needed: `_process` is
   `void` on `Node`, `float` on `AnimationNode`, and `bool` on `MainLoop`, so a
   name-only table is unsound. `godot --dump-extension-api` is the source.
3. **Callers** — the hazard that is easy to miss. `-> void` makes every
   statically-typed call site that consumes the return value a hard error *in a
   file the fix never touched* (`gdscript_analyzer.cpp:3455`). Untyped receivers
   degrade silently instead.

Every failure mode is a compile-time `Parse Error` that stops the script
loading, not a latent runtime bug, so a bad fix is loud. The fixer belongs
outside `lint.Rule` — rules are single-file by construction — taking the whole
`*project.Snapshot`, with a `lint fix` subcommand shaped like `format write`,
including printing the written list on a partial failure.

Corpus signal, from four of our Godot projects (4,934 functions, addons
excluded): 231 functions are unannotated (4.7%), and 209 of those — 90.5% —
have no `return` statement at all. So `-> void` is the fix for roughly nine in
ten findings. That count is from a line-based heuristic, not from gdparser, so
it should be re-measured with the census before the fix is scoped.

**The Variant-return census.** House precedent to copy exists:
`architecture.Report` carries `Files` and `Edges` as a sorted inventory
alongside `Diagnostics`, gated in text mode by `--show-edges`, and
`uid.Report.Scripts` is a bare metric in a JSON report. A census must stay
outside `HasFindings`/`HasErrors` so it cannot make a run fail. Report
"unannotated" and "explicit `Variant`" as separate buckets, and do not claim to
resolve the override-inherits-parent-type case in v1.

The investigation recommends shipping the census in the *same release* as these
rules, on the grounds that it is read-only, cheap, and is what tells a project
how large its problem is before it turns a rule on. That is a sequencing call
worth making deliberately; it does not change this plan.

**Godot performance rules** — they need a call-context notion `lint` does not
have.
