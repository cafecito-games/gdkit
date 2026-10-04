package lint

import (
	"strings"
	"testing"
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
	if len(found) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(found))
	}
	want := `"push_error" writes to the engine output directly; use a logger abstraction instead`
	if found[0].Message != want {
		t.Fatalf("message = %q, want %q", found[0].Message, want)
	}
	// The span covers the callee, not the whole call, so an editor highlights
	// the name the project has to replace.
	if found[0].Column != 2 || found[0].EndLine != 2 || found[0].EndColumn != 12 {
		t.Fatalf("unexpected span %+v", found[0])
	}
}

func TestNoEngineLoggingNamesTheConfiguredLogger(t *testing.T) {
	config := engineLoggingConfig()
	config.NoEngineLogging.Logger = "Log"
	found := lintSourceWithConfig(t, config, "no-engine-logging", "func a():\n\tprint(\"p\")\n")
	if len(found) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(found))
	}
	want := `"print" writes to the engine output directly; use "Log" instead`
	if found[0].Message != want {
		t.Fatalf("message = %q, want %q", found[0].Message, want)
	}
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
		if err == nil || !strings.Contains(err.Error(), "no-engine-logging") {
			t.Errorf("Validate() with %q = %v, want a configuration error", name, err)
		}
	}
}

func TestNoEngineLoggingDefaultsAreNotSharedBetweenConfigs(t *testing.T) {
	first := DefaultConfig()
	first.NoEngineLogging.Functions[0] = "replaced"
	if second := DefaultConfig(); second.NoEngineLogging.Functions[0] == "replaced" {
		t.Fatal("DefaultConfig() shares its function list between calls")
	}
}
