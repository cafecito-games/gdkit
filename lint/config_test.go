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
