package lint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// unusedConfig enables the rule, which ships inert.
func unusedConfig() Config {
	config := DefaultConfig()
	config.Enable = []string{"unused-private-member"}
	return config
}

// unusedDiagnostics returns only the unused-private-member diagnostics.
func unusedDiagnostics(report Report) []Diagnostic {
	var out []Diagnostic
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == "unused-private-member" {
			out = append(out, diagnostic)
		}
	}
	return out
}

func TestUnusedPrivateMemberFires(t *testing.T) {
	cases := []struct {
		name   string
		source string
		lines  []int
	}{
		{"unused private function", "extends Node\n\nfunc _helper():\n\tpass\n", []int{3}},
		{"unused private constant", "extends Node\n\nconst _LIMIT = 10\n", []int{3}},
		{"unused private static function", "static func _unused():\n\tpass\n", []int{1}},
		{"unused private function of an inner class", "extends Node\n\nclass Helper:\n\tfunc _work():\n\t\tpass\n", []int{4}},
		{"both kinds at once", "extends Node\n\nconst _OLD = 2\n\nfunc _dead():\n\tpass\n", []int{3, 5}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertRuleWithConfig(t, unusedConfig(), "unused-private-member", testCase.source, testCase.lines...)
		})
	}
}

func TestUnusedPrivateMemberStaysSilentWhenReferenced(t *testing.T) {
	cases := []struct {
		name   string
		source string
	}{
		{"function called by name", "extends Node\n\nfunc _helper():\n\tpass\n\nfunc caller():\n\t_helper()\n"},
		{"function recursive", "extends Node\n\nfunc _recur():\n\t_recur()\n"},
		{"constant read", "extends Node\n\nconst _USED = 1\n\nfunc read_value():\n\treturn _USED\n"},
		{"function reached through self", "extends Node\n\nfunc _helper():\n\tpass\n\nfunc caller():\n\tself._helper()\n"},
		{"function dispatched through call", "extends Node\n\nfunc _helper():\n\tpass\n\nfunc caller():\n\tcall(\"_helper\")\n"},
		{"function referenced from an inner class", "extends Node\n\nfunc _helper():\n\tpass\n\nclass Helper:\n\tfunc call_it():\n\t\t_helper()\n"},
		{"function referenced from a lambda", "extends Node\n\nfunc _helper():\n\tpass\n\nfunc make():\n\treturn func(): _helper()\n"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertRuleWithConfig(t, unusedConfig(), "unused-private-member", testCase.source)
		})
	}
}

func TestUnusedPrivateMemberStaysSilentOnExemptMembers(t *testing.T) {
	cases := []struct {
		name   string
		source string
	}{
		{"Node virtual _ready", "extends Node\n\nfunc _ready():\n\tpass\n"},
		{"Node virtual _process", "extends Node\n\nfunc _process(delta):\n\tpass\n"},
		{"Object core virtual _init", "extends Node\n\nfunc _init():\n\tpass\n"},
		{"Object core virtual _to_string", "extends Node\n\nfunc _to_string():\n\treturn \"\"\n"},
		{"Object core virtual _get", "extends Node\n\nfunc _get(property):\n\treturn null\n"},
		{"Object core virtual on a custom base", "extends SomeCustom\n\nfunc _to_string():\n\treturn \"\"\n"},
		{"inherited virtual _toggled on Button", "extends Button\n\nfunc _toggled(button_pressed):\n\tpass\n"},
		{"inherited virtual _initialize on SceneTree", "extends SceneTree\n\nfunc _initialize():\n\tpass\n"},
		{"virtual _log_error on Logger", "extends Logger\n\nfunc _log_error(message):\n\tpass\n"},
		{"editor signal handler", "extends Node\n\nfunc _on_button_pressed():\n\tpass\n"},
		{"public members are never candidates", "extends Node\n\nfunc helper():\n\tpass\n\nconst LIMIT = 1\n\nvar value = 1\n"},
		{"a private variable is never a candidate", "extends Node\n\nvar _value = 1\n"},
		{"a signal is never a candidate", "extends Node\n\nsignal changed\n"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertRuleWithConfig(t, unusedConfig(), "unused-private-member", testCase.source)
		})
	}
}

