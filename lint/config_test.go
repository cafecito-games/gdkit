package lint

import (
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/internal/versiongate"
)

func TestDefaultConfigTargetsTheNewestGodot(t *testing.T) {
	if got := DefaultConfig().GodotVersion; got != "4.7" {
		t.Fatalf("GodotVersion = %q, want \"4.7\"", got)
	}
}

func TestValidateRejectsAMalformedGodotVersion(t *testing.T) {
	config := DefaultConfig()
	config.GodotVersion = "v4.7"
	err := config.Validate()
	if err == nil || !strings.Contains(err.Error(), "godot_version") {
		t.Fatalf("Validate() = %v, want an error naming godot_version", err)
	}
}

// supports is what a version-gated rule asks before reporting, so a project is
// never told to write a type annotation its engine cannot parse.
func TestContextSupportsComparesTheConfiguredVersion(t *testing.T) {
	tests := []struct {
		configured string
		floor      versiongate.Version
		want       bool
	}{
		{configured: "4.7", floor: versiongate.Version{Major: 4}, want: true},
		{configured: "4.7", floor: versiongate.Version{Major: 4, Minor: 4}, want: true},
		{configured: "4.3", floor: versiongate.Version{Major: 4, Minor: 4}, want: false},
		{configured: "4.4", floor: versiongate.Version{Major: 4, Minor: 4}, want: true},
		{configured: "4.4.1", floor: versiongate.Version{Major: 4, Minor: 4, Patch: 2}, want: false},
		{configured: "4.1", floor: versiongate.Version{Major: 4, Minor: 2}, want: false},
	}
	for _, test := range tests {
		config := DefaultConfig()
		config.GodotVersion = test.configured
		compiled, err := config.validate()
		if err != nil {
			t.Fatalf("validate() = %v", err)
		}
		context := Context{Config: config, compiled: compiled}
		if got := context.supports(test.floor); got != test.want {
			t.Errorf("godot_version %q supports(%v) = %t, want %t", test.configured, test.floor, got, test.want)
		}
	}
}

// A Context built without a compiled configuration supports nothing, rather
// than dereferencing a nil pointer.
func TestContextSupportsNothingWithoutACompiledConfig(t *testing.T) {
	context := Context{}
	if context.supports(versiongate.Version{Major: 4}) {
		t.Error("a zero Context reported support")
	}
}

// An empty exempt list means "no exemptions". This is the opposite of
// missing-docstring, whose empty list turns that rule off; inertness is
// PendingRule's job and never a list's.
func TestContextExemptMatchesGlobPatterns(t *testing.T) {
	config := DefaultConfig()
	config.RequireReturnType = []string{"_ready", "_on_*"}
	compiled, err := config.validate()
	if err != nil {
		t.Fatalf("validate() = %v", err)
	}
	context := Context{Config: config, compiled: compiled}

	tests := []struct {
		rule string
		name string
		want bool
	}{
		{rule: "require-return-type", name: "_ready", want: true},
		{rule: "require-return-type", name: "_on_button_pressed", want: true},
		{rule: "require-return-type", name: "_process", want: false},
		{rule: "require-return-type", name: "", want: false},
		// A pattern on one rule never exempts another.
		{rule: "require-argument-type", name: "_ready", want: false},
		// A known rule with no configured patterns, and a rule that is not
		// known at all, exempt nothing.
		{rule: "require-typed-collection", name: "_ready", want: false},
		{rule: "no-such-rule", name: "_ready", want: false},
	}
	for _, test := range tests {
		if got := context.exempt(test.rule, test.name); got != test.want {
			t.Errorf("exempt(%q, %q) = %t, want %t", test.rule, test.name, got, test.want)
		}
	}
}

// A Context built without a compiled configuration exempts nothing, rather
// than dereferencing a nil pointer.
func TestContextExemptsNothingWithoutACompiledConfig(t *testing.T) {
	context := Context{}
	if context.exempt("require-return-type", "_ready") {
		t.Error("a zero Context reported an exemption")
	}
}

// The six JSON names are public contract: they appear in config files, in JSON
// output, and in inline ignore comments, so a typo in a tag breaks user
// projects silently. Decoding real JSON is what pins them; setting the struct
// field directly would pass with any tag at all.
func TestExemptListsDecodeFromTheirRuleNames(t *testing.T) {
	tests := []struct {
		name  string
		field func(Config) []string
	}{
		{name: "require-return-type", field: func(c Config) []string { return c.RequireReturnType }},
		{name: "require-argument-type", field: func(c Config) []string { return c.RequireArgumentType }},
		{name: "require-variable-type", field: func(c Config) []string { return c.RequireVariableType }},
		{name: "require-typed-collection", field: func(c Config) []string { return c.RequireTypedCollection }},
		{name: "require-signal-argument-type", field: func(c Config) []string { return c.RequireSignalArgumentType }},
		{name: "require-typed-loop-variable", field: func(c Config) []string { return c.RequireTypedLoopVariable }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeConfigFile(t, root, DefaultConfigPath, `{"`+test.name+`": ["_ready"]}`)
			config, err := LoadConfig(root, "")
			if err != nil {
				t.Fatal(err)
			}
			got := test.field(config)
			if len(got) != 1 || got[0] != "_ready" {
				t.Errorf("field = %q, want [\"_ready\"]", got)
			}
		})
	}
}

func TestExemptListsAreNilWhenNoConfigFileNamesThem(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, DefaultConfigPath, `{"max-line-length": 80}`)
	config, err := LoadConfig(root, "")
	if err != nil {
		t.Fatal(err)
	}
	for name, patterns := range config.exemptPatterns() {
		if patterns != nil {
			t.Errorf("%s = %q, want nil", name, patterns)
		}
	}
}
