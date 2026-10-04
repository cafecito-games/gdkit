# GDScript code generation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `gdkit gen`, a fifth gdkit tool that generates `_to_string` and `equals` into a GDScript class that opted in, inside a sentinel-delimited region it owns and verifies.

**Architecture:** A new top-level `generate/` package mirroring `format` and `uid`: a pure `Check` on a `*Generator` built by `New`, a single `Apply` writer, its own config and diagnostic names, and `cmd/gdkit` holding only flags and exit codes. Unlike `lint`'s per-file rules, `generate` indexes the whole project, because `equals` is only sound with the entire inheritance graph visible. `project` gains a `Selection` concept so the universe can be parsed unfiltered while the tool acts on a filtered subset.

**Tech Stack:** Go 1.25, `github.com/cafecito-games/gdparser` v0.1.5 (`ast`, `format`), standard library only otherwise.

**Spec:** [docs/superpowers/specs/2026-10-04-gdscript-codegen-design.md](../specs/2026-10-04-gdscript-codegen-design.md). Read it before Task 5; the capability rules are stated there precisely and this plan does not restate their justification.

**Deferred:** `deep_equals` has [its own spec](../specs/2026-10-04-gdscript-deep-equals-design.md) and is not in this plan. It is *not* a drop-in third generator — it widens capability resolution to follow field references and adds a builtin type catalogue, a recursion-state helper method, and diagnostic severity. Do not build shortcuts that assume a later generator only needs an `emit_*.go`.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `project/project.go` | gains `Selection`, `Config.Selection`, `Snapshot.Selected` |
| `generate/model.go` | `Diagnostic`, `Result`, `Report`, rule name constants |
| `generate/config.go` | `Config`, `DefaultConfig`, `LoadConfig`, `Validate` |
| `generate/marker.go` | the three directive forms and the region sentinels |
| `generate/region.go` | region extent, the owned leading gap, splicing |
| `generate/fields.go` | member `var` selection |
| `generate/index.go` | `class_name`, inheritance, declared signatures, fields, cycles |
| `generate/capability.go` | `provider` truth table and the demotion fixed point |
| `generate/generator.go` | `Generator` interface, `Signature`, the registry |
| `generate/emit_to_string.go` | the `to_string` generator |
| `generate/emit_equals.go` | the `equals` generator |
| `generate/check.go` | `New`, `Check`, the 4–6 re-resolution loop |
| `generate/verify.go` | outside-the-region invariant and the format oracle |
| `generate/apply.go` | the only writer, including `--prune` |
| `cmd/gdkit/generate.go` | `gen check`/`write`/`init` flags, output, exit codes |

Tests sit beside each file as `*_test.go`, plus `generate/testing_test.go` for shared helpers.

---

### Task 0: `project.Selection`

**Goal:** `project.Load` can parse the whole project while reporting which scripts a caller should act on.

**Files:**
- Modify: `project/project.go`
- Test: `project/project_test.go`

**Acceptance Criteria:**
- [ ] `Config.Selection == nil` leaves `Snapshot.Selected` equal to `Snapshot.Paths`
- [ ] A `Selection` narrows `Selected` without removing anything from `Paths` or `Scripts`
- [ ] `Selection.HonorIgnoreFile` reads the root `.gdkitignore`
- [ ] The four existing tools are unaffected

**Verify:** `go test -race ./project` → ok

**Steps:**

- [ ] **Step 1: Write the failing test**

```go
func TestLoadSelectionNarrowsSelectedButNotPaths(t *testing.T) {
	root := t.TempDir()
	write(t, root, "keep.gd", "class_name Keep\n")
	write(t, root, "addons/vendor.gd", "class_name Vendor\n")
	snapshot, err := Load(Config{
		Root:      root,
		Selection: &Selection{SourceRoots: []string{"."}, Exclude: []string{"addons/**"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Paths) != 2 {
		t.Fatalf("Paths = %v, want both scripts", snapshot.Paths)
	}
	if !reflect.DeepEqual(snapshot.Selected, []string{"keep.gd"}) {
		t.Errorf("Selected = %v, want [keep.gd]", snapshot.Selected)
	}
	if snapshot.Scripts["addons/vendor.gd"] == nil {
		t.Error("an unselected script must still be parsed and indexed")
	}
}

func TestLoadWithNoSelectionSelectsEverything(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.gd", "class_name A\n")
	snapshot, err := Load(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Selected, snapshot.Paths) {
		t.Errorf("Selected = %v, want Paths %v", snapshot.Selected, snapshot.Paths)
	}
}
```

Add the `write` helper beside the other test helpers if `project_test.go` has none:

```go
func write(t *testing.T, root, name, contents string) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -race ./project -run TestLoadSelection`
Expected: FAIL — `undefined: Selection`

- [ ] **Step 3: Add the type and the field**

In `project/project.go`, above `Config`:

```go
// Selection narrows which parsed scripts a tool acts on. Scripts outside it
// are still walked, parsed, and present in the snapshot, so a tool that needs
// a complete class_name and inheritance index can have one while still
// generating or reporting on a subset. architecture leaves Exclude on the
// universe instead, because it acts on everything it indexes.
type Selection struct {
	SourceRoots     []string
	Exclude         []string
	HonorIgnoreFile bool
}
```

Add to `Config`:

```go
	// Selection, when non-nil, narrows what the caller acts on without
	// narrowing the universe that is walked and parsed.
	Selection *Selection
```

Add to `Snapshot`:

```go
	// Selected is the subset of Paths that Config.Selection admits, sorted.
	// It equals Paths when Selection is nil.
	Selected []string
```

- [ ] **Step 4: Compute `Selected` in `Load`**

After `Paths` is sorted, before `Load` returns, add:

```go
	selected, err := selectPaths(root, config.Selection, snapshot.Paths)
	if err != nil {
		return nil, err
	}
	snapshot.Selected = selected
```

And the function, which reuses the matcher `Load` already builds for the universe:

```go
// selectPaths returns the subset of paths that selection admits, sorted. A nil
// selection admits everything, which is what the four tools that predate
// Selection pass.
func selectPaths(root string, selection *Selection, paths []string) ([]string, error) {
	if selection == nil {
		return paths, nil
	}
	var ignored *ignore.Matcher
	if selection.HonorIgnoreFile {
		matcher, err := loadIgnoreFile(root)
		if err != nil {
			return nil, err
		}
		ignored = matcher
	}
	selected := make([]string, 0, len(paths))
	for _, path := range paths {
		if !underAnyRoot(path, selection.SourceRoots) {
			continue
		}
		if glob.MatchAny(selection.Exclude, path) {
			continue
		}
		if ignored != nil && ignored.Match(path) {
			continue
		}
		selected = append(selected, path)
	}
	return selected, nil
}
```

Factor the existing `.gdkitignore` read in `Load` into `loadIgnoreFile(root)` and the existing source-root containment test into `underAnyRoot(path, roots)` so both callers share one implementation. If `Load` has no such containment helper because it prunes during the walk, add:

```go
// underAnyRoot reports whether path sits under one of roots. An empty or "."
// root admits everything, matching how Load treats SourceRoots.
func underAnyRoot(path string, roots []string) bool {
	if len(roots) == 0 {
		return true
	}
	for _, root := range roots {
		root = strings.TrimSuffix(filepath.ToSlash(root), "/")
		if root == "" || root == "." || path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./project && go test -race ./architecture ./lint ./format ./uid`
Expected: all ok — the four existing tools pass `Selection: nil` and are unaffected

- [ ] **Step 6: Commit**

```bash
git add project/project.go project/project_test.go
git commit -m "Let a tool act on a subset of the project it indexes"
```

---

### Task 1: Diagnostics and the report model

**Goal:** `generate`'s public result shape exists, mirroring `format`'s so one output handler can serve both.

**Files:**
- Create: `generate/model.go`, `generate/model_test.go`

**Acceptance Criteria:**
- [ ] Rule name constants match the spec's table exactly
- [ ] `Diagnostic.String()` renders in the same text form `lint` and `format` use
- [ ] `Report.sort()` orders by path, line, column, rule
- [ ] No severity field: v1 has no warnings

**Verify:** `go test -race ./generate` → ok

**Steps:**

- [ ] **Step 1: Write the failing test**

```go
func TestDiagnosticStringMatchesTheSharedTextForm(t *testing.T) {
	d := Diagnostic{Rule: ruleStale, Message: "generated region is out of date", Path: "hex.gd", Line: 14}
	want := "hex.gd:14: Error: generated region is out of date (generate.stale)"
	if got := d.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestReportSortIsDeterministic(t *testing.T) {
	report := Report{
		Results: []Result{{Path: "b.gd"}, {Path: "a.gd"}},
		Diagnostics: []Diagnostic{
			{Path: "a.gd", Line: 2, Rule: ruleMarker},
			{Path: "a.gd", Line: 1, Rule: ruleStale},
			{Path: "a.gd", Line: 1, Rule: ruleConflict},
		},
	}
	report.sort()
	if report.Results[0].Path != "a.gd" {
		t.Errorf("results not sorted by path: %+v", report.Results)
	}
	gotRules := []string{report.Diagnostics[0].Rule, report.Diagnostics[1].Rule, report.Diagnostics[2].Rule}
	wantRules := []string{ruleConflict, ruleStale, ruleMarker}
	if !reflect.DeepEqual(gotRules, wantRules) {
		t.Errorf("diagnostics = %v, want %v", gotRules, wantRules)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -race ./generate`
Expected: FAIL — no such package

- [ ] **Step 3: Write `generate/model.go`**

```go
// Package generate writes boilerplate value-object methods into a GDScript
// class that opted in, inside a sentinel-delimited region it owns.
//
// It is a whole-project analysis rather than a per-file one: equals composes
// with an ancestor's implementation and is refused when a descendant would
// inherit an unsound one, so both answers need the entire inheritance graph.
// Check is pure; Apply is the only writer.
package generate

import (
	"fmt"
	"sort"
)

// The rules a generate run reports. They are a public contract: they appear in
// JSON output and in configuration, so renaming one breaks user projects.
const (
	// ruleSourceParse marks a file that could not be parsed. It is reported
	// across the whole universe, not only the selection, when an
	// inheritance-sensitive generator is requested.
	ruleSourceParse = "source-parse"
	// ruleClassNameDuplicate marks two scripts claiming one class_name that a
	// requested class needs to resolve.
	ruleClassNameDuplicate = "class_name.duplicate"
	// ruleStale marks a region that is missing or out of date. Only check
	// reports it; for write, fixing it is the point.
	ruleStale = "generate.stale"
	// ruleMarker marks a malformed directive, an unknown generator name, or a
	// second region in one class.
	ruleMarker = "generate.marker"
	// ruleConflict marks a method declared outside the region with the same
	// name as one a requested generator emits, in an incompatible shape.
	// GDScript has no overloading, so emitting ours would not compile.
	ruleConflict = "generate.conflict"
	// ruleUnsupported marks a class generate refuses.
	ruleUnsupported = "generate.unsupported"
	// ruleOrphaned marks a region whose class no longer opts in.
	ruleOrphaned = "generate.orphaned"
	// ruleUnsafe marks a candidate that verification or the format oracle
	// refused.
	ruleUnsafe = "generate.unsafe"
)

// Diagnostic explains why one class was not generated for. Line and Column are
// 1-based. There is no severity field: every v1 rule is an error, and the
// deep_equals generator is what introduces the first warning.
type Diagnostic struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
}

// String renders the diagnostic in the text form gdkit lint and format use, so
// one output handler serves all three.
func (d Diagnostic) String() string {
	return fmt.Sprintf("%s:%d: Error: %s (%s)", d.Path, d.Line, d.Message, d.Rule)
}

// Result is the outcome for one file that produced a candidate.
type Result struct {
	// Path is project-relative and slash-separated.
	Path string `json:"path"`
	// Changed reports that the candidate differs from the file on disk.
	Changed bool `json:"changed"`
}

// Report is the deterministic public result of one run.
type Report struct {
	Results     []Result     `json:"results"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// HasChanges reports whether any file would be rewritten.
func (r Report) HasChanges() bool {
	for _, result := range r.Results {
		if result.Changed {
			return true
		}
	}
	return false
}

// HasDiagnostics reports whether any class was refused.
func (r Report) HasDiagnostics() bool { return len(r.Diagnostics) > 0 }

func (r *Report) sort() {
	sort.SliceStable(r.Results, func(i, j int) bool { return r.Results[i].Path < r.Results[j].Path })
	sort.SliceStable(r.Diagnostics, func(i, j int) bool {
		a, b := r.Diagnostics[i], r.Diagnostics[j]
		switch {
		case a.Path != b.Path:
			return a.Path < b.Path
		case a.Line != b.Line:
			return a.Line < b.Line
		case a.Column != b.Column:
			return a.Column < b.Column
		default:
			return a.Rule < b.Rule
		}
	})
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -race ./generate`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add generate/model.go generate/model_test.go
git commit -m "Add the generate report model and diagnostic names"
```

---

### Task 2: The marker grammar

**Goal:** The three directive forms parse, strictly, with no change to `internal/suppression`.

**Files:**
- Create: `generate/marker.go`, `generate/marker_test.go`

**Acceptance Criteria:**
- [ ] `# gdkit:generate = to_string, equals` yields both names
- [ ] `gdlint` is accepted in place of `gdkit`, as the suppression directives do
- [ ] A trailing remark is an error, not a silently mangled last name
- [ ] `# gdkit:generate:ignore` does not match the generate form, and vice versa
- [ ] `# gdkit:generate:ignore` does not match `ignore-field`
- [ ] `lint` reports nothing on a file carrying any of the three forms

**Verify:** `go test -race ./generate -run TestMarker` and `go test -race ./lint -run TestLintIgnoresGenerateMarkers` → ok

**Steps:**

- [ ] **Step 1: Write the failing test**

```go
func TestMarkerGenerate(t *testing.T) {
	for line, want := range map[string][]string{
		"# gdkit:generate = to_string, equals":  {"to_string", "equals"},
		"\t# gdkit : generate=equals":           {"equals"},
		"# gdlint:generate = to_string":         {"to_string"},
		"# gdkit:generate=  equals ,to_string ": {"equals", "to_string"},
	} {
		got, err := MatchGenerate(line)
		if err != nil {
			t.Errorf("MatchGenerate(%q) errored: %v", line, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("MatchGenerate(%q) = %v, want %v", line, got, want)
		}
	}
}

// Unlike internal/suppression, which lets a list run to end of line for gdlint
// compatibility, this directive is strict: a remark is an error rather than
// part of the last name.
func TestMarkerGenerateRejectsRubbish(t *testing.T) {
	for _, line := range []string{
		"# gdkit:generate = equals remark",
		"# gdkit:generate = ",
		"# gdkit:generate = equals,",
		"# gdkit:generate = no_such_generator",
	} {
		if _, err := MatchGenerate(line); err == nil {
			t.Errorf("MatchGenerate(%q) accepted a bad list", line)
		}
	}
}

func TestMarkerGenerateIgnoresOtherDirectives(t *testing.T) {
	for _, line := range []string{
		"# gdkit:generate:ignore",
		"# gdkit:generate:ignore-field",
		"# gdkit:ignore = equals",
		"# generate = equals",
	} {
		if names, err := MatchGenerate(line); err == nil && names != nil {
			t.Errorf("MatchGenerate(%q) = %v, want no match", line, names)
		}
	}
}

func TestMarkerIgnoreFormsAreDistinct(t *testing.T) {
	if !MatchIgnore("# gdkit:generate:ignore") {
		t.Error("the class form did not match")
	}
	if MatchIgnore("# gdkit:generate:ignore-field") {
		t.Error("the class form matched the field form")
	}
	if !MatchIgnoreField("var x = 1  # gdkit:generate:ignore-field") {
		t.Error("the field form did not match trailing")
	}
	if MatchIgnoreField("# gdkit:generate:ignore") {
		t.Error("the field form matched the class form")
	}
}

// internal/suppression must not see these as suppression directives, or every
// marker in every adopting project becomes an unknown-ignore finding.
func TestMarkersAreNotSuppressionDirectives(t *testing.T) {
	for _, line := range []string{
		"# gdkit:generate = equals",
		"# gdkit:generate:ignore",
		"# gdkit:generate:ignore-field",
	} {
		for _, kind := range suppression.Kinds {
			if _, _, ok := suppression.Match(kind, line); ok {
				t.Errorf("suppression.Match(%v, %q) matched", kind, line)
			}
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -race ./generate -run TestMarker`
Expected: FAIL — `undefined: MatchGenerate`

- [ ] **Step 3: Write `generate/marker.go`**

```go
package generate

import (
	"fmt"
	"regexp"
	"strings"
)

// The region sentinels. A region's content is owned by gen write; the comments
// say so in the file itself, which is why no checksum is needed.
const (
	beginSentinel = "# gdkit:generated:begin"
	endSentinel   = "# gdkit:generated:end"
)

// The marker directives. They borrow the shape of the three suppression
// directives in internal/suppression — "#", gdkit or gdlint, ":", the
// directive word — but they are parsed here, and they are strict where that
// package is deliberately lax. gdlint lets a rule list run to the end of the
// line, which is why a trailing remark silently breaks "# gdkit:ignore"; a
// gdkit-original directive owes gdlint nothing, so a remark is an error.
//
// The class-level and field-level opt-outs are spelled differently on purpose.
// One spelling for both is ambiguous: a standalone comment directly above the
// first var of a class satisfies the definition of both at once, and resolving
// that by position would make inserting a field change the meaning of a
// comment nobody touched.
var (
	generatePattern    = regexp.MustCompile(`#\s*(?:gdlint|gdkit)\s*:\s*generate\s*=\s*(.*)$`)
	ignorePattern      = regexp.MustCompile(`#\s*(?:gdlint|gdkit)\s*:\s*generate\s*:\s*ignore\s*$`)
	ignoreFieldPattern = regexp.MustCompile(`#\s*(?:gdlint|gdkit)\s*:\s*generate\s*:\s*ignore-field\s*$`)
)

