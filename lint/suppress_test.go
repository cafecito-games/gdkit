package lint

import "testing"

// Each expectation in this file was observed from gdlint itself on the same
// source, with function-name as the probe rule.

func TestIgnoreAppliesToItsOwnLineAndTheNext(t *testing.T) {
	assertNoRule(t, "function-name", "func BadName(): # gdlint:ignore = function-name\n\tpass\n")
	assertNoRule(t, "function-name", "# gdlint:ignore = function-name\nfunc BadName():\n\tpass\n")
}

func TestIgnoreDoesNotReachTwoLinesDownOrUpward(t *testing.T) {
	assertRule(t, "function-name", "# gdlint:ignore = function-name\n\nfunc BadName():\n\tpass\n", 3)
	assertRule(t, "function-name", "func BadName():\n\tpass # gdlint:ignore = function-name\n", 1)
}

func TestIgnoreOnlyCoversTheLinesItReaches(t *testing.T) {
	source := "func BadName(): # gdlint:ignore = function-name\n\tpass\nfunc BadTwo():\n\tpass\n"
	assertRule(t, "function-name", source, 3)
}

func TestIgnoreListsSeveralRulesWithTolerantSpacing(t *testing.T) {
	assertNoRule(t, "function-name", "# gdlint:ignore = function-name,class-name\nfunc BadName():\n\tpass\n")
	assertNoRule(t, "function-name", "# gdlint:ignore = class-name , function-name\nfunc BadName():\n\tpass\n")
	assertNoRule(t, "function-name", "#gdlint : ignore=function-name\nfunc BadName():\n\tpass\n")
	assertNoRule(t, "function-name", "#   gdlint:  ignore   =   function-name\nfunc BadName():\n\tpass\n")
}

func TestIgnoreForAnotherRuleSuppressesNothingElse(t *testing.T) {
	assertRule(t, "function-name", "# gdlint:ignore = class-name\nfunc BadName():\n\tpass\n", 2)
}

func TestIgnoreKeywordIsCaseSensitive(t *testing.T) {
	assertRule(t, "function-name", "# GDLINT:ignore = function-name\nfunc BadName():\n\tpass\n", 2)
}

func TestIgnoreListRunsToEndOfLine(t *testing.T) {
	assertRule(t, "function-name", "func BadName(): # gdlint:ignore = function-name # because\n\tpass\n", 1)
}

// gdlint finds directives by searching raw lines, so one inside a string
// literal is read too. The closing quote then becomes part of the rule name.
func TestIgnoreInsideStringLiteralIsReadButNamesAMangledRule(t *testing.T) {
	source := "var s = \"# gdlint:ignore = function-name\"\nfunc BadName():\n\tpass\n"
	assertRule(t, "function-name", source, 2)
	assertRule(t, "unknown-ignore", source, 1)
}

func TestGdkitSpellingIsAccepted(t *testing.T) {
	assertNoRule(t, "function-name", "# gdkit:ignore = function-name\nfunc BadName():\n\tpass\n")
	assertNoRule(t, "function-name", "# gdkit:disable = function-name\nfunc BadName():\n\tpass\n")
}

func TestIgnoreMatchesDiagnosticStartLine(t *testing.T) {
	source := "func BadName(a,\n\t\tb): # gdlint:ignore = function-name\n\tpass\n"
	assertRule(t, "function-name", source, 1)
}

func TestDisableOnOwnLineStartsAtThatLineAndRunsToFileEnd(t *testing.T) {
	source := "# gdlint:disable = function-name\nfunc BadName():\n\tpass\n\nfunc BadTwo():\n\tpass\n"
	assertNoRule(t, "function-name", source)
}

func TestDisableAfterCodeStartsOnTheNextLine(t *testing.T) {
	source := "func BadName(): # gdlint:disable = function-name\n\tpass\n\nfunc BadTwo():\n\tpass\n"
	assertRule(t, "function-name", source, 1)
}

func TestEnableEndsDisable(t *testing.T) {
	source := "# gdlint:disable = function-name\nfunc BadName():\n\tpass\n# gdlint:enable = function-name\nfunc BadTwo():\n\tpass\n"
	assertRule(t, "function-name", source, 5)
}

func TestDisableInsideABlock(t *testing.T) {
	source := "class A:\n\t# gdlint:disable = function-name\n\tfunc BadIn():\n\t\tpass\n\t# gdlint:enable = function-name\n\n\tfunc BadAfter():\n\t\tpass\n\nfunc BadOutside():\n\tpass\n"
	assertRule(t, "function-name", source, 7, 10)
}

