package lint

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/project"
)

// pendingFakeRule is a fakeRule that ships inert. Its name must be a real rule
// name because configuration validates Enable and Disable against the registry.
type pendingFakeRule struct {
	fakeRule
}

func (r pendingFakeRule) PendingSince() string { return "0.3.0" }

func pendingRule(name string) pendingFakeRule {
	return pendingFakeRule{fakeRule{
		name:        name,
		diagnostics: []Diagnostic{{Message: "pending fired", Line: 1, Column: 1}},
	}}
}

func TestPendingRuleStaysInertByDefault(t *testing.T) {
	report := lintWithRules(t, DefaultConfig(), pendingRule("max-line-length"))
	if len(report.Diagnostics) != 0 {
		t.Fatalf("pending rule ran without being enabled: %v", report.Diagnostics)
	}
}

func TestPendingRuleRunsWhenEnabledByName(t *testing.T) {
	config := DefaultConfig()
	config.Enable = []string{"max-line-length"}
	report := lintWithRules(t, config, pendingRule("max-line-length"))
	if len(report.Diagnostics) != 1 {
		t.Fatalf("enabled pending rule did not run: %v", report.Diagnostics)
	}
}

func TestPendingRuleRunsWhenNewRulesAreEnabled(t *testing.T) {
	config := DefaultConfig()
	config.EnableNewRules = true
	report := lintWithRules(t, config, pendingRule("max-line-length"))
	if len(report.Diagnostics) != 1 {
		t.Fatalf("enable_new_rules did not run the pending rule: %v", report.Diagnostics)
	}
}

// Disable is the stronger statement: a project that lists a rule in both is
// most likely turning off something it opted in to earlier.
func TestDisableWinsOverEnableForAPendingRule(t *testing.T) {
	config := DefaultConfig()
	config.Enable = []string{"max-line-length"}
	config.Disable = []string{"max-line-length"}
	report := lintWithRules(t, config, pendingRule("max-line-length"))
	if len(report.Diagnostics) != 0 {
		t.Fatalf("disable did not win over enable: %v", report.Diagnostics)
	}
}

func TestDisableWinsOverEnableNewRules(t *testing.T) {
	config := DefaultConfig()
	config.EnableNewRules = true
	config.Disable = []string{"max-line-length"}
	report := lintWithRules(t, config, pendingRule("max-line-length"))
	if len(report.Diagnostics) != 0 {
		t.Fatalf("disable did not win over enable_new_rules: %v", report.Diagnostics)
	}
}

// A rule that does not ship inert is unaffected by the mechanism, which is what
// keeps every existing project's verdict unchanged.
func TestNonPendingRuleRunsWithoutBeingEnabled(t *testing.T) {
	report := lintWithRules(t, DefaultConfig(), fakeRule{
		name:        "max-line-length",
		diagnostics: []Diagnostic{{Message: "fired", Line: 1, Column: 1}},
	})
	if len(report.Diagnostics) != 1 {
		t.Fatalf("non-pending rule did not run: %v", report.Diagnostics)
	}
}

// Enable must keep validating after a rule graduates to running by default,
// otherwise every config that opted in breaks on upgrade.
func TestEnableAcceptsARuleThatIsNotPending(t *testing.T) {
	config := DefaultConfig()
	config.Enable = []string{"max-line-length"}
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	report := lintWithRules(t, config, fakeRule{
		name:        "max-line-length",
		diagnostics: []Diagnostic{{Message: "fired", Line: 1, Column: 1}},
	})
	if len(report.Diagnostics) != 1 {
		t.Fatalf("enabling an already-running rule changed it: %v", report.Diagnostics)
	}
}

func TestEnableRejectsAnUnknownRule(t *testing.T) {
	config := DefaultConfig()
	config.Enable = []string{"no-such-rule"}
	err := config.Validate()
	if err == nil || !strings.Contains(err.Error(), `enable names unknown rule "no-such-rule"`) {
		t.Fatalf("Validate() = %v, want an unknown-rule error", err)
	}
}

// The inert rules are pinned so that a rule cannot start or stop shipping inert
// unnoticed: either direction changes what an upgrade reports on an unchanged
// configuration. missing-docstring is absent because it is inert through its
// own empty configuration value rather than through PendingRule.
func TestPendingRulesAreExactlyTheInertOnes(t *testing.T) {
	want := []string{
		"no-engine-logging", "require-argument-type", "require-return-type",
		"require-typed-collection", "require-variable-type",
	}
	got := PendingRuleNames()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PendingRuleNames() = %v, want %v", got, want)
	}
	for _, name := range want {
		if !IsPendingRule(name) {
			t.Errorf("IsPendingRule(%q) = false", name)
		}
	}
	if IsPendingRule("max-line-length") {
		t.Error("max-line-length reports as pending")
	}
	if IsPendingRule("no-such-rule") {
		t.Error("an unregistered name reports as pending")
	}
	// The driver's own diagnostics are nameable but not registered rules.
	if IsPendingRule("source-parse") || IsPendingRule("unknown-ignore") {
		t.Error("a driver diagnostic reports as pending")
	}
}

// A pending rule is skipped before it is ever asked to check a file, so opting
// out costs nothing at runtime.
func TestPendingRuleIsNotInvokedWhenInert(t *testing.T) {
	var called bool
	rule := watchfulPendingRule{called: &called}
	config := DefaultConfig()
	report := lintWithRules(t, config, rule)
	if called {
		t.Error("an inert pending rule was still asked to check a file")
	}
	if len(report.Diagnostics) != 0 {
		t.Fatalf("got %v", report.Diagnostics)
	}
}

type watchfulPendingRule struct {
	called *bool
}

func (r watchfulPendingRule) Name() string         { return "max-line-length" }
func (r watchfulPendingRule) PendingSince() string { return "0.3.0" }
func (r watchfulPendingRule) Check(*Context, *project.Script) []Diagnostic {
	*r.called = true
	return nil
}
