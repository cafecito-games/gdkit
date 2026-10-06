package semanticsource

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/internal/semantic"
	"github.com/cafecito-games/gdkit/project"
)

func TestSnapshotUsesFullUniverseAndResolvesStaticScriptPaths(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		".gdkitignore":  "hidden.gd\n",
		"base.gd":       "class_name Base\n",
		"base.gd.uid":   "uid://base\n",
		"nested/sub.gd": "extends \"../base.gd\"\n",
		"hidden.gd":     "class_name Hidden\n",
		"broken.gd":     "func (((\n",
		"project.godot": "[autoload]\nGameState=\"*res://base.gd\"\n",
	}
	for name, contents := range files {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := project.Load(project.Config{Root: root, Selection: &project.Selection{SourceRoots: []string{"."}, HonorIgnoreFile: true}})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(snapshot.Selected, "hidden.gd") {
		t.Fatal("fixture did not exclude hidden.gd")
	}
	source := NewSnapshot(snapshot)
	if !slices.Contains(source.Paths(), "hidden.gd") {
		t.Fatalf("Paths = %v, want ignored file in full universe", source.Paths())
	}
	index := semantic.BuildIndex(source)
	if index.Classes["hidden.gd"] == nil {
		t.Error("ignored class was dropped from semantic index")
	}
	for _, test := range []struct{ from, target, want string }{
		{"nested/sub.gd", "../base.gd", "base.gd"},
		{"nested/sub.gd", "res://base.gd", "base.gd"},
		{"nested/sub.gd", "uid://base", "base.gd"},
	} {
		if got, ok := source.ResolvePath(test.from, test.target); !ok || got != test.want {
			t.Errorf("ResolvePath(%q, %q) = %q, %v", test.from, test.target, got, ok)
		}
	}
	if _, ok := source.ResolvePath("base.gd", "res://gone.gd"); ok {
		t.Error("missing path resolved")
	}
	if _, ok := source.ResolvePath("base.gd", "uid://gone"); ok {
		t.Error("missing UID resolved")
	}
	if source.Autoloads()["GameState"] != "base.gd" {
		t.Errorf("Autoloads = %v", source.Autoloads())
	}
	if !slices.Equal(source.ParseFailures(), []string{"broken.gd"}) {
		t.Errorf("ParseFailures = %v", source.ParseFailures())
	}
}

