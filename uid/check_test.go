package uid

import (
	"reflect"
	"testing"
)

func TestCheckPassesAProjectWhereEveryScriptHasASidecar(t *testing.T) {
	report := checkProject(t, map[string]string{
		"player.gd":          "extends Node\n",
		"player.gd.uid":      "uid://b\n",
		"world/enemy.gd":     "extends Node\n",
		"world/enemy.gd.uid": "uid://c\n",
	})
	if report.HasDiagnostics() {
		t.Fatalf("diagnostics = %v, want none", report.Diagnostics)
	}
	if report.Scripts != 2 {
		t.Errorf("Scripts = %d, want 2", report.Scripts)
	}
}

func TestCheckReportsAScriptWithNoSidecar(t *testing.T) {
	report := checkProject(t, map[string]string{
		"player.gd":          "extends Node\n",
		"world/enemy.gd":     "extends Node\n",
		"world/enemy.gd.uid": "uid://c\n",
	})
	if got := rules(report); !reflect.DeepEqual(got, []string{RuleMissing}) {
		t.Fatalf("rules = %v, want one %s", got, RuleMissing)
	}
	diagnostic := report.Diagnostics[0]
	if diagnostic.Path != "player.gd" {
		t.Errorf("Path = %q, want player.gd", diagnostic.Path)
	}
	if diagnostic.UID != "" {
		t.Errorf("UID = %q, want empty", diagnostic.UID)
	}
}

func TestCheckReportsMalformedSidecars(t *testing.T) {
	report := checkProject(t, map[string]string{
		"blank.gd":        "extends Node\n",
		"blank.gd.uid":    "\n",
		"path.gd":         "extends Node\n",
		"path.gd.uid":     "res://path.gd\n",
		"zed.gd":          "extends Node\n",
		"zed.gd.uid":      "uid://az\n",
		"overflow.gd":     "extends Node\n",
		"overflow.gd.uid": "uid://d4n4ub6itg401\n",
	})
	want := []string{"blank.gd", "overflow.gd", "path.gd", "zed.gd"}
	if got := paths(report); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule != RuleMalformed {
			t.Errorf("%s: rule = %s, want %s", diagnostic.Path, diagnostic.Rule, RuleMalformed)
		}
	}
}

func TestCheckReportsDuplicatesAgainstTheFirstClaimant(t *testing.T) {
	report := checkProject(t, map[string]string{
		"a.gd":     "extends Node\n",
		"a.gd.uid": "uid://bb\n",
		"b.gd":     "extends Node\n",
		"b.gd.uid": "uid://bb\n",
		"c.gd":     "extends Node\n",
		"c.gd.uid": "uid://bb\n",
	})
	// a.gd sorts first, so it keeps the identifier and the other two are
	// reported against it.
	want := []string{"b.gd", "c.gd"}
	if got := paths(report); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule != RuleDuplicate {
			t.Errorf("%s: rule = %s, want %s", diagnostic.Path, diagnostic.Rule, RuleDuplicate)
		}
		if diagnostic.UID != "uid://bb" {
			t.Errorf("%s: UID = %q, want uid://bb", diagnostic.Path, diagnostic.UID)
		}
	}
}

// Padding makes two different texts name one id, which is still a duplicate.
func TestCheckReportsDuplicatesThroughPaddedText(t *testing.T) {
	report := checkProject(t, map[string]string{
		"a.gd":     "extends Node\n",
		"a.gd.uid": "uid://b\n",
		"b.gd":     "extends Node\n",
		"b.gd.uid": "uid://aab\n",
	})
	if got := rules(report); !reflect.DeepEqual(got, []string{RuleDuplicate}) {
		t.Fatalf("rules = %v, want one %s", got, RuleDuplicate)
	}
}

// A sidecar carrying no identifier cannot duplicate anything, so it is only
// ever malformed.
func TestCheckDoesNotCallMalformedSidecarsDuplicates(t *testing.T) {
	report := checkProject(t, map[string]string{
		"a.gd":     "extends Node\n",
		"a.gd.uid": "nonsense\n",
		"b.gd":     "extends Node\n",
		"b.gd.uid": "nonsense\n",
	})
	if got := rules(report); !reflect.DeepEqual(got, []string{RuleMalformed, RuleMalformed}) {
		t.Fatalf("rules = %v, want two %s", got, RuleMalformed)
	}
}

func TestCheckIgnoresSidecarsBesideFilesThatAreNotScripts(t *testing.T) {
	report := checkProject(t, map[string]string{
		"player.gd":           "extends Node\n",
		"player.gd.uid":       "uid://b\n",
		"water.gdshader.uid":  "uid://c\n",
		"broken.gdshader.uid": "nonsense\n",
	})
	if report.HasDiagnostics() {
		t.Fatalf("diagnostics = %v, want none", report.Diagnostics)
	}
}

func TestCheckSkipsScriptsHiddenByTheIgnoreFile(t *testing.T) {
	report := checkProject(t, map[string]string{
		".gdkitignore":     "addons/\n",
		"player.gd":        "extends Node\n",
		"player.gd.uid":    "uid://b\n",
		"addons/vendor.gd": "extends Node\n",
	})
	if report.HasDiagnostics() {
		t.Fatalf("diagnostics = %v, want none", report.Diagnostics)
	}
	if report.Scripts != 1 {
		t.Errorf("Scripts = %d, want 1", report.Scripts)
	}
}

// A script that does not parse still needs an identity; nothing here reads the
// syntax tree.
func TestCheckReportsAScriptThatDoesNotParse(t *testing.T) {
	report := checkProject(t, map[string]string{"broken.gd": "func (((\n"})
	if got := rules(report); !reflect.DeepEqual(got, []string{RuleMissing}) {
		t.Fatalf("rules = %v, want one %s", got, RuleMissing)
	}
}

func TestReportPartitionsItsDiagnostics(t *testing.T) {
	report := checkProject(t, map[string]string{
		"a.gd":     "extends Node\n",
		"a.gd.uid": "uid://bb\n",
		"b.gd":     "extends Node\n",
		"b.gd.uid": "uid://bb\n",
		"c.gd":     "extends Node\n",
		"c.gd.uid": "nonsense\n",
		"d.gd":     "extends Node\n",
	})
	if got := len(report.Missing()); got != 1 {
		t.Errorf("Missing() has %d diagnostics, want 1", got)
	}
	if got := len(report.Repairs()); got != 2 {
		t.Errorf("Repairs() has %d diagnostics, want 2", got)
	}
}