// MatchGenerate parses an opt-in directive. It returns (nil, nil) when the line
// holds no such directive, and an error when it holds a malformed one, so a
// typo is reported rather than ignored.
func MatchGenerate(line string) ([]string, error) {
	match := generatePattern.FindStringSubmatch(line)
	if match == nil {
		return nil, nil
	}
	list := strings.TrimSpace(match[1])
	if list == "" {
		return nil, fmt.Errorf("gdkit:generate names no generator")
	}
	names := []string{}
	for _, item := range strings.Split(list, ",") {
		name := strings.TrimSpace(item)
		if name == "" {
			return nil, fmt.Errorf("gdkit:generate has an empty generator name")
		}
		if !isGeneratorName(name) {
			return nil, fmt.Errorf("unknown generator %q", name)
		}
		names = append(names, name)
	}
	return names, nil
}

// MatchIgnore reports the class-level opt-out, which stands alone.
func MatchIgnore(line string) bool { return ignorePattern.MatchString(line) }

// MatchIgnoreField reports the field-level opt-out, which may stand alone above
// a var or trail it.
func MatchIgnoreField(line string) bool { return ignoreFieldPattern.MatchString(line) }

// standsAlone reports whether a line holds nothing but a comment.
func standsAlone(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "#") }
```

Note that `ignorePattern` and `ignoreFieldPattern` are anchored with `\s*$`, which is what keeps `ignore` from matching `ignore-field`. `isGeneratorName` comes from the registry in Task 7; until then, stub it in `marker.go` as `func isGeneratorName(string) bool { return true }` and delete the stub when the registry lands. Remove the stub in Task 7 — leaving it would make `generate.marker` silently stop firing on unknown names.

- [ ] **Step 4: Add the lint cross-check**

In `lint/linter_test.go`:

```go
// A generate marker must not look like a suppression directive. If
// internal/suppression's regex ever widened to match one, every marker in
// every adopting project would become an unknown-ignore finding.
func TestLintIgnoresGenerateMarkers(t *testing.T) {
	source := "class_name Hex\nextends RefCounted\n\n" +
		"# gdkit:generate = to_string, equals\n" +
		"# gdkit:generate:ignore\n" +
		"var q: int  # gdkit:generate:ignore-field\n"
	report := lintSource(t, DefaultConfig(), source)
	for _, d := range report.Diagnostics {
		if d.Rule == "unknown-ignore" {
			t.Errorf("a generate marker was read as a suppression directive: %s", d)
		}
	}
}
```

Use whatever single-source helper `lint`'s tests already provide in place of `lintSource` if it is named differently.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./generate -run TestMarker && go test -race ./lint -run TestLintIgnoresGenerateMarkers`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add generate/marker.go generate/marker_test.go lint/linter_test.go
git commit -m "Add the gdkit:generate directive grammar"
```

---

### Task 3: Region extent and splicing

**Goal:** A region's byte span is found exactly, including the leading gap it owns, and a new region can be spliced in.

**Files:**
- Create: `generate/region.go`, `generate/region_test.go`

**Acceptance Criteria:**
- [ ] The span runs from the begin-sentinel line's first byte to the newline ending the end-sentinel line
- [ ] The span extends backwards over immediately preceding blank lines
- [ ] Trailing blank lines after the end sentinel are *not* in the span
- [ ] A second region in one file is an error
- [ ] An unterminated region is an error
- [ ] Splicing into a file with no region appends at end of file with the right leading gap

**Verify:** `go test -race ./generate -run TestRegion` → ok

**Steps:**

- [ ] **Step 1: Write the failing test**

```go
func TestRegionExtentOwnsTheLeadingGapOnly(t *testing.T) {
	source := []byte("var q: int\n\n\n# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end\n\n\nvar r: int\n")
	span, found, err := FindRegion(source)
	if err != nil || !found {
		t.Fatalf("FindRegion = %v, %v", found, err)
	}
	// The two blank lines before the begin sentinel are owned.
	if got := string(source[span.Start:span.End]); got != "\n\n# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end\n" {
		t.Errorf("span = %q", got)
	}
	// The blank lines after the end sentinel are not.
	if !bytes.HasPrefix(source[span.End:], []byte("\n\nvar r: int\n")) {
		t.Errorf("trailing gap was taken into the span: %q", source[span.End:])
	}
}

func TestRegionExtentAtStartOfFile(t *testing.T) {
	source := []byte("# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end\n")
	span, found, err := FindRegion(source)
	if err != nil || !found {
		t.Fatalf("FindRegion = %v, %v", found, err)
	}
	if span.Start != 0 || span.End != len(source) {
		t.Errorf("span = %+v, want the whole file", span)
	}
}

func TestRegionRejectsASecondRegionAndAnUnterminatedOne(t *testing.T) {
	two := []byte("# gdkit:generated:begin\n# gdkit:generated:end\n# gdkit:generated:begin\n# gdkit:generated:end\n")
	if _, _, err := FindRegion(two); err == nil {
		t.Error("two regions were accepted")
	}
	open := []byte("# gdkit:generated:begin\nfunc f():\n\tpass\n")
	if _, _, err := FindRegion(open); err == nil {
		t.Error("an unterminated region was accepted")
	}
}

func TestRegionNotFound(t *testing.T) {
	if _, found, err := FindRegion([]byte("var q: int\n")); found || err != nil {
		t.Errorf("FindRegion = %v, %v, want not found", found, err)
	}
}

