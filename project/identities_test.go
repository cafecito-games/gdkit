package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadIdentities writes files into a temp project and loads it the way uid
// does, with the identity table populated.
func loadIdentities(t *testing.T, files map[string]string) *Snapshot {
	t.Helper()
	root := t.TempDir()
	writeFiles(t, root, files)
	snapshot, err := Load(Config{Root: root, HonorIgnoreFile: true, Identities: true})
	require.NoError(t, err)
	return snapshot
}

// claimOf returns the single claim naming owner, failing when there is not
// exactly one.
func claimOf(t *testing.T, snapshot *Snapshot, owner string) Claim {
	t.Helper()
	var found []Claim
	for _, claim := range snapshot.Claims {
		if claim.Owner == owner {
			found = append(found, claim)
		}
	}
	require.Len(t, found, 1)
	return found[0]
}

func TestLoadRecordsAClaimForEveryDeclarationMechanism(t *testing.T) {
	snapshot := loadIdentities(t, map[string]string{
		"player.gd":          "extends Node\n",
		"player.gd.uid":      "uid://bbb\n",
		"water.gdshader.uid": "uid://ccc\n",
		"main.tscn":          "[gd_scene load_steps=2 format=3 uid=\"uid://ddd\"]\n",
		"theme.tres":         "[gd_resource type=\"Theme\" format=3 uid=\"uid://eee\"]\n",
		"icon.png":           "",
		"icon.png.import":    "[remap]\n\nimporter=\"texture\"\nuid=\"uid://fff\"\n",
	})

	want := map[string]struct {
		uid  string
		path string
		line int
		kind ClaimKind
	}{
		"player.gd":      {"uid://bbb", "player.gd.uid", 1, ClaimSidecar},
		"water.gdshader": {"uid://ccc", "water.gdshader.uid", 1, ClaimSidecar},
		"main.tscn":      {"uid://ddd", "main.tscn", 1, ClaimHeader},
		"theme.tres":     {"uid://eee", "theme.tres", 1, ClaimHeader},
		"icon.png":       {"uid://fff", "icon.png.import", 4, ClaimImport},
	}
	require.Len(t, snapshot.Claims, len(want))
	for owner, expected := range want {
		claim := claimOf(t, snapshot, owner)
		assert.Equal(t, expected.uid, claim.UID, owner)
		assert.Equal(t, expected.path, claim.Path, owner)
		assert.Equal(t, expected.line, claim.Line, owner)
		assert.Equal(t, expected.kind, claim.Kind, owner)
		assert.False(t, claim.Ignored, owner)
	}
}

func TestLoadRecordsHeaderClaimAfterLeadingComments(t *testing.T) {
	// Godot v4.7.2 loads this exact text form: a text-resource header may
	// follow leading semicolon comments and blank lines. The declaration still
	// belongs to the header's physical line for uid diagnostics and repairs.
	snapshot := loadIdentities(t, map[string]string{
		"commented.tscn": "; retained by Godot\n\n[gd_scene format=3 uid=\"uid://b\"]\n",
	})
	claim := claimOf(t, snapshot, "commented.tscn")
	require.Equal(t, "uid://b", claim.UID)
	require.Equal(t, "commented.tscn", claim.Path)
	require.Equal(t, 3, claim.Line)
	require.Equal(t, ClaimHeader, claim.Kind)
	require.False(t, snapshot.IdentityIncomplete)
}

// A declaration no decoder accepts is still a declaration: uid reports on it,
// so dropping it here would hide it.
func TestLoadKeepsADeclarationVerbatim(t *testing.T) {
	snapshot := loadIdentities(t, map[string]string{
		"a.gd":      "extends Node\n",
		"a.gd.uid":  "nonsense\n",
		"main.tscn": "[gd_scene format=3 uid=\"uid://b_local_screen\"]\n",
	})
	assert.Equal(t, "nonsense", claimOf(t, snapshot, "a.gd").UID)
	assert.Equal(t, "uid://b_local_screen", claimOf(t, snapshot, "main.tscn").UID)
}

