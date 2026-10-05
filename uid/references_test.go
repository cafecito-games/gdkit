package uid

import (
	"reflect"
	"testing"
)

// scene renders a .tscn with the given identity and ext_resource lines, which
// is the shape Godot writes.
func scene(identity string, resources ...string) string {
	text := "[gd_scene load_steps=2 format=3 uid=\"" + identity + "\"]\n\n"
	for _, resource := range resources {
		text += resource + "\n"
	}
	return text + "\n[node name=\"Root\" type=\"Node\"]\n"
}

// external renders one [ext_resource] line naming a script by identity and
// path, the pair that lets a repair resolve itself.
func external(identity, path, id string) string {
	return "[ext_resource type=\"Script\" uid=\"" + identity + "\" path=\"res://" + path + "\" id=\"" + id + "\"]"
}

func TestCheckReportsAReferenceNoFileClaims(t *testing.T) {
	report := checkProject(t, map[string]string{
		"player.gd":     "extends Node\n",
		"player.gd.uid": "uid://bbb\n",
		"loader.gd":     "extends Node\n\nconst Scene := preload(\"uid://xyy\")\n",
		"loader.gd.uid": "uid://ccc\n",
		"main.tscn":     scene("uid://ddd", external("uid://xxx", "player.gd", "1_a")),
		"theme.tres":    "[gd_resource type=\"Theme\" format=3 uid=\"uid://eee\"]\n\n" + external("uid://xxy", "player.gd", "1_b") + "\n",
	})
	if got := rules(report); !reflect.DeepEqual(got, []string{RuleDangling, RuleDangling, RuleDangling}) {
		t.Fatalf("rules = %v, want three %s (%v)", got, RuleDangling, report.Diagnostics)
	}
	want := []string{"loader.gd", "main.tscn", "theme.tres"}
	if got := paths(report); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	// The two that name a path can be repaired from it; the preload cannot,
	// because nothing in the script says what was meant.
	if got := pathsOf(withRule(report.Rewritable(), RuleDangling)); !reflect.DeepEqual(got, []string{"main.tscn", "theme.tres"}) {
		t.Errorf("Rewritable() = %v, want the two that name a path", got)
	}
	if got := pathsOf(report.Reported()); !reflect.DeepEqual(got, []string{"loader.gd"}) {
		t.Errorf("Reported() = %v, want the preload", got)
	}
	if got := report.Diagnostics[0].Line; got != 3 {
		t.Errorf("preload line = %d, want 3", got)
	}
}

// load and ResourceLoader.load are references too, and none of the three names
// a path, so a broken one is report-only.
func TestCheckReportsEveryScriptLoadForm(t *testing.T) {
	report := checkProject(t, map[string]string{
		"loader.gd": `extends Node

const Scene := preload("uid://xxx")


func ready() -> void:
	var one := load("uid://xxy")
	var two := ResourceLoader.load("uid://xyy")
	print(one, two)
`,
		"loader.gd.uid": "uid://bbb\n",
	})
	if got := rules(report); !reflect.DeepEqual(got, []string{RuleDangling, RuleDangling, RuleDangling}) {
		t.Fatalf("rules = %v, want three %s", got, RuleDangling)
	}
	if got := len(report.Reported()); got != 3 {
		t.Errorf("Reported() has %d diagnostics, want 3", got)
	}
}

// A reference holding text no decoder accepts resolves to nothing, which is
// the same defect as naming an identifier nobody claims. Only a *declaration*
// is reported as malformed.
func TestCheckReportsAnUndecodableReferenceAsDangling(t *testing.T) {
	report := checkProject(t, map[string]string{
		"player.gd":     "extends Node\n",
		"player.gd.uid": "uid://bbb\n",
		"main.tscn":     scene("uid://ccc", external("uid://b_local_screen", "player.gd", "1_a")),
	})
	if got := rules(report); !reflect.DeepEqual(got, []string{RuleDangling}) {
		t.Fatalf("rules = %v, want one %s", got, RuleDangling)
	}
}

func TestCheckReportsAReferenceThatResolvesToAnotherFile(t *testing.T) {
	report := checkProject(t, map[string]string{
		"player.gd":     "extends Node\n",
		"player.gd.uid": "uid://bbb\n",
		"enemy.gd":      "extends Node\n",
		"enemy.gd.uid":  "uid://ccc\n",
		// The uid is enemy.gd's identity while the path names player.gd, so
		// it resolves and loads the wrong script, with no warning at all.
		"main.tscn": scene("uid://ddd", external("uid://ccc", "player.gd", "1_a")),
	})
	if got := rules(report); !reflect.DeepEqual(got, []string{RuleCrossed}) {
		t.Fatalf("rules = %v, want one %s (%v)", got, RuleCrossed, report.Diagnostics)
	}
	diagnostic := report.Diagnostics[0]
	if diagnostic.Path != "main.tscn" || diagnostic.Line != 3 || diagnostic.Target != "player.gd" {
		t.Errorf("diagnostic = %+v, want it at main.tscn:3 naming player.gd", diagnostic)
	}
	if !diagnostic.repairable {
		t.Error("the reference names a path with an identity of its own, so it is repairable")
	}
}