func TestSpliceAppendsWithTheOwnedGap(t *testing.T) {
	source := []byte("class_name Hex\n\nvar q: int\n")
	region := "# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end\n"
	got, _, err := Splice(source, region, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := "class_name Hex\n\nvar q: int\n\n\n" + region
	if string(got) != want {
		t.Errorf("Splice = %q, want %q", got, want)
	}
}

func TestSpliceReplacesInPlaceAndKeepsEverythingElse(t *testing.T) {
	source := []byte("var q: int\n\n\n# gdkit:generated:begin\nold\n# gdkit:generated:end\n\nvar r: int\n")
	region := "# gdkit:generated:begin\nnew\n# gdkit:generated:end\n"
	got, _, err := Splice(source, region, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := "var q: int\n\n\n" + region + "\nvar r: int\n"
	if string(got) != want {
		t.Errorf("Splice = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -race ./generate -run TestRegion`
Expected: FAIL — `undefined: FindRegion`

- [ ] **Step 3: Write `generate/region.go`**

```go
package generate

import (
	"bytes"
	"fmt"
	"strings"
)

// Span is a byte range in a file: [Start, End).
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// FindRegion locates the generated region in source.
//
// The span runs from the first byte of the begin-sentinel line to the newline
// ending the end-sentinel line, extended backwards over every immediately
// preceding blank line. Trailing blank lines after the end sentinel are not
// owned: they belong to the gap before whatever follows.
//
// The asymmetry is not arbitrary. gdparser attributes the gap before a comment
// run that documents a declaration to the top of that run, and the begin
// sentinel is such a run sitting above a func. So the formatter considers the
// leading gap part of the region and the trailing gap part of the next
// statement, and owning exactly the leading one is what lets the region be
// formatted in isolation without disturbing bytes outside it.
func FindRegion(source []byte) (Span, bool, error) {
	begin := indexOfSentinelLine(source, beginSentinel)
	if begin < 0 {
		if indexOfSentinelLine(source, endSentinel) >= 0 {
			return Span{}, false, fmt.Errorf("a %s has no %s", endSentinel, beginSentinel)
		}
		return Span{}, false, nil
	}
	afterBegin := begin + 1
	end := indexOfSentinelLineFrom(source, endSentinel, afterBegin)
	if end < 0 {
		return Span{}, false, fmt.Errorf("a %s has no %s", beginSentinel, endSentinel)
	}
	if next := indexOfSentinelLineFrom(source, beginSentinel, end+1); next >= 0 {
		return Span{}, false, fmt.Errorf("a class holds more than one generated region")
	}
	stop := end
	if newline := bytes.IndexByte(source[end:], '\n'); newline >= 0 {
		stop = end + newline + 1
	} else {
		stop = len(source)
	}
	return Span{Start: extendOverLeadingBlankLines(source, begin), End: stop}, true, nil
}

// extendOverLeadingBlankLines walks back from the start of the line at offset
// over lines that hold nothing but whitespace.
func extendOverLeadingBlankLines(source []byte, offset int) int {
	for offset > 0 {
		previousEnd := offset - 1
		previousStart := bytes.LastIndexByte(source[:previousEnd], '\n') + 1
		if strings.TrimSpace(string(source[previousStart:previousEnd])) != "" {
			return offset
		}
		offset = previousStart
	}
	return offset
}

// indexOfSentinelLine returns the offset of the start of the line holding
// sentinel as its only content besides whitespace, or -1.
func indexOfSentinelLine(source []byte, sentinel string) int {
	return indexOfSentinelLineFrom(source, sentinel, 0)
}

func indexOfSentinelLineFrom(source []byte, sentinel string, from int) int {
	for offset := from; offset < len(source); {
		lineEnd := offset + bytes.IndexByte(source[offset:], '\n')
		if lineEnd < offset {
			lineEnd = len(source)
		}
		if strings.TrimSpace(string(source[offset:lineEnd])) == sentinel {
			return offset
		}
		offset = lineEnd + 1
	}
	return -1
}

// Splice puts region into source, replacing an existing region or appending
// one at end of file. gap is the number of blank lines the region owns before
// itself, which is blank_lines.top_level except at the start of a file. It
// returns the new contents and the region's span within them.
func Splice(source []byte, region string, gap int) ([]byte, Span, error) {
	span, found, err := FindRegion(source)
	if err != nil {
		return nil, Span{}, err
	}
	var prefix, suffix []byte
	if found {
		prefix, suffix = source[:span.Start], source[span.End:]
	} else {
		prefix, suffix = source, nil
	}
	leading := strings.Repeat("\n", gap)
	if len(prefix) == 0 {
		// At the start of a file there is nothing to separate the region from.
		leading = ""
	} else if !bytes.HasSuffix(prefix, []byte("\n")) {
		// A file whose last line has no newline still needs one before the
		// sentinel, on top of the gap.
		leading = "\n" + leading
	}
	var out bytes.Buffer
	out.Grow(len(prefix) + len(leading) + len(region) + len(suffix))
	out.Write(prefix)
	out.WriteString(leading)
	start := out.Len()
	out.WriteString(region)
	end := out.Len()
	out.Write(suffix)
	return out.Bytes(), Span{Start: start - len(leading), End: end}, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -race ./generate -run "TestRegion|TestSplice"`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add generate/region.go generate/region_test.go
git commit -m "Define the generated region's byte extent"
```

---

### Task 4: Field selection

**Goal:** A class's selectable fields are computed from its AST, in declaration order, with the per-field opt-out honoured.

**Files:**
- Create: `generate/fields.go`, `generate/fields_test.go`, `generate/testing_test.go`

**Acceptance Criteria:**
- [ ] Every member `var` is selected, in declaration order
- [ ] `const`, `static var`, and `@onready var` are never selected
- [ ] A field with `# gdkit:generate:ignore-field` trailing or on the line above is excluded
- [ ] A field declared inside the generated region is not selected
- [ ] A property with accessors is selected by name

**Verify:** `go test -race ./generate -run TestSelectFields` → ok

**Steps:**

- [ ] **Step 1: Write the shared test helper**

`generate/testing_test.go`:

```go
package generate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdkit/format"
	"github.com/cafecito-games/gdkit/project"
)

// loadProject writes files into a temp project and loads it the way generate
// does: the universe unfiltered, the selection carrying config's filters.
func loadProject(t *testing.T, config Config, files map[string]string) *project.Snapshot {
	t.Helper()
	root := t.TempDir()
	for name, contents := range files {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := project.Load(project.Config{
		Root: root,
		Selection: &project.Selection{
			SourceRoots:     config.SourceRoots,
			Exclude:         config.Exclude,
			HonorIgnoreFile: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// checkProject writes files into a temp project and runs a full check.
func checkProject(t *testing.T, config Config, files map[string]string) (Report, *project.Snapshot) {
	t.Helper()
	snapshot := loadProject(t, config, files)
	generator, err := New(config, format.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return generator.Check(snapshot).Report(), snapshot
}

// generated returns the region gen would write into the single file a.gd,
// failing the test if the class was refused.
func generated(t *testing.T, config Config, files map[string]string) string {
	t.Helper()
	snapshot := loadProject(t, config, files)
	generator, err := New(config, format.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	plan := generator.Check(snapshot)
	if report := plan.Report(); report.HasDiagnostics() {
		t.Fatalf("class was refused: %s", report.Diagnostics[0])
	}
	for _, candidate := range plan.Candidates {
		if candidate.Path == "a.gd" {
			return string(candidate.Contents[candidate.Region.Start:candidate.Region.End])
		}
	}
	t.Fatal("no candidate for a.gd")
	return ""
}

// assertDiagnostic fails unless report holds exactly one diagnostic with rule.
func assertDiagnostic(t *testing.T, report Report, rule string) Diagnostic {
	t.Helper()
	if len(report.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want exactly one %s", report.Diagnostics, rule)
	}
	if report.Diagnostics[0].Rule != rule {
		t.Fatalf("rule = %q, want %q (%s)", report.Diagnostics[0].Rule, rule, report.Diagnostics[0])
	}
	return report.Diagnostics[0]
}
```

- [ ] **Step 2: Write the failing test**

```go
func TestSelectFieldsTakesInstanceVarsInOrder(t *testing.T) {
	source := "class_name Hex\nextends RefCounted\n\n" +
		"var q: int\n" +
		"@export var name: String\n" +
		"var _cache: Dictionary  # gdkit:generate:ignore-field\n" +
		"# gdkit:generate:ignore-field\n" +
		"var skipped: int\n" +
		"@onready var label: Label\n" +
		"const ORIGIN := Vector2i()\n" +
		"static var count: int\n" +
		"var computed: int:\n\tget:\n\t\treturn 1\n"
	fields := selectFieldsOf(t, source)
	want := []string{"q", "name", "computed"}
	var got []string
	for _, field := range fields {
		got = append(got, field.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %v, want %v", got, want)
	}
}

func TestSelectFieldsRecordsTheDeclaredType(t *testing.T) {
	fields := selectFieldsOf(t, "var q: int\nvar r := 0\nvar s\n")
	if fields[0].Type != "int" || fields[0].Inferred {
		t.Errorf("q = %+v, want type int", fields[0])
	}
	// ":=" is static typing as far as Godot is concerned, but the inferred
	// type is not in the tree, so only the flag is recorded. deep_equals
	// relies on this distinction; v1 records it so that generator needs no
	// change here.
	if fields[1].Type != "" || !fields[1].Inferred {
		t.Errorf("r = %+v, want inferred with no type", fields[1])
	}
	if fields[2].Type != "" || fields[2].Inferred {
		t.Errorf("s = %+v, want untyped", fields[2])
	}
}

func TestSelectFieldsSkipsVarsInsideTheRegion(t *testing.T) {
	source := "var q: int\n\n# gdkit:generated:begin\nvar leaked: int\n# gdkit:generated:end\n"
	fields := selectFieldsOf(t, source)
	if len(fields) != 1 || fields[0].Name != "q" {
		t.Errorf("fields = %+v, want only q", fields)
	}
}

func selectFieldsOf(t *testing.T, source string) []Field {
	t.Helper()
	file, err := gdparser.Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	script := &project.Script{Path: "a.gd", Source: []byte(source), File: file}
	span, _, err := FindRegion([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	return SelectFields(file.Statements, script, span)
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test -race ./generate -run TestSelectFields`
Expected: FAIL — `undefined: SelectFields`

- [ ] **Step 4: Write `generate/fields.go`**

```go
package generate

import (
	"strings"

	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser/ast"
)

// Field is one selectable member of a class.
type Field struct {
	// Name is the identifier the generated code reads, always as self.Name.
	Name string
	// Type is the declared type, empty when there is none.
	Type string
	// Inferred reports a ":=" declaration. Godot types it statically, but the
	// inferred type is not in the tree, so the name of the type is unknown.
	// Only deep_equals needs the distinction; v1 records it anyway so that
	// generator needs no change to this file.
	Inferred bool
	// Line is 1-based.
	Line int
}

// SelectFields returns the selectable fields among statements, in declaration
// order.
//
// Never selected: a const, which GDScript types from its value and which is
// not instance state; a static var, which is not instance state either; an
// @onready var, which is node wiring rather than state and which is null
// before _ready; and anything inside the generated region, which is ours.
//
// A property with accessors is selected by name. The generated code reads
// self.q, which runs the getter, exactly as hand-written code would.
func SelectFields(statements []ast.Statement, script *project.Script, region Span) []Field {
	fields := []Field{}
	for _, statement := range statements {
		declaration, ok := statement.(*ast.VariableDeclaration)
		if !ok {
			continue
		}
		if declaration.Constant || declaration.Static {
			continue
		}
		if hasAnnotation(declaration.Annotations, "onready") {
			continue
		}
		offset := declaration.Span().Start.Offset
		if region.Start <= offset && offset < region.End {
			continue
		}
		line, _, _ := script.Position(offset)
		if fieldOptedOut(script, line) {
			continue
		}
		fields = append(fields, Field{
			Name:     declaration.Name,
			Type:     declaration.Type,
			Inferred: declaration.Inferred,
			Line:     line,
		})
	}
	return fields
}

// fieldOptedOut reports the field-level opt-out, which may trail the
// declaration or stand alone on the line above it.
func fieldOptedOut(script *project.Script, line int) bool {
	if MatchIgnoreField(script.Line(line)) {
		return true
	}
	if line > 1 {
		above := script.Line(line - 1)
		if standsAlone(above) && MatchIgnoreField(above) {
			return true
		}
	}
	return false
}

func hasAnnotation(annotations []*ast.Annotation, name string) bool {
	for _, annotation := range annotations {
		if strings.EqualFold(annotation.Name, name) {
			return true
		}
	}
	return false
}
```

If `project.Script` has no `Line(n int) string` accessor, add one beside `LineCount`:

```go
// Line returns the 1-based line n without its terminator, or "" when n is out
// of range.
func (s *Script) Line(n int) string {
	if n < 1 || n > len(s.Lines) {
		return ""
	}
	start := s.Lines[n-1]
	end := len(s.Source)
	if n < len(s.Lines) {
		end = s.Lines[n]
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(s.Source[start:end]), "\n"), "\r")
}
```

Check first whether `Script` already exposes the position helper used as `script.Position(offset)`; `project.go` has a `position` path around line 85 that returns `(line, column, message)`. Use whatever is exported; if nothing is, add `func (s *Script) LineAt(offset int) int` by binary search over `s.Lines` and use that instead.

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test -race ./generate -run TestSelectFields`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add generate/fields.go generate/fields_test.go generate/testing_test.go project/project.go
git commit -m "Select the member vars a generated method reads"
```

---

### Task 5: The index

**Goal:** One pass over the whole universe records every fact the capability rules need.

**Files:**
- Create: `generate/index.go`, `generate/index_test.go`

**Acceptance Criteria:**
- [ ] `class_name` maps to its script; a duplicate is recorded
- [ ] **Inner classes are indexed**, with a composite identity, although they can never be generation targets
- [ ] The `extends` edge is resolved for **every** project form: a `class_name`, a `res://` path, and a `uid://` identifier
- [ ] Declared methods are recorded with name, staticness, arity, and whether they sit inside the region
- [ ] Selectable fields are recorded **for every class in the universe**, not only requested ones
- [ ] Inheritance SCCs are detected, and `ReachesCycle` is true for a class whose ancestry enters one
- [ ] Ancestry traversal terminates on a cycle

**Verify:** `go test -race ./generate -run TestIndex` → ok

**Steps:**

- [ ] **Step 1: Write the failing test**

```go
func TestIndexRecordsMethodsFieldsAndInheritance(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n\nfunc equals(p_other: Variant) -> bool:\n\treturn true\n",
		"sub.gd":  "class_name Sub\nextends Base\n\nvar r: int\n",
	})
	base := index.ByClassName["Base"]
	sub := index.ByClassName["Sub"]
	if base == nil || sub == nil {
		t.Fatal("class_name index is incomplete")
	}
	if sub.ParentID != "base.gd" {
		t.Errorf("Sub.ParentID = %q, want base.gd", sub.ParentID)
	}
	if len(sub.Fields) != 1 || sub.Fields[0].Name != "r" {
		t.Errorf("Sub.Fields = %+v", sub.Fields)
	}
	method, ok := base.Methods["equals"]
	if !ok || method.Signature.Arity != 1 || method.Signature.Static || method.InRegion {
		t.Errorf("Base.equals = %+v, %v", method, ok)
	}
}

// Field metadata is needed for classes that never opt in, because both
// inheritance rules ask whether some other class declares a selectable field.
func TestIndexRecordsFieldsForUnrequestedClasses(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n\nvar r: int\n",
	})
	if fields := index.ByClassName["Sub"].Fields; len(fields) != 1 {
		t.Errorf("an unrequested class has no field metadata: %+v", fields)
	}
}

func TestIndexDetectsInheritanceCyclesAndTerminates(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name A\nextends B\n",
		"b.gd": "class_name B\nextends A\n",
		"c.gd": "class_name C\nextends A\n\nvar q: int\n",
	})
	if !index.InCycle["a.gd"] || !index.InCycle["b.gd"] {
		t.Error("the A/B cycle was not detected")
	}
	if index.InCycle["c.gd"] {
		t.Error("C is not itself in the cycle")
	}
	// C's ancestry reaches the cycle, which is just as unresolvable.
	if !index.ReachesCycle("c.gd") {
		t.Error("ReachesCycle(C) = false, want true")
	}
	// Must terminate rather than recurse forever.
	if got := index.Ancestry("c.gd"); len(got) == 0 {
		t.Error("Ancestry returned nothing")
	}
}

// An unrequested inner class can extend a generated base and add fields,
// which is exactly what the descendant rule exists to catch. Leaving inner
// classes out of the graph would defeat it silently.
func TestIndexRecordsInnerClasses(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"holder.gd": "class_name Holder\nextends RefCounted\n\n" +
			"class Nested extends Base:\n\tvar r: int\n",
	})
	nested := index.Classes["holder.gd#Nested"]
	if nested == nil {
		t.Fatal("the inner class was not indexed")
	}
	if !nested.Inner {
		t.Error("the inner class is not marked Inner")
	}
	if nested.ParentID != "base.gd" {
		t.Errorf("Nested.ParentID = %q, want base.gd", nested.ParentID)
	}
	if len(nested.Fields) != 1 || nested.Fields[0].Name != "r" {
		t.Errorf("Nested.Fields = %+v, want [r]", nested.Fields)
	}
	if index.TopLevel["holder.gd"].Inner {
		t.Error("the file's top-level class was marked Inner")
	}
	// The inner class is a descendant of Base for the refusal rule.
	if got := index.Descendants("base.gd"); !slices.Contains(got, "holder.gd#Nested") {
		t.Errorf("Descendants(base) = %v, want the inner class", got)
	}
}

// A subclass written with a path or uid form would otherwise look parentless,
// which skips the super composition and both refusal rules without saying so.
func TestIndexResolvesEveryInheritanceForm(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd":     "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"base.gd.uid": "uid://ckb1n0mqp2v7x",
		"by_name.gd":  "class_name ByName\nextends Base\n",
		"by_path.gd":  "class_name ByPath\nextends \"res://base.gd\"\n",
		"by_uid.gd":   "class_name ByUID\nextends \"uid://ckb1n0mqp2v7x\"\n",
	})
	for _, path := range []string{"by_name.gd", "by_path.gd", "by_uid.gd"} {
		if got := index.TopLevel[path].ParentID; got != "base.gd" {
			t.Errorf("%s ParentID = %q, want base.gd", path, got)
		}
	}
}

func TestIndexBuildsNestedIdentitiesWithoutLosingSegments(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name Holder\nextends RefCounted\n\n" +
			"class A:\n\tclass B:\n\t\tclass C:\n\t\t\tvar q: int\n" +
			"\nclass D:\n\tclass C:\n\t\tvar r: int\n",
	})
	deep := index.Classes["a.gd#A#B#C"]
	if deep == nil {
		t.Fatalf("a.gd#A#B#C missing; have %v", sortedKeys(index.Classes))
	}
	if len(deep.Fields) != 1 || deep.Fields[0].Name != "q" {
		t.Errorf("A.B.C fields = %+v, want [q]", deep.Fields)
	}
	// A sibling subtree's C must not collide with A.B.C.
	sibling := index.Classes["a.gd#D#C"]
	if sibling == nil || len(sibling.Fields) != 1 || sibling.Fields[0].Name != "r" {
		t.Errorf("D.C = %+v, want its own identity holding [r]", sibling)
	}
}

// Calling FindRegion per class gave every inner class in a generated file the
// file's region, so the orphan scan reported one region once per inner class.
func TestAnInnerClassDoesNotInheritTheFilesRegion(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name Holder\nextends RefCounted\n\nclass Nested:\n\tvar q: int\n\n" +
			"# gdkit:generated:begin\nfunc _to_string() -> String:\n\treturn \"Holder()\"\n# gdkit:generated:end\n",
	})
	if !index.TopLevel["a.gd"].HasRegion {
		t.Error("the top-level class does not own the region")
	}
	if index.Classes["a.gd#Nested"].HasRegion {
		t.Error("an inner class claimed the file's region")
	}
}

// A sentinel inside an inner class is not the top-level region; splicing it
// would rewrite at the wrong indentation.
func TestARegionInsideAnInnerClassIsRefused(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name Holder\nextends RefCounted\n\nclass Nested:\n" +
			"\t# gdkit:generated:begin\n\tfunc f():\n\t\tpass\n\t# gdkit:generated:end\n",
	})
	top := index.TopLevel["a.gd"]
	if top.HasRegion || top.RegionError == nil {
		t.Errorf("an inner-owned region was taken as the top-level one: %+v", top)
	}
}

func TestIndexResolvesTheRemainingInheritanceForms(t *testing.T) {
	index := indexOf(t, map[string]string{
		"domain/base.gd":   "class_name Base\nextends RefCounted\n\nvar q: int\n\nclass Inner:\n\tvar r: int\n",
		"domain/rel.gd":    "class_name Rel\nextends \"base.gd\"\n",
		"domain/chain.gd":  "class_name Chain\nextends \"res://domain/base.gd\".Inner\n",
		"domain/dotted.gd": "class_name Dotted\nextends Base.Inner\n",
	})
	if got := index.TopLevel["domain/rel.gd"].ParentID; got != "domain/base.gd" {
		t.Errorf("relative path ParentID = %q, want domain/base.gd", got)
	}
	for _, path := range []string{"domain/chain.gd", "domain/dotted.gd"} {
		if got := index.TopLevel[path].ParentID; got != "domain/base.gd#Inner" {
			t.Errorf("%s ParentID = %q, want domain/base.gd#Inner", path, got)
		}
	}
}

// An inner class shadows a global of the same name, as Godot's own scope
// lookup does.
func TestALexicalInnerClassShadowsAGlobal(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n",
		"a.gd": "class_name Holder\nextends RefCounted\n\n" +
			"class Base:\n\tvar q: int\n\nclass Child extends Base:\n\tvar r: int\n",
	})
	if got := index.Classes["a.gd#Child"].ParentID; got != "a.gd#Base" {
		t.Errorf("Child.ParentID = %q, want the lexical a.gd#Base", got)
	}
}

// A path names a file, not an engine type, so failing to resolve one leaves
// the graph incomplete and must refuse rather than shrug.
func TestAnUnresolvableBaseDemotesEquals(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd":     "class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
		"stray.gd": "class_name Stray\nextends \"res://gone.gd\"\n",
	})
	if !index.TopLevel["stray.gd"].UnresolvedBase {
		t.Fatal("an unresolvable res:// base was treated as an engine type")
	}
	capabilities := Resolve(index, map[string][]string{"a.gd": {"equals"}}, nil)
	if capabilities.Realizable("a.gd", equalsSignature) {
		t.Error("equals generated with an incomplete inheritance graph")
	}
}

// "extends Control" must stay an engine type, or no real project can generate.
func TestABareEngineBaseIsNotUnresolved(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name Panel\nextends Control\n\n# gdkit:generate = equals\nvar q: int\n",
	})
	if index.TopLevel["a.gd"].UnresolvedBase {
		t.Error("an engine base was reported as unresolved")
	}
	capabilities := Resolve(index, map[string][]string{"a.gd": {"equals"}}, nil)
	if !capabilities.Realizable("a.gd", equalsSignature) {
		t.Error("an engine base blocked generation")
	}
}

// A marker inside a function body is not a class-level marker, and must not
// opt the enclosing class in.
func TestAMarkerInsideAFunctionDoesNotOptIn(t *testing.T) {
	report, _ := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\nvar q: int\n\n" +
			"func f() -> void:\n\t# gdkit:generate = to_string\n\tpass\n",
	})
	if report.HasChanges() {
		t.Error("a marker inside a function opted the class in")
	}
}

// An inner class cannot be a generation target, and a marker on one must be
// reported rather than quietly doing nothing.
func TestAMarkerOnAnInnerClassIsRefused(t *testing.T) {
	report, _ := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Holder\nextends RefCounted\n\n" +
			"class Nested extends RefCounted:\n\t# gdkit:generate = to_string\n\tvar q: int\n",
	})
	assertDiagnostic(t, report, ruleUnsupported)
}

func TestIndexRecordsDuplicateClassNames(t *testing.T) {
	index := indexOf(t, map[string]string{
		"one.gd": "class_name Dup\n",
		"two.gd": "class_name Dup\n",
	})
	if len(index.DuplicateClassNames["Dup"]) != 2 {
		t.Errorf("duplicates = %+v", index.DuplicateClassNames)
	}
}

func indexOf(t *testing.T, files map[string]string) *Index {
	t.Helper()
	snapshot := loadProject(t, DefaultConfig(), files)
	return BuildIndex(snapshot)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -race ./generate -run TestIndex`
Expected: FAIL — `undefined: BuildIndex`

- [ ] **Step 3: Write `generate/index.go`**

```go
package generate

import (
	"sort"

	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser/ast"
)

// Signature identifies a method shape. GDScript has no overloading, so a name
// identifies at most one method in a class; the rest of the shape decides
// whether an existing method can stand in for a generated one.
type Signature struct {
	Name   string
	Static bool
	Arity  int
}

// Method is one declared method of a class.
type Method struct {
	Signature Signature
	// InRegion reports that the declaration sits inside the generated region.
	// Such a method is never an immutable provider: its existence is
	// contingent on this run, and --prune may remove it.
	InRegion bool
	Line     int
}

// Class is one class in the universe, top-level or inner.
//
// Inner classes are indexed even though they can never be generation targets.
// An unrequested inner class can extend a generated base and add fields, which
// is exactly what the descendant refusal rule exists to catch, so leaving them
// out of the graph would defeat it. Indexing them is also what lets an inner
// class carrying a marker be reported as generate.unsupported rather than
// silently ignored.
type Class struct {
	// ID identifies the class across the universe: the path for a top-level
	// class, and path + "#" + name for an inner one. Every map and every
	// traversal in Index is keyed on this, not on Path.
	ID string
	// Path is project-relative and slash-separated. An inner class shares its
	// file's path with the class that encloses it.
	Path string
	// Inner reports a class declared with "class" inside another. It can carry
	// a marker, which is refused, and it can be a descendant, which matters.
	Inner bool
	// Name is the class_name, or the file's base name without .gd. It is what
	// _to_string prints.
	Name string
	// HasClassName reports an explicit class_name declaration.
	HasClassName bool
	// Extends is the base as written, which may be a class name, a res:// or
	// uid:// path, or an engine type.
	Extends string
	// ParentID is Extends resolved to another indexed class's ID, empty when
	// it names an engine type or could not be resolved.
	ParentID string
	Fields     []Field
	Methods    map[string]Method
	// Body is the span of the class's body in the file. It is what attributes
	// a directive comment and a region sentinel to the class that owns it: a
	// marker inside an inner class, or inside a function, must not opt in the
	// file's top-level class.
	Body Span
	// Region is the generated region this class owns, zero when it owns none.
	// Only a top-level class can own one in v1. An earlier draft called
	// FindRegion for every class, which gave every inner class in a generated
	// file HasRegion=true and made the orphan scan report the file's single
	// region once per inner class.
	Region Span
	// HasRegion reports a generated region owned by this class.
	HasRegion bool
	// RegionError is the error FindRegion returned, if any.
	RegionError error
	// UnresolvedBase reports an extends target that should have named a
	// project file but did not. It makes the inheritance graph incomplete, so
	// it demotes every inheritance-sensitive pair in the universe — this class
	// could be an unseen field-adding descendant of any requested one.
	UnresolvedBase bool
	Line        int
	Column      int
}

// Index is every fact the capability rules need, over the whole universe.
type Index struct {
	// Classes is keyed by Class.ID, so it holds inner classes too.
	Classes map[string]*Class
	// TopLevel is keyed by path and holds only the file's outermost class,
	// which is the only kind that can be a generation target.
	TopLevel            map[string]*Class
	ByClassName         map[string]*Class
	DuplicateClassNames map[string][]string
	// Children maps a class ID to the IDs that extend it directly.
	Children map[string][]string
	// InCycle marks a class that sits in an inheritance cycle. project.Load
	// parses syntactically and does not check that extends edges are acyclic,
	// and cycle detection lives in architecture, so generate must find its
	// own.
	InCycle map[string]bool
	// ParseFailures are the paths that did not parse, sorted.
	ParseFailures []string
}

// BuildIndex walks the whole snapshot, not only its selection: equals composes
// with an ancestor and is refused when a descendant would inherit an unsound
// implementation, and a hidden file would take its class_name and its
// extends edge out of the graph that decides both.
func BuildIndex(snapshot *project.Snapshot) *Index {
	index := &Index{
		Classes:             map[string]*Class{},
		TopLevel:            map[string]*Class{},
		ByClassName:         map[string]*Class{},
		DuplicateClassNames: map[string][]string{},
		Children:            map[string][]string{},
		InCycle:             map[string]bool{},
		ParseFailures:       []string{},
	}
	for _, path := range snapshot.Paths {
		script := snapshot.Scripts[path]
		if script.ParseError != nil {
			index.ParseFailures = append(index.ParseFailures, path)
			continue
		}
		region, found, regionErr := FindRegion(script.Source)
		whole := Span{Start: 0, End: len(script.Source)}
		top := buildClass(path, path, false, script, script.File.Statements, whole, region, found, regionErr)
		index.Classes[top.ID] = top
		index.TopLevel[path] = top
		for _, inner := range buildInnerClasses(path, path, script, script.File.Statements, region, found, regionErr) {
			index.Classes[inner.ID] = inner
			// A sentinel inside an inner class body is not the top-level
			// region. Rewriting it would splice at the wrong indentation, so
			// the top-level class disowns the region and the file is refused.
			if found && inner.Body.Start <= region.Start && region.End <= inner.Body.End {
				top.Region, top.HasRegion = Span{}, false
				top.RegionError = fmt.Errorf("a generated region sits inside the inner class %q", inner.Name)
			}
		}
	}
	for _, path := range sortedKeys(index.Classes) {
		class := index.Classes[path]
		if !class.HasClassName {
			continue
		}
		if existing, ok := index.ByClassName[class.Name]; ok {
			duplicates := index.DuplicateClassNames[class.Name]
			if len(duplicates) == 0 {
				duplicates = append(duplicates, existing.Path)
			}
			index.DuplicateClassNames[class.Name] = append(duplicates, path)
			continue
		}
		index.ByClassName[class.Name] = class
	}
	for _, id := range sortedKeys(index.Classes) {
		class := index.Classes[id]
		if parent, ok := index.resolveExtends(snapshot, class); ok {
			class.ParentID = parent.ID
			index.Children[parent.ID] = append(index.Children[parent.ID], id)
		}
	}
	index.findCycles()
	sort.Strings(index.ParseFailures)
	return index
}

// buildClass records one class from the statements of its body.
//
// region is the file's region span and is passed in rather than recomputed,
// and it is attributed to this class only when the class owns it — which in v1
// means only a top-level class. Calling FindRegion per class would hand every
// inner class in a generated file the same region.
func buildClass(id, path string, inner bool, script *project.Script, statements []ast.Statement, body Span, region Span, found bool, regionErr error) *Class {
	class := &Class{
		ID:      id,
		Path:    path,
		Inner:   inner,
		Name:    baseName(path),
		Methods: map[string]Method{},
		Body:    body,
		Line:    1,
		Column:  1,
	}
	if !inner {
		class.Region, class.HasRegion, class.RegionError = region, found, regionErr
	}
	for _, statement := range statements {
		switch node := statement.(type) {
		case *ast.Directive:
			// gdparser models class_name and extends as one Directive node
			// keyed by the keyword, and a single line may carry both, as in
			// "class_name Hex extends RefCounted".
			switch node.Name {
			case "class_name":
				if identifier, ok := node.Value.(*ast.Identifier); ok {
					class.Name, class.HasClassName = identifier.Name, true
				}
				if node.Extends != nil {
					class.ExtendsExpr = node.Extends
				}
			case "extends":
				class.ExtendsExpr = node.Value
			}
		case *ast.FunctionDeclaration:
			offset := node.Span().Start.Offset
			line := lineAt(script, offset)
			class.Methods[node.Name] = Method{
				Signature: Signature{Name: node.Name, Static: node.Static, Arity: len(node.Parameters)},
				InRegion:  found && region.Start <= offset && offset < region.End,
				Line:      line,
			}
		}
	}
	class.Fields = SelectFields(statements, script, region)
	return class
}

// buildInnerClasses records every class declared inside statements,
// recursively. An identity is its enclosing class's ID plus "#" plus its name,
// so Outer.Inner.Deep is "path#Outer#Inner#Deep".
//
// The enclosing ID is passed down rather than patched onto the returned
// classes afterwards. An earlier draft rewrote each returned ID as
// parentID + "#" + nested.Name, which dropped every middle segment: A.B.C
// became path#A#C, and two sibling subtrees holding a C would then collide on
// one ID.
func buildInnerClasses(enclosingID, path string, script *project.Script, statements []ast.Statement, region Span, found bool, regionErr error) []*Class {
	classes := []*Class{}
	for _, statement := range statements {
		declaration, ok := statement.(*ast.ClassDeclaration)
		if !ok {
			continue
		}
		id := enclosingID + "#" + declaration.Name
		span := declaration.Span()
		body := Span{Start: span.Start.Offset, End: span.End.Offset}
		class := buildClass(id, path, true, script, declaration.Body, body, region, found, regionErr)
		class.Name = declaration.Name
		class.Extends = declaration.Extends
		class.Line = lineAt(script, span.Start.Offset)
		classes = append(classes, class)
		classes = append(classes, buildInnerClasses(id, path, script, declaration.Body, region, found, regionErr)...)
	}
	return classes
}

// resolveExtends resolves a class's extends target to another indexed class.
//
// Every form a project script can use has to resolve, not just a class_name. A
// subclass written "extends \"res://base.gd\"" would otherwise look
// parentless, which silently skips the super composition and both refusal
// rules — it would compare only its own fields and nothing would say so.
//
// The target is a base followed by an arbitrary member chain, so it is parsed
// structurally rather than with a single Cut:
//
//	extends Base                       a global class_name
//	extends Outer.Inner.Deep           a chain of inner classes
//	extends "res://domain/base.gd"     an absolute project path
//	extends "base.gd"                  a path relative to this script
//	extends "uid://b2u1q..."           an identifier, via Snapshot.UIDs
//	extends "res://base.gd".Inner      a path plus a chain
//	extends Sibling                    an inner class in lexical scope
//
// The second return value is "resolved". A target that *should* name a project
// file but does not — a path, an identifier, or a chain whose head resolved —
// sets class.UnresolvedBase, which is a hard condition rather than a shrug: a
// path names a file, not an engine type, so failing to resolve one means the
// graph is incomplete. A bare identifier that is not a project class is taken
// to be an engine type, which is the only reading that lets "extends Node"
// work; a typo there is a project Godot itself will not load.
func (i *Index) resolveExtends(snapshot *project.Snapshot, class *Class) (*Class, bool) {
	target, ok := parseExtends(class.ExtendsExpr)
	if !ok {
		return nil, false
	}
	base, chain := target.Name, target.Chain
	if target.Path != "" {
		base = target.Path
	}
	if base == "" {
		return nil, false
	}
	var head *Class
	switch {
	case target.Path == "":
		// A bare identifier is looked up in lexical scope first — an inner
		// class of an enclosing class shadows a global of the same name — and
		// then globally.
		if lexical, found := i.lexicalLookup(class, base); found {
			head = lexical
		} else if global, found := i.ByClassName[base]; found {
			head = global
		} else {
			// Not a project class: an engine type, which is not a parent for
			// our purposes.
			return nil, false
		}
	case strings.HasPrefix(base, "uid://"):
		path, ok := snapshot.UIDs[base]
		if !ok {
			class.UnresolvedBase = true
			return nil, false
		}
		head = i.TopLevel[path]
	case strings.HasPrefix(base, "res://"):
		head = i.TopLevel[strings.TrimPrefix(base, "res://")]
	default:
		// Any other quoted path is relative to the declaring script's
		// directory, as Godot's analyzer resolves it.
		head = i.TopLevel[joinRelative(class.Path, base)]
	}
	if head == nil {
		class.UnresolvedBase = true
		return nil, false
	}
	for _, segment := range chain {
		next, ok := i.Classes[head.ID+"#"+segment]
		if !ok {
			class.UnresolvedBase = true
			return nil, false
		}
		head = next
	}
	return head, true
}

// extendsTarget is the parsed form of a base class: a base plus the member
// chain reaching an inner class.
type extendsTarget struct {
	// Path is the quoted script path, when the base was a string literal.
	Path string
	// Name is the identifier, when the base was not a string literal.
	Name string
	// Chain is the dotted inner-class names after the base.
	Chain []string
}

// parseExtends reads the base-class form out of the AST rather than out of the
// source text.
//
// gdparser's parseBaseClassExpression has already done this work: a base class
// is a StringLiteral or an Identifier, optionally wrapped in a MemberExpression
// chain, which is exactly the structure these rules need. An earlier draft of
// this plan re-derived it by splitting the rendered string on quotes and dots,
// which is both redundant and the kind of text handling the repository's "no
// regex over source text" invariant exists to keep out of gdkit.
func parseExtends(expression ast.Expression) (extendsTarget, bool) {
	target := extendsTarget{}
	for {
		switch node := expression.(type) {
		case *ast.MemberExpression:
			target.Chain = append([]string{node.Property}, target.Chain...)
			expression = node.Object
		case *ast.Identifier:
			target.Name = node.Name
			return target, true
		case *ast.StringLiteral:
			target.Path = node.Value
			return target, true
		default:
			return extendsTarget{}, false
		}
	}
}

// joinRelative resolves a path relative to the directory of from.
func joinRelative(from, relative string) string {
	directory := path.Dir(from)
	if directory == "." {
		return path.Clean(relative)
	}
	return path.Clean(directory + "/" + relative)
}

// lexicalLookup finds name as an inner class of class or of any class
// enclosing it, innermost first, which is the scope Godot searches before the
// global class list.
func (i *Index) lexicalLookup(class *Class, name string) (*Class, bool) {
	for scope := class.ID; scope != ""; {
		if found, ok := i.Classes[scope+"#"+name]; ok && found.ID != class.ID {
			return found, true
		}
		cut := strings.LastIndexByte(scope, '#')
		if cut < 0 {
			break
		}
		scope = scope[:cut]
	}
	return nil, false
}

// Ancestry returns id and every ancestor above it, nearest first. It carries
// a visited set, so a cycle truncates the walk rather than hanging it.
func (i *Index) Ancestry(path string) []string {
	ancestry := []string{}
	seen := map[string]bool{}
	for current := path; current != "" && !seen[current]; {
		seen[current] = true
		ancestry = append(ancestry, current)
		class := i.Classes[current]
		if class == nil {
			break
		}
		current = class.ParentID
	}
	return ancestry
}

// ReachesCycle reports whether path or any ancestor sits in an inheritance
// cycle. A class above a cycle is as unresolvable as one inside it, because
// provider() has no answer for its parent.
func (i *Index) ReachesCycle(path string) bool {
	for _, ancestor := range i.Ancestry(path) {
		if i.InCycle[ancestor] {
			return true
		}
	}
	return false
}

// Descendants returns every class below path, transitively, in sorted order.
func (i *Index) Descendants(path string) []string {
	found := []string{}
	seen := map[string]bool{path: true}
	queue := append([]string{}, i.Children[path]...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if seen[current] {
			continue
		}
		seen[current] = true
		found = append(found, current)
		queue = append(queue, i.Children[current]...)
	}
	sort.Strings(found)
	return found
}

// findCycles marks every class on an extends cycle. Each class has at most one
// parent, so a cycle is reached by walking up and finding a path already on
// the current walk — no general SCC algorithm is needed.
func (i *Index) findCycles() {
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	for _, path := range sortedKeys(i.Classes) {
		if state[path] != 0 {
			continue
		}
		walk := []string{}
		position := map[string]int{}
		for current := path; current != ""; {
			if state[current] == done {
				break
			}
			if at, ok := position[current]; ok {
				for _, member := range walk[at:] {
					i.InCycle[member] = true
				}
				break
			}
			position[current] = len(walk)
			walk = append(walk, current)
			state[current] = visiting
			class := i.Classes[current]
			if class == nil {
				break
			}
			current = class.ParentID
		}
		for _, member := range walk {
			state[member] = done
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
```

Add `baseName` and `lineAt` helpers:

```go
// baseName is the file's name without directories or the .gd suffix, which is
// what _to_string prints for a class that declares no class_name.
func baseName(path string) string {
	name := path
	if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
		name = name[slash+1:]
	}
	return strings.TrimSuffix(name, ".gd")
}

// lineAt is the 1-based line holding offset.
func lineAt(script *project.Script, offset int) int {
	return sort.SearchInts(script.Lines, offset+1)
}
```

Confirm the AST node names against gdparser v0.1.5 before writing: `ast.ClassNameDeclaration` and `ast.ExtendsDeclaration` are assumed here from `ClassDeclaration`'s shape. Run `grep -n "^type .*Declaration struct" $(go env GOMODCACHE)/github.com/cafecito-games/gdparser@v0.1.5/ast/statement.go` and use the real names; if `class_name` and `extends` are fields on `ast.File` rather than statements, read them from there instead and drop those two switch cases.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -race ./generate -run TestIndex`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add generate/index.go generate/index_test.go
git commit -m "Index the inheritance graph generate reasons over"
```

---

### Task 6: Capability resolution

**Goal:** `provider` follows the four-row truth table, and realizability is a monotone demotion to stability.

**Files:**
- Create: `generate/capability.go`, `generate/capability_test.go`

**Acceptance Criteria:**
- [ ] `provider` stops at the nearest declaration **by name**; an incompatible one is a barrier
- [ ] A method inside a generated region is a provider only when that pair is realizable, and never when the region is orphaned
- [ ] A `compatibleHandwritten` pair is never demoted
- [ ] The ancestry rule demotes a class whose strict ancestry has fields and whose parent has no provider
- [ ] The descendant rule demotes a class whose descendant would inherit an unsound method
- [ ] Demoting `B` cascades to demote `A` in the spec's A/B/C graph
- [ ] Resolution terminates

**Verify:** `go test -race ./generate -run TestCapability` → ok

**Steps:**

- [ ] **Step 1: Write the failing test**

```go
// GDScript's runtime lookup does not walk past an incompatible override, so
// neither may provider(). A has a good equals, B shadows it with a wrong one,
// so C has no provider rather than A.
func TestProviderStopsAtAnIncompatibleDeclaration(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name A\nextends RefCounted\n\nfunc equals(p_other: Variant) -> bool:\n\treturn true\n",
		"b.gd": "class_name B\nextends A\n\nfunc equals(x, y) -> bool:\n\treturn true\n",
		"c.gd": "class_name C\nextends B\n",
	})
	capabilities := Resolve(index, nil, nil)
	if _, ok := capabilities.Provider(index, "c.gd", equalsSignature); ok {
		t.Error("provider walked past an incompatible override")
	}
	if provider, ok := capabilities.Provider(index, "b.gd", equalsSignature); ok {
		t.Errorf("B's own incompatible equals was taken as a provider: %v", provider)
	}
}

// An orphan's method physically exists, so the walk must not fall through as
// though it were absent; and --prune may delete it, so nothing may compose
// with it. Barrier is the only answer safe both before and after pruning.
func TestProviderTreatsAnOrphanedRegionAsABarrier(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n\n" +
			"# gdkit:generated:begin\nfunc equals(p_other: Variant) -> bool:\n\treturn true\n# gdkit:generated:end\n",
		"sub.gd": "class_name Sub\nextends Base\n\n# gdkit:generate = equals\nvar r: int\n",
	})
	// Base does not opt in, so its region is orphaned.
	requested := map[string][]string{"sub.gd": {"equals"}}
	capabilities := Resolve(index, requested, nil)
	if _, ok := capabilities.Provider(index, "base.gd", equalsSignature); ok {
		t.Error("an orphaned region supplied a capability")
	}
	if capabilities.Realizable("sub.gd", equalsSignature) {
		t.Error("Sub composed with an orphaned parent")
	}
}

// The descendant rule demotes B, which moves provider(C) from B to A, which
// must then demote A. A single parents-first pass settles A too early.
func TestDemotionCascadesUpTheHierarchy(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name A\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
		"b.gd": "class_name B\nextends A\n\n# gdkit:generate = equals\nvar r: int\n",
		"c.gd": "class_name C\nextends B\n\nvar s: int\n",
	})
	requested := map[string][]string{"a.gd": {"equals"}, "b.gd": {"equals"}}
	capabilities := Resolve(index, requested, nil)
	if capabilities.Realizable("b.gd", equalsSignature) {
		t.Error("B was not demoted by its field-adding descendant C")
	}
	if capabilities.Realizable("a.gd", equalsSignature) {
		t.Error("A was not demoted after B's demotion moved provider(C) to A")
	}
}

