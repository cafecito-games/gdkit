package lint

import (
	"testing"

	"github.com/cafecito-games/gdkit/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.Empty(t, report.Diagnostics, "pending rule ran without being enabled")
}

func TestPendingRuleRunsWhenEnabledByName(t *testing.T) {
	config := DefaultConfig()
	config.Enable = []string{"max-line-length"}
	report := lintWithRules(t, config, pendingRule("max-line-length"))
	require.Len(t, report.Diagnostics, 1, "enabled pending rule did not run")
}

func TestPendingRuleRunsWhenNewRulesAreEnabled(t *testing.T) {
	config := DefaultConfig()
	config.EnableNewRules = true
	report := lintWithRules(t, config, pendingRule("max-line-length"))
	require.Len(t, report.Diagnostics, 1, "enable_new_rules did not run the pending rule")
}

// Disable is the stronger statement: a project that lists a rule in both is
// most likely turning off something it opted in to earlier.
func TestDisableWinsOverEnableForAPendingRule(t *testing.T) {
	config := DefaultConfig()
	config.Enable = []string{"max-line-length"}
	config.Disable = []string{"max-line-length"}
	report := lintWithRules(t, config, pendingRule("max-line-length"))
	require.Empty(t, report.Diagnostics, "disable did not win over enable")
}

func TestDisableWinsOverEnableNewRules(t *testing.T) {
	config := DefaultConfig()
	config.EnableNewRules = true
	config.Disable = []string{"max-line-length"}
	report := lintWithRules(t, config, pendingRule("max-line-length"))
	require.Empty(t, report.Diagnostics, "disable did not win over enable_new_rules")
}

// A rule that does not ship inert is unaffected by the mechanism, which is what
// keeps every existing project's verdict unchanged.
func TestNonPendingRuleRunsWithoutBeingEnabled(t *testing.T) {
	report := lintWithRules(t, DefaultConfig(), fakeRule{
		name:        "max-line-length",
		diagnostics: []Diagnostic{{Message: "fired", Line: 1, Column: 1}},
	})
	require.Len(t, report.Diagnostics, 1, "non-pending rule did not run")
}

// Enable must keep validating after a rule graduates to running by default,
// otherwise every config that opted in breaks on upgrade.
func TestEnableAcceptsARuleThatIsNotPending(t *testing.T) {
	config := DefaultConfig()
	config.Enable = []string{"max-line-length"}
	err := config.Validate()
	require.NoError(t, err)
	report := lintWithRules(t, config, fakeRule{
		name:        "max-line-length",
		diagnostics: []Diagnostic{{Message: "fired", Line: 1, Column: 1}},
	})
	require.Len(t, report.Diagnostics, 1, "enabling an already-running rule changed it")
}

func TestEnableRejectsAnUnknownRule(t *testing.T) {
	config := DefaultConfig()
	config.Enable = []string{"no-such-rule"}
	err := config.Validate()
	require.Error(t, err)
	require.ErrorContains(t, err, `enable names unknown rule "no-such-rule"`)
}

// The inert rules are pinned so that a rule cannot start or stop shipping inert
// unnoticed: either direction changes what an upgrade reports on an unchanged
// configuration. missing-docstring is absent because it is inert through its
// own empty configuration value rather than through PendingRule.
func TestPendingRulesAreExactlyTheInertOnes(t *testing.T) {
	want := []string{
		"inconsistent-return-statements", "no-engine-logging",
		"require-argument-type", "require-return-type",
		"require-signal-argument-type", "require-typed-collection",
		"require-typed-loop-variable", "require-variable-type",
		"unused-private-member",
	}
	got := PendingRuleNames()
	require.Equal(t, want, got)
	for _, name := range want {
		assert.True(t, IsPendingRule(name))
	}
	assert.False(t, IsPendingRule("max-line-length"), "max-line-length reports as pending")
	assert.False(t, IsPendingRule("no-such-rule"), "an unregistered name reports as pending")
	// The driver's own diagnostics are nameable but not registered rules.
	assert.False(t, IsPendingRule("source-parse"), "a driver diagnostic reports as pending")
	assert.False(t, IsPendingRule("unknown-ignore"), "a driver diagnostic reports as pending")
}

// TestEveryRuleDescribesItself pins that `gdkit lint rules` has a description
// for every nameable rule. A rule added without one would list with an empty
// description, which is worse than no listing at all.
func TestEveryRuleDescribesItself(t *testing.T) {
	assert.Len(t, RuleInfos(), len(RuleNames()))
	for _, info := range RuleInfos() {
		assert.NotEmpty(t, info.Description)
	}
}

// A pending rule is skipped before it is ever asked to check a file, so opting
// out costs nothing at runtime.
func TestPendingRuleIsNotInvokedWhenInert(t *testing.T) {
	var called bool
	rule := watchfulPendingRule{called: &called}
	config := DefaultConfig()
	report := lintWithRules(t, config, rule)
	assert.False(t, called, "an inert pending rule was still asked to check a file")
	require.Empty(t, report.Diagnostics)
}

type watchfulPendingRule struct {
	called *bool
}

func (r watchfulPendingRule) Name() string         { return "max-line-length" }
func (r watchfulPendingRule) Describe() string     { return "test" }
func (r watchfulPendingRule) PendingSince() string { return "0.3.0" }
func (r watchfulPendingRule) Check(*Context, *project.Script) []Diagnostic {
	*r.called = true
	return nil
}
