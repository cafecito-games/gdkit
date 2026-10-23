package generate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiagnosticStringMatchesTheSharedTextForm(t *testing.T) {
	d := Diagnostic{Rule: ruleStale, Message: "generated region is out of date", Path: "hex.gd", Line: 14}
	want := "hex.gd:14: Error: generated region is out of date (generate.stale)"
	got := d.String()
	assert.Equal(t, want, got)
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
	assert.Equal(t, "a.gd", report.Results[0].Path, "results not sorted by path")
	got := []string{report.Diagnostics[0].Rule, report.Diagnostics[1].Rule, report.Diagnostics[2].Rule}
	want := []string{ruleConflict, ruleStale, ruleMarker}
	assert.Equal(t, want, got)
}

func TestReportPredicates(t *testing.T) {
	empty := Report{}
	assert.False(t, empty.HasChanges(), "an empty report reports changes")
	assert.False(t, empty.HasDiagnostics(), "an empty report reports diagnostics")
	assert.True(t, (Report{Results: []Result{{Path: "a.gd", Changed: true}}}).HasChanges(), "HasChanges missed a changed result")
	assert.True(t, (Report{Diagnostics: []Diagnostic{{Rule: ruleStale}}}).HasDiagnostics(), "HasDiagnostics missed a diagnostic")
}