// A conflicted parent must not look like a provider on the first pass, or its
// child composes with a method that is never emitted. This is why local
// blockers are computed before the first Resolve rather than discovered after.
func TestAConflictedParentDoesNotProvideForItsChild(t *testing.T) {
	report, _ := checkProject(t, DefaultConfig(), map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n\n" +
			"static func equals(a, b) -> bool:\n\treturn true\n",
		"sub.gd": "class_name Sub\nextends Base\n\n# gdkit:generate = equals\nvar r: int\n",
	})
	refused := map[string]string{}
	for _, diagnostic := range report.Diagnostics {
		refused[diagnostic.Path] = diagnostic.Rule
	}
	if refused["base.gd"] != ruleConflict {
		t.Errorf("base.gd = %q, want %s", refused["base.gd"], ruleConflict)
	}
	if refused["sub.gd"] != ruleUnsupported {
		t.Errorf("sub.gd = %q, want %s: it composed with a conflicted parent",
			refused["sub.gd"], ruleUnsupported)
	}
}

// A blocked generator must not erase a hand-written provider something else
// relies on.
func TestAHandwrittenProviderSurvivesABlockedGenerator(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n\nfunc equals(p_other: Variant) -> bool:\n\treturn true\n",
		"sub.gd":  "class_name Sub\nextends Base\n\n# gdkit:generate = equals\nvar r: int\n",
	})
	requested := map[string][]string{"base.gd": {"equals"}, "sub.gd": {"equals"}}
	blockers := map[string]bool{"base.gd": true}
	capabilities := Resolve(index, requested, blockers)
	if !capabilities.Realizable("base.gd", equalsSignature) {
		t.Error("a blocked generator erased the hand-written equals")
	}
	if !capabilities.Realizable("sub.gd", equalsSignature) {
		t.Error("Sub was demoted although its parent still provides equals")
	}
}

func TestResolveRefusesAClassWhoseAncestryHasFieldsButNoProvider(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n\n# gdkit:generate = equals\nvar r: int\n",
	})
	capabilities := Resolve(index, map[string][]string{"sub.gd": {"equals"}}, nil)
	if capabilities.Realizable("sub.gd", equalsSignature) {
		t.Error("Sub generated equals with no provider for Base's q")
	}
}

