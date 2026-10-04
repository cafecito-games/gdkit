package generate

import (
	"slices"
	"testing"
)

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
	got := []string{report.Diagnostics[0].Rule, report.Diagnostics[1].Rule, report.Diagnostics[2].Rule}
	want := []string{ruleConflict, ruleStale, ruleMarker}
	if !slices.Equal(got, want) {
		t.Errorf("diagnostics = %v, want %v", got, want)
	}
}

func TestReportPredicates(t *testing.T) {
	empty := Report{}
	if empty.HasChanges() || empty.HasDiagnostics() {
		t.Error("an empty report reports changes or diagnostics")
	}
	if !(Report{Results: []Result{{Path: "a.gd", Changed: true}}}).HasChanges() {
		t.Error("HasChanges missed a changed result")
	}
	if !(Report{Diagnostics: []Diagnostic{{Rule: ruleStale}}}).HasDiagnostics() {
		t.Error("HasDiagnostics missed a diagnostic")
	}
}