func TestUnusedPrivateMemberReportsNonVirtualNames(t *testing.T) {
	cases := []struct {
		name   string
		source string
		lines  []int
	}{
		{"_toggled on RefCounted is dead code", "extends RefCounted\n\nfunc _toggled():\n\tpass\n", []int{3}},
		{"_ready on RefCounted is dead code", "extends RefCounted\n\nfunc _ready():\n\tpass\n", []int{3}},
		{"_process on RefCounted is dead code", "extends RefCounted\n\nfunc _process(delta):\n\tpass\n", []int{3}},
		{"_draw on a plain Object subclass is dead code", "extends Object\n\nfunc _draw():\n\tpass\n", []int{3}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertRuleWithConfig(t, unusedConfig(), "unused-private-member", testCase.source, testCase.lines...)
		})
	}
}

func TestUnusedPrivateMemberStaysInertByDefault(t *testing.T) {
	assertNoRule(t, "unused-private-member", "extends Node\n\nfunc _helper():\n\tpass\n")
}

func TestUnusedPrivateMemberMessage(t *testing.T) {
	found := lintSourceWithConfig(t, unusedConfig(), "unused-private-member", "extends Node\n\nfunc _helper():\n\tpass\n")
	require.Len(t, found, 1)
	want := `unused private function "_helper"`
	require.Equal(t, want, found[0].Message)
	require.Equal(t, 6, found[0].Column)
}

// A protected helper a base class declares for its subclasses is used, not
// dead, when a project descendant references it.
func TestUnusedPrivateMemberStaysSilentWhenAProjectDescendantReferencesIt(t *testing.T) {
	report := lintProject(t, unusedConfig(), map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nfunc _wire():\n\tpass\n",
		"sub.gd":  "class_name Sub\nextends Base\n\nfunc connect():\n\t_wire()\n",
	})
	found := unusedDiagnostics(report)
	require.Empty(t, found, "base-class helper reported as dead")
}

// An override of a project-defined virtual is used, not dead, because an
// ancestor declares the name. The base's own declaration is already covered by
// its in-file reference, so only the override would have been reported.
func TestUnusedPrivateMemberStaysSilentWhenOverridingAProjectVirtual(t *testing.T) {
	report := lintProject(t, unusedConfig(), map[string]string{
		"highlight.gd": "class_name EntityWorldHighlight\nextends Node\n\nfunc _highlight_z_index() -> int:\n\treturn 0\n\nfunc _ready():\n\t_highlight_z_index()\n",
		"follow.gd":    "class_name FollowHighlight\nextends EntityWorldHighlight\n\nfunc _highlight_z_index() -> int:\n\treturn 1\n",
	})
	found := unusedDiagnostics(report)
	require.Empty(t, found, "project virtual override reported as dead")
}

// An ancestor that references a name without declaring it still makes a
// descendant override of that name used, the template-method shape.
func TestUnusedPrivateMemberStaysSilentWhenAnAncestorReferencesIt(t *testing.T) {
	report := lintProject(t, unusedConfig(), map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nfunc run():\n\t_do_work()\n",
		"sub.gd":  "class_name Sub\nextends Base\n\nfunc _do_work():\n\tpass\n",
	})
	found := unusedDiagnostics(report)
	require.Empty(t, found, "template-method override reported as dead")
}

// A descendant that reaches a base helper by string dispatch counts too, the
// same generosity the single-file string test already grants.
func TestUnusedPrivateMemberStaysSilentWhenADescendantDispatchesByString(t *testing.T) {
	report := lintProject(t, unusedConfig(), map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nfunc _wire():\n\tpass\n",
		"sub.gd":  "class_name Sub\nextends Base\n\nfunc connect():\n\tcall(\"_wire\")\n",
	})
	found := unusedDiagnostics(report)
	require.Empty(t, found, "string-dispatched base helper reported as dead")
}

