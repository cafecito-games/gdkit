package generate

import (
	"strings"
	"testing"
)

// helpersStub is the two lines gen init writes; the region is gen's.
const helpersStub = "class_name GDKitHelpers\nextends RefCounted\n"

func TestHelpersFileGetsARegionWithoutOptingIn(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"gdkit_helpers.gd": helpersStub,
	})
	if !report.HasChanges() {
		t.Fatalf("the helpers file was not given a region: %+v", report)
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule != ruleStale {
			t.Errorf("unexpected diagnostic %+v", diagnostic)
		}
	}
}

func TestHelpersFileIsNeverAnOrphan(t *testing.T) {
	written := applyOnce(t, DefaultConfig(), map[string]string{
		"gdkit_helpers.gd": helpersStub,
	})
	report := checkProject(t, DefaultConfig(), map[string]string{
		"gdkit_helpers.gd": written["gdkit_helpers.gd"],
	})
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == ruleOrphaned {
			t.Errorf("the helpers file was reported as an orphan: %+v", diagnostic)
		}
	}
	if report.HasChanges() {
		t.Errorf("a freshly written helpers file is not stable: %+v", report)
	}

	config := DefaultConfig()
	config.SourceRoots = []string{"elsewhere"}
	unselected := checkProject(t, config, map[string]string{
		"gdkit_helpers.gd": written["gdkit_helpers.gd"],
		"elsewhere/a.gd":   "class_name A\nextends RefCounted\n",
	})
	for _, diagnostic := range unselected.Diagnostics {
		if diagnostic.Rule == ruleOrphaned {
			t.Errorf("an unselected helpers file was reported as an orphan: %+v", diagnostic)
		}
	}
}

func TestHelpersRegionComparesContainersDeeply(t *testing.T) {
	written := applyOnce(t, DefaultConfig(), map[string]string{
		"gdkit_helpers.gd": helpersStub,
	})["gdkit_helpers.gd"]
	for _, want := range []string{
		"static func deep_equals(p_lhs: Variant, p_rhs: Variant) -> bool:",
		"if p_lhs == p_rhs:",
		"if p_lhs is Array and p_rhs is Array:",
		"if p_lhs is Dictionary and p_rhs is Dictionary:",
		"if not p_rhs.has(key):",
		"if p_lhs == null or p_rhs == null:",
		"if p_lhs is Object and p_lhs.has_method(\"deep_equals\"):",
		"if p_lhs is Object and p_lhs.has_method(\"equals\"):",
	} {
		if !strings.Contains(written, want) {
			t.Errorf("generated helpers lack %q:\n%s", want, written)
		}
	}
	isObject := strings.Index(written, "is Object")
	hasMethod := strings.Index(written, "has_method")
	if isObject < 0 || hasMethod < 0 || isObject > hasMethod {
		t.Errorf("is Object must precede the first has_method:\n%s", written)
	}
}

func TestHelpersIsNotAPublicGeneratorName(t *testing.T) {
	if isGeneratorName("helpers") {
		t.Error("helpers must not be a configuration or directive spelling")
	}
	for _, name := range GeneratorNames() {
		if name == "helpers" {
			t.Error("GeneratorNames lists helpers")
		}
	}
}

func TestAHandWrittenStaticHelperIsHonoured(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"gdkit_helpers.gd": helpersStub + "\nstatic func deep_equals(a, b) -> bool:\n\treturn a == b\n",
	})
	if report.HasChanges() {
		t.Errorf("a compatible hand-written helper should suppress emission: %+v", report)
	}
}

func TestAnIncompatibleHandWrittenHelperConflicts(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"gdkit_helpers.gd": helpersStub + "\nfunc deep_equals(a) -> bool:\n\treturn true\n",
	})
	assertDiagnostic(t, report, ruleConflict)
}
