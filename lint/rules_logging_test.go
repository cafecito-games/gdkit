package lint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// engineLoggingConfig opts in to the rule, which ships inert.
func engineLoggingConfig() Config {
	config := DefaultConfig()
	config.Enable = []string{"no-engine-logging"}
	return config
}

func TestNoEngineLoggingIsInertUntilEnabled(t *testing.T) {
	source := "func a():\n\tpush_error(\"boom\")\n"
	assertNoRule(t, "no-engine-logging", source)
	assertRuleWithConfig(t, engineLoggingConfig(), "no-engine-logging", source, 2)
}

func TestNoEngineLoggingReportsEveryDefaultFunction(t *testing.T) {
	source := `func a():
	push_warning("w")
	push_error("e")
	print("p")
	prints("a", "b")
	printt("a", "b")
	printraw("r")
	printerr("e")
	print_rich("[b]r[/b]")
	print_debug("d")
	print_stack()
	OS.alert("a")
`
	assertRuleWithConfig(t, engineLoggingConfig(), "no-engine-logging", source,
		2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12)
}

func TestNoEngineLoggingMessageAndSpan(t *testing.T) {
	found := lintSourceWithConfig(t, engineLoggingConfig(), "no-engine-logging",
		"func a():\n\tpush_error(\"boom\")\n")
	require.Len(t, found, 1)
	want := `"push_error" writes to the engine output directly; use a logger abstraction instead`
	require.Equal(t, want, found[0].Message)
	// The span covers the callee, not the whole call, so an editor highlights
	// the name the project has to replace.
	require.Equal(t, 2, found[0].Column)
	require.Equal(t, 2, found[0].EndLine)
	require.Equal(t, 12, found[0].EndColumn)
}

func TestNoEngineLoggingNamesTheConfiguredLogger(t *testing.T) {
	config := engineLoggingConfig()
	config.NoEngineLogging.Logger = "Log"
	found := lintSourceWithConfig(t, config, "no-engine-logging", "func a():\n\tprint(\"p\")\n")
	require.Len(t, found, 1)
	want := `"print" writes to the engine output directly; use "Log" instead`
	require.Equal(t, want, found[0].Message)
}

// A logger's own method may be named like the engine function it replaces. The
// rule matches the function a call reaches, not the word, so a call through an
// object is silent unless the configuration qualifies the name.
func TestNoEngineLoggingIgnoresACallThroughAnObject(t *testing.T) {
	source := `var logger = Logger.new()

func a():
	logger.push_error("e")
	logger.print("p")
	Log.push_warning("w")
	self.print("p")
	_loggers[0].push_error("e")
	get_logger().print("p")
`
	assertRuleWithConfig(t, engineLoggingConfig(), "no-engine-logging", source)
}

func TestNoEngineLoggingReportsAQualifiedNameOnlyOnItsObject(t *testing.T) {
	config := engineLoggingConfig()
	config.NoEngineLogging.Functions = []string{"OS.alert"}
	source := `func a():
	OS.alert("a")
	Dialog.alert("a")
	alert("a")
	push_error("e")
`
	assertRuleWithConfig(t, config, "no-engine-logging", source, 2)
}

func TestNoEngineLoggingTrimsAndExtendsTheFunctionList(t *testing.T) {
	config := engineLoggingConfig()
	config.NoEngineLogging.Functions = []string{"push_error", "breakpoint_log"}
	source := `func a():
	push_error("e")
	push_warning("w")
	print("p")
	breakpoint_log("b")
`
	assertRuleWithConfig(t, config, "no-engine-logging", source, 2, 5)
}

func TestNoEngineLoggingSilencedByAnEmptyFunctionList(t *testing.T) {
	config := engineLoggingConfig()
	config.NoEngineLogging.Functions = []string{}
	assertRuleWithConfig(t, config, "no-engine-logging", "func a():\n\tpush_error(\"e\")\n")
}

func TestNoEngineLoggingFindsCallsAnywhereInAFile(t *testing.T) {
	source := `var greeting = prints("hi")

func a(message = push_error("default")):
	var report = func(): print(message)
	report.call()
	if message:
		push_warning(message)

class Inner:
	func b():
		printerr("e")
`
	assertRuleWithConfig(t, engineLoggingConfig(), "no-engine-logging", source, 1, 3, 4, 7, 11)
}

// Comments and strings cannot create a finding: the rule reads the parsed tree,
// never the source text.
func TestNoEngineLoggingIgnoresCommentsAndStrings(t *testing.T) {
	source := `func a():
	# push_error("e")
	var note = "push_error(\"e\")"
	var code = """
	print("p")
	"""
	return [note, code]
`
	assertRuleWithConfig(t, engineLoggingConfig(), "no-engine-logging", source)
}

func TestNoEngineLoggingHonorsASuppressionComment(t *testing.T) {
	source := `func a():
	# gdkit:ignore = no-engine-logging
	push_error("e")
	push_warning("w")
`
	assertRuleWithConfig(t, engineLoggingConfig(), "no-engine-logging", source, 4)
}

func TestNoEngineLoggingRejectsAMalformedFunctionName(t *testing.T) {
	for _, name := range []string{"", "print()", "OS .alert", "OS.", ".alert", "OS.alert.extra", "9print"} {
		config := DefaultConfig()
		config.NoEngineLogging.Functions = []string{name}
		err := config.Validate()
		assert.Error(t, err)
		assert.ErrorContains(t, err, "no-engine-logging")
	}
}

func TestNoEngineLoggingDefaultsAreNotSharedBetweenConfigs(t *testing.T) {
	first := DefaultConfig()
	first.NoEngineLogging.Functions[0] = "replaced"
	second := DefaultConfig()
	require.NotEqual(t, "replaced", second.NoEngineLogging.Functions[0], "DefaultConfig() shares its function list between calls")
}