// An inner class that extends the base by script path is a descendant too, so
// its reference to the base helper keeps the helper alive.
func TestUnusedPrivateMemberStaysSilentWhenAnInnerClassDescendantReferencesIt(t *testing.T) {
	report := lintProject(t, unusedConfig(), map[string]string{
		"base.gd":   "extends RefCounted\n\nfunc _wire():\n\tpass\n",
		"holder.gd": "extends RefCounted\n\nclass Child extends \"res://base.gd\":\n\tfunc run():\n\t\t_wire()\n",
	})
	found := unusedDiagnostics(report)
	require.Empty(t, found, "inner-class descendant's helper use reported as dead")
}

// The widening must not hide dead code in a leaf class: a private member with
// no descendants and no project ancestors is still reported.
func TestUnusedPrivateMemberStillReportsDeadCodeInALeafClass(t *testing.T) {
	report := lintProject(t, unusedConfig(), map[string]string{
		"leaf.gd": "class_name Leaf\nextends RefCounted\n\nfunc _dead():\n\tpass\n",
	})
	found := unusedDiagnostics(report)
	require.Len(t, found, 1)
	require.Equal(t, 4, found[0].Line)
}

// A live engine callback stays exempt when its native base is reached through a
// project class, not only when the script spells the native base directly.
func TestUnusedPrivateMemberResolvesNativeBaseThroughProjectClass(t *testing.T) {
	report := lintProject(t, unusedConfig(), map[string]string{
		"base.gd":     "class_name ProbeBase\nextends Node2D\n",
		"indirect.gd": "class_name ProbeIndirect\nextends ProbeBase\n\nfunc _draw() -> void:\n\tpass\n",
	})
	found := unusedDiagnostics(report)
	require.Empty(t, found, "engine callback reached through a project class reported as dead")
}

// An inner class whose native base is written as a body extends statement is
// still a CanvasItem subclass, so its _draw is an engine callback.
func TestUnusedPrivateMemberResolvesInnerClassBodyExtends(t *testing.T) {
	report := lintProject(t, unusedConfig(), map[string]string{
		"inner.gd": "extends Node\n\nclass Canvas:\n\textends Control\n\n\tfunc _draw() -> void:\n\t\tpass\n",
	})
	found := unusedDiagnostics(report)
	require.Empty(t, found, "engine callback in a body-extends inner class reported as dead")
}

// The inline extends spelling of an inner class keeps its engine callback exempt
// too.
func TestUnusedPrivateMemberResolvesInnerClassInlineExtends(t *testing.T) {
	report := lintProject(t, unusedConfig(), map[string]string{
		"inner.gd": "extends Node\n\nclass Canvas extends Control:\n\tfunc _draw() -> void:\n\t\tpass\n",
	})
	found := unusedDiagnostics(report)
	require.Empty(t, found, "engine callback in an inline-extends inner class reported as dead")
}

// Resolving through a project class must not exempt a name its native ancestor
// does not declare: a _draw whose native chain ends at RefCounted stays dead.
func TestUnusedPrivateMemberStillReportsNonVirtualThroughProjectClass(t *testing.T) {
	report := lintProject(t, unusedConfig(), map[string]string{
		"base.gd": "class_name ProbeBase\nextends RefCounted\n",
		"leaf.gd": "extends ProbeBase\n\nfunc _draw() -> void:\n\tpass\n",
	})
	found := unusedDiagnostics(report)
	require.Len(t, found, 1)
	require.Equal(t, 3, found[0].Line)
}

// A private helper called directly from an unrelated script, with a receiver
// that resolves to the declaring class, is used, not dead.
func TestUnusedPrivateMemberStaysSilentWhenAnUnrelatedScriptCallsIt(t *testing.T) {
	report := lintProject(t, unusedConfig(), map[string]string{
		"subject.gd": "class_name ProbeSubject\nextends RefCounted\n\nfunc _compute() -> int:\n\treturn 7\n",
		"caller.gd":  "extends RefCounted\n\nfunc run() -> int:\n\treturn ProbeSubject.new()._compute()\n",
	})
	found := unusedDiagnostics(report)
	require.Empty(t, found, "directly referenced helper reported as dead")
}

