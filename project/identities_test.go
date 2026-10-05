package project

import (
	"testing"
)

// loadIdentities writes files into a temp project and loads it the way uid
// does, with the identity table populated.
func loadIdentities(t *testing.T, files map[string]string) *Snapshot {
	t.Helper()
	root := t.TempDir()
	writeFiles(t, root, files)
	snapshot, err := Load(Config{Root: root, HonorIgnoreFile: true, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
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
	if len(found) != 1 {
		t.Fatalf("claims for %s = %+v, want exactly one", owner, found)
	}
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
	if len(snapshot.Claims) != len(want) {
		t.Fatalf("claims = %+v, want %d", snapshot.Claims, len(want))
	}
	for owner, expected := range want {
		claim := claimOf(t, snapshot, owner)
		if claim.UID != expected.uid || claim.Path != expected.path ||
			claim.Line != expected.line || claim.Kind != expected.kind || claim.Ignored {
			t.Errorf("claim for %s = %+v, want %+v", owner, claim, expected)
		}
	}
}

// A declaration no decoder accepts is still a declaration: uid reports on it,
// so dropping it here would hide it.
func TestLoadKeepsADeclarationVerbatim(t *testing.T) {
	snapshot := loadIdentities(t, map[string]string{
		"a.gd":      "extends Node\n",
		"a.gd.uid":  "nonsense\n",
		"main.tscn": "[gd_scene format=3 uid=\"uid://b_local_screen\"]\n",
	})
	if got := claimOf(t, snapshot, "a.gd").UID; got != "nonsense" {
		t.Errorf("sidecar claim = %q, want it verbatim", got)
	}
	if got := claimOf(t, snapshot, "main.tscn").UID; got != "uid://b_local_screen" {
		t.Errorf("header claim = %q, want it verbatim", got)
	}
}

func TestLoadRecordsAnIgnoredClaimAsAClaimant(t *testing.T) {
	snapshot := loadIdentities(t, map[string]string{
		".gdkitignore":               "addons/\n",
		"addons/vendor/thing.gd":     "extends Node\n",
		"addons/vendor/thing.gd.uid": "uid://bbb\n",
		"addons/vendor/panel.tscn":   "[gd_scene format=3 uid=\"uid://ccc\"]\n",
	})
	for _, owner := range []string{"addons/vendor/thing.gd", "addons/vendor/panel.tscn"} {
		if claim := claimOf(t, snapshot, owner); !claim.Ignored {
			t.Errorf("claim for %s = %+v, want it marked ignored", owner, claim)
		}
	}
	// It is a claimant but not something uid speaks for, so it stays out of
	// the sidecar list and out of the resolved table.
	if len(snapshot.Sidecars) != 0 {
		t.Errorf("Sidecars = %+v, want none", snapshot.Sidecars)
	}
	if len(snapshot.UIDs) != 0 {
		t.Errorf("UIDs = %v, want none", snapshot.UIDs)
	}
}

func TestLoadRecordsExternalResourceReferences(t *testing.T) {
	snapshot := loadIdentities(t, map[string]string{
		"main.tscn": `[gd_scene load_steps=3 format=3 uid="uid://bbb"]

[ext_resource type="Script" uid="uid://ccc" path="res://player.gd" id="1_abc"]
[ext_resource type="Texture2D" path="res://icon.png" id="2_def"]

[node name="Main" type="Node"]
`,
	})
	if len(snapshot.References) != 1 {
		t.Fatalf("references = %+v, want one", snapshot.References)
	}
	reference := snapshot.References[0]
	want := Reference{UID: "uid://ccc", Target: "player.gd", Path: "main.tscn", Line: 3, Kind: ReferenceExternal}
	if reference != want {
		t.Errorf("reference = %+v, want %+v", reference, want)
	}
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
	if len(got) != len(want) {
		t.Fatalf("references = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("reference %d = %+v, want %+v", i, got[i], want[i])
		}
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
	if len(snapshot.References) != 0 {
		t.Fatalf("references = %+v, want none", snapshot.References)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Claims) != 0 || len(snapshot.References) != 0 {
		t.Fatalf("claims = %+v, references = %+v, want neither", snapshot.Claims, snapshot.References)
	}
	// The header is still indexed, which is what the other tools rely on.
	if snapshot.UIDs["uid://ddd"] != "main.tscn" {
		t.Errorf("UIDs = %v, want the header indexed", snapshot.UIDs)
	}
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
		if got := resourceAttribute(line, key); got != want {
			t.Errorf("resourceAttribute(%q) = %q, want %q", key, got, want)
		}
	}
}