// _to_string is subject to local blockers alone, so none of the
// inheritance-sensitive conditions may refuse it.
func TestToStringIsNotRefusedByInheritanceConditions(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd":   "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"sub.gd":    "class_name Sub\nextends Base\n\n# gdkit:generate = to_string\nvar r: int\n",
		"cycle_a.gd": "class_name CycleA\nextends CycleB\n",
		"cycle_b.gd": "class_name CycleB\nextends CycleA\n",
		"broken.gd":  "func (((\n",
	})
	capabilities := Resolve(index, map[string][]string{"sub.gd": {"to_string"}}, nil)
	if !capabilities.Realizable("sub.gd", toStringSignature) {
		t.Error("to_string was refused by a condition that cannot affect it")
	}
}

// A newly requested parent with no region yet must still provide for its
// child, or a first adoption of equals across a hierarchy can never start.
func TestANewlyRequestedParentIsAVirtualProvider(t *testing.T) {
	index := indexOf(t, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n\n# gdkit:generate = equals\nvar r: int\n",
	})
	requested := map[string][]string{"base.gd": {"equals"}, "sub.gd": {"equals"}}
	capabilities := Resolve(index, requested, nil)
	if provider, ok := capabilities.Provider(index, "sub.gd", equalsSignature); !ok || provider != "sub.gd" {
		t.Errorf("Provider(sub) = %q, %v, want sub.gd", provider, ok)
	}
	if provider, ok := capabilities.Provider(index, "base.gd", equalsSignature); !ok || provider != "base.gd" {
		t.Errorf("Provider(base) = %q, %v, want base.gd as a virtual provider", provider, ok)
	}
	if !capabilities.Realizable("sub.gd", equalsSignature) {
		t.Error("Sub could not compose with a parent that is itself newly requested")
	}
}

// Reporting the parse failure is not enough: write applies candidates despite
// unrelated diagnostics, so the capability has to be withdrawn.
func TestAUniverseParseFailureDemotesEquals(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd":      "class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
		"broken.gd": "func (((\n",
	})
	capabilities := Resolve(index, map[string][]string{"a.gd": {"equals"}}, nil)
	if capabilities.Realizable("a.gd", equalsSignature) {
		t.Error("equals stayed realizable with an unreadable file in the universe")
	}
}

func TestResolveRefusesAClassWhoseAncestryReachesACycle(t *testing.T) {
	index := indexOf(t, map[string]string{
		"a.gd": "class_name A\nextends B\n",
		"b.gd": "class_name B\nextends A\n",
		"c.gd": "class_name C\nextends A\n\n# gdkit:generate = equals\nvar q: int\n",
	})
	capabilities := Resolve(index, map[string][]string{"c.gd": {"equals"}}, nil)
	if capabilities.Realizable("c.gd", equalsSignature) {
		t.Error("a class above an inheritance cycle generated equals")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -race ./generate -run TestCapability`
Expected: FAIL — `undefined: Resolve`

- [ ] **Step 3: Write `generate/capability.go`**

```go
package generate

// equalsSignature is the shape equals must have to be callable as
// origin.equals(other). It is a package variable so tests and the emitter
// agree on one definition.
var equalsSignature = Signature{Name: "equals", Static: false, Arity: 1}

// toStringSignature is Godot's virtual, which takes no arguments.
var toStringSignature = Signature{Name: "_to_string", Static: false, Arity: 0}

type pair struct {
	path      string
	signature Signature
}

// Capabilities records which (class, signature) pairs will exist after this
// run, and where a class's implementation comes from.
type Capabilities struct {
	realizable  map[pair]bool
	handwritten map[pair]bool
	orphaned    map[string]bool
	// inheritanceSensitive is the generator's NeedsInheritanceGraph, recorded
	// per generated pair at seed time.
	inheritanceSensitive map[pair]bool
}

// Realizable reports that the class will have a callable implementation of
// signature once this run completes.
func (c *Capabilities) Realizable(path string, signature Signature) bool {
	return c.realizable[pair{path, signature}]
}

// Provider walks path's ancestry and returns the class whose implementation of
// signature a call from path would reach.
//
// The walk stops at the nearest class declaring a method with signature's
// name — not the nearest compatible one — because GDScript's runtime lookup
// does not walk past an incompatible override either. What it finds decides
// the outcome:
//
//	compatible, hand-written outside any region   -> provider
//	incompatible, hand-written                    -> barrier, no provider
//	inside a region, that pair realizable         -> provider
//	inside a region, not realizable or orphaned   -> barrier, no provider
//
// The last case is why an orphaned region is a barrier rather than invisible:
// its method physically exists, so falling through to an ancestor would emit a
// super call that lands somewhere else, but --prune may delete it, so nothing
// may compose with it either.
func (c *Capabilities) Provider(index *Index, path string, signature Signature) (string, bool) {
	for _, ancestor := range index.Ancestry(path) {
		class := index.Classes[ancestor]
		if class == nil {
			return "", false
		}
		method, declared := class.Methods[signature.Name]
		if !declared {
			// Virtual provider: nothing is declared yet, but a realizable
			// requested pair promises the method will exist. This row is what
			// makes a first adoption work — without it a newly requested
			// parent could never provide for its child, which is the case the
			// optimistic seed exists to serve.
			if c.realizable[pair{ancestor, signature}] {
				return ancestor, true
			}
			continue
		}
		if method.InRegion {
			if c.orphaned[ancestor] || !c.realizable[pair{ancestor, signature}] {
				return "", false
			}
			return ancestor, true
		}
		if method.Signature != signature {
			return "", false
		}
		return ancestor, true
	}
	return "", false
}

// Resolve computes realizability by monotone demotion to stability.
//
// The obvious phrasing is circular: optimistic seeding makes a requested pair
// realizable, so provider(C, S) is C itself, and "demote when the provider is
// not realizable" can never detect a missing parent provider. So the three
// demotion conditions are stated literally, and provider is recomputed against
// the current set on every pass.
//
// requested maps a path to the generator names it asked for. blockers marks a
// path carrying a local blocking diagnostic, which includes a verification
// failure discovered after emission — Check re-enters Resolve with those.
func Resolve(index *Index, requested map[string][]string, blockers map[string]bool) *Capabilities {
	// Two conditions make the inheritance graph untrustworthy as a whole, and
	// both demote every inheritance-sensitive pair rather than one class's.
	// Inheritance is a reverse dependency: the class that would invalidate a
	// generated equals is a descendant, and nothing in the base names it, so
	// an incomplete graph cannot be narrowed to the pairs it affects.
	//
	// Reporting them as diagnostics is not enough, because gen write applies
	// candidates despite unrelated diagnostics and would write the unsound
	// method anyway.
	universeUnreadable := len(index.ParseFailures) > 0
	for _, class := range index.Classes {
		// An extends target that should have named a project file but did
		// not. A bare identifier that is not a project class is an engine
		// type and does not set this.
		if class.UnresolvedBase {
			universeUnreadable = true
			break
		}
	}
	capabilities := &Capabilities{
		realizable:           map[pair]bool{},
		handwritten:          map[pair]bool{},
		orphaned:             map[string]bool{},
		inheritanceSensitive: map[pair]bool{},
	}
	for path, class := range index.Classes {
		if class.HasRegion && len(requested[path]) == 0 {
			capabilities.orphaned[path] = true
		}
	}
	// Seed: realizable₀ = compatibleHandwritten ∨ requested.
	generated := map[pair]bool{}
	for path, class := range index.Classes {
		for _, method := range class.Methods {
			if method.InRegion {
				continue
			}
			key := pair{path, method.Signature}
			capabilities.realizable[key] = true
			capabilities.handwritten[key] = true
		}
	}
	for path, names := range requested {
		for _, name := range names {
			generator := generatorByName(name)
			for _, signature := range generator.Signatures() {
				key := pair{path, signature}
				capabilities.inheritanceSensitive[key] = generator.NeedsInheritanceGraph()
				if capabilities.handwritten[key] {
					continue
				}
				capabilities.realizable[key] = true
				generated[key] = true
			}
		}
	}
	// Iterate: demote a generated pair on a local blocker, a failed ancestry
	// rule, or a failed descendant rule. The set only shrinks, so this stops.
	for {
		demoted := false
		for key := range generated {
			if !capabilities.realizable[key] {
				continue
			}
			if capabilities.demote(index, key, blockers, universeUnreadable) {
				capabilities.realizable[key] = false
				demoted = true
			}
		}
		if !demoted {
			return capabilities
		}
	}
}

// demote reports whether the generated pair fails any demotion condition.
//
// The conditions split on NeedsInheritanceGraph. Only local blockers apply to
// every signature; the cycle, universe-parse, duplicate-class_name, ancestry,
// and descendant conditions apply solely to a signature whose soundness
// depends on the inheritance graph. _to_string neither composes with an
// ancestor nor walks one, so a cycle elsewhere, an unparseable unrelated file,
// and a fieldful ancestor without a _to_string cannot affect it.
func (c *Capabilities) demote(index *Index, key pair, blockers map[string]bool, universeUnreadable bool) bool {
	class := index.Classes[key.path]
	if class == nil || blockers[key.path] || class.RegionError != nil {
		return true
	}
	if !c.sensitive(key) {
		return false
	}
	if universeUnreadable || index.ReachesCycle(key.path) {
		return true
	}
	// A class_name this pair must resolve to find its provider is claimed by
	// more than one script, so provider() has no answer.
	for _, ancestor := range index.Ancestry(key.path) {
		name := index.Classes[ancestor].Extends
		if len(index.DuplicateClassNames[name]) > 0 {
			return true
		}
	}
	// Ancestry rule: a strict ancestor declares a selectable field and the
	// parent has no provider to compose with.
	if ancestryHasFields(index, key.path) {
		if _, ok := c.Provider(index, class.ParentID, key.signature); !ok {
			return true
		}
	}
	// Descendant rule: a descendant would inherit this implementation while
	// adding state it does not compare.
	for _, descendant := range index.Descendants(key.path) {
		provider, ok := c.Provider(index, descendant, key.signature)
		if !ok || provider != key.path {
			continue
		}
		if pathBetweenHasFields(index, key.path, descendant) {
			return true
		}
	}
	return false
}

// ancestryHasFields reports whether any strict ancestor of path declares a
// selectable field.
func ancestryHasFields(index *Index, path string) bool {
	ancestry := index.Ancestry(path)
	for _, ancestor := range ancestry[1:] {
		if len(index.Classes[ancestor].Fields) > 0 {
			return true
		}
	}
	return false
}

// pathBetweenHasFields reports whether any class from base (exclusive) to
// descendant (inclusive) declares a selectable field.
func pathBetweenHasFields(index *Index, base, descendant string) bool {
	for _, ancestor := range index.Ancestry(descendant) {
		if ancestor == base {
			return false
		}
		if len(index.Classes[ancestor].Fields) > 0 {
			return true
		}
	}
	return false
}

// sensitive reports whether this pair is subject to the inheritance-sensitive
// demotion conditions. It is recorded per pair when the set is seeded, from the
// generator's own NeedsInheritanceGraph, rather than inferred from the
// signature: a future generator may emit several methods whose sensitivity
// differs, and deriving it from the shape would silently get that wrong.
func (c *Capabilities) sensitive(key pair) bool { return c.inheritanceSensitive[key] }
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -race ./generate -run TestCapability`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add generate/capability.go generate/capability_test.go
git commit -m "Resolve which generated methods will actually exist"
```

---

### Task 7: The registry and `to_string`

**Goal:** The `Generator` interface, a registry with a fixed emission order, and the first generator.

**Files:**
- Create: `generate/generator.go`, `generate/emit_to_string.go`, `generate/emit_to_string_test.go`
- Modify: `generate/marker.go` (delete the `isGeneratorName` stub)

**Acceptance Criteria:**
- [ ] `isGeneratorName` consults the registry, so an unknown name is a `generate.marker`
- [ ] Generators emit in registry order regardless of directive order
- [ ] `_to_string` names the `class_name`, or the file base name when there is none
- [ ] A class with no selected fields emits `"Hex()"`
- [ ] Every field reference is `self.`-qualified

**Verify:** `go test -race ./generate -run TestToString` → ok

**Steps:**

- [ ] **Step 1: Write the failing test**

```go
func TestToStringEmitsSelfQualifiedFields(t *testing.T) {
	got := emitOf(t, toStringGenerator{}, "class_name Hex\nextends RefCounted\n\nvar q: int\nvar r: int\n")
	want := "func _to_string() -> String:\n\treturn \"Hex(q=%s, r=%s)\" % [self.q, self.r]\n"
	if got != want {
		t.Errorf("emit =\n%q\nwant\n%q", got, want)
	}
}

func TestToStringUsesTheFileNameWithoutAClassName(t *testing.T) {
	got := emitOf(t, toStringGenerator{}, "extends RefCounted\n\nvar q: int\n")
	if !strings.Contains(got, `"a(q=%s)"`) {
		t.Errorf("emit = %q, want the file base name", got)
	}
}

func TestToStringWithNoFields(t *testing.T) {
	got := emitOf(t, toStringGenerator{}, "class_name Hex\nextends RefCounted\n")
	want := "func _to_string() -> String:\n\treturn \"Hex()\"\n"
	if got != want {
		t.Errorf("emit = %q, want %q", got, want)
	}
}

// A field whose name collides with the parameter must still be read as a
// field, which is what self. is for.
func TestToStringQualifiesAFieldNamedLikeTheParameter(t *testing.T) {
	got := emitOf(t, toStringGenerator{}, "class_name Hex\nextends RefCounted\n\nvar p_other: int\n")
	if !strings.Contains(got, "self.p_other") {
		t.Errorf("emit = %q, want self.p_other", got)
	}
}

// emitOf runs one generator over a single file named a.gd.
func emitOf(t *testing.T, generator Generator, source string) string {
	t.Helper()
	snapshot := loadProject(t, DefaultConfig(), map[string]string{"a.gd": source})
	index := BuildIndex(snapshot)
	capabilities := Resolve(index, map[string][]string{"a.gd": {generator.Name()}}, nil)
	text, diagnostics := generator.Emit(index.Classes["a.gd"], index, capabilities)
	if len(diagnostics) > 0 {
		t.Fatalf("emit produced diagnostics: %+v", diagnostics)
	}
	return text
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -race ./generate -run TestToString`
Expected: FAIL — `undefined: toStringGenerator`

- [ ] **Step 3: Write `generate/generator.go`**

```go
package generate

import "sort"

// Generator emits one family of methods for a class.
//
// A later generator is not necessarily only an emitter and a registry entry.
// deep_equals, specified separately, widens capability resolution to follow
// field references and adds a builtin type catalogue, a recursion-state helper
// method, and diagnostic severity. Do not assume this interface is the whole
// extension point.
type Generator interface {
	// Name is the directive and configuration spelling: "to_string".
	Name() string
	// Signatures are the methods it emits. A class declaring one of these
	// outside the region satisfies the request; declaring the same name in an
	// incompatible shape is a generate.conflict, because GDScript has no
	// overloading and emitting ours would not compile.
	Signatures() []Signature
	// NeedsInheritanceGraph reports that soundness depends on the inheritance
	// graph, which is what subjects a pair to the cycle, universe-parse,
	// duplicate-class_name, ancestry, and descendant demotion conditions, and
	// what makes a universe-wide parse failure blocking. It belongs on the
	// generator rather than being inferred from a signature, because a future
	// generator may emit several methods with mixed sensitivity.
	NeedsInheritanceGraph() bool
	// Emit returns the method text, unformatted and at indent 0.
	Emit(class *Class, index *Index, capabilities *Capabilities) (string, []Diagnostic)
}

// registry is every generator, in the order they emit. Emission order is fixed
// here rather than taken from the directive, so a region's content depends
// only on the class and not on how the marker was written.
var registry = []Generator{
	toStringGenerator{},
	equalsGenerator{},
}

// generatorByName returns the registered generator, or nil.
func generatorByName(name string) Generator {
	for _, generator := range registry {
		if generator.Name() == name {
			return generator
		}
	}
	return nil
}

// isGeneratorName reports a registered generator name, which is what makes an
// unknown name in a directive a generate.marker rather than a silent no-op.
func isGeneratorName(name string) bool { return generatorByName(name) != nil }

// GeneratorNames lists every registered generator, sorted, for the init
// template and for error messages.
func GeneratorNames() []string {
	names := make([]string, 0, len(registry))
	for _, generator := range registry {
		names = append(names, generator.Name())
	}
	sort.Strings(names)
	return names
}

// inRegistryOrder returns the requested generators in emission order.
func inRegistryOrder(names []string) []Generator {
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[name] = true
	}
	ordered := []Generator{}
	for _, generator := range registry {
		if wanted[generator.Name()] {
			ordered = append(ordered, generator)
		}
	}
	return ordered
}
```

Delete the `isGeneratorName` stub from `marker.go` now.

- [ ] **Step 4: Write `generate/emit_to_string.go`**

```go
package generate

import (
	"fmt"
	"strings"
)

// toStringGenerator emits Godot's _to_string virtual.
type toStringGenerator struct{}

func (toStringGenerator) Name() string { return "to_string" }

func (toStringGenerator) Signatures() []Signature { return []Signature{toStringSignature} }

// NeedsInheritanceGraph is false: _to_string neither composes with an ancestor
// nor walks one. An inherited one naming too few fields prints an incomplete
// value, which is wrong output rather than a wrong answer, and Godot's own
// str() on a subclass is no better. So a fieldful ancestor without a
// _to_string, a cycle elsewhere, and an unparseable unrelated file are all
// irrelevant to it.
func (toStringGenerator) NeedsInheritanceGraph() bool { return false }

// Emit renders the class name and every selected field.
//
// There is no configuration for this format. A project wanting a different one
// hand-writes _to_string and does not opt in, which is better than a style
// option nobody can change later without rewriting every region in every
// adopting project.
//
// It does not compose with an ancestor, because its soundness does not depend
// on the inheritance graph: an inherited _to_string naming too few fields
// prints an incomplete value rather than answering a question wrongly.
func (toStringGenerator) Emit(class *Class, _ *Index, _ *Capabilities) (string, []Diagnostic) {
	var body strings.Builder
	body.WriteString("func _to_string() -> String:\n")
	if len(class.Fields) == 0 {
		fmt.Fprintf(&body, "\treturn \"%s()\"\n", class.Name)
		return body.String(), nil
	}
	placeholders := make([]string, 0, len(class.Fields))
	arguments := make([]string, 0, len(class.Fields))
	for _, field := range class.Fields {
		placeholders = append(placeholders, field.Name+"=%s")
		arguments = append(arguments, "self."+field.Name)
	}
	fmt.Fprintf(&body, "\treturn \"%s(%s)\" %% [%s]\n",
		class.Name, strings.Join(placeholders, ", "), strings.Join(arguments, ", "))
	return body.String(), nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test -race ./generate -run TestToString`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add generate/generator.go generate/emit_to_string.go generate/emit_to_string_test.go generate/marker.go
git commit -m "Add the generator registry and the to_string generator"
```

---

### Task 8: The `equals` generator

**Goal:** `equals` emits a script-identity guard, the `super` composition when the parent provides one, and a field-wise comparison.

**Files:**
- Create: `generate/emit_equals.go`, `generate/emit_equals_test.go`

**Acceptance Criteria:**
- [ ] The guard is `p_other is Object` then `get_script()` identity, not `is <ClassName>`
- [ ] `super.equals(p_other)` is emitted when and only when the parent has a provider
- [ ] Fields are `self.`-qualified on the left and `p_other.`-qualified on the right
- [ ] A class with no fields and no parent provider returns `true`

**Verify:** `go test -race ./generate -run TestEquals` → ok

**Steps:**

- [ ] **Step 1: Write the failing test**

```go
func TestEqualsEmitsTheScriptIdentityGuard(t *testing.T) {
	got := emitOf(t, equalsGenerator{}, "class_name Hex\nextends RefCounted\n\nvar q: int\nvar r: int\n")
	want := "func equals(p_other: Variant) -> bool:\n" +
		"\tif not (p_other is Object):\n\t\treturn false\n" +
		"\tif p_other.get_script() != get_script():\n\t\treturn false\n" +
		"\treturn self.q == p_other.q and self.r == p_other.r\n"
	if got != want {
		t.Errorf("emit =\n%s\nwant\n%s", got, want)
	}
}

// Script identity is symmetric, which is the property a value object needs.
// "is Hex" would make Hex.new().equals(SubHex.new()) true and the reverse
// false, and could not name a class with no class_name at all.
func TestEqualsDoesNotUseAnIsCheck(t *testing.T) {
	got := emitOf(t, equalsGenerator{}, "class_name Hex\nextends RefCounted\n\nvar q: int\n")
	if strings.Contains(got, "is Hex") {
		t.Errorf("emit used an is check: %q", got)
	}
}

func TestEqualsComposesWithTheParentProvider(t *testing.T) {
	got := emitFor(t, "sub.gd", equalsGenerator{}, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n\n" +
			"func equals(p_other: Variant) -> bool:\n\treturn true\n",
		"sub.gd": "class_name Sub\nextends Base\n\nvar r: int\n",
	})
	if !strings.Contains(got, "\tif not super.equals(p_other):\n\t\treturn false\n") {
		t.Errorf("emit did not compose with Base.equals:\n%s", got)
	}
	if !strings.Contains(got, "return self.r == p_other.r") {
		t.Errorf("emit did not compare its own field:\n%s", got)
	}
	if strings.Contains(got, "self.q") {
		t.Errorf("emit reached into the parent's field instead of composing:\n%s", got)
	}
}

func TestEqualsOmitsSuperWithNoParentProvider(t *testing.T) {
	got := emitOf(t, equalsGenerator{}, "class_name Hex\nextends RefCounted\n\nvar q: int\n")
	if strings.Contains(got, "super.equals") {
		t.Errorf("emit called super with no provider:\n%s", got)
	}
}

func TestEqualsWithNoFieldsAndNoParent(t *testing.T) {
	got := emitOf(t, equalsGenerator{}, "class_name Marker\nextends RefCounted\n")
	if !strings.HasSuffix(got, "\treturn true\n") {
		t.Errorf("emit = %q, want a trailing return true", got)
	}
}

// emitFor runs one generator over the named file of a multi-file project.
func emitFor(t *testing.T, path string, generator Generator, files map[string]string) string {
	t.Helper()
	snapshot := loadProject(t, DefaultConfig(), files)
	index := BuildIndex(snapshot)
	capabilities := Resolve(index, map[string][]string{path: {generator.Name()}}, nil)
	text, diagnostics := generator.Emit(index.Classes[path], index, capabilities)
	if len(diagnostics) > 0 {
		t.Fatalf("emit produced diagnostics: %+v", diagnostics)
	}
	return text
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -race ./generate -run TestEquals`
Expected: FAIL — `undefined: equalsGenerator`

- [ ] **Step 3: Write `generate/emit_equals.go`**

```go
package generate

import (
	"fmt"
	"strings"
)

// equalsGenerator emits a value-equality method.
type equalsGenerator struct{}

func (equalsGenerator) Name() string { return "equals" }

func (equalsGenerator) Signatures() []Signature { return []Signature{equalsSignature} }

// NeedsInheritanceGraph is true: equals composes with its ancestor's provider
// and is refused when a descendant would inherit an unsound implementation, so
// both answers need the whole graph to be visible and trustworthy.
func (equalsGenerator) NeedsInheritanceGraph() bool { return true }

// Emit renders the guard, the ancestor composition, and the field comparison.
//
// The guard is script identity rather than "is <ClassName>" for two reasons.
// It works for a class that declares no class_name, which "is" cannot name at
// all. And it is symmetric: two instances are equal candidates only when they
// are the same script, so a.equals(b) == b.equals(a) always holds, where "is"
// quietly answers true one way and false the other across a subclass.
//
// Script identity alone is not sufficient, which is why the composition
// matters: two instances of a subclass answer the same get_script(), so an
// inherited comparison would pass the guard and then ignore every field the
// subclass added. Composing with the parent's provider means every field in
// the ancestry is compared, each by the class that declares it. Capability
// resolution refuses the cases where no such provider exists.
//
// Object-valued fields compare by reference, because that is what == does to
// an Object in Godot 4. Array and Dictionary fields compare by value, because
// that is what == does to those. Structural comparison of a nested value
// object is deep_equals's job.
func (equalsGenerator) Emit(class *Class, index *Index, capabilities *Capabilities) (string, []Diagnostic) {
	var body strings.Builder
	body.WriteString("func equals(p_other: Variant) -> bool:\n")
	body.WriteString("\tif not (p_other is Object):\n\t\treturn false\n")
	body.WriteString("\tif p_other.get_script() != get_script():\n\t\treturn false\n")
	if _, ok := capabilities.Provider(index, class.ParentID, equalsSignature); ok {
		body.WriteString("\tif not super.equals(p_other):\n\t\treturn false\n")
	}
	if len(class.Fields) == 0 {
		body.WriteString("\treturn true\n")
		return body.String(), nil
	}
	comparisons := make([]string, 0, len(class.Fields))
	for _, field := range class.Fields {
		comparisons = append(comparisons, fmt.Sprintf("self.%s == p_other.%s", field.Name, field.Name))
	}
	// Emitted on one line; the formatter wraps it to the project's line_width.
	fmt.Fprintf(&body, "\treturn %s\n", strings.Join(comparisons, " and "))
	return body.String(), nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -race ./generate -run TestEquals`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add generate/emit_equals.go generate/emit_equals_test.go
git commit -m "Add the equals generator with ancestor composition"
```

---

### Task 9: Configuration

**Goal:** `.gdkit/generate.json` loads onto the defaults, rejects unknown keys, and unions overlapping entries.

**Files:**
- Create: `generate/config.go`, `generate/config_test.go`

**Acceptance Criteria:**
- [ ] `DefaultConfig().Exclude` matches `format.DefaultConfig().Exclude` exactly
- [ ] A file matching several `generate` entries gets the union of their generators
- [ ] An unknown key at any depth is an error naming its full JSON path
- [ ] `minimum_gdkit_version` is checked before the unknown-key walk
- [ ] An unknown generator name in config is a validation error
- [ ] There is no `godot_version` key

**Verify:** `go test -race ./generate -run TestConfig` and `go test -race ./generate -run TestLoadConfig` → ok

**Steps:**

- [ ] **Step 1: Write the failing test**

```go
// An empty exclude default would make addons/** a generation target, so a glob
// written for the project's own code could start rewriting a third-party addon.
func TestDefaultExcludeMatchesFormat(t *testing.T) {
	if !reflect.DeepEqual(DefaultConfig().Exclude, format.DefaultConfig().Exclude) {
		t.Errorf("Exclude = %v, want format's %v", DefaultConfig().Exclude, format.DefaultConfig().Exclude)
	}
}

// Union rather than first-match, matching the grain of architecture's
// dependencies, where capability is added by adding a rule. Union is also
// order-independent, so reordering the list cannot change the output.
func TestGeneratorsForUnionsOverlappingEntries(t *testing.T) {
	config := DefaultConfig()
	config.Generate = []Entry{
		{Paths: []string{"**/*.gd"}, Generators: []string{"to_string"}},
		{Paths: []string{"domain/**"}, Generators: []string{"equals"}},
	}
	compiled, err := config.compile()
	if err != nil {
		t.Fatal(err)
	}
	got := compiled.generatorsFor("domain/hex.gd")
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"equals", "to_string"}) {
		t.Errorf("generatorsFor = %v, want both", got)
	}
	if got := compiled.generatorsFor("ui/panel.gd"); !reflect.DeepEqual(got, []string{"to_string"}) {
		t.Errorf("generatorsFor = %v, want only to_string", got)
	}
}

func TestLoadConfigRejectsAnUnknownKeyByPath(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"generate":[{"paths":["a/**"],"generatorz":["equals"]}]}`)
	_, err := LoadConfig(root, DefaultConfigPath)
	if err == nil || !strings.Contains(err.Error(), "generate[0].generatorz") {
		t.Errorf("err = %v, want one naming generate[0].generatorz", err)
	}
}

// A config written for a newer gdkit carries both the floor and the syntax
// that needed it, so checking the floor later would report an unknown key and
// point the reader at a typo instead of a binary that is too old.
func TestLoadConfigReportsTheMinimumVersionBeforeAnUnknownKey(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"minimum_gdkit_version":"99.0.0","not_a_key":1}`)
	_, err := LoadConfig(root, DefaultConfigPath)
	if err == nil || strings.Contains(err.Error(), "not_a_key") {
		t.Errorf("err = %v, want the version floor", err)
	}
}

func TestValidateRejectsAnUnknownGeneratorName(t *testing.T) {
	config := DefaultConfig()
	config.Generate = []Entry{{Paths: []string{"a/**"}, Generators: []string{"nope"}}}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("err = %v, want one naming nope", err)
	}
}

func TestConfigHasNoGodotVersionKey(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"godot_version":"4.7"}`)
	if _, err := LoadConfig(root, DefaultConfigPath); err == nil {
		t.Error("godot_version was accepted; nothing in this design is version-gated")
	}
}