func TestSnapshotResolvesImmutableResourceInventoryAndUIDClaims(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		".gdkitignore":         "ignored/\n",
		"scripts/loader.gd":    "class_name Loader\n",
		"actors/enemy.gd":      "class_name Enemy\n",
		"actors/enemy.gd.uid":  "uid://c\n",
		"ignored/other.gd":     "class_name Other\n",
		"ignored/other.gd.uid": "uid://c\n",
		"scenes/main.tscn":     "[gd_scene format=3 uid=\"uid://d\"]\n",
		"theme.tres":           "[gd_resource type=\"Theme\" format=3 uid=\"uid://e\"]\n",
		"art/icon.png":         "binary",
		"art/icon.png.import":  "[remap]\nuid=\"uid://f\"\n",
		"shared.tscn":          "[gd_scene format=3 uid=\"uid://g\"]\n",
		"shared.tscn.import":   "[remap]\nuid=\"uid://g\"\n",
	}
	for name, contents := range files {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	source := NewSnapshot(snapshot)
	var resolver semantic.ResourceResolver = source
	for _, testCase := range []struct {
		name, from, target string
		state              semantic.ResourceState
		kind               semantic.ResourceKind
		path               string
		provenance         semantic.ResourceProvenance
		reason             string
	}{
		{name: "relative script", target: "../actors/enemy.gd", state: semantic.ResourceFound, kind: semantic.ResourceScript, path: "actors/enemy.gd", provenance: semantic.ResourceLiteralPath},
		{name: "scene", target: "res://scenes/main.tscn", state: semantic.ResourceFound, kind: semantic.ResourceScene, path: "scenes/main.tscn", provenance: semantic.ResourceLiteralPath},
		{name: "text resource", target: "res://theme.tres", state: semantic.ResourceFound, kind: semantic.ResourceText, path: "theme.tres", provenance: semantic.ResourceLiteralPath},
		{name: "unique uid", target: "uid://d", state: semantic.ResourceFound, kind: semantic.ResourceScene, path: "scenes/main.tscn", provenance: semantic.ResourceUIDClaim},
		{name: "text uid", target: "uid://e", state: semantic.ResourceFound, kind: semantic.ResourceText, path: "theme.tres", provenance: semantic.ResourceUIDClaim},
		{name: "imported", target: "res://art/icon.png", state: semantic.ResourceUnsupportedKind, kind: semantic.ResourceImported, provenance: semantic.ResourceLiteralPath},
		{name: "imported uid", target: "uid://f", state: semantic.ResourceUnsupportedKind, kind: semantic.ResourceImported, provenance: semantic.ResourceUIDClaim},
		{name: "same owner header import", target: "uid://g", state: semantic.ResourceFound, kind: semantic.ResourceScene, path: "shared.tscn", provenance: semantic.ResourceUIDClaim},
		{name: "missing", target: "res://gone.gd", state: semantic.ResourceMissing, kind: semantic.ResourceUnknown, provenance: semantic.ResourceLiteralPath},
		{name: "empty", target: "", state: semantic.ResourceInvalid, kind: semantic.ResourceUnknown, provenance: semantic.ResourceLiteralPath},
		{name: "backslash", target: `res:\\bad.gd`, state: semantic.ResourceInvalid, kind: semantic.ResourceUnknown, provenance: semantic.ResourceLiteralPath},
		{name: "invalid uid", target: "uid://z", state: semantic.ResourceInvalid, kind: semantic.ResourceUnknown, provenance: semantic.ResourceUIDClaim},
		{name: "foreign scheme", target: "user://save.gd", state: semantic.ResourceInvalid, kind: semantic.ResourceUnknown, provenance: semantic.ResourceLiteralPath},
		{name: "absolute", target: "/outside.gd", state: semantic.ResourceInvalid, kind: semantic.ResourceUnknown, provenance: semantic.ResourceLiteralPath},
		{name: "absolute res path", target: "res:///outside.gd", state: semantic.ResourceInvalid, kind: semantic.ResourceUnknown, provenance: semantic.ResourceLiteralPath},
		{name: "escapes", target: "../../outside.gd", state: semantic.ResourceEscapesProject, kind: semantic.ResourceUnknown, provenance: semantic.ResourceLiteralPath},
		{name: "unknown source", from: "unknown.gd", target: "res://actors/enemy.gd", state: semantic.ResourceInvalid, kind: semantic.ResourceUnknown, provenance: semantic.ResourceLiteralPath},
		{name: "ignored duplicate uid", target: "uid://c", state: semantic.ResourceAmbiguousUID, kind: semantic.ResourceUnknown, provenance: semantic.ResourceUIDClaim, reason: "resource UID has multiple captured claimants: actors/enemy.gd, ignored/other.gd"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			from := testCase.from
			if from == "" {
				from = "scripts/loader.gd"
			}
			got := resolver.ResolveResource(from, testCase.target)
			if got.State() != testCase.state || got.Kind() != testCase.kind || got.Path() != testCase.path || got.Provenance() != testCase.provenance {
				t.Fatalf("ResolveResource(%q) = state=%s kind=%s path=%q provenance=%s reason=%q", testCase.target, got.State(), got.Kind(), got.Path(), got.Provenance(), got.Reason())
			}
			if got.State() != semantic.ResourceFound && strings.TrimSpace(got.Reason()) == "" {
				t.Fatalf("ResolveResource(%q) returned an unexplained non-found result", testCase.target)
			}
			if testCase.reason != "" && got.Reason() != testCase.reason {
				t.Fatalf("ResolveResource(%q) reason = %q, want deterministic %q", testCase.target, got.Reason(), testCase.reason)
			}
			if strings.Contains(got.Reason(), root) {
				t.Fatalf("ResolveResource(%q) leaked host root in reason %q", testCase.target, got.Reason())
			}
		})
	}
}

