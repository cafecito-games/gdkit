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
	// Four long lines, none of which has a break point to use: a documentation
	// URL, a deep res:// path, a pair of generated class names whose call
	// brackets nothing, and an expression that brackets nothing either. The
	// fixture produces no diagnostics at all.
	"irreducible_line.gd": {},
	// Neither a setter parameter nor a match bind has a naming rule, so the
	// badly named ones in this fixture stay silent.
	"modern_syntax.gd": {
		// "else" after "return" inside a property getter.
		{31, "no-else-return"},
		// "ignored" is not mentioned by the lambda that is the whole body.
		{45, "unused-argument"},
		// A lambda parameter is named like a function argument, and the
		// variable holding the lambda like any local.
		{51, "function-argument-name"},
		{51, "function-variable-name"},
		// "else" after "return" inside a multi-line lambda.
		{60, "no-else-return"},
		// Inside a one-line lambda body.
		{62, "comparison-with-itself"},
		// pass beside another statement in a lambda body.
		{65, "unnecessary-pass"},
		// A lone match bind evaluated for nothing.
		{89, "expression-not-assigned"},
		// A pattern guard leads its own expression.
		{90, "comparison-with-itself"},
		// A Lua-style dictionary as a statement. On the line above, the key
		// "height" counts as a use of the argument, so that one is silent.
		{96, "expression-not-assigned"},
		// The rest parameter is never referenced.
		{100, "unused-argument"},
		// A member access through a keyword-shaped name is still unused.
		{110, "expression-not-assigned"},
		// A typed-array local is named like any other local.
		{116, "function-variable-name"},
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

// docstringFixtureExpectations is the complete set of diagnostics the files in
// testdata/docstrings must produce with every missing-docstring member kind
// enabled. The rule reports nothing under the default configuration, so it
// needs a fixture group of its own.
var docstringFixtureExpectations = map[string][]fixtureFinding{
	"documented.gd": {},
	"missing.gd": {
		// Reported at the class_name directive, not at the top of the file.
		{1, "missing-docstring"},
		{4, "missing-docstring"},
		{6, "missing-docstring"},
		{8, "missing-docstring"},
		// "count" only; a leading underscore makes a member private.
		{10, "missing-docstring"},
		{13, "missing-docstring"},
		// A static function is public API even though no design limit counts it.
		{19, "missing-docstring"},
		// The inner class, then its own members.
		{22, "missing-docstring"},
		{23, "missing-docstring"},
		{25, "missing-docstring"},
	},
}

// loggingFixtureExpectations is the complete set of diagnostics the files in
// testdata/logging must produce once no-engine-logging is enabled. The rule
// ships inert, so it needs a fixture group of its own.
var loggingFixtureExpectations = map[string][]fixtureFinding{
	"logger.gd": {},
	"engine_logging.gd": {
		{10, "no-engine-logging"},
		{11, "no-engine-logging"},
		{12, "no-engine-logging"},
		{13, "no-engine-logging"},
		{14, "no-engine-logging"},
		{15, "no-engine-logging"},
		{16, "no-engine-logging"},
		{17, "no-engine-logging"},
		{18, "no-engine-logging"},
		{19, "no-engine-logging"},
		{20, "no-engine-logging"},
		// Nothing from the second function: a call through an object reaches
		// the project's logger, and the last one is suppressed by comment.
	},
}

// loggingFixtureConfig opts in to the inert rule and leaves its function list
// at the default, so the fixture covers the shipped policy.
func loggingFixtureConfig() Config {
	config := DefaultConfig()
	config.Enable = []string{"no-engine-logging"}
	return config
}

// docstringFixtureConfig enables every member kind, so one fixture covers the
// whole rule.
func docstringFixtureConfig() Config {
	config := DefaultConfig()
	config.MissingDocstring = []string{
		docKindClass, docKindFunc, docKindSignal,
		docKindVar, docKindConst, docKindEnum,
	}
	return config
}

// lintFixtures lints a fixture directory and groups the diagnostics by file.
func lintFixtures(t *testing.T, directory string, config Config) map[string][]fixtureFinding {
	t.Helper()
	linter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := loadCorpus(t, filepath.Join("testdata", directory), config)
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
	checkFixtures(t, lintFixtures(t, "rules", DefaultConfig()), fixtureExpectations)
}

// TestDocstringFixtureDiagnostics is TestFixtureDiagnostics for the fixtures
// that only produce diagnostics once missing-docstring is configured.
func TestDocstringFixtureDiagnostics(t *testing.T) {
	checkFixtures(t, lintFixtures(t, "docstrings", docstringFixtureConfig()), docstringFixtureExpectations)
}

// TestLoggingFixtureDiagnostics is TestFixtureDiagnostics for the fixtures that
// only produce diagnostics once no-engine-logging is enabled.
func TestLoggingFixtureDiagnostics(t *testing.T) {
	checkFixtures(t, lintFixtures(t, "logging", loggingFixtureConfig()), loggingFixtureExpectations)
}

func checkFixtures(t *testing.T, found map[string][]fixtureFinding, expectations map[string][]fixtureFinding) {
	t.Helper()

	for path, want := range expectations {
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
		if _, expected := expectations[path]; !expected {
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
	for _, group := range []map[string][]fixtureFinding{fixtureExpectations, docstringFixtureExpectations, loggingFixtureExpectations} {
		for _, findings := range group {
			for _, finding := range findings {
				exercised[finding.rule] = true
			}
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
