package lint

import "testing"

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