func TestLoadRecordsAnIgnoredClaimAsAClaimant(t *testing.T) {
	snapshot := loadIdentities(t, map[string]string{
		".gdkitignore":               "addons/\n",
		"addons/vendor/thing.gd":     "extends Node\n",
		"addons/vendor/thing.gd.uid": "uid://bbb\n",
		"addons/vendor/panel.tscn":   "[gd_scene format=3 uid=\"uid://ccc\"]\n",
	})
	for _, owner := range []string{"addons/vendor/thing.gd", "addons/vendor/panel.tscn"} {
		assert.True(t, claimOf(t, snapshot, owner).Ignored, owner)
	}
	// It is a claimant but not something uid speaks for, so it stays out of
	// the sidecar list and out of the resolved table.
	assert.Len(t, snapshot.Sidecars, 0)
	assert.Len(t, snapshot.UIDs, 0)
}

func TestLoadRecordsExternalResourceReferences(t *testing.T) {
	snapshot := loadIdentities(t, map[string]string{
		"main.tscn": `[gd_scene load_steps=3 format=3 uid="uid://bbb"]

[ext_resource type="Script" uid="uid://ccc" path="res://player.gd" id="1_abc"]
[ext_resource type="Texture2D" path="res://icon.png" id="2_def"]

[node name="Main" type="Node"]
`,
	})
	require.Len(t, snapshot.References, 1)
	reference := snapshot.References[0]
	want := Reference{UID: "uid://ccc", Target: "player.gd", Path: "main.tscn", Line: 3, Kind: ReferenceExternal}
	assert.Equal(t, want, reference)
}

func TestLoadRecordsScriptLoadReferences(t *testing.T) {
	snapshot := loadIdentities(t, map[string]string{
		"player.gd": `extends Node

const Scene := preload("uid://bbb")
# preload("uid://nope") in a comment is not a reference


func ready() -> void:
	var thing := load("uid://ccc")
	var other := ResourceLoader.load("uid://ddd")
	var path := load("res://world.tscn")
	print(thing, other, path, "uid://eee")
`,
	})
	var got []Reference
	for _, reference := range snapshot.References {
		got = append(got, reference)
	}
	want := []Reference{
		{UID: "uid://bbb", Path: "player.gd", Line: 3, Kind: ReferenceLoad},
		{UID: "uid://ccc", Path: "player.gd", Line: 8, Kind: ReferenceLoad},
		{UID: "uid://ddd", Path: "player.gd", Line: 9, Kind: ReferenceLoad},
	}
	require.Len(t, got, len(want))
	for i := range want {
		assert.Equal(t, want[i], got[i])
	}
}

// A reference inside an ignored path is never reported or rewritten, so it is
// not collected at all.
func TestLoadSkipsReferencesInIgnoredFiles(t *testing.T) {
	snapshot := loadIdentities(t, map[string]string{
		".gdkitignore": "addons/\n",
		"addons/panel.tscn": `[gd_scene format=3 uid="uid://bbb"]

[ext_resource type="Script" uid="uid://nope" path="res://addons/panel.gd" id="1_a"]
`,
		"addons/panel.gd": "extends Node\n\nconst X := preload(\"uid://nope\")\n",
	})
	require.Len(t, snapshot.References, 0)
}

// The other four tools read a resource's header and nothing else, so leaving
// Identities off must not start collecting the rest of it.
func TestLoadWithoutIdentitiesCollectsNothing(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"player.gd":     "extends Node\n\nconst X := preload(\"uid://ccc\")\n",
		"player.gd.uid": "uid://bbb\n",
		"main.tscn":     "[gd_scene format=3 uid=\"uid://ddd\"]\n\n[ext_resource type=\"Script\" uid=\"uid://ccc\" path=\"res://player.gd\" id=\"1\"]\n",
	})
	snapshot, err := Load(Config{Root: root, HonorIgnoreFile: true})
	require.NoError(t, err)
	require.Len(t, snapshot.Claims, 0)
	require.Len(t, snapshot.References, 0)
	// The header is still indexed, which is what the other tools rely on.
	assert.Equal(t, "main.tscn", snapshot.UIDs["uid://ddd"])
}

func TestResourceAttributeMatchesWholeKeys(t *testing.T) {
	line := `[ext_resource type="Script" uid="uid://ccc" path="res://player.gd" id="1_abc"]`
	cases := map[string]string{
		"uid":  "uid://ccc",
		"path": "res://player.gd",
		"type": "Script",
		"id":   "1_abc",
		// "d" is the tail of both uid= and id=, and matches neither.
		"d":    "",
		"ui":   "",
		"none": "",
	}
	for key, want := range cases {
		assert.Equal(t, want, resourceAttribute(line, key), key)
	}
}
