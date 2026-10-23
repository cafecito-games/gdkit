package uid

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckPassesAProjectWhereEveryScriptHasASidecar(t *testing.T) {
	report := checkProject(t, map[string]string{
		"player.gd":          "extends Node\n",
		"player.gd.uid":      "uid://b\n",
		"world/enemy.gd":     "extends Node\n",
		"world/enemy.gd.uid": "uid://c\n",
	})
	require.False(t, report.HasDiagnostics())
	assert.Equal(t, 2, report.Scripts)
}

func TestCheckReportsAScriptWithNoSidecar(t *testing.T) {
	report := checkProject(t, map[string]string{
		"player.gd":          "extends Node\n",
		"world/enemy.gd":     "extends Node\n",
		"world/enemy.gd.uid": "uid://c\n",
	})
	require.Equal(t, []string{RuleMissing}, rules(report))
	diagnostic := report.Diagnostics[0]
	assert.Equal(t, "player.gd", diagnostic.Path)
	assert.Empty(t, diagnostic.UID)
}

func TestCheckReportsMalformedSidecars(t *testing.T) {
	report := checkProject(t, map[string]string{
		"blank.gd":        "extends Node\n",
		"blank.gd.uid":    "\n",
		"path.gd":         "extends Node\n",
		"path.gd.uid":     "res://path.gd\n",
		"zed.gd":          "extends Node\n",
		"zed.gd.uid":      "uid://az\n",
		"overflow.gd":     "extends Node\n",
		"overflow.gd.uid": "uid://d4n4ub6itg401\n",
	})
	want := []string{"blank.gd", "overflow.gd", "path.gd", "zed.gd"}
	require.Equal(t, want, paths(report))
	for _, diagnostic := range report.Diagnostics {
		assert.Equal(t, RuleMalformed, diagnostic.Rule, diagnostic.Path)
	}
}

func TestCheckReportsDuplicatesAgainstTheFirstClaimant(t *testing.T) {
	report := checkProject(t, map[string]string{
		"a.gd":     "extends Node\n",
		"a.gd.uid": "uid://bb\n",
		"b.gd":     "extends Node\n",
		"b.gd.uid": "uid://bb\n",
		"c.gd":     "extends Node\n",
		"c.gd.uid": "uid://bb\n",
	})
	// a.gd sorts first, so it keeps the identifier and the other two are
	// reported against it.
	want := []string{"b.gd", "c.gd"}
	require.Equal(t, want, paths(report))
	for _, diagnostic := range report.Diagnostics {
		assert.Equal(t, RuleDuplicate, diagnostic.Rule, diagnostic.Path)
		assert.Equal(t, "uid://bb", diagnostic.UID, diagnostic.Path)
	}
}

// Padding makes two different texts name one id, which is still a duplicate.
func TestCheckReportsDuplicatesThroughPaddedText(t *testing.T) {
	report := checkProject(t, map[string]string{
		"a.gd":     "extends Node\n",
		"a.gd.uid": "uid://b\n",
		"b.gd":     "extends Node\n",
		"b.gd.uid": "uid://aab\n",
	})
	require.Equal(t, []string{RuleDuplicate}, rules(report))
}

// A sidecar carrying no identifier cannot duplicate anything, so it is only
// ever malformed.
func TestCheckDoesNotCallMalformedSidecarsDuplicates(t *testing.T) {
	report := checkProject(t, map[string]string{
		"a.gd":     "extends Node\n",
		"a.gd.uid": "nonsense\n",
		"b.gd":     "extends Node\n",
		"b.gd.uid": "nonsense\n",
	})
	require.Equal(t, []string{RuleMalformed, RuleMalformed}, rules(report))
}

// A file that is not a script is never reported for *having* no identity:
// gdkit does not know which of them Godot would have given one. A sidecar that
// exists is still checked, because a shader's identity is as resolvable as a
// script's and an unusable one breaks the same references.
func TestCheckDoesNotAskForASidecarBesideAFileThatIsNotAScript(t *testing.T) {
	report := checkProject(t, map[string]string{
		"player.gd":          "extends Node\n",
		"player.gd.uid":      "uid://b\n",
		"water.gdshader":     "shader_type canvas_item;\n",
		"water.gdshader.uid": "uid://c\n",
		"plain.gdshader":     "shader_type canvas_item;\n",
	})
	require.False(t, report.HasDiagnostics())
}

func TestCheckReportsAMalformedSidecarBesideAFileThatIsNotAScript(t *testing.T) {
	report := checkProject(t, map[string]string{
		"broken.gdshader":     "shader_type canvas_item;\n",
		"broken.gdshader.uid": "nonsense\n",
	})
	require.Equal(t, []string{RuleMalformed}, rules(report))
	assert.Equal(t, "broken.gdshader", report.Diagnostics[0].Path)
}

func TestCheckSkipsScriptsHiddenByTheIgnoreFile(t *testing.T) {
	report := checkProject(t, map[string]string{
		".gdkitignore":     "addons/\n",
		"player.gd":        "extends Node\n",
		"player.gd.uid":    "uid://b\n",
		"addons/vendor.gd": "extends Node\n",
	})
	require.False(t, report.HasDiagnostics())
	assert.Equal(t, 1, report.Scripts)
}

// A script that does not parse still needs an identity; nothing here reads the
// syntax tree.
func TestCheckReportsAScriptThatDoesNotParse(t *testing.T) {
	report := checkProject(t, map[string]string{"broken.gd": "func (((\n"})
	require.Equal(t, []string{RuleMissing}, rules(report))
}

func TestReportPartitionsItsDiagnostics(t *testing.T) {
	report := checkProject(t, map[string]string{
		"a.gd":     "extends Node\n",
		"a.gd.uid": "uid://bb\n",
		"b.gd":     "extends Node\n",
		"b.gd.uid": "uid://bb\n",
		"c.gd":     "extends Node\n",
		"c.gd.uid": "nonsense\n",
		"d.gd":     "extends Node\n",
	})
	assert.Len(t, report.Missing(), 1)
	assert.Len(t, report.Repairs(), 2)
}