// The inherited lookup still resolves to the declaring base class, so a helper
// reached through a subclass receiver keeps the base's declaration alive.
func TestUnusedPrivateMemberStaysSilentWhenCalledThroughInheritedReceiver(t *testing.T) {
	report := lintProject(t, unusedConfig(), map[string]string{
		"base.gd":   "class_name ProbeBase\nextends RefCounted\n\nfunc _compute() -> int:\n\treturn 7\n",
		"sub.gd":    "class_name ProbeSub\nextends ProbeBase\n",
		"caller.gd": "extends RefCounted\n\nfunc run() -> int:\n\treturn ProbeSub.new()._compute()\n",
	})
	found := unusedDiagnostics(report)
	require.Empty(t, found, "helper reached through an inherited receiver reported as dead")
}

// A receiver the analyzer cannot type — here an untyped parameter — stays out of
// scope, exactly as the rule's provable-reference contract requires.
func TestUnusedPrivateMemberStillReportsWhenReceiverIsVariant(t *testing.T) {
	report := lintProject(t, unusedConfig(), map[string]string{
		"subject.gd": "class_name ProbeSubject\nextends RefCounted\n\nfunc _compute() -> int:\n\treturn 7\n",
		"caller.gd":  "extends RefCounted\n\nfunc run(target) -> int:\n\treturn target._compute()\n",
	})
	found := unusedDiagnostics(report)
	require.Len(t, found, 1)
	require.Equal(t, 4, found[0].Line)
}

// A failed reduction inside a call argument must not attribute the outer member
// access to the wrong declaring class, and must still attribute it to the
// explicitly constructed receiver. Here ProbeSubject.mode has an unresolvable
// type, so ProbeSubject.new().mode yields Unknown; the outer _compute access is
// ProbeWorker._compute, not ProbeSubject._compute. ProbeSubject's dead _compute
// stays reported while ProbeWorker's referenced _compute stays silent.
func TestUnusedPrivateMemberDoesNotAttributeNestedMemberToWrongClass(t *testing.T) {
	report := lintProject(t, unusedConfig(), map[string]string{
		"subject.gd": "class_name ProbeSubject\nextends RefCounted\nvar mode: Node.ProcessMode = Node.PROCESS_MODE_INHERIT\n\nfunc _compute() -> int:\n\treturn 7\n",
		"worker.gd":  "class_name ProbeWorker\nextends RefCounted\nfunc _init(mode: Node.ProcessMode) -> void:\n\tpass\n\nfunc _compute() -> int:\n\treturn 8\n",
		"caller.gd":  "extends RefCounted\n\nfunc run() -> int:\n\treturn ProbeWorker.new(ProbeSubject.new().mode)._compute()\n",
	})
	found := unusedDiagnostics(report)
	var subjectLines, workerLines []int
	for _, diagnostic := range found {
		switch diagnostic.Path {
		case "subject.gd":
			subjectLines = append(subjectLines, diagnostic.Line)
		case "worker.gd":
			workerLines = append(workerLines, diagnostic.Line)
		}
	}
	require.Equal(t, []int{5}, subjectLines, "ProbeSubject._compute must stay dead")
	require.Empty(t, workerLines, "ProbeWorker._compute is referenced and must stay silent")
}

// An unrelated source parse failure must not blank the native ancestry of a
// valid selected script: its live engine callback stays exempt.
func TestUnusedPrivateMemberIgnoresUnrelatedParseFailure(t *testing.T) {
	report := lintProject(t, unusedConfig(), map[string]string{
		"canvas.gd": "extends Node2D\n\nfunc _draw() -> void:\n\tpass\n",
		"broken.gd": "func (\n",
	})
	found := unusedDiagnostics(report)
	require.Empty(t, found, "engine callback reported as dead because an unrelated file failed to parse")
}
