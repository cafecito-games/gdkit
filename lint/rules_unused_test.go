package lint

import "testing"

// unusedConfig enables the rule, which ships inert.
func unusedConfig() Config {
	config := DefaultConfig()
	config.Enable = []string{"unused-private-member"}
	return config
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
	if len(found) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(found), found)
	}
	if want := `unused private function "_helper"`; found[0].Message != want {
		t.Fatalf("message = %q, want %q", found[0].Message, want)
	}
	if found[0].Column != 6 {
		t.Fatalf("column = %d, want 6", found[0].Column)
	}
}