func writeConfig(t *testing.T, root, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(DefaultConfigPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -race ./generate -run TestConfig`
Expected: FAIL — `undefined: DefaultConfig`

- [ ] **Step 3: Write `generate/config.go`**

Model it on `format/config.go` and `architecture/config.go`: read the file, run `checkSingleValue`, then `checkMinimumVersion`, then `checkUnknownKeys`, then decode onto the defaults, then `Validate`. Reuse `architecture`'s `checkUnknownKeys` approach — a reflected walk of the raw document against `Config`'s shape that names the offending key's full JSON path, because `DisallowUnknownFields` reports the key alone and that does not locate a typo in a large config. If that function is unexported in `architecture`, lift it into `internal/configcheck` in this task and have both packages call it rather than copying it.

```go
package generate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cafecito-games/gdkit/internal/glob"
)

// DefaultConfigPath is where gen init writes and gen check reads.
const DefaultConfigPath = ".gdkit/generate.json"

// Entry opts the files matching Paths into Generators.
type Entry struct {
	Paths      []string `json:"paths"`
	Generators []string `json:"generators"`
}

// Config is the generate policy. There is no godot_version key: every semantic
// this tool relies on is constant across Godot 4.
type Config struct {
	Version     int      `json:"version"`
	SourceRoots []string `json:"source_roots"`
	Exclude     []string `json:"exclude"`
	// Generate opts files in by path. A file matching several entries gets the
	// union of their generators.
	Generate []Entry `json:"generate"`
	// MinimumGdkitVersion is the floor this config needs, checked before the
	// unknown-key walk.
	MinimumGdkitVersion string `json:"minimum_gdkit_version,omitempty"`
}

// DefaultConfig is both the zero-config policy and what gen init writes.
//
// Exclude matches format.DefaultConfig() exactly rather than being empty: an
// empty default would make addons/** a generation target, so a glob someone
// wrote for their own code could start rewriting a third-party addon.
func DefaultConfig() Config {
	return Config{
		Version:     1,
		SourceRoots: []string{"."},
		Exclude:     []string{".git/**", ".godot/**", ".gdkit/**", "addons/**"},
		Generate:    []Entry{},
	}
}

// Validate compiles every glob and checks every generator name, so a bad
// config fails as a configuration error rather than silently generating
// nothing.
func (c Config) Validate() error {
	_, err := c.compile()
	return err
}

// compiled holds the config's prepared forms.
type compiled struct {
	entries []compiledEntry
}

type compiledEntry struct {
	paths      []string
	generators []string
}

func (c Config) compile() (*compiled, error) {
	result := &compiled{}
	for index, entry := range c.Generate {
		if len(entry.Paths) == 0 {
			return nil, fmt.Errorf("generate[%d]: paths is required", index)
		}
		if len(entry.Generators) == 0 {
			return nil, fmt.Errorf("generate[%d]: generators is required", index)
		}
		for _, pattern := range entry.Paths {
			if err := glob.Validate(pattern); err != nil {
				return nil, fmt.Errorf("generate[%d].paths %q: %w", index, pattern, err)
			}
		}
		for _, name := range entry.Generators {
			if !isGeneratorName(name) {
				return nil, fmt.Errorf("generate[%d].generators: unknown generator %q, want one of %v",
					index, name, GeneratorNames())
			}
		}
		result.entries = append(result.entries, compiledEntry{paths: entry.Paths, generators: entry.Generators})
	}
	return result, nil
}

// generatorsFor returns the union of the generators every matching entry opts
// path into, deduplicated.
func (c *compiled) generatorsFor(path string) []string {
	seen := map[string]bool{}
	names := []string{}
	for _, entry := range c.entries {
		if !glob.MatchAny(entry.paths, path) {
			continue
		}
		for _, name := range entry.generators {
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}
```

Write `LoadConfig` by following `format.LoadConfig` exactly for the read-and-decode shape, and `architecture.LoadConfig` for the ordering of `checkSingleValue`, `checkMinimumVersion`, and `checkUnknownKeys`. Clear `Generate` before decoding and restore it only when the file omits the key, for the reason `CLAUDE.md` records: `encoding/json` decodes an array element onto whatever the slice already holds at that index, so without that a declared entry would inherit fields from a default entry at the same position.

If `glob` has no exported `Validate`, use whatever `architecture`'s `Config.Validate` calls to compile a pattern.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./generate -run "TestConfig|TestDefault|TestGenerators|TestLoadConfig|TestValidate"`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add generate/config.go generate/config_test.go
git commit -m "Add the generate configuration"
```

---

### Task 10: `Check`, splicing, and verification

**Goal:** The whole pipeline runs, with the 4–6 re-resolution loop and the format oracle.

**Files:**
- Create: `generate/check.go`, `generate/check_test.go`, `generate/verify.go`, `generate/verify_test.go`

**Acceptance Criteria:**
- [ ] Opt-in resolves config, then directive, then `ignore`
- [ ] `generate.stale` is reported for a missing or out-of-date region
- [ ] Every byte outside the region is identical, or the candidate is refused `generate.unsafe`
- [ ] The format oracle runs on a candidate whose source was format-clean, and refuses one it would change
- [ ] A verification failure re-enters resolution, and a child composing with a refused parent is also refused
- [ ] `generate.conflict` fires on an incompatible same-name method
- [ ] An orphaned region is reported anywhere in the universe, including an excluded file
- [ ] A universe-wide parse failure blocks `equals` but not `to_string`
- [ ] `gen write` is idempotent; a second `Check` reports no change

**Verify:** `go test -race ./generate` → ok

**Steps:**

- [ ] **Step 1: Write the failing test**

```go
func TestCheckReportsAMissingRegionAsStale(t *testing.T) {
	report, _ := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n",
	})
	assertDiagnostic(t, report, ruleStale)
	if !report.HasChanges() {
		t.Error("a missing region is a change")
	}
}

func TestCheckIsCleanOnAnUpToDateRegion(t *testing.T) {
	source := "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n"
	report, _ := checkProject(t, DefaultConfig(), map[string]string{"a.gd": source})
	// Apply what check wants, then check again: nothing should change.
	written := applyOnce(t, DefaultConfig(), map[string]string{"a.gd": source})
	report, _ = checkProject(t, DefaultConfig(), map[string]string{"a.gd": written["a.gd"]})
	if report.HasChanges() || report.HasDiagnostics() {
		t.Errorf("second run was not clean: %+v %+v", report.Results, report.Diagnostics)
	}
}

func TestCheckHonoursThePrecedenceOfIgnoreOverConfig(t *testing.T) {
	config := DefaultConfig()
	config.Generate = []Entry{{Paths: []string{"**/*.gd"}, Generators: []string{"to_string"}}}
	report, _ := checkProject(t, config, map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate:ignore\nvar q: int\n",
	})
	if report.HasChanges() || report.HasDiagnostics() {
		t.Errorf("ignore did not beat config: %+v %+v", report.Results, report.Diagnostics)
	}
}

