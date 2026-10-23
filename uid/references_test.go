package uid

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.Equal(t, []string{RuleDangling, RuleDangling, RuleDangling}, rules(report))
	want := []string{"loader.gd", "main.tscn", "theme.tres"}
	require.Equal(t, want, paths(report))
	// The two that name a path can be repaired from it; the preload cannot,
	// because nothing in the script says what was meant.
	assert.Equal(t, []string{"main.tscn", "theme.tres"}, pathsOf(withRule(report.Rewritable(), RuleDangling)))
	assert.Equal(t, []string{"loader.gd"}, pathsOf(report.Reported()))
	assert.Equal(t, 3, report.Diagnostics[0].Line)
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
	require.Equal(t, []string{RuleDangling, RuleDangling, RuleDangling}, rules(report))
	assert.Len(t, report.Reported(), 3)
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
	require.Equal(t, []string{RuleDangling}, rules(report))
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
	require.Equal(t, []string{RuleCrossed}, rules(report))
	diagnostic := report.Diagnostics[0]
	assert.Equal(t, "main.tscn", diagnostic.Path)
	assert.Equal(t, 3, diagnostic.Line)
	assert.Equal(t, "player.gd", diagnostic.Target)
	assert.True(t, diagnostic.repairable)
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
	require.False(t, report.HasDiagnostics())
}

// Padding makes two texts name one identifier, and Godot resolves both, so a
// reference that spells the identity differently is not a defect.
func TestCheckAcceptsAPaddedReference(t *testing.T) {
	report := checkProject(t, map[string]string{
		"player.gd":     "extends Node\n",
		"player.gd.uid": "uid://b\n",
		"main.tscn":     scene("uid://ccc", external("uid://aab", "player.gd", "1_a")),
	})
	require.False(t, report.HasDiagnostics())
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
			require.Equal(t, want, paths(report))
			for _, diagnostic := range report.Diagnostics {
				assert.Equal(t, RuleMalformed, diagnostic.Rule, diagnostic.Path)
				assert.Equal(t, text, diagnostic.UID, diagnostic.Path)
			}
			// Every one of them can be reissued, which is what makes the
			// references to them fixable in the same run.
			assert.Len(t, report.Repairs(), len(want))
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
	require.False(t, report.HasDiagnostics())
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
