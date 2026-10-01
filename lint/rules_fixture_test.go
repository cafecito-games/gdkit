package lint

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"
)

// fixtureFinding is one expected diagnostic, identified by the position and rule
// a reader can check against the fixture by eye.
type fixtureFinding struct {
	line int
	rule string
}

// fixtureExpectations is the complete set of diagnostics the files in
// testdata/rules must produce under the default configuration. Each fixture
// isolates a group of rules, and between them they exercise every rule the
// linter registers, which TestFixturesExerciseEveryRule enforces.
//
// Expectations are exact: an unexpected diagnostic fails just as a missing one
// does. A rule that changes which line it reports therefore shows up here even
// when its own test still passes.
var fixtureExpectations = map[string][]fixtureFinding{
	"basics.gd": {
		// The second preload of the same path, not the first.
		{4, "duplicated-load"},
		// pass beside other statements in the body.
		{9, "unnecessary-pass"},
		// "second" is never referenced.
		{12, "unused-argument"},
		// Comparisons and arithmetic evaluated for nothing.
		{18, "expression-not-assigned"},
		{19, "expression-not-assigned"},
		{23, "comparison-with-itself"},
	},
	"design.gd": {
		// Reported at the last return, not at the function.
		{17, "max-returns"},
		{20, "function-arguments-number"},
	},
	"if_return.gd": {
		{7, "no-else-return"},
		{14, "no-elif-return"},
	},
	"long_file.gd": {
		// Reported on the final line of the file.
		{1005, "max-file-lines"},
	},
	"long_line.gd": {
		{5, "max-line-length"},
	},
	"names.gd": {
		{1, "class-name"},
		{4, "signal-name"},
		// The enum and its first member are both wrong, on one line.
		{5, "enum-element-name"},
		{5, "enum-name"},
		{6, "constant-name"},
		// A const initialized by preload uses the load-aware rule.
		{7, "load-constant-name"},
		{8, "class-variable-name"},
		{9, "class-load-variable-name"},
		{11, "sub-class-name"},
		// The function and its parameter are both wrong, on one line.
		{15, "function-argument-name"},
		{15, "function-name"},
		{16, "function-variable-name"},
		// A local initialized by preload uses the preload-aware rule.
		{17, "function-preload-variable-name"},
		{18, "loop-variable-name"},
	},
	"order.gd": {
		// A signal and a const after a plain variable.
		{4, "class-definitions-order"},
		{5, "class-definitions-order"},
	},
	"public_methods.gd": {
		// File scope is reported at the start of the file.
		{1, "max-public-methods"},
	},
	"whitespace.gd": {
		{4, "trailing-whitespace"},
		{5, "trailing-whitespace"},
		// Fires per offending line, not once per file.
		{10, "mixed-tabs-and-spaces"},
		{11, "mixed-tabs-and-spaces"},
	},
}

// lintFixtures lints testdata/rules and groups the diagnostics by file.
func lintFixtures(t *testing.T) map[string][]fixtureFinding {
	t.Helper()
	config := DefaultConfig()
	linter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := loadCorpus(t, filepath.Join("testdata", "rules"), config)
	found := make(map[string][]fixtureFinding)
	for _, diagnostic := range linter.Lint(snapshot).Diagnostics {
		found[diagnostic.Path] = append(found[diagnostic.Path], fixtureFinding{diagnostic.Line, diagnostic.Rule})
	}
	for path := range found {
		sortFindings(found[path])
	}
	return found
}

func sortFindings(findings []fixtureFinding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].line != findings[j].line {
			return findings[i].line < findings[j].line
		}
		return findings[i].rule < findings[j].rule
	})
}

func formatFindings(findings []fixtureFinding) string {
	lines := make([]string, 0, len(findings))
	for _, finding := range findings {
		lines = append(lines, fmt.Sprintf("\n\t%4d  %s", finding.line, finding.rule))
	}
	if len(lines) == 0 {
		return "\n\t(none)"
	}
	out := ""
	for _, line := range lines {
		out += line
	}
	return out
}

// TestFixtureDiagnostics pins the exact diagnostics each fixture produces. It
// complements the per-rule tests: those check a rule in isolation, while this
// checks the rules together over whole files, so a rule that starts firing where
// it should not is caught even when its own test still passes.
func TestFixtureDiagnostics(t *testing.T) {
	found := lintFixtures(t)

	for path, want := range fixtureExpectations {
		sortFindings(want)
		got := found[path]
		if len(got) != len(want) {
			t.Errorf("%s produced %d diagnostics, want %d\ngot:%s\nwant:%s",
				path, len(got), len(want), formatFindings(got), formatFindings(want))
			continue
		}
		for index := range want {
			if got[index] != want[index] {
				t.Errorf("%s diagnostics differ\ngot:%s\nwant:%s",
					path, formatFindings(got), formatFindings(want))
				break
			}
		}
	}

	for path := range found {
		if _, expected := fixtureExpectations[path]; !expected {
			t.Errorf("%s produced diagnostics but has no expectations:%s", path, formatFindings(found[path]))
		}
	}
}

// TestFixturesExerciseEveryRule guards against a rule going silently dead. A
// rule whose traversal breaks reports nothing, which its own test can miss if
// the test is wrong in the same way. Every registered rule must fire at least
// once across the fixtures.
//
// The two driver-reported rules are excluded: source-parse needs an unparseable
// file, and unknown-ignore needs a bad suppression comment. Neither belongs in a
// fixture whose diagnostics are otherwise exact.
func TestFixturesExerciseEveryRule(t *testing.T) {
	driverReported := map[string]bool{"source-parse": true, "unknown-ignore": true}

	exercised := make(map[string]bool)
	for _, findings := range fixtureExpectations {
		for _, finding := range findings {
			exercised[finding.rule] = true
		}
	}

	var missing []string
	for _, name := range RuleNames() {
		if !driverReported[name] && !exercised[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("no fixture exercises these rules: %v", missing)
	}

	for rule := range exercised {
		if !IsRule(rule) {
			t.Errorf("fixture expects %q, which is not a registered rule", rule)
		}
	}
}