func TestCheckReportsAnIncompatibleSameNameMethodAsAConflict(t *testing.T) {
	report, _ := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n\n" +
			"static func equals(a, b) -> bool:\n\treturn true\n",
	})
	assertDiagnostic(t, report, ruleConflict)
}

func TestCheckSatisfiedByACompatibleHandwrittenMethod(t *testing.T) {
	report, _ := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n\n" +
			"func equals(p_other: Variant) -> bool:\n\treturn true\n",
	})
	if report.HasDiagnostics() || report.HasChanges() {
		t.Errorf("a compatible method did not satisfy the request: %+v %+v", report.Diagnostics, report.Results)
	}
}

// Excluding a path is the most likely way to orphan a region, so it must not
// also be the way to hide it.
func TestCheckReportsAnOrphanedRegionInAnExcludedFile(t *testing.T) {
	report, _ := checkProject(t, DefaultConfig(), map[string]string{
		"addons/vendor.gd": "class_name Vendor\nextends RefCounted\n\n" +
			"# gdkit:generated:begin\nfunc _to_string() -> String:\n\treturn \"Vendor()\"\n# gdkit:generated:end\n",
	})
	assertDiagnostic(t, report, ruleOrphaned)
}

// Inheritance is a reverse dependency: an unparseable file may extend a
// requested base and add fields, and nothing in the base names it. So equals
// cannot be trusted while anything is unreadable, while to_string can.
func TestAnUnparseableFileBlocksEqualsButNotToString(t *testing.T) {
	files := map[string]string{
		"a.gd":      "class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
		"broken.gd": "func (((\n",
	}
	report, _ := checkProject(t, DefaultConfig(), files)
	var sawParse bool
	for _, d := range report.Diagnostics {
		if d.Rule == ruleSourceParse {
			sawParse = true
		}
	}
	if !sawParse {
		t.Error("equals ran with an unparseable file in the universe")
	}

	files["a.gd"] = "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n"
	report, _ = checkProject(t, DefaultConfig(), files)
	for _, d := range report.Diagnostics {
		if d.Rule == ruleSourceParse {
			t.Errorf("to_string was blocked by an unparseable unrelated file: %s", d)
		}
	}
}

func TestCheckKeepsEveryByteOutsideTheRegion(t *testing.T) {
	source := "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q:int\n\n\nfunc untouched( ) :\n\tpass\n"
	written := applyOnce(t, DefaultConfig(), map[string]string{"a.gd": source})
	got := written["a.gd"]
	// The deliberately unformatted code outside the region must survive
	// verbatim: gen write is not a partial format write.
	if !strings.Contains(got, "var q:int") || !strings.Contains(got, "func untouched( ) :") {
		t.Errorf("code outside the region was reformatted:\n%s", got)
	}
}

// applyOnce runs check then Apply over a temp project and returns the files as
// they ended up on disk.
func applyOnce(t *testing.T, config Config, files map[string]string) map[string]string {
	t.Helper()
	root := t.TempDir()
	for name, contents := range files {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := project.Load(project.Config{
		Root:      root,
		Selection: &project.Selection{SourceRoots: config.SourceRoots, Exclude: config.Exclude, HonorIgnoreFile: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	generator, err := New(config, format.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	plan := generator.Check(snapshot)
	if _, err := Apply(snapshot, plan, false); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for name := range files {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		out[name] = string(contents)
	}
	return out
}
```

- [ ] **Step 2: Write the verification test**

```go
// The oracle turns the formatting invariant from a claim in a document into a
// precondition of writing.
func TestVerifyRefusesACandidateTheFormatterWouldChange(t *testing.T) {
	snapshot := loadProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n",
	})
	generator, err := New(DefaultConfig(), format.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	// Stand in an emitter whose output is valid GDScript but not canonical, so
	// the oracle is what rejects it rather than the tree or the byte check.
	generator.emit = func(Generator, *Class, *Index, *Capabilities) (string, []Diagnostic) {
		return "func _to_string() -> String:\n\t\t\treturn \"Hex()\"\n", nil
	}
	report := generator.Check(snapshot).Report()
	assertDiagnostic(t, report, ruleUnsafe)
}

// A verification failure withdraws a capability, so a child that composed with
// it must be refused too. Without the re-resolution loop the child emits a
// super call against a method that was never written.
func TestARefusedParentRefusesTheChildThatComposedWithIt(t *testing.T) {
	snapshot := loadProject(t, DefaultConfig(), map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n\n# gdkit:generate = equals\nvar r: int\n",
	})
	generator, err := New(DefaultConfig(), format.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	// Damage only Base's output, so only Base fails verification.
	generator.emit = func(g Generator, class *Class, index *Index, capabilities *Capabilities) (string, []Diagnostic) {
		if class.Path == "base.gd" {
			return "func equals(p_other: Variant) -> bool:\n\t\t\treturn true\n", nil
		}
		return g.Emit(class, index, capabilities)
	}
	report := generator.Check(snapshot).Report()
	refused := map[string]bool{}
	for _, d := range report.Diagnostics {
		refused[d.Path] = true
	}
	if !refused["base.gd"] {
		t.Error("base.gd was not refused")
	}
	if !refused["sub.gd"] {
		t.Error("sub.gd generated a super call against a method that will not exist")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test -race ./generate -run "TestCheck|TestVerify|TestAn|TestA"`
Expected: FAIL — `undefined: New`

- [ ] **Step 4: Write `generate/check.go`**

```go
package generate

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/cafecito-games/gdkit/format"
	"github.com/cafecito-games/gdkit/project"
	gdformat "github.com/cafecito-games/gdparser/format"
)

// Generator computes the generated region of every class that opted in.
type Generator struct {
	config   Config
	compiled *compiled
	options  gdformat.Options
	// emit renders one generator's text. It is a field so a test can stand in
	// an emitter that damages its output and prove the damage is refused,
	// which is the only way to reach verify from Check.
	emit func(Generator, *Class, *Index, *Capabilities) (string, []Diagnostic)
}

// New builds a Generator, rejecting an invalid config. It takes the project's
// format config as well, because the region is canonicalised with it: without
// that, gen write and format check would fight over the region forever.
func New(config Config, formatting format.Config) (*Generator, error) {
	compiledConfig, err := config.compile()
	if err != nil {
		return nil, err
	}
	if err := formatting.Validate(); err != nil {
		return nil, err
	}
	options, err := formatting.Options()
	if err != nil {
		return nil, err
	}
	return &Generator{
		config:   config,
		compiled: compiledConfig,
		options:  options,
		emit: func(g Generator, class *Class, index *Index, capabilities *Capabilities) (string, []Diagnostic) {
			return g.Emit(class, index, capabilities)
		},
	}, nil
}

// Candidate is a file generate can rewrite, and the contents to write.
type Candidate struct {
	Path     string
	Class    string
	Contents []byte
	Region   Span
	Changed  bool
}

// Plan is the outcome of one run. A class with a blocking diagnostic
// contributes no candidate.
type Plan struct {
	Candidates  []Candidate
	Diagnostics []Diagnostic
	// Orphans are the paths holding a region whose class no longer opts in.
	// --prune removes these; without it they are reported and left alone.
	Orphans []string
}

// Report renders the plan's public, JSON-shaped result.
func (p Plan) Report() Report {
	report := Report{Results: []Result{}, Diagnostics: append([]Diagnostic{}, p.Diagnostics...)}
	for _, candidate := range p.Candidates {
		report.Results = append(report.Results, Result{Path: candidate.Path, Changed: candidate.Changed})
		if candidate.Changed {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Rule:    ruleStale,
				Message: "generated region is out of date",
				Path:    candidate.Path,
				Line:    1,
				Column:  1,
			})
		}
	}
	report.sort()
	return report
}

// Check computes the plan. It performs no I/O.
//
// Steps 4 to 6 of the pipeline — resolve, emit, verify — run as a loop,
// because a generate.unsafe refusal is only discovered after emission and it
// withdraws a capability a descendant may already have composed with. Blockers
// only accumulate and are bounded by the number of classes, so this
// terminates.
// localBlockers marks every requested class carrying a blocking condition that
// is knowable before emission: a malformed marker or region, an inner class,
// or a method declared outside the region whose name collides with one a
// requested generator emits in an incompatible shape.
func (g *Generator) localBlockers(index *Index, requested map[string][]string, markers []Diagnostic) map[string]bool {
	blockers := map[string]bool{}
	for _, diagnostic := range markers {
		if diagnostic.Rule == ruleMarker {
			blockers[diagnostic.Path] = true
		}
	}
	for path, names := range requested {
		// Only a top-level class can be requested: resolveOptIn refuses an
		// inner-class marker outright rather than adding it here, so an inner
		// class never reaches this map.
		class := index.TopLevel[path]
		if class == nil || class.RegionError != nil {
			blockers[path] = true
			continue
		}
		for _, generator := range inRegistryOrder(names) {
			for _, signature := range generator.Signatures() {
				declared, ok := class.Methods[signature.Name]
				if !ok || declared.InRegion {
					continue
				}
				if declared.Signature != signature {
					blockers[path] = true
				}
			}
		}
	}
	return blockers
}

func (g *Generator) Check(snapshot *project.Snapshot) Plan {
	index := BuildIndex(snapshot)
	requested, markerDiagnostics := g.resolveOptIn(snapshot, index)
	plan := Plan{Candidates: []Candidate{}, Diagnostics: markerDiagnostics, Orphans: []string{}}

	// Local blockers are computed before the first Resolve, not discovered
	// after it. The transfer function lists marker, conflict, and inner-class
	// as conditions that demote a pair, so resolution cannot be correct on a
	// pass that has not seen them yet: a conflicted parent would look like a
	// provider and its child would compose with a method that is never
	// emitted. Only generate.unsafe is genuinely late, because it cannot be
	// known until the candidate exists, and that is what the outer loop is
	// for.
	blockers := g.localBlockers(index, requested, markerDiagnostics)
	for {
		capabilities := Resolve(index, requested, blockers)
		attempt := g.buildPlan(snapshot, index, requested, capabilities, blockers)
		newBlocker := false
		for _, diagnostic := range attempt.Diagnostics {
			if diagnostic.Rule == ruleUnsafe && !blockers[diagnostic.Path] {
				blockers[diagnostic.Path] = true
				newBlocker = true
			}
		}
		if !newBlocker {
			plan.Candidates = attempt.Candidates
			plan.Diagnostics = append(plan.Diagnostics, attempt.Diagnostics...)
			plan.Orphans = attempt.Orphans
			return plan
		}
	}
}
```

Write `resolveOptIn` and `buildPlan` in the same file:

- `resolveOptIn` walks `snapshot.Selected` and, for each standalone directive
  line, **attributes it to the innermost class whose `Body` span contains it**
  before doing anything else. This is not optional bookkeeping: a line scan
  that returns path-keyed requests lets a marker written inside an inner class,
  or even inside a function body, opt in the file's top-level class.

  ```go
  // ownerOf returns the innermost class whose body contains offset. The
  // innermost wins, which is why candidates are compared by span width.
  func (i *Index) ownerOf(path string, offset int) *Class {
  	owner := i.TopLevel[path]
  	for _, class := range i.Classes {
  		if class.Path != path || !class.Inner {
  			continue
  		}
  		if class.Body.Start > offset || offset >= class.Body.End {
  			continue
  		}
  		if owner == nil || class.Body.End-class.Body.Start < owner.Body.End-owner.Body.Start {
  			owner = class
  		}
  	}
  	return owner
  }
  ```

  A directive owned by an inner class yields `generate.unsupported` naming that
  class, and is **not** added to the top-level class's request. A directive
  owned by the top-level class behaves as before: `MatchGenerate` populates the
  request, a `MatchGenerate` error or a `RegionError` is a `generate.marker`,
  config supplies the list when no directive is present, and `MatchIgnore`
  beats both.
- `buildPlan` walks requested paths in sorted order, and for each: skips and diagnoses an inner class or a class whose `Realizable` is false (with the reason — `ruleUnsupported`), diagnoses `ruleConflict` for an incompatible same-name method, skips a signature a compatible hand-written method already satisfies, emits each generator in `inRegistryOrder`, wraps the bodies in the sentinels joined by `blank_lines.top_level` newlines, canonicalises with `canonicaliseRegion`, splices with `Splice`, verifies, and appends the candidate. It also appends `ruleSourceParse` diagnostics for every `index.ParseFailures` entry when any requested generator has `NeedsInheritanceGraph`, `ruleClassNameDuplicate` for a duplicate a requested class needs, and fills `Orphans` from every class in the universe with a region and no request.

```go
// canonicaliseRegion formats the region's text in isolation, so that no byte
// outside it can change. Formatting the spliced file instead would let
// gdparser reformat code elsewhere whenever the file was not already
// canonical, which is a partial format write wearing gen's name.
//
// This is sound only because the region sits at indent 0, which is why v1 is
// limited to top-level classes: an inner class's region would need
// re-indentation afterwards.
func (g *Generator) canonicaliseRegion(region string) (string, error) {
	file, err := gdparser.Parse([]byte(region))
	if err != nil {
		return "", fmt.Errorf("the generated region does not parse: %w", err)
	}
	return gdformat.FileWithOptions(file, g.options), nil
}
```

- [ ] **Step 5: Write `generate/verify.go`**

```go
package generate

import (
	"bytes"
	"fmt"

	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser"
	gdformat "github.com/cafecito-games/gdparser/format"
)

// verify checks a candidate before it is offered.
//
// format's safety net cannot be reused: its value is the promise that a
// rewrite changes nothing but layout, enforced by comparing the token stream,
// and a generator changes the token stream by definition. The replacement is
// tighter and cheaper — every byte outside the region is identical, the result
// reparses, and a file that was canonical stays canonical.
func (g *Generator) verify(script *project.Script, candidate Candidate, source Span, hadRegion bool) error {
	before := source.Start
	if !hadRegion {
		before = len(script.Source)
	}
	if !bytes.Equal(script.Source[:before], candidate.Contents[:candidate.Region.Start]) {
		return fmt.Errorf("bytes before the generated region changed")
	}
	var sourceAfter []byte
	if hadRegion {
		sourceAfter = script.Source[source.End:]
	}
	if !bytes.Equal(sourceAfter, candidate.Contents[candidate.Region.End:]) {
		return fmt.Errorf("bytes after the generated region changed")
	}
	if _, err := gdparser.ParseFile(candidate.Path, candidate.Contents); err != nil {
		return fmt.Errorf("the result does not parse: %w", err)
	}
	return g.verifyFormatOracle(script, candidate)
}

// verifyFormatOracle runs the project's full-file formatter over the candidate
// as a read-only check, when the source file was already canonical.
//
// Owning the region's leading gap is an argument that a canonical file stays
// canonical, not a proof, and the argument is about another package's
// behaviour: gdparser's blankLineGaps attributes the gap before a comment run
// documenting a declaration to the top of that run. So the claim is checked on
// every real candidate rather than only in fixtures.
func (g *Generator) verifyFormatOracle(script *project.Script, candidate Candidate) error {
	if !g.canonical(script.Path, script.Source) {
		// The file was not canonical to begin with, so gen owes it nothing
		// beyond leaving the rest of it alone, which verify already checked.
		return nil
	}
	if !g.canonical(candidate.Path, candidate.Contents) {
		return fmt.Errorf("the result is not formatted, although the source was")
	}
	return nil
}

func (g *Generator) canonical(path string, contents []byte) bool {
	file, err := gdparser.ParseFile(path, contents)
	if err != nil {
		return false
	}
	return gdformat.FileWithOptions(file, g.options) == string(contents)
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./generate`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add generate/check.go generate/check_test.go generate/verify.go generate/verify_test.go
git commit -m "Compute and verify the generated region"
```

---

### Task 11: `Apply`

**Goal:** The only writer, with the same atomicity and staleness guard `format.Apply` has, plus `--prune`.

**Files:**
- Create: `generate/apply.go`, `generate/apply_test.go`

**Acceptance Criteria:**
- [ ] Only changed candidates are written, in path order
- [ ] A file whose contents no longer match the snapshot is not overwritten
- [ ] A write is atomic: temp file beside the target, renamed over it
- [ ] Permission bits are preserved
- [ ] `prune` removes an orphaned region, including its owned leading gap
- [ ] `prune` refuses to write outside the selection
- [ ] Paths already written are returned alongside an error

**Verify:** `go test -race ./generate -run TestApply` → ok

**Steps:**

- [ ] **Step 1: Write the failing test**

```go
func TestApplyWritesOnlyChangedCandidates(t *testing.T) {
	written := applyOnce(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n",
		"b.gd": "class_name Plain\nextends RefCounted\n\nvar q: int\n",
	})
	if !strings.Contains(written["a.gd"], beginSentinel) {
		t.Errorf("a.gd was not generated:\n%s", written["a.gd"])
	}
	if strings.Contains(written["b.gd"], beginSentinel) {
		t.Errorf("b.gd opted into nothing but was written:\n%s", written["b.gd"])
	}
}

func TestApplyRefusesAFileThatChangedOnDisk(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.gd")
	source := "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := project.Load(project.Config{Root: root, Selection: &project.Selection{SourceRoots: []string{"."}}})
	if err != nil {
		t.Fatal(err)
	}
	generator, err := New(DefaultConfig(), format.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	plan := generator.Check(snapshot)
	// Someone edits the file between the read and the write.
	if err := os.WriteFile(path, []byte(source+"var late: int\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(snapshot, plan, false); err == nil {
		t.Error("Apply overwrote a file that changed under it")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "var late: int") {
		t.Error("the concurrent edit was lost")
	}
}

func TestApplyPrunesAnOrphanedRegionWithItsLeadingGap(t *testing.T) {
	root := t.TempDir()
	source := "class_name Hex\nextends RefCounted\n\nvar q: int\n\n\n" +
		"# gdkit:generated:begin\nfunc _to_string() -> String:\n\treturn \"Hex()\"\n# gdkit:generated:end\n"
	if err := os.WriteFile(filepath.Join(root, "a.gd"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := project.Load(project.Config{Root: root, Selection: &project.Selection{SourceRoots: []string{"."}}})
	if err != nil {
		t.Fatal(err)
	}
	generator, err := New(DefaultConfig(), format.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(snapshot, generator.Check(snapshot), true); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "a.gd"))
	if err != nil {
		t.Fatal(err)
	}
	want := "class_name Hex\nextends RefCounted\n\nvar q: int\n"
	if string(contents) != want {
		t.Errorf("pruned = %q, want %q", contents, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -race ./generate -run TestApply`
Expected: FAIL — `undefined: Apply`

- [ ] **Step 3: Write `generate/apply.go`**

Copy the structure of `format/apply.go` exactly — it already solves atomicity, the staleness guard, and the permission bits, and `generate` has the same requirements. Add pruning:

```go
// Apply writes every changed candidate back to its file under snapshot.Root
// and returns the project-relative paths written, in order. When prune is set
// it also removes orphaned regions.
//
// A file whose contents no longer match the snapshot is not overwritten. It
// stops at the first such file or I/O failure, returning the paths already
// written alongside the error, because that list is what tells the caller the
// state the project is in.
func Apply(snapshot *project.Snapshot, plan Plan, prune bool) ([]string, error) {
	written := []string{}
	for _, candidate := range plan.Candidates {
		if !candidate.Changed {
			continue
		}
		script := snapshot.Scripts[candidate.Path]
		if script == nil {
			return written, fmt.Errorf("write %s: file is not in the snapshot", candidate.Path)
		}
		target := filepath.Join(snapshot.Root, filepath.FromSlash(candidate.Path))
		if err := replaceFile(target, script.Source, candidate.Contents); err != nil {
			return written, fmt.Errorf("write %s: %w", candidate.Path, err)
		}
		written = append(written, candidate.Path)
	}
	if !prune {
		return written, nil
	}
	selected := map[string]bool{}
	for _, path := range snapshot.Selected {
		selected[path] = true
	}
	for _, path := range plan.Orphans {
		// --prune does not write outside the selection. The diagnostic says
		// the file must re-enter it, rather than the tool quietly reaching
		// into a directory the config excluded.
		if !selected[path] {
			continue
		}
		script := snapshot.Scripts[path]
		span, found, err := FindRegion(script.Source)
		if err != nil || !found {
			continue
		}
		pruned := append(append([]byte{}, script.Source[:span.Start]...), script.Source[span.End:]...)
		target := filepath.Join(snapshot.Root, filepath.FromSlash(path))
		if err := replaceFile(target, script.Source, pruned); err != nil {
			return written, fmt.Errorf("prune %s: %w", path, err)
		}
		written = append(written, path)
	}
	return written, nil
}
```

`replaceFile` is `format/apply.go`'s, with the temp-file prefix changed to `.gdkit-generate-*`. Lift it into `internal/atomicwrite` and have both `format` and `generate` call it rather than keeping two copies; its comment explaining the re-read before rename moves with it.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -race ./generate -run TestApply`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add generate/apply.go generate/apply_test.go internal/atomicwrite format/apply.go
git commit -m "Write the generated regions atomically"
```

---

### Task 12: Command wiring

**Goal:** `gdkit gen check`, `gen write`, and `gen init` behave exactly as the other tools do.

**Files:**
- Create: `cmd/gdkit/generate.go`, `cmd/gdkit/generate_test.go`
- Modify: `cmd/gdkit/main.go` (dispatch and `usageText`)

**Acceptance Criteria:**
- [ ] `gdkit gen` with no subcommand runs `check`
- [ ] Exit `0` clean, `1` findings, `2` configuration/usage/IO
- [ ] `gen write` exits 1 only on a blocking diagnostic, not on staleness it fixed
- [ ] `--format json` puts an exit-2 failure on stderr with stdout empty
- [ ] `--diff` with `--format json` is a usage error
- [ ] `gen write` prints changed files on stdout even when a write fails part-way
- [ ] `--format` is validated before anything else can fail

**Verify:** `go test -race ./cmd/gdkit -run TestGen` → ok

**Steps:**

- [ ] **Step 1: Write the failing test**

Follow `cmd/gdkit/format_test.go`'s shape exactly. Required cases:

```go
func TestGenCheckExitCodes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.gd", "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", "check", root}, &stdout, &stderr); code != 1 {
		t.Errorf("exit = %d, want 1 for a stale region (%s)", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"gen", "write", root}, &stdout, &stderr); code != 0 {
		t.Errorf("write exit = %d, want 0 (%s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "a.gd") {
		t.Errorf("write did not name the file it changed: %q", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"gen", "check", root}, &stdout, &stderr); code != 0 {
		t.Errorf("exit = %d, want 0 after write (%s)", code, stdout.String())
	}
}

func TestGenBareRunsCheck(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", root}, &stdout, &stderr); code != 0 {
		t.Errorf("exit = %d, want 0 (%s)", code, stderr.String())
	}
}

func TestGenJSONFailureGoesToStderrWithEmptyStdout(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gdkit/generate.json", `{"not_a_key": 1}`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", "check", "--format", "json", root}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty so a consumer can tell no report from an empty one", stdout.String())
	}
	var envelope struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatalf("stderr is not an envelope: %v (%q)", err, stderr.String())
	}
	if envelope.Kind == "" {
		t.Error("the envelope carries no failure kind")
	}
}

func TestGenRejectsDiffWithJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"gen", "check", "--diff", "--format", "json", t.TempDir()}, &stdout, &stderr); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -race ./cmd/gdkit -run TestGen`
Expected: FAIL — exit 2, `unknown command "gen"`

- [ ] **Step 3: Write `cmd/gdkit/generate.go`**

Copy `cmd/gdkit/format.go` and adapt. Keep the same ordering of checks, which `CLAUDE.md` records as load-bearing: `--format` is validated before anything else can fail, because its value decides how every later failure is reported; an unknown `--format` is itself reported as text, since a caller who misspelled it cannot be assumed to parse an envelope; and `init` has no `--format` at all.

```go
func runGen(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runGenCheck(nil, stdout, stderr)
	}
	switch args[0] {
	case "check":
		return runGenCheck(args[1:], stdout, stderr)
	case "write":
		return runGenWrite(args[1:], stdout, stderr)
	case "init":
		return runGenInit(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	default:
		// A bare root is the common case: "gdkit gen ." runs check.
		if !strings.HasPrefix(args[0], "-") {
			return runGenCheck(args, stdout, stderr)
		}
		return runGenCheck(args, stdout, stderr)
	}
}
```

`genProject` loads the config, loads the project with `Selection` carrying `source_roots`, `exclude`, and `HonorIgnoreFile: true`, loads the format config from `format.LoadConfig` so the region is canonicalised with the project's own style, builds the `Generator`, and returns `(snapshot, Plan, error)`. `runGenWrite` calls `Apply`, prints every written path on stdout *before* returning on error, and exits 1 only when `plan` holds a diagnostic that is not `generate.stale`.

- [ ] **Step 4: Wire the dispatch and usage**

In `cmd/gdkit/main.go`, add to the switch:

```go
	case "gen":
		return runGen(args[1:], stdout, stderr)
```

And to `usageText`, in both blocks:

```
  gdkit gen check    [flags] [project-root]
  gdkit gen write    [flags] [project-root]
  gdkit gen init     [flags] [project-root]
```
```
  gen check      report classes whose generated methods are missing or stale
  gen write      write the generated methods into the classes that opted in
  gen init       write the default .gdkit/generate.json configuration
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./cmd/gdkit`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add cmd/gdkit/generate.go cmd/gdkit/generate_test.go cmd/gdkit/main.go
git commit -m "Wire the gdkit gen command"
```

---

### Task 13: Corpus invariants and documentation

**Goal:** The four invariants hold over every fixture, and the repo's own docs describe the new tool.

**Files:**
- Create: `generate/invariants_test.go`
- Modify: `README.md`, `CLAUDE.md`

**Acceptance Criteria:**
- [ ] `gen write` is idempotent over every fixture
- [ ] The format oracle reports no change on any candidate whose source was format-clean
- [ ] `gen write` then `gen check` is clean
- [ ] Removing a region and regenerating returns the same bytes
- [ ] `README.md` documents `gen check`/`write`/`init`, every rule name, and the marker forms
- [ ] `CLAUDE.md` describes `generate/` and `project.Selection` in the package list

**Verify:** `gofmt -l . && go vet ./... && go build ./... && go test -race ./...` → clean

**Steps:**

- [ ] **Step 1: Write the invariant test**

```go
// fixtures is every source the invariants run over. Each must opt in, so that
// a candidate is actually produced.
var fixtures = map[string]map[string]string{
	"to_string only": {
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\nvar r: int\n",
	},
	"equals only": {
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
	},
	"both": {
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string, equals\nvar q: int\nvar r: int\n",
	},
	"no fields": {
		"a.gd": "class_name Marker\nextends RefCounted\n\n# gdkit:generate = to_string, equals\n",
	},
	"many fields wrapping past line_width": {
		"a.gd": "class_name Wide\nextends RefCounted\n\n# gdkit:generate = equals\n" +
			"var alpha: int\nvar bravo: int\nvar charlie: int\nvar delta: int\nvar echo: int\n" +
			"var foxtrot: int\nvar golf: int\nvar hotel: int\nvar india: int\nvar juliet: int\n",
	},
	"composing subclass": {
		"base.gd": "class_name Base\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n\n# gdkit:generate = equals\nvar r: int\n",
	},
	"no class_name": {
		"a.gd": "extends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n",
	},
	"field named like the parameter": {
		"a.gd": "class_name Odd\nextends RefCounted\n\n# gdkit:generate = to_string, equals\nvar p_other: int\n",
	},
	"region already present and current": {
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n",
	},
}

func TestWriteIsIdempotent(t *testing.T) {
	for name, files := range fixtures {
		t.Run(name, func(t *testing.T) {
			once := applyOnce(t, DefaultConfig(), files)
			twice := applyOnce(t, DefaultConfig(), once)
			if !reflect.DeepEqual(once, twice) {
				t.Errorf("a second write changed the files:\nfirst:\n%v\nsecond:\n%v", once, twice)
			}
		})
	}
}

func TestWriteThenCheckIsClean(t *testing.T) {
	for name, files := range fixtures {
		t.Run(name, func(t *testing.T) {
			written := applyOnce(t, DefaultConfig(), files)
			report, _ := checkProject(t, DefaultConfig(), written)
			if report.HasChanges() || report.HasDiagnostics() {
				t.Errorf("check after write: %+v %+v", report.Results, report.Diagnostics)
			}
		})
	}
}

// The oracle as a corpus test, on top of its per-candidate use in verify: a
// generated file must be canonical whenever its source was.
func TestGeneratedFilesAreFormatClean(t *testing.T) {
	for name, files := range fixtures {
		t.Run(name, func(t *testing.T) {
			written := applyOnce(t, DefaultConfig(), files)
			formatConfig := format.DefaultConfig()
			for path, contents := range written {
				report, _ := formatProjectFor(t, formatConfig, map[string]string{path: contents})
				if report.HasChanges() {
					t.Errorf("%s is not formatted after generation:\n%s", path, contents)
				}
			}
		})
	}
}

func TestRemovingTheRegionRegeneratesTheSameBytes(t *testing.T) {
	for name, files := range fixtures {
		t.Run(name, func(t *testing.T) {
			written := applyOnce(t, DefaultConfig(), files)
			stripped := map[string]string{}
			for path, contents := range written {
				span, found, err := FindRegion([]byte(contents))
				if err != nil {
					t.Fatal(err)
				}
				if !found {
					stripped[path] = contents
					continue
				}
				stripped[path] = contents[:span.Start] + contents[span.End:]
			}
			again := applyOnce(t, DefaultConfig(), stripped)
			if !reflect.DeepEqual(written, again) {
				t.Errorf("regeneration differed:\nbefore:\n%v\nafter:\n%v", written, again)
			}
		})
	}
}
```

`formatProjectFor` is a thin wrapper that builds a `format.Formatter` over a one-file temp project and returns its report; write it in `testing_test.go` beside the other helpers, since `format`'s own helper is unexported.

- [ ] **Step 2: Run the invariants**

Run: `go test -race ./generate -run "TestWriteIsIdempotent|TestWriteThenCheckIsClean|TestGeneratedFilesAreFormatClean|TestRemovingTheRegion"`
Expected: PASS. A failure here is a real defect in the region extent rules — fix the extent, not the fixture.

- [ ] **Step 3: Document in `README.md`**

Add a `gdkit gen` section alongside the other four tools, covering: the three marker forms and their precedence; that the class-level and field-level opt-outs are spelled differently; the `.gdkit/generate.json` shape with union semantics; every rule name in a table (`source-parse`, `class_name.duplicate`, `generate.stale`, `generate.marker`, `generate.conflict`, `generate.unsupported`, `generate.orphaned`, `generate.unsafe`); that `gen write` should be run on a clean tree; that an orphaned region is retained until `--prune`; and that `deep_equals` is planned separately.

- [ ] **Step 4: Document in `CLAUDE.md`**

Change "Twelve packages" to "Thirteen packages" and add:

```markdown
- `generate/` — the code generator behind `gdkit gen`: writes `_to_string` and
  `equals` into a class that opted in, inside a sentinel-delimited region it
  owns. Unlike `lint`, it is a whole-project analysis, because `equals`
  composes with an ancestor's implementation and is refused when a descendant
  would inherit an unsound one, so both answers need the entire inheritance
  graph. `Check` is pure and `Apply` is the only writer. Its region extent
  rules reproduce gdparser's `blankLineGaps` deliberately — the region owns the
  blank lines before it and not after, because the formatter attributes the
  leading gap to the comment run that documents a declaration — and
  `verifyFormatOracle` checks that rather than trusting it. `generate.stale`,
  `generate.marker`, `generate.conflict`, `generate.unsupported`,
  `generate.orphaned`, and `generate.unsafe` are its public diagnostic names.
  `deep_equals` is specified but deliberately not implemented; see
  `docs/superpowers/specs/2026-10-04-gdscript-deep-equals-design.md`.
```

Add to the `project/` entry:

```markdown
  `Config.Selection` is how `generate` acts on a filtered subset while indexing
  the whole project; a nil Selection, which the other four tools pass, selects
  everything.
```

Add a `### Capability resolution` subsection recording the two things an earlier
design got wrong, so they are not reintroduced: that `provider` stops at the
nearest declaration **by name** because GDScript's runtime lookup does not walk
past an incompatible override, and that resolution must iterate because the
descendant refusal rule makes capability flow both up and down the hierarchy.

- [ ] **Step 5: Run the full verification**

```bash
gofmt -l .
go vet ./...
go build ./...
go test -race ./...
```
Expected: `gofmt -l .` prints nothing, the rest clean.

- [ ] **Step 6: Commit**

```bash
git add generate/invariants_test.go generate/testing_test.go README.md CLAUDE.md
git commit -m "Pin the generate invariants and document gdkit gen"
```

---

## Self-review notes

**Spec coverage.** Every section of the v1 spec maps to a task: marker → 2, region extent and orphans → 3 and 11, universe/selection → 0, pipeline order → 10, capabilities (truth table, fixed point, SCCs) → 6, composition and the two refusal rules → 6 and 8, parse-failure scope exception → 10, formatting and the oracle → 10, verification → 10, plan model and report → 1 and 10, package shape → all, registry → 7, field selection → 4, identifiers → 7 and 8, `_to_string` → 7, `equals` → 8, diagnostics → 1 with each rule raised in 10, configuration → 9, command surface → 12, testing → every task plus 13.

**Known soft spots to resolve while implementing, not to paper over:**

1. **AST node names in Task 5.** `ast.ClassNameDeclaration` and `ast.ExtendsDeclaration` are inferred, not verified. Grep `gdparser@v0.1.5/ast/statement.go` first; `class_name` and `extends` may be fields on `ast.File`.
2. **`format.Config.Options()` in Task 10.** `format/config.go` has `options()` unexported. Export it, or add `format.Config.Options()` as a thin exported wrapper — do not duplicate the mapping from config values to `gdformat.Options`.
3. **`checkUnknownKeys` and `replaceFile` are being lifted** into shared homes (`internal/configcheck`, `internal/atomicwrite`) in Tasks 9 and 11. Lift rather than copy; a second copy of either will drift.
4. **`project.Script` accessors in Task 4.** `Line(n)` and a line-for-offset lookup may not exist. Check before adding.
