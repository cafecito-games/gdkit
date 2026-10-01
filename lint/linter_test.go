package lint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/project"
)

func TestTrailingWhitespaceIsReportedAtTheFirstOffendingColumn(t *testing.T) {
	assertRule(t, "trailing-whitespace", "var a := 1   \nvar b := 2\nvar c := 3\t\n", 1, 3)

	found := lintSource(t, "trailing-whitespace", "var a := 1   \n")
	if found[0].Column != 11 {
		t.Errorf("Column = %d, want 11", found[0].Column)
	}
	if found[0].Path != "a.gd" {
		t.Errorf("Path = %q, want a.gd", found[0].Path)
	}
}

func TestDiagnosticStringUsesGdlintShape(t *testing.T) {
	diagnostic := Diagnostic{Rule: "function-name", Severity: SeverityError, Message: `Function name "doThing" is not valid`, Path: "player.gd", Line: 12, Column: 6}
	want := `player.gd:12: Error: Function name "doThing" is not valid (function-name)`
	if got := diagnostic.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestDisabledRuleProducesNothing(t *testing.T) {
	config := DefaultConfig()
	config.Disable = []string{"trailing-whitespace"}
	report := lintProject(t, config, map[string]string{"a.gd": "var a := 1   \n"})
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == "trailing-whitespace" {
			t.Fatal("a disabled rule must not fire")
		}
	}
}

func TestUnparseableFileReportsOnlySourceParse(t *testing.T) {
	report := lintProject(t, DefaultConfig(), map[string]string{"a.gd": "func (   \n"})
	if len(report.Diagnostics) != 1 {
		t.Fatalf("want exactly one diagnostic, got %v", report.Diagnostics)
	}
	if report.Diagnostics[0].Rule != "source-parse" {
		t.Errorf("Rule = %q, want source-parse", report.Diagnostics[0].Rule)
	}
}

func TestReportSortsByPathLineColumnRule(t *testing.T) {
	report := lintProject(t, DefaultConfig(), map[string]string{
		"b.gd": "var x := 1   \n",
		"a.gd": "var y := 1   \nvar z := 2   \n",
	})
	var order []string
	for _, diagnostic := range report.Diagnostics {
		order = append(order, diagnostic.Path)
	}
	if len(order) < 3 || order[0] != "a.gd" || order[1] != "a.gd" || order[2] != "b.gd" {
		t.Errorf("unsorted: %v", order)
	}
}

func TestNewRejectsAnUncompilableNamePattern(t *testing.T) {
	config := DefaultConfig()
	config.FunctionName = "([a-z"
	if _, err := New(config); err == nil {
		t.Error("New must reject an invalid regex")
	}
}

// fakeRule reports a fixed diagnostic set, deliberately pre-filled with wrong
// stamped fields so tests can prove the driver overwrites them.
type fakeRule struct {
	name        string
	diagnostics []Diagnostic
}

func (r fakeRule) Name() string                                 { return r.name }
func (r fakeRule) Check(*Context, *project.Script) []Diagnostic { return r.diagnostics }

// lintWithRules runs rules over one a.gd file through the driver alone. Rule
// names must be real ones because configuration validates against the registry.
func lintWithRules(t *testing.T, config Config, rules ...Rule) Report {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.gd"), []byte("var a := 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := project.Load(project.Config{Root: root, SourceRoots: config.SourceRoots, Exclude: config.Exclude})
	if err != nil {
		t.Fatal(err)
	}
	linter, err := newLinter(config, rules)
	if err != nil {
		t.Fatal(err)
	}
	return linter.Lint(snapshot)
}

func TestDriverStampsRulePathAndSeverity(t *testing.T) {
	rule := fakeRule{name: "max-line-length", diagnostics: []Diagnostic{{
		Rule: "wrong", Path: "wrong.gd", Severity: SeverityWarning, Message: "m", Line: 1, Column: 1,
	}}}
	report := lintWithRules(t, DefaultConfig(), rule)
	if len(report.Diagnostics) != 1 {
		t.Fatalf("got %v", report.Diagnostics)
	}
	got := report.Diagnostics[0]
	if got.Rule != "max-line-length" || got.Path != "a.gd" || got.Severity != SeverityError {
		t.Errorf("not stamped: %+v", got)
	}
}