func TestCheckPassesAProjectWhoseReferencesResolve(t *testing.T) {
	report := checkProject(t, map[string]string{
		"player.gd":       "extends Node\n",
		"player.gd.uid":   "uid://bbb\n",
		"loader.gd":       "extends Node\n\nconst Scene := preload(\"uid://ddd\")\n",
		"loader.gd.uid":   "uid://ccc\n",
		"icon.png":        "",
		"icon.png.import": "[remap]\n\nuid=\"uid://eee\"\n",
		"main.tscn": scene("uid://ddd",
			external("uid://bbb", "player.gd", "1_a"),
			"[ext_resource type=\"Texture2D\" uid=\"uid://eee\" path=\"res://icon.png\" id=\"2_b\"]"),
	})
	if report.HasDiagnostics() {
		t.Fatalf("diagnostics = %v, want none", report.Diagnostics)
	}
}

// Padding makes two texts name one identifier, and Godot resolves both, so a
// reference that spells the identity differently is not a defect.
func TestCheckAcceptsAPaddedReference(t *testing.T) {
	report := checkProject(t, map[string]string{
		"player.gd":     "extends Node\n",
		"player.gd.uid": "uid://b\n",
		"main.tscn":     scene("uid://ccc", external("uid://aab", "player.gd", "1_a")),
	})
	if report.HasDiagnostics() {
		t.Fatalf("diagnostics = %v, want none", report.Diagnostics)
	}
}

func TestCheckReportsAMalformedIdentityWhereverItIsDeclared(t *testing.T) {
	for name, text := range map[string]string{
		"holding z":          "uid://az",
		"holding 9":          "uid://a9",
		"holding uppercase":  "uid://aB",
		"holding underscore": "uid://b_local_reg_screen",
		"past 63 bits":       "uid://d4n4ub6itg401",
	} {
		t.Run(name, func(t *testing.T) {
			report := checkProject(t, map[string]string{
				"shader.gdshader":     "shader_type canvas_item;\n",
				"shader.gdshader.uid": text + "\n",
				"main.tscn":           scene(text),
				"theme.tres":          "[gd_resource type=\"Theme\" format=3 uid=\"" + text + "\"]\n",
				"icon.png":            "",
				"icon.png.import":     "[remap]\n\nuid=\"" + text + "\"\n",
			})
			want := []string{"icon.png", "main.tscn", "shader.gdshader", "theme.tres"}
			if got := paths(report); !reflect.DeepEqual(got, want) {
				t.Fatalf("paths = %v, want %v", got, want)
			}
			for _, diagnostic := range report.Diagnostics {
				if diagnostic.Rule != RuleMalformed {
					t.Errorf("%s: rule = %s, want %s", diagnostic.Path, diagnostic.Rule, RuleMalformed)
				}
				if diagnostic.UID != text {
					t.Errorf("%s: UID = %q, want %q", diagnostic.Path, diagnostic.UID, text)
				}
			}
			// Every one of them can be reissued, which is what makes the
			// references to them fixable in the same run.
			if got := len(report.Repairs()); got != len(want) {
				t.Errorf("Repairs() has %d diagnostics, want %d", got, len(want))
			}
		})
	}
}

// Excluding addons/ must not turn every reference into a vendored addon
// dangling: a hidden file still owns its identity.
func TestCheckResolvesAgainstAnIgnoredClaimant(t *testing.T) {
	report := checkProject(t, map[string]string{
		".gdkitignore":                "addons/\n",
		"addons/vendor/plugin.gd":     "extends Node\n",
		"addons/vendor/plugin.gd.uid": "uid://fff\n",
		"addons/vendor/panel.tscn":    scene("uid://ggg"),
		"main.tscn": scene("uid://ddd",
			external("uid://fff", "addons/vendor/plugin.gd", "1_a"),
			"[ext_resource type=\"PackedScene\" uid=\"uid://ggg\" path=\"res://addons/vendor/panel.tscn\" id=\"2_b\"]"),
	})
	if report.HasDiagnostics() {
		t.Fatalf("diagnostics = %v, want none", report.Diagnostics)
	}
}

// withRule narrows a group to one rule, so a test can speak about the dangling
// half of a mixed list.
func withRule(diagnostics []Diagnostic, rule string) []Diagnostic {
	var found []Diagnostic
	for _, diagnostic := range diagnostics {
		if diagnostic.Rule == rule {
			found = append(found, diagnostic)
		}
	}
	return found
}