func TestEnableForAnotherRuleDoesNotEndDisable(t *testing.T) {
	source := "# gdlint:disable = function-name\n# gdlint:enable = class-name\nfunc BadName():\n\tpass\n"
	assertNoRule(t, "function-name", source)
}

func TestEarliestEnableClipsEveryDisableOfTheRule(t *testing.T) {
	source := "# gdlint:disable = function-name\nfunc BadA():\n\tpass\n# gdlint:enable = function-name\nfunc BadB():\n\tpass\n# gdlint:disable = function-name\nfunc BadC():\n\tpass\n"
	assertRule(t, "function-name", source, 5, 8)
}

func TestEnableBeforeDisableLeavesNoActiveRange(t *testing.T) {
	source := "# gdlint:enable = function-name\n# gdlint:disable = function-name\nfunc BadName():\n\tpass\n"
	assertRule(t, "function-name", source, 3)
}

func TestUnknownRuleInIgnoreIsReportedAndSuppressesNothing(t *testing.T) {
	source := "# gdlint:ignore = nonsense\nfunc BadName():\n\tpass\n"
	assertRule(t, "unknown-ignore", source, 1)
	assertRule(t, "function-name", source, 2)
}

func TestUnknownRuleIsReportedForDisableAndEnable(t *testing.T) {
	source := "# gdlint:disable = nonsense\n# gdlint:enable = other\nfunc good():\n\tpass\n"
	assertRule(t, "unknown-ignore", source, 1, 2)
}

func TestUnknownRuleDoesNotSpoilKnownRulesInTheSameList(t *testing.T) {
	source := "# gdlint:ignore = nonsense, function-name\nfunc BadName():\n\tpass\n"
	assertRule(t, "unknown-ignore", source, 1)
	assertNoRule(t, "function-name", source)
}

func TestUnknownIgnoreReportsPositionOfTheComment(t *testing.T) {
	found := lintSource(t, "unknown-ignore", "func good(): # gdlint:ignore = nonsense\n\tpass\n")
	if len(found) != 1 || found[0].Line != 1 || found[0].Column != 14 {
		t.Fatalf("got %+v, want one finding at 1:14", found)
	}
	if found[0].Severity != SeverityError {
		t.Errorf("Severity = %q, want error", found[0].Severity)
	}
}

func TestEmptyRuleNameIsUnknown(t *testing.T) {
	assertRule(t, "unknown-ignore", "# gdlint:ignore = ,function-name\nfunc good():\n\tpass\n", 1)
}

func TestUnknownIgnoreCanBeConfigured(t *testing.T) {
	source := "# gdlint:ignore = nonsense\nfunc good():\n\tpass\n"

	disabled := DefaultConfig()
	disabled.Disable = []string{"unknown-ignore"}
	assertRuleWithConfig(t, disabled, "unknown-ignore", source)

	lenient := DefaultConfig()
	lenient.Severity = map[string]Severity{"unknown-ignore": SeverityWarning}
	found := lintSourceWithConfig(t, lenient, "unknown-ignore", source)
	if len(found) != 1 || found[0].Severity != SeverityWarning {
		t.Errorf("got %+v, want one warning", found)
	}
}

func TestUnknownIgnoreCanItselfBeIgnored(t *testing.T) {
	assertNoRule(t, "unknown-ignore", "# gdlint:ignore = nonsense, unknown-ignore\nfunc good():\n\tpass\n")
}

func TestSuppressionCoversEveryRule(t *testing.T) {
	source := "# gdlint:disable = max-line-length, trailing-whitespace, function-name\nfunc BadName():   \n\tpass # " +
		"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n"
	for _, rule := range []string{"max-line-length", "trailing-whitespace", "function-name"} {
		assertNoRule(t, rule, source)
	}
}

func TestParseFailureCannotBeIgnored(t *testing.T) {
	source := "# gdlint:ignore = source-parse\nfunc (\n"
	report := lintProject(t, DefaultConfig(), map[string]string{"a.gd": source})
	if len(report.Diagnostics) != 1 || report.Diagnostics[0].Rule != "source-parse" {
		t.Errorf("got %+v, want only source-parse", report.Diagnostics)
	}
}

func TestReservedRulesAreNameable(t *testing.T) {
	for _, name := range []string{"source-parse", "unknown-ignore"} {
		if !IsRule(name) {
			t.Errorf("IsRule(%q) = false", name)
		}
	}
	found := map[string]bool{}
	for _, name := range RuleNames() {
		found[name] = true
	}
	if !found["unknown-ignore"] || !found["source-parse"] {
		t.Errorf("RuleNames() = %v", RuleNames())
	}
}