func TestSnapshotResourceResolutionDoesNotConsultDiskAfterConstruction(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"loader.gd":  "class_name Loader\n",
		"theme.tres": "[gd_resource type=\"Theme\" format=3 uid=\"uid://b\"]\n",
	} {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := project.Load(project.Config{Root: root, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	source := NewSnapshot(snapshot)
	if err := os.Remove(filepath.Join(root, "theme.tres")); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"res://theme.tres", "uid://b"} {
		got := source.ResolveResource("loader.gd", target)
		if got.State() != semantic.ResourceFound || got.Kind() != semantic.ResourceText || got.Path() != "theme.tres" {
			t.Fatalf("ResolveResource(%q) after disk removal = state=%s kind=%s path=%q reason=%q", target, got.State(), got.Kind(), got.Path(), got.Reason())
		}
	}
}

func TestSnapshotDoesNotUseUIDWinnerMapWithoutClaimEvidence(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"loader.gd":     "class_name Loader\n",
		"target.gd":     "class_name Target\n",
		"target.gd.uid": "uid://b\n",
	} {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := project.Load(project.Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.UIDs["uid://b"] != "target.gd" {
		t.Fatalf("fixture did not produce the compatibility winner map: %v", snapshot.UIDs)
	}
	got := NewSnapshot(snapshot).ResolveResource("loader.gd", "uid://b")
	if got.State() != semantic.ResourceInvalid || got.Provenance() != semantic.ResourceUIDClaim || !strings.Contains(got.Reason(), "not requested") {
		t.Fatalf("UID without claim evidence = state=%s provenance=%s reason=%q", got.State(), got.Provenance(), got.Reason())
	}
}

func TestSnapshotFailsClosedForIncompleteIdentityClaimEvidence(t *testing.T) {
	source := NewSnapshot(&project.Snapshot{
		Paths:              []string{"loader.gd"},
		Scripts:            map[string]*project.Script{"loader.gd": {}},
		Resources:          []project.Resource{{Path: "target.gd", Kind: project.ResourceScript}},
		Claims:             []project.Claim{{UID: "uid://b", Owner: "target.gd", Path: "target.gd", Line: 1, Kind: project.ClaimSidecar}},
		IdentityEvidence:   true,
		IdentityIncomplete: true,
	})
	got := source.ResolveResource("loader.gd", "uid://b")
	if got.State() != semantic.ResourceInvalid || got.Provenance() != semantic.ResourceUIDClaim || !strings.Contains(got.Reason(), "incomplete") {
		t.Fatalf("incomplete UID evidence = state=%s provenance=%s reason=%q", got.State(), got.Provenance(), got.Reason())
	}
}

func TestSnapshotFailsClosedForProducerNarrowedIdentityEvidence(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"loader.gd":             "class_name Loader\n",
		"target.gd":             "class_name Target\n",
		"target.gd.uid":         "uid://b\n",
		"excluded/other.gd":     "class_name Other\n",
		"excluded/other.gd.uid": "uid://b\n",
	} {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := project.Load(project.Config{Root: root, Exclude: []string{"excluded/**"}, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.IdentityIncomplete {
		t.Fatal("fixture did not retain narrowed identity evidence")
	}
	got := NewSnapshot(snapshot).ResolveResource("loader.gd", "uid://b")
	if got.State() != semantic.ResourceInvalid || got.Provenance() != semantic.ResourceUIDClaim || !strings.Contains(got.Reason(), "incomplete") {
		t.Fatalf("narrowed UID evidence = state=%s provenance=%s reason=%q", got.State(), got.Provenance(), got.Reason())
	}
}

func TestSnapshotFailsClosedForProducerSymlinkedClaimEvidence(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	for name, contents := range map[string]string{
		"loader.gd":     "class_name Loader\n",
		"target.gd":     "class_name Target\n",
		"target.gd.uid": "uid://b\n",
	} {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "other.gd.uid"), []byte("uid://b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlinks are unavailable in this test environment: %v", err)
	}
	snapshot, err := project.Load(project.Config{Root: root, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.IdentityIncomplete {
		t.Fatal("fixture did not retain omitted symlinked claim evidence")
	}
	got := NewSnapshot(snapshot).ResolveResource("loader.gd", "uid://b")
	if got.State() != semantic.ResourceInvalid || got.Provenance() != semantic.ResourceUIDClaim || !strings.Contains(got.Reason(), "incomplete") {
		t.Fatalf("symlinked UID evidence = state=%s provenance=%s reason=%q", got.State(), got.Provenance(), got.Reason())
	}
}

func TestSnapshotRejectsUnknownResourceInventoryKinds(t *testing.T) {
	source := NewSnapshot(&project.Snapshot{
		Paths:     []string{"loader.gd"},
		Scripts:   map[string]*project.Script{"loader.gd": {}},
		Resources: []project.Resource{{Path: "mystery.asset", Kind: project.ResourceKind(99)}},
	})
	got := source.ResolveResource("loader.gd", "res://mystery.asset")
	if got.State() != semantic.ResourceMissing || got.Kind() != semantic.ResourceUnknown || !strings.Contains(got.Reason(), "inventory") {
		t.Fatalf("unknown resource kind = state=%s kind=%s reason=%q, want missing Unknown inventory result", got.State(), got.Kind(), got.Reason())
	}
}

func TestSnapshotFailsClosedForMalformedCapturedClaimEvidence(t *testing.T) {
	source := NewSnapshot(&project.Snapshot{
		Paths:            []string{"loader.gd"},
		Scripts:          map[string]*project.Script{"loader.gd": {}},
		Resources:        []project.Resource{{Path: "target.tres", Kind: project.ResourceText}},
		Claims:           []project.Claim{{UID: "uid://b", Owner: "../target.tres", Path: "target.tres", Line: 1, Kind: project.ClaimHeader}},
		IdentityEvidence: true,
	})
	got := source.ResolveResource("loader.gd", "uid://b")
	if got.State() != semantic.ResourceInvalid || got.Provenance() != semantic.ResourceUIDClaim || !strings.Contains(got.Reason(), "malformed") {
		t.Fatalf("malformed claim evidence = state=%s provenance=%s reason=%q", got.State(), got.Provenance(), got.Reason())
	}
}

func TestSnapshotCopiesProviderCollections(t *testing.T) {
	snapshot := &project.Snapshot{
		Paths:            []string{"a.gd"},
		Scripts:          map[string]*project.Script{"a.gd": {}},
		UIDs:             map[string]string{"uid://a": "a.gd"},
		Autoloads:        map[string]string{"A": "a.gd"},
		Resources:        []project.Resource{{Path: "a.gd", Kind: project.ResourceScript}, {Path: "theme.tres", Kind: project.ResourceText}},
		Claims:           []project.Claim{{UID: "uid://b", Owner: "theme.tres", Path: "theme.tres", Line: 1, Kind: project.ClaimHeader}},
		IdentityEvidence: true,
	}
	source := NewSnapshot(snapshot)
	snapshot.Paths[0] = "changed.gd"
	snapshot.UIDs["uid://a"] = "changed.gd"
	snapshot.Autoloads["A"] = "changed.gd"
	snapshot.Resources[1] = project.Resource{Path: "changed.tres", Kind: project.ResourceImported}
	snapshot.Claims[0] = project.Claim{UID: "uid://b", Owner: "changed.tres", Path: "changed.tres", Line: 1, Kind: project.ClaimHeader}
	if !slices.Equal(source.Paths(), []string{"a.gd"}) {
		t.Errorf("Paths = %v", source.Paths())
	}
	if got, ok := source.ResolvePath("x.gd", "uid://a"); !ok || got != "a.gd" {
		t.Errorf("UID resolution = %q, %v", got, ok)
	}
	if source.Autoloads()["A"] != "a.gd" {
		t.Errorf("Autoloads = %v", source.Autoloads())
	}
	if got := source.ResolveResource("a.gd", "res://theme.tres"); got.State() != semantic.ResourceFound || got.Kind() != semantic.ResourceText || got.Path() != "theme.tres" {
		t.Errorf("resource inventory copy = state=%s kind=%s path=%q", got.State(), got.Kind(), got.Path())
	}
	if got := source.ResolveResource("a.gd", "uid://b"); got.State() != semantic.ResourceFound || got.Kind() != semantic.ResourceText || got.Path() != "theme.tres" {
		t.Errorf("UID claim copy = state=%s kind=%s path=%q reason=%q", got.State(), got.Kind(), got.Path(), got.Reason())
	}
}