func TestDriverAppliesSeverityOverride(t *testing.T) {
	config := DefaultConfig()
	config.Severity = map[string]Severity{"max-line-length": SeverityWarning}
	rule := fakeRule{name: "max-line-length", diagnostics: []Diagnostic{{Message: "m", Line: 1, Column: 1}}}
	report := lintWithRules(t, config, rule)
	if len(report.Diagnostics) != 1 || report.Diagnostics[0].Severity != SeverityWarning {
		t.Errorf("got %+v", report.Diagnostics)
	}
}

func TestDriverSortsSameLineDiagnosticsByRuleName(t *testing.T) {
	diagnostics := []Diagnostic{{Message: "m", Line: 1, Column: 1}}
	report := lintWithRules(t, DefaultConfig(),
		fakeRule{name: "mixed-tabs-and-spaces", diagnostics: diagnostics},
		fakeRule{name: "max-line-length", diagnostics: diagnostics},
	)
	if len(report.Diagnostics) != 2 ||
		report.Diagnostics[0].Rule != "max-line-length" ||
		report.Diagnostics[1].Rule != "mixed-tabs-and-spaces" {
		t.Errorf("unsorted: %+v", report.Diagnostics)
	}
}

func TestDriverSkipsDisabledRules(t *testing.T) {
	config := DefaultConfig()
	config.Disable = []string{"max-line-length"}
	rule := fakeRule{name: "max-line-length", diagnostics: []Diagnostic{{Message: "m", Line: 1, Column: 1}}}
	if report := lintWithRules(t, config, rule); len(report.Diagnostics) != 0 {
		t.Errorf("got %+v", report.Diagnostics)
	}
}

func TestEmptyReportMarshalsAsAnEmptyArray(t *testing.T) {
	report := lintWithRules(t, DefaultConfig())
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"diagnostics":[]`) {
		t.Errorf("got %s", data)
	}
}

func TestRegisterRejectsReservedNames(t *testing.T) {
	for _, name := range []string{"source-parse", "unknown-ignore"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("register(%q) must panic", name)
				}
			}()
			register(fakeRule{name: name})
		}()
	}
}

func writeConfigFile(t *testing.T, root, name, contents string) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfigRejectsAnInvalidNamePattern(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, DefaultConfigPath, `{"function-name": "([a-z"}`)
	if _, err := LoadConfig(root, ""); err == nil || !strings.Contains(err.Error(), "function-name") {
		t.Errorf("want an error naming function-name, got %v", err)
	}
}

func TestValidateRejectsAnEmptyNamePattern(t *testing.T) {
	config := DefaultConfig()
	config.SignalName = ""
	if err := config.Validate(); err == nil {
		t.Error("an empty pattern must be rejected")
	}
}

func TestValidateRejectsNegativeLimits(t *testing.T) {
	mutations := map[string]func(*Config){
		"max-returns":               func(c *Config) { c.MaxReturns = -1 },
		"max-public-methods":        func(c *Config) { c.MaxPublicMethods = -1 },
		"function-arguments-number": func(c *Config) { c.FunctionArgumentsNumber = -1 },
		"max-file-lines":            func(c *Config) { c.MaxFileLines = -1 },
		"max-line-length":           func(c *Config) { c.MaxLineLength = -1 },
		"tab-characters":            func(c *Config) { c.TabCharacters = -1 },
	}
	for name, mutate := range mutations {
		config := DefaultConfig()
		mutate(&config)
		if err := config.Validate(); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: want an error naming it, got %v", name, err)
		}
	}
}

func TestLoadConfigFallsBackToDefaultsOnlyWhenNoNameIsGiven(t *testing.T) {
	root := t.TempDir()
	config, err := LoadConfig(root, "")
	if err != nil {
		t.Fatalf("implicit default path must fall back: %v", err)
	}
	if config.MaxLineLength != DefaultConfig().MaxLineLength {
		t.Error("expected defaults")
	}
	if _, err := LoadConfig(root, DefaultConfigPath); err == nil {
		t.Error("an explicitly named missing config must be an error")
	}
	if _, err := LoadConfig(root, "other.json"); err == nil {
		t.Error("an explicitly named missing config must be an error")
	}
}

func TestLoadConfigRejectsUnknownKeys(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, DefaultConfigPath, `{"max-line-lenght": 80}`)
	_, err := LoadConfig(root, "")
	if err == nil || !strings.Contains(err.Error(), "max-line-lenght") {
		t.Errorf("want an error naming the key, got %v", err)
	}
}

func TestLintSourceFailsOnUnparseableFixture(t *testing.T) {
	if got := lintSource(t, "source-parse", "func (   \n"); len(got) != 1 {
		t.Errorf("asking for source-parse must still work, got %v", got)
	}
}
