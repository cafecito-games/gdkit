package project

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const godotThemeBinaryFixture = "UlNSQwAAAAAAAAAABAAAAAcAAAAGAAAABgAAAFRoZW1lAAAAAAAAAAAAAwAAAPzo8aEmmiAKAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAGAAAAGAAAAHJlc291cmNlX2xvY2FsX3RvX3NjZW5lAA4AAAByZXNvdXJjZV9uYW1lABMAAABkZWZhdWx0X2Jhc2Vfc2NhbGUADQAAAGRlZmF1bHRfZm9udAASAAAAZGVmYXVsdF9mb250X3NpemUABwAAAHNjcmlwdAAAAAAAAQAAABQAAABsb2NhbDovL1RoZW1lX2I3NGl2AAUBAAAAAAAABgAAAFRoZW1lAAEAAAAFAAAAAQAAAFJTUkM="

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadDiscoversParsesAndExcludes(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"player.gd":           "var speed := 1\n",
		"nested/enemy.gd":     "var health := 2\n",
		"addons/plugin/no.gd": "var ignored := 3\n",
		"nested/enemy.gd.uid": "uid://abc123\n",
		"notes.txt":           "not gdscript\n",
	})

	snapshot, err := Load(Config{Root: root, SourceRoots: []string{"."}, Exclude: []string{"addons/**"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"nested/enemy.gd", "player.gd"}
	if !slices.Equal(snapshot.Paths, want) {
		t.Fatalf("Paths = %v, want %v", snapshot.Paths, want)
	}
	script := snapshot.Scripts["player.gd"]
	if script == nil || script.File == nil || script.ParseError != nil {
		t.Fatalf("player.gd did not parse: %#v", script)
	}
	if string(script.Source) != "var speed := 1\n" {
		t.Errorf("Source = %q", script.Source)
	}
	if len(script.Lines) != 1 {
		t.Errorf("Lines = %v, want one line start", script.Lines)
	}
	if snapshot.UIDs["uid://abc123"] != "nested/enemy.gd" {
		t.Errorf("UIDs = %v", snapshot.UIDs)
	}
}

func TestLoadPublishesSortedResourceInventory(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"scripts/loader.gd":   "extends Node\n",
		"scripts/broken.gd":   "func (((\n",
		"world/main.tscn":     "[gd_scene format=3]\n",
		"theme.tres":          "[gd_resource type=\"Theme\" format=3]\n",
		"art/icon.png":        "binary",
		"art/icon.png.import": "[remap]\nimporter=\"texture\"\n",
		"notes.txt":           "not a Godot resource owner",
	})

	snapshot, err := Load(Config{Root: root, SourceRoots: []string{"."}, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.IdentityEvidence {
		t.Fatal("identity evidence request was not retained")
	}
	want := []Resource{
		{Path: "art/icon.png", Kind: ResourceImported},
		{Path: "scripts/broken.gd", Kind: ResourceScript},
		{Path: "scripts/loader.gd", Kind: ResourceScript},
		{Path: "theme.tres", Kind: ResourceText},
		{Path: "world/main.tscn", Kind: ResourceScene},
	}
	if !slices.Equal(snapshot.Resources, want) {
		t.Fatalf("Resources = %#v, want %#v", snapshot.Resources, want)
	}
}

func TestLoadKeepsIgnoredResourceOwnersOutOfInventoryInEveryIdentityMode(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".gdkitignore":           "hidden/\n",
		"loader.gd":              "class_name Loader\n",
		"hidden/script.gd":       "class_name Hidden\n",
		"hidden/script.gd.uid":   "uid://b\n",
		"hidden/scene.tscn":      "[gd_scene format=3 uid=\"uid://c\"]\n",
		"hidden/theme.tres":      "[gd_resource type=\"Theme\" format=3 uid=\"uid://d\"]\n",
		"hidden/icon.png":        "binary",
		"hidden/icon.png.import": "[remap]\nuid=\"uid://e\"\n",
	})
	want := []Resource{{Path: "loader.gd", Kind: ResourceScript}}
	for _, identities := range []bool{false, true} {
		t.Run(fmt.Sprintf("identities=%t", identities), func(t *testing.T) {
			snapshot, err := Load(Config{Root: root, HonorIgnoreFile: true, Identities: identities})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(snapshot.Resources, want) {
				t.Fatalf("Resources = %#v, want ignored owners excluded regardless of Identities", snapshot.Resources)
			}
			if identities && len(snapshot.Claims) != 4 {
				t.Fatalf("Claims = %#v, want all four ignored declarations retained", snapshot.Claims)
			}
		})
	}
}

func TestLoadKeepsGeneratedMetadataOutOfInventoryInEveryIdentityMode(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"loader.gd":          "class_name Loader\n",
		".godot/cache.gd":    "class_name Cache\n",
		".godot/scene.tscn":  "[gd_scene format=3]\n",
		".git/hooks/tool.gd": "class_name Hook\n",
		".git/theme.tres":    "[gd_resource type=\"Theme\" format=3]\n",
	})
	want := []Resource{{Path: "loader.gd", Kind: ResourceScript}}
	for _, identities := range []bool{false, true} {
		t.Run(fmt.Sprintf("identities=%t", identities), func(t *testing.T) {
			snapshot, err := Load(Config{Root: root, Identities: identities})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(snapshot.Resources, want) {
				t.Fatalf("Resources = %#v, want generated metadata excluded regardless of Identities", snapshot.Resources)
			}
		})
	}
}

func TestLoadMarksIdentityEvidenceIncompleteWhenCoverageIsNarrowed(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"included/loader.gd":   "class_name Loader\n",
		"outside/other.gd":     "class_name Other\n",
		"outside/other.gd.uid": "uid://b\n",
	})
	for _, config := range []Config{
		{Root: root, SourceRoots: []string{"included"}, Identities: true},
		{Root: root, Exclude: []string{"outside/**"}, Identities: true},
	} {
		snapshot, err := Load(config)
		if err != nil {
			t.Fatal(err)
		}
		if !snapshot.IdentityEvidence || !snapshot.IdentityIncomplete {
			t.Fatalf("identity capture = requested:%t incomplete:%t, want requested but incomplete for narrowed discovery", snapshot.IdentityEvidence, snapshot.IdentityIncomplete)
		}
	}
}

func TestLoadMarksUnreadableIdentityClaimEvidenceIncomplete(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "unreadable.gd.uid")
	writeFiles(t, root, map[string]string{
		"loader.gd":         "class_name Loader\n",
		"unreadable.gd":     "class_name Unreadable\n",
		"unreadable.gd.uid": "uid://b\n",
	})
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	snapshot, err := Load(Config{Root: root, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.IdentityEvidence || !snapshot.IdentityIncomplete {
		t.Fatalf("identity capture = requested:%t incomplete:%t, want unreadable claimant evidence to be incomplete", snapshot.IdentityEvidence, snapshot.IdentityIncomplete)
	}
}

func TestLoadMarksUnscannableIdentityClaimEvidenceIncomplete(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"loader.gd":     "class_name Loader\n",
		"oversize.tscn": strings.Repeat("x", maxResourceLine+1) + "\n",
	})
	snapshot, err := Load(Config{Root: root, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.IdentityEvidence || !snapshot.IdentityIncomplete {
		t.Fatalf("identity capture = requested:%t incomplete:%t, want oversized claim source to be incomplete", snapshot.IdentityEvidence, snapshot.IdentityIncomplete)
	}
}

func TestLoadMarksLeadingCommentUnscannableIdentityClaimEvidenceIncomplete(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"loader.gd":     "class_name Loader\n",
		"oversize.tscn": "; Godot permits leading comments\n" + strings.Repeat("x", maxResourceLine+1) + "\n",
	})
	snapshot, err := Load(Config{Root: root, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.IdentityEvidence || !snapshot.IdentityIncomplete {
		t.Fatalf("identity capture = requested:%t incomplete:%t, want an unreadable post-comment header to remain incomplete", snapshot.IdentityEvidence, snapshot.IdentityIncomplete)
	}
}

func TestLoadMarksRealGodotBinaryResourceClaimEvidenceIncomplete(t *testing.T) {
	// These are byte-for-byte outputs from Godot
	// v4.7.2.stable.cafecito_e76255129.ed1daf0bf: ResourceSaver.save followed
	// by ResourceSaver.set_uid for a Resource (.res), PackedScene (.scn), and
	// Theme (.theme). They are intentionally opaque here: this loader must not
	// claim complete UID evidence when a real binary resource can carry its own
	// UID, regardless of its resource-base extension.
	fixtures := []struct {
		path string
		data string
	}{
		{
			path: "duplicate.res",
			data: "UlNSQwAAAAAAAAAABAAAAAcAAAAGAAAACQAAAFJlc291cmNlAAAAAAAAAAAAAwAAAH7QHZsmWj5hAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAADAAAAGAAAAHJlc291cmNlX2xvY2FsX3RvX3NjZW5lAA4AAAByZXNvdXJjZV9uYW1lAAcAAABzY3JpcHQAAAAAAAEAAAAXAAAAbG9jYWw6Ly9SZXNvdXJjZV9icm84aADNAAAAAAAAAAkAAABSZXNvdXJjZQABAAAAAgAAAAEAAABSU1JD",
		},
		{
			path: "duplicate.scn",
			data: "UlNSQwAAAAAAAAAABAAAAAcAAAAGAAAADAAAAFBhY2tlZFNjZW5lAAAAAAAAAAAAAwAAADRLcSzKYVliAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEAAAAGAAAAHJlc291cmNlX2xvY2FsX3RvX3NjZW5lAA4AAAByZXNvdXJjZV9uYW1lAAkAAABfYnVuZGxlZAAHAAAAc2NyaXB0AAAAAAABAAAAGgAAAGxvY2FsOi8vUGFja2VkU2NlbmVfM3Z1aGUA4AAAAAAAAAAMAAAAUGFja2VkU2NlbmUAAQAAAAMAAAABAAAAUlNSQw==",
		},
		{
			path: "duplicate.theme",
			data: godotThemeBinaryFixture,
		},
		{
			path: "compressed.theme",
			data: "UlNDQwIAAAAAEAAAHAEAAKoAAAAotS/9YBwABQUAgsgdJ5A5jQG7/////z/uxSDIavht2YhT2t5Fg/WlwFIEto0MU7yzFhVJATMkhyRgmBOKiIoSghb/4WeOkgpzq5Nqs3C84DRYX3Obug2S8WVt0shvWK12EdZL3SaM+ZqDL9VhGVzFfsTf3HgeSsXiXH7Aain1aH7nD3wPAFml0FgAfwyK2sFaG3kQjocpmkvT7CWdhstP7gGYj3tl5w4GsFJTQ0M=",
		},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.path, func(t *testing.T) {
			root := t.TempDir()
			data, err := base64.StdEncoding.DecodeString(fixture.data)
			if err != nil {
				t.Fatal(err)
			}
			if len(data) < 4 || (string(data[:4]) != "RSRC" && string(data[:4]) != "RSCC") {
				t.Fatalf("producer fixture %s has unexpected binary resource magic", fixture.path)
			}
			writeFiles(t, root, map[string]string{"loader.gd": "class_name Loader\n"})
			path := filepath.Join(root, fixture.path)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			snapshot, err := Load(Config{Root: root, Identities: true})
			if err != nil {
				t.Fatal(err)
			}
			if !snapshot.IdentityEvidence || !snapshot.IdentityIncomplete {
				t.Fatalf("identity capture = requested:%t incomplete:%t, want real binary resource to make opaque UID evidence incomplete", snapshot.IdentityEvidence, snapshot.IdentityIncomplete)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("Load changed real producer bytes for %s", fixture.path)
			}
		})
	}
}

func TestLoadExcludesGeneratedMetadataFromIdentityEvidence(t *testing.T) {
	// This is the exact non-.res Theme producer fixture above. A .godot cache
	// may contain it, but its UID is not part of the project's claimant
	// universe and must not poison a complete project identity capture.
	binary, err := base64.StdEncoding.DecodeString(godotThemeBinaryFixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name    string
		exclude []string
	}{
		{name: "no explicit metadata exclusion"},
		{name: "metadata-only exclusion", exclude: []string{".git/**", ".godot/**"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			writeFiles(t, root, map[string]string{
				"loader.gd":              "class_name Loader\n",
				"target.gd":              "class_name Target\n",
				"target.gd.uid":          "uid://b\n",
				".godot/cache.gd.uid":    "uid://b\n",
				".git/objects/claim.uid": "uid://b\n",
			})
			cachePath := filepath.Join(root, ".godot", "imported", "opaque.theme")
			if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cachePath, binary, 0o644); err != nil {
				t.Fatal(err)
			}

			snapshot, err := Load(Config{Root: root, Exclude: testCase.exclude, Identities: true})
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.IdentityIncomplete {
				t.Fatal("generated metadata made project identity evidence incomplete")
			}
			if len(snapshot.Claims) != 1 || snapshot.Claims[0].Owner != "target.gd" || snapshot.Claims[0].UID != "uid://b" {
				t.Fatalf("Claims = %#v, want only the project claimant", snapshot.Claims)
			}
			if snapshot.UIDs["uid://b"] != "target.gd" {
				t.Fatalf("UIDs = %#v, want project claimant only", snapshot.UIDs)
			}
		})
	}
}

func TestLoadKeepsHeaderClaimEvidenceCompleteAfterReferenceScanFailure(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"loader.gd":  "class_name Loader\n",
		"scene.tscn": "[gd_scene format=3 uid=\"uid://b\"]\n" + strings.Repeat("x", maxResourceLine+1) + "\n",
	})
	snapshot, err := Load(Config{Root: root, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.IdentityIncomplete {
		t.Fatal("a later reference scan failure must not make the already-read header claimant incomplete")
	}
}

func TestResourceKindVocabularyIsClosed(t *testing.T) {
	for _, kind := range []ResourceKind{ResourceScript, ResourceScene, ResourceText, ResourceImported} {
		if kind.String() == "" {
			t.Fatalf("resource kind %d is not closed", kind)
		}
	}
}

func TestLoadReportsParseFailureOnTheScript(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"broken.gd": "func (\n",
		"fine.gd":   "var ok := 1\n",
	})

	snapshot, err := Load(Config{Root: root, SourceRoots: []string{"."}})
	if err != nil {
		t.Fatalf("Load should not fail on an unparseable file: %v", err)
	}
	if snapshot.Scripts["broken.gd"].ParseError == nil {
		t.Error("broken.gd should carry a ParseError")
	}
	if snapshot.Scripts["broken.gd"].File != nil {
		t.Error("broken.gd should have no tree")
	}
	if snapshot.Scripts["fine.gd"].ParseError != nil {
		t.Error("fine.gd should have parsed")
	}
}

func TestLoadRejectsSourceRootOutsideProject(t *testing.T) {
	root := t.TempDir()
	if _, err := Load(Config{Root: root, SourceRoots: []string{".."}}); err == nil {
		t.Error("a source root above the project root must be an error")
	}
}

func TestLineStartsCoverEveryLine(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"a.gd": "var a := 1\nvar b := 2\nvar c := 3\n"})
	snapshot, err := Load(Config{Root: root, SourceRoots: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	lines := snapshot.Scripts["a.gd"].Lines
	if !slices.Equal(lines, []int{0, 11, 22}) {
		t.Errorf("Lines = %v, want [0 11 22]", lines)
	}
}

func loadSingleScript(t *testing.T, source string) *Script {
	t.Helper()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"a.gd": source})
	snapshot, err := Load(Config{Root: root, SourceRoots: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	script := snapshot.Scripts["a.gd"]
	if script == nil {
		t.Fatal("a.gd was not discovered")
	}
	return script
}

func TestScriptLineAndLineCount(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		wantCount int
		wantLines []string
	}{
		{"ordinary", "var a := 1\nvar b := 2\nvar c := 3\n", 3, []string{"var a := 1", "var b := 2", "var c := 3"}},
		{"no trailing newline", "var a := 1\nvar b := 2", 2, []string{"var a := 1", "var b := 2"}},
		{"trailing newline adds no line", "var a := 1\n", 1, []string{"var a := 1"}},
		{"empty file", "", 0, nil},
		{"only a newline", "\n", 1, []string{""}},
		{"blank line in the middle", "a\n\nb\n", 3, []string{"a", "", "b"}},
		{"trailing blank line", "a\n\n", 2, []string{"a", ""}},
		{"crlf", "var a := 1\r\nvar b := 2\r\n", 2, []string{"var a := 1", "var b := 2"}},
		{"crlf without trailing newline", "a\r\nb", 2, []string{"a", "b"}},
		{"trailing whitespace is preserved", "a \t\nb\n", 2, []string{"a \t", "b"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := loadSingleScript(t, test.source)
			if got := script.LineCount(); got != test.wantCount {
				t.Fatalf("LineCount() = %d, want %d", got, test.wantCount)
			}
			if len(script.Lines) != script.LineCount() {
				t.Errorf("len(Lines) = %d, LineCount() = %d", len(script.Lines), script.LineCount())
			}
			if test.wantCount > 0 && script.Lines[0] != 0 {
				t.Errorf("Lines[0] = %d, want 0", script.Lines[0])
			}
			for index, want := range test.wantLines {
				if got := script.Line(index + 1); got != want {
					t.Errorf("Line(%d) = %q, want %q", index+1, got, want)
				}
			}
		})
	}
}

func TestScriptLineOutOfRangeReturnsEmpty(t *testing.T) {
	script := loadSingleScript(t, "var a := 1\nvar b := 2\nvar c := 3\n")
	for _, number := range []int{0, -1, 4, 1000} {
		if got := script.Line(number); got != "" {
			t.Errorf("Line(%d) = %q, want empty", number, got)
		}
	}
	empty := loadSingleScript(t, "")
	if got := empty.Line(1); got != "" {
		t.Errorf("Line(1) on an empty file = %q, want empty", got)
	}
}

func loadHonoringIgnoreFile(t *testing.T, root string, exclude ...string) *Snapshot {
	t.Helper()
	snapshot, err := Load(Config{Root: root, SourceRoots: []string{"."}, Exclude: exclude, HonorIgnoreFile: true})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestLoadSkipsPathsFromTheIgnoreFile(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".gdkitignore":                      "addons/\n*.pb.gd\n",
		"player.gd":                         "var speed := 1\n",
		"addons/plugin/no.gd":               "var ignored := 2\n",
		"apps/editor/addons/tool/no.gd":     "var ignored := 3\n",
		"apps/editor/main.gd":               "var kept := 4\n",
		"client/protocol/messages.pb.gd":    "var ignored := 5\n",
		"client/protocol/plain.gd":          "var kept := 6\n",
		"apps/editor/addons/tool/README.md": "not gdscript\n",
	})

	snapshot := loadHonoringIgnoreFile(t, root)
	want := []string{"apps/editor/main.gd", "client/protocol/plain.gd", "player.gd"}
	if !slices.Equal(snapshot.Paths, want) {
		t.Fatalf("Paths = %v, want %v", snapshot.Paths, want)
	}
}

func TestLoadReincludesInsideAnIgnoredDirectory(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".gdkitignore":                        "addons/\n!addons/our_plugin/\n",
		"player.gd":                           "var speed := 1\n",
		"addons/third_party/no.gd":            "var ignored := 2\n",
		"addons/our_plugin/plugin.gd":         "var kept := 3\n",
		"addons/our_plugin/nested/helper.gd":  "var kept := 4\n",
		"apps/editor/addons/our_plugin/no.gd": "var ignored := 5\n",
	})

	snapshot := loadHonoringIgnoreFile(t, root)
	want := []string{"addons/our_plugin/nested/helper.gd", "addons/our_plugin/plugin.gd", "player.gd"}
	if !slices.Equal(snapshot.Paths, want) {
		t.Fatalf("Paths = %v, want %v", snapshot.Paths, want)
	}
}

func TestLoadConfigExcludeWinsOverIgnoreFileNegation(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".gdkitignore":                "!addons/our_plugin/\n!vendor/keep.gd\n",
		"player.gd":                   "var speed := 1\n",
		"addons/our_plugin/plugin.gd": "var excluded := 2\n",
		"vendor/keep.gd":              "var excluded := 3\n",
	})

	snapshot := loadHonoringIgnoreFile(t, root, "addons/**", "vendor/*.gd")
	want := []string{"player.gd"}
	if !slices.Equal(snapshot.Paths, want) {
		t.Fatalf("Paths = %v, want %v", snapshot.Paths, want)
	}
}

func TestLoadDoesNotConsultTheIgnoreFileUnlessAsked(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".gdkitignore":        "addons/\n",
		"player.gd":           "var speed := 1\n",
		"addons/plugin/in.gd": "var kept := 2\n",
	})

	snapshot, err := Load(Config{Root: root, SourceRoots: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"addons/plugin/in.gd", "player.gd"}
	if !slices.Equal(snapshot.Paths, want) {
		t.Fatalf("Paths = %v, want %v", snapshot.Paths, want)
	}

	writeFiles(t, root, map[string]string{".gdkitignore": "[unterminated\n"})
	if _, err := Load(Config{Root: root, SourceRoots: []string{"."}}); err != nil {
		t.Fatalf("a malformed ignore file must not matter when it is not honored: %v", err)
	}
}

func TestLoadWithoutAnIgnoreFileIgnoresNothing(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"addons/plugin/in.gd": "var kept := 1\n"})

	snapshot := loadHonoringIgnoreFile(t, root)
	if want := []string{"addons/plugin/in.gd"}; !slices.Equal(snapshot.Paths, want) {
		t.Fatalf("Paths = %v, want %v", snapshot.Paths, want)
	}
}

func TestLoadReadsOnlyTheRootIgnoreFile(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"nested/.gdkitignore": "*.gd\n",
		"nested/enemy.gd":     "var health := 1\n",
	})

	snapshot := loadHonoringIgnoreFile(t, root)
	if want := []string{"nested/enemy.gd"}; !slices.Equal(snapshot.Paths, want) {
		t.Fatalf("Paths = %v, want %v", snapshot.Paths, want)
	}
}

func TestLoadRejectsMalformedIgnoreFile(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".gdkitignore": "addons/\n[unterminated\n",
		"player.gd":    "var speed := 1\n",
	})

	_, err := Load(Config{Root: root, SourceRoots: []string{"."}, HonorIgnoreFile: true})
	if err == nil {
		t.Fatal("a malformed ignore file must be an error")
	}
	if !strings.HasPrefix(err.Error(), "parse .gdkitignore: line 2: ") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsUnreadableIgnoreFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, IgnoreFileName), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Load(Config{Root: root, SourceRoots: []string{"."}, HonorIgnoreFile: true})
	if err == nil {
		t.Fatal("an unreadable ignore file must be an error")
	}
	if !strings.HasPrefix(err.Error(), "read .gdkitignore: ") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadDoesNotRegisterUIDOfIgnoredScript(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".gdkitignore":                       "addons/\n*.pb.gd\n!addons/our_plugin/\n",
		"player.gd":                          "var speed := 1\n",
		"player.gd.uid":                      "uid://player\n",
		"addons/third_party/no.gd":           "var ignored := 2\n",
		"addons/third_party/no.gd.uid":       "uid://thirdparty\n",
		"addons/our_plugin/plugin.gd":        "var kept := 3\n",
		"addons/our_plugin/plugin.gd.uid":    "uid://ourplugin\n",
		"client/protocol/messages.pb.gd":     "var ignored := 4\n",
		"client/protocol/messages.pb.gd.uid": "uid://protocol\n",
	})

	snapshot := loadHonoringIgnoreFile(t, root)
	want := map[string]string{"uid://player": "player.gd", "uid://ourplugin": "addons/our_plugin/plugin.gd"}
	if !maps.Equal(snapshot.UIDs, want) {
		t.Fatalf("UIDs = %v, want %v", snapshot.UIDs, want)
	}
}

func TestParseFailureLocatesParserAndLexerErrors(t *testing.T) {
	cases := map[string]struct {
		source       string
		line, column int
		message      string
	}{
		"parser": {"var a = 1\nvar b = 2\nfunc (:\n", 3, 6, "expected function name"},
		"lexer":  {"var a = 1\nvar s = \"\\x\"\n", 2, 11, `invalid escape "\x" in string`},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			script := loadSingleScript(t, testCase.source)
			if script.ParseError == nil {
				t.Fatal("fixture parsed")
			}
			line, column, message := script.ParseFailure()
			if line != testCase.line || column != testCase.column || message != testCase.message {
				t.Fatalf("ParseFailure() = %d, %d, %q; want %d, %d, %q", line, column, message, testCase.line, testCase.column, testCase.message)
			}
		})
	}
}

func TestParseFailureFallsBackToTheStartOfTheFile(t *testing.T) {
	script := &Script{Path: "a.gd", ParseError: errors.New("opaque failure")}
	if line, column, message := script.ParseFailure(); line != 1 || column != 1 || message != "opaque failure" {
		t.Fatalf("ParseFailure() = %d, %d, %q", line, column, message)
	}
}

func TestLoadRecordsSidecarsItCannotResolve(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"player.gd":           "var speed := 1\n",
		"player.gd.uid":       "uid://abc123\n",
		"broken.gd":           "var health := 2\n",
		"broken.gd.uid":       "nonsense\n",
		"water.gdshader.uid":  "uid://shader\n",
		"nested/enemy.gd":     "var health := 3\n",
		"nested/enemy.gd.uid": "uid://abc123\n",
	})

	snapshot, err := Load(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	want := []Sidecar{
		{Path: "broken.gd.uid", Owner: "broken.gd", Text: "nonsense"},
		{Path: "nested/enemy.gd.uid", Owner: "nested/enemy.gd", Text: "uid://abc123"},
		{Path: "player.gd.uid", Owner: "player.gd", Text: "uid://abc123"},
		{Path: "water.gdshader.uid", Owner: "water.gdshader", Text: "uid://shader"},
	}
	if !slices.Equal(snapshot.Sidecars, want) {
		t.Fatalf("Sidecars = %+v, want %+v", snapshot.Sidecars, want)
	}
	// UIDs resolves an identifier to one path, so the duplicate above leaves
	// a single entry. Sidecars is where both claimants survive.
	if len(snapshot.UIDs) != 2 {
		t.Errorf("UIDs = %v, want two entries", snapshot.UIDs)
	}
}

func TestLoadSkipsSidecarsOfIgnoredFiles(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".gdkitignore":            "addons/\n",
		"player.gd":               "var speed := 1\n",
		"player.gd.uid":           "uid://abc123\n",
		"addons/plugin/no.gd":     "var ignored := 2\n",
		"addons/plugin/no.gd.uid": "uid://ignored\n",
	})

	snapshot, err := Load(Config{Root: root, HonorIgnoreFile: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []Sidecar{{Path: "player.gd.uid", Owner: "player.gd", Text: "uid://abc123"}}
	if !slices.Equal(snapshot.Sidecars, want) {
		t.Fatalf("Sidecars = %+v, want %+v", snapshot.Sidecars, want)
	}
}

func TestLoadIndexesIdentifiersDeclaredInsideResources(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"ui/panel.tscn":       "[gd_scene load_steps=2 format=3 uid=\"uid://dscene\"]\n\n[ext_resource type=\"Script\" uid=\"uid://bother\" path=\"res://ui/panel.gd\" id=\"1\"]\n",
		"ui/theme.tres":       "[gd_resource type=\"Theme\" format=3 uid=\"uid://ctheme\"]\n",
		"art/icon.svg.import": "[remap]\n\nimporter=\"texture\"\nuid=\"uid://bicon\"\npath=\"res://.godot/imported/icon.svg-abc.ctex\"\n\n[deps]\n\nuid=\"uid://bnotthis\"\n",
		"ui/panel.gd.uid":     "uid://bpanel\n",
		"ui/panel.gd":         "extends Control\n",
	})

	snapshot, err := Load(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"uid://dscene": "ui/panel.tscn",
		"uid://ctheme": "ui/theme.tres",
		"uid://bicon":  "art/icon.svg",
		"uid://bpanel": "ui/panel.gd",
	}
	if !maps.Equal(snapshot.UIDs, want) {
		t.Fatalf("UIDs = %v, want %v", snapshot.UIDs, want)
	}
	if len(snapshot.Sidecars) != 1 {
		t.Errorf("Sidecars = %#v, want only the .uid file", snapshot.Sidecars)
	}
}

func TestLoadPrefersSidecarOverResourceHeaderForTheSameIdentifier(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"ui/panel.tscn":   "[gd_scene format=3 uid=\"uid://bshared\"]\n",
		"ui/panel.gd.uid": "uid://bshared\n",
		"ui/panel.gd":     "extends Control\n",
	})

	snapshot, err := Load(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.UIDs["uid://bshared"] != "ui/panel.gd" {
		t.Fatalf("UIDs = %v, want the sidecar owner to win", snapshot.UIDs)
	}
}

func TestLoadSelectionNarrowsSelectedButNotPaths(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"keep.gd":          "class_name Keep\n",
		"addons/vendor.gd": "class_name Vendor\n",
	})
	snapshot, err := Load(Config{
		Root:      root,
		Selection: &Selection{SourceRoots: []string{"."}, Exclude: []string{"addons/**"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Paths) != 2 {
		t.Fatalf("Paths = %v, want both scripts", snapshot.Paths)
	}
	if !slices.Equal(snapshot.Selected, []string{"keep.gd"}) {
		t.Errorf("Selected = %v, want [keep.gd]", snapshot.Selected)
	}
	if snapshot.Scripts["addons/vendor.gd"] == nil {
		t.Error("an unselected script must still be parsed and indexed")
	}
}

func TestLoadWithNoSelectionSelectsEverything(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"a.gd": "class_name A\n"})
	snapshot, err := Load(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(snapshot.Selected, snapshot.Paths) {
		t.Errorf("Selected = %v, want Paths %v", snapshot.Selected, snapshot.Paths)
	}
}

func TestLoadSelectionHonorsTheIgnoreFile(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"keep.gd":      "class_name Keep\n",
		"hidden.gd":    "class_name Hidden\n",
		IgnoreFileName: "hidden.gd\n",
	})
	snapshot, err := Load(Config{
		Root:      root,
		Selection: &Selection{SourceRoots: []string{"."}, HonorIgnoreFile: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(snapshot.Selected, []string{"keep.gd"}) {
		t.Errorf("Selected = %v, want [keep.gd]", snapshot.Selected)
	}
	if snapshot.Scripts["hidden.gd"] == nil {
		t.Error("an ignored script must still be in the snapshot so its class_name stays indexed")
	}
}

func TestLoadSelectionNarrowsBySourceRoot(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"src/a.gd":   "class_name A\n",
		"tools/b.gd": "class_name B\n",
	})
	snapshot, err := Load(Config{
		Root:      root,
		Selection: &Selection{SourceRoots: []string{"src"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(snapshot.Selected, []string{"src/a.gd"}) {
		t.Errorf("Selected = %v, want [src/a.gd]", snapshot.Selected)
	}
	if len(snapshot.Paths) != 2 {
		t.Errorf("Paths = %v, want both", snapshot.Paths)
	}
}

func TestLoadReadsScriptBackedAutoloads(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"game_state.gd": "class_name GameState\nextends Node\n",
		"menu.tscn":     "[gd_scene]\n",
		ManifestFileName: "config_version=5\n\n" +
			"[application]\nconfig/name=\"Demo\"\n\n" +
			"[autoload]\n; a comment\n" +
			"GameState=\"*res://game_state.gd\"\n" +
			"Plain=\"res://game_state.gd\"\n" +
			"Menu=\"*res://menu.tscn\"\n\n" +
			"[rendering]\nquality=1\n",
	})
	snapshot, err := Load(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Autoloads["GameState"]; got != "game_state.gd" {
		t.Errorf("GameState = %q, want game_state.gd", got)
	}
	// The "*" prefix only marks the singleton enabled; it is not part of the path.
	if got := snapshot.Autoloads["Plain"]; got != "game_state.gd" {
		t.Errorf("Plain = %q, want game_state.gd", got)
	}
	// A scene declares no class, so it cannot be extended.
	if _, ok := snapshot.Autoloads["Menu"]; ok {
		t.Error("a scene-backed autoload was recorded")
	}
	// Keys outside [autoload] must not leak in.
	if _, ok := snapshot.Autoloads["quality"]; ok {
		t.Error("a key from another section was recorded")
	}
}

func TestLoadWithNoManifestHasNoAutoloads(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"a.gd": "class_name A\n"})
	snapshot, err := Load(Config{Root: root})
	if err != nil {
		t.Fatalf("a project without a manifest must still load: %v", err)
	}
	if len(snapshot.Autoloads) != 0 {
		t.Errorf("Autoloads = %v, want empty", snapshot.Autoloads)
	}
}

// TestSelectionAdmitsExactlyWhatFilteredDiscoveryWalks pins the equality lint
// depends on when it moves its source_roots, exclude, and .gdkitignore filters
// from discovery into Selection so the semantic analyzer can see every script:
// the broad load's Selected must be the filtered load's Paths, and a
// configuration the filtered load rejects must be rejected identically. A
// divergence means a tool that indexes more than it acts on would report on a
// different set of files than the filtered walk admits -- or, worse, report a
// clean run on a configuration the filtered walk would have failed.
//
// The spellings matter. The configuration layers accept a root written "./src"
// and an exclude pattern that names a directory rather than the files under it,
// and the walk prunes those while a naive per-file match does not.
func TestSelectionAdmitsExactlyWhatFilteredDiscoveryWalks(t *testing.T) {
	tests := []struct {
		name        string
		sourceRoots []string
		exclude     []string
		ignore      string
		wantPaths   []string
		wantError   bool
	}{
		{
			name:        "plain source root",
			sourceRoots: []string{"src"},
			wantPaths:   []string{"src/generated/gen.gd", "src/hidden.gd", "src/keep.gd", "src/nested/keep.gd"},
		},
		{name: "source root written with a leading dot", sourceRoots: []string{"./src"},
			wantPaths: []string{"src/generated/gen.gd", "src/hidden.gd", "src/keep.gd", "src/nested/keep.gd"}},
		{name: "source root written with a trailing slash", sourceRoots: []string{"src/"},
			wantPaths: []string{"src/generated/gen.gd", "src/hidden.gd", "src/keep.gd", "src/nested/keep.gd"}},
		{name: "source root that does not exist", sourceRoots: []string{"nope"}, wantError: true},
		{name: "source root that is a file", sourceRoots: []string{"src/keep.gd"}, wantError: true},
		{
			name:        "recursive exclude",
			sourceRoots: []string{"."},
			exclude:     []string{"**/generated/**", "addons/**"},
			wantPaths:   []string{"src/hidden.gd", "src/keep.gd", "src/nested/keep.gd", "tools/outside.gd"},
		},
		{
			name:        "exclude naming a directory",
			sourceRoots: []string{"."},
			exclude:     []string{"src/generated"},
			wantPaths: []string{
				"addons/vendor.gd", "src/hidden.gd", "src/keep.gd",
				"src/nested/keep.gd", "tools/outside.gd",
			},
		},
		{
			name:        "exclude naming a directory with a trailing slash",
			sourceRoots: []string{"."},
			exclude:     []string{"src/generated/"},
			wantPaths: []string{
				"addons/vendor.gd", "src/hidden.gd", "src/keep.gd",
				"src/nested/keep.gd", "tools/outside.gd",
			},
		},
		{
			name:        "exclude matching one segment",
			sourceRoots: []string{"."},
			exclude:     []string{"src/*"},
			wantPaths:   []string{"addons/vendor.gd", "tools/outside.gd"},
		},
		{
			name:        "ignore file naming a directory",
			sourceRoots: []string{"."},
			ignore:      "src/generated/\n",
			wantPaths: []string{
				"addons/vendor.gd", "src/hidden.gd", "src/keep.gd",
				"src/nested/keep.gd", "tools/outside.gd",
			},
		},
		{
			// The walk visits the source root itself, so a pattern matching the
			// root prunes everything under it -- under every root, since each
			// root's own walk starts by matching it.
			name:        "exclude matching the source root itself",
			sourceRoots: []string{"src", "src/nested"},
			exclude:     []string{"src/nested"},
			wantPaths:   []string{"src/generated/gen.gd", "src/hidden.gd", "src/keep.gd"},
		},
		{
			name:        "all three filters together",
			sourceRoots: []string{"src"},
			exclude:     []string{"**/generated/**"},
			ignore:      "src/hidden.gd\n",
			wantPaths:   []string{"src/keep.gd", "src/nested/keep.gd"},
		},
	}
	// Every file outside the selected set is still a dependency the broad load
	// walks and parses, which is the whole point of moving the filters.
	universe := []string{
		"addons/vendor.gd", "src/generated/gen.gd", "src/hidden.gd",
		"src/keep.gd", "src/nested/keep.gd", "tools/outside.gd",
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeFiles(t, root, map[string]string{
				"src/keep.gd":          "class_name Keep\n",
				"src/nested/keep.gd":   "class_name NestedKeep\n",
				"src/generated/gen.gd": "class_name Gen\n",
				"src/hidden.gd":        "class_name Hidden\n",
				"tools/outside.gd":     "class_name Outside\n",
				"addons/vendor.gd":     "class_name Vendor\n",
			})
			if test.ignore != "" {
				writeFiles(t, root, map[string]string{IgnoreFileName: test.ignore})
			}

			filtered, filteredErr := Load(Config{
				Root:            root,
				SourceRoots:     test.sourceRoots,
				Exclude:         test.exclude,
				HonorIgnoreFile: true,
			})
			broad, broadErr := Load(Config{
				Root: root,
				Selection: &Selection{
					SourceRoots:     test.sourceRoots,
					Exclude:         test.exclude,
					HonorIgnoreFile: true,
				},
			})
			if test.wantError {
				if filteredErr == nil || broadErr == nil {
					t.Fatalf("filtered err = %v, broad err = %v, want both to fail", filteredErr, broadErr)
				}
				if filteredErr.Error() != broadErr.Error() {
					t.Fatalf("broad err = %q, want the filtered err %q", broadErr, filteredErr)
				}
				return
			}
			if filteredErr != nil || broadErr != nil {
				t.Fatalf("filtered err = %v, broad err = %v", filteredErr, broadErr)
			}
			if !slices.Equal(filtered.Paths, test.wantPaths) {
				t.Fatalf("filtered Paths = %v, want %v", filtered.Paths, test.wantPaths)
			}
			if !slices.Equal(broad.Selected, filtered.Paths) {
				t.Fatalf("broad Selected = %v, want filtered Paths %v", broad.Selected, filtered.Paths)
			}
			if !slices.Equal(broad.Paths, universe) {
				t.Fatalf("broad Paths = %v, want the whole universe %v", broad.Paths, universe)
			}
		})
	}
}

// TestSelectionDoesNotExcludeAboveTheSourceRoot is the other bound on matching
// an exclude pattern against a path's directories: the walk starts at the
// source root and never visits anything above it, so a pattern that matches an
// ancestor of the root must prune nothing. Testing every directory from the
// project root down would drop the whole tree here.
func TestSelectionDoesNotExcludeAboveTheSourceRoot(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"addons/mine/src/a.gd": "class_name A\n"})
	sourceRoots := []string{"addons/mine/src"}
	exclude := []string{"addons/*"}

	filtered, err := Load(Config{
		Root:            root,
		SourceRoots:     sourceRoots,
		Exclude:         exclude,
		HonorIgnoreFile: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	broad, err := Load(Config{
		Root: root,
		Selection: &Selection{
			SourceRoots:     sourceRoots,
			Exclude:         exclude,
			HonorIgnoreFile: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(filtered.Paths, []string{"addons/mine/src/a.gd"}) {
		t.Fatalf("filtered Paths = %v, want the script under the source root", filtered.Paths)
	}
	if !slices.Equal(broad.Selected, filtered.Paths) {
		t.Fatalf("broad Selected = %v, want filtered Paths %v", broad.Selected, filtered.Paths)
	}
}

// mountExternal builds the layout Uzir uses to share a Godot addon: a
// directory outside the project, and a directory symlink inside it pointing at
// that directory by a relative path. It returns the external directory.
func mountExternal(t *testing.T, root, logicalMount string, files map[string]string) string {
	t.Helper()
	external := t.TempDir()
	writeFiles(t, external, files)
	absolute := filepath.Join(root, filepath.FromSlash(logicalMount))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, absolute); err != nil {
		t.Skipf("this platform cannot create a directory symlink: %v", err)
	}
	return external
}

// TestFollowDirectorySymlinksIndexesTheMountUnderItsLogicalPath is the
// capability's reason to exist: a repository-managed addon mount enters the
// universe under the path Godot loads it by, and nothing in the snapshot names
// the host directory it actually lives in.
func TestFollowDirectorySymlinksIndexesTheMountUnderItsLogicalPath(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"features/pack_view.gd": "var manifest: PackManifest\n"})
	external := mountExternal(t, root, "addons/worldmap_runtime", map[string]string{
		"pack_manifest.gd":     "class_name PackManifest\n",
		"nested/helper.gd":     "class_name PackHelper\n",
		"worldmap.tscn":        "[gd_scene load_steps=1 format=3 uid=\"uid://mount01\"]\n",
		"pack_manifest.gd.uid": "uid://mount02\n",
	})

	off, err := Load(Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(off.Paths, "addons/worldmap_runtime/pack_manifest.gd") {
		t.Fatalf("the capability is off but the mount was walked: %v", off.Paths)
	}

	snapshot, err := Load(Config{Root: root, FollowDirectorySymlinks: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"addons/worldmap_runtime/nested/helper.gd",
		"addons/worldmap_runtime/pack_manifest.gd",
		"features/pack_view.gd",
	}
	if !slices.Equal(snapshot.Paths, want) {
		t.Fatalf("Paths = %v, want %v", snapshot.Paths, want)
	}
	script := snapshot.Scripts["addons/worldmap_runtime/pack_manifest.gd"]
	if script == nil || script.ParseError != nil || script.Path != "addons/worldmap_runtime/pack_manifest.gd" {
		t.Fatalf("mounted script = %#v", script)
	}
	if string(script.Source) != "class_name PackManifest\n" {
		t.Errorf("Source = %q", script.Source)
	}
	if snapshot.UIDs["uid://mount01"] != "addons/worldmap_runtime/worldmap.tscn" {
		t.Errorf("scene identity = %v", snapshot.UIDs)
	}
	if snapshot.UIDs["uid://mount02"] != "addons/worldmap_runtime/pack_manifest.gd" {
		t.Errorf("sidecar identity = %v", snapshot.UIDs)
	}
	// Logical paths are the only public identity. The external directory is
	// validation evidence and must appear nowhere.
	for _, path := range snapshot.Paths {
		if strings.Contains(path, external) {
			t.Fatalf("Paths leaked the canonical host path: %q", path)
		}
	}
	for _, resource := range snapshot.Resources {
		if strings.Contains(resource.Path, external) {
			t.Fatalf("Resources leaked the canonical host path: %q", resource.Path)
		}
	}
	for identifier, owner := range snapshot.UIDs {
		if strings.Contains(owner, external) {
			t.Fatalf("UIDs[%q] leaked the canonical host path: %q", identifier, owner)
		}
	}
}

// TestFollowDirectorySymlinksFollowsNestedMounts covers a mount that itself
// mounts another external directory. The nested link is authorized by the same
// capability and the walk that reached it, and the deeper target's host
// location must stay as invisible as the first one's.
func TestFollowDirectorySymlinksFollowsNestedMounts(t *testing.T) {
	root := t.TempDir()
	outer := mountExternal(t, root, "addons/outer", map[string]string{"outer.gd": "class_name Outer\n"})
	inner := mountExternal(t, outer, "inner", map[string]string{"deep/inner.gd": "class_name Inner\n"})

	snapshot, err := Load(Config{Root: root, FollowDirectorySymlinks: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"addons/outer/inner/deep/inner.gd", "addons/outer/outer.gd"}
	if !slices.Equal(snapshot.Paths, want) {
		t.Fatalf("Paths = %v, want %v", snapshot.Paths, want)
	}
	for _, path := range snapshot.Paths {
		if strings.Contains(path, outer) || strings.Contains(path, inner) {
			t.Fatalf("Paths leaked a canonical host path: %q", path)
		}
	}
}

// TestFollowDirectorySymlinksKeepsTwoLogicalMountsOfOneTarget is why cycle
// detection is ancestry-scoped rather than a global visited set. Mounting one
// shared addon at two logical paths is a layout Godot loads twice, so both
// logical sources must survive and let the existing duplicate handling see two
// claimants.
func TestFollowDirectorySymlinksKeepsTwoLogicalMountsOfOneTarget(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	writeFiles(t, external, map[string]string{
		"shared.gd":     "class_name Shared\n",
		"shared.gd.uid": "uid://shared1\n",
	})
	for _, mount := range []string{"addons/first", "addons/second"} {
		absolute := filepath.Join(root, filepath.FromSlash(mount))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, absolute); err != nil {
			t.Skipf("this platform cannot create a directory symlink: %v", err)
		}
	}

	snapshot, err := Load(Config{Root: root, FollowDirectorySymlinks: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"addons/first/shared.gd", "addons/second/shared.gd"}
	if !slices.Equal(snapshot.Paths, want) {
		t.Fatalf("Paths = %v, want %v", snapshot.Paths, want)
	}
	owners := []string{}
	for _, sidecar := range snapshot.Sidecars {
		owners = append(owners, sidecar.Owner)
	}
	if !slices.Equal(owners, want) {
		t.Fatalf("Sidecars owners = %v, want both logical claimants %v", owners, want)
	}
}

// TestFollowDirectorySymlinksFailsClosedOnUnprovableTargets pins every row of
// the fail-closed contract that cannot establish target evidence. None of them
// may publish a snapshot, and each error must name the logical link path and
// nothing else.
func TestFollowDirectorySymlinksFailsClosedOnUnprovableTargets(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T, root string)
		want  string
		// composed marks an error the loader writes itself rather than
		// wrapping one from the OS. Those must carry the logical path and
		// nothing else; a wrapped OS error keeps the path it reports, which is
		// the convention the rest of the loader already follows.
		composed bool
	}{
		{
			name: "broken link",
			build: func(t *testing.T, root string) {
				if err := os.Symlink(filepath.Join(root, "absent"), filepath.Join(root, "mount")); err != nil {
					t.Skipf("this platform cannot create a symlink: %v", err)
				}
			},
			want: "resolve symlink mount",
		},
		{
			name: "relative link with no target",
			build: func(t *testing.T, root string) {
				if err := os.Symlink("../../common/godot-addons/worldmap_runtime", filepath.Join(root, "mount")); err != nil {
					t.Skipf("this platform cannot create a symlink: %v", err)
				}
			},
			want: "resolve symlink mount",
		},
		{
			name: "target type cannot be proven",
			build: func(t *testing.T, root string) {
				vault := t.TempDir()
				writeFiles(t, vault, map[string]string{"hidden/hidden.gd": "var x := 1\n"})
				if err := os.Symlink(filepath.Join(vault, "hidden"), filepath.Join(root, "mount")); err != nil {
					t.Skipf("this platform cannot create a symlink: %v", err)
				}
				if err := os.Chmod(vault, 0o000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(vault, 0o755) })
			},
			want: "resolve symlink mount",
		},
		{
			name: "proven directory cannot be read",
			build: func(t *testing.T, root string) {
				external := t.TempDir()
				writeFiles(t, external, map[string]string{"sealed/sealed.gd": "var x := 1\n"})
				sealed := filepath.Join(external, "sealed")
				if err := os.Symlink(sealed, filepath.Join(root, "mount")); err != nil {
					t.Skipf("this platform cannot create a symlink: %v", err)
				}
				if err := os.Chmod(sealed, 0o000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })
			},
			want: "read directory mount",
		},
		{
			name: "direct cycle",
			build: func(t *testing.T, root string) {
				if err := os.Symlink(root, filepath.Join(root, "mount")); err != nil {
					t.Skipf("this platform cannot create a symlink: %v", err)
				}
			},
			want:     "symlink mount closes a directory cycle",
			composed: true,
		},
		{
			name: "cycle through a nested mount",
			build: func(t *testing.T, root string) {
				external := t.TempDir()
				writeFiles(t, external, map[string]string{"here.gd": "var x := 1\n"})
				if err := os.Symlink(external, filepath.Join(root, "mount")); err != nil {
					t.Skipf("this platform cannot create a symlink: %v", err)
				}
				if err := os.Symlink(external, filepath.Join(external, "again")); err != nil {
					t.Skipf("this platform cannot create a symlink: %v", err)
				}
			},
			want:     "symlink mount/again closes a directory cycle",
			composed: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			writeFiles(t, root, map[string]string{"own.gd": "var own := 1\n"})
			testCase.build(t, root)

			off, err := Load(Config{Root: root})
			if err != nil {
				t.Fatalf("the capability is off, so the load must be unaffected: %v", err)
			}
			if !slices.Equal(off.Paths, []string{"own.gd"}) {
				t.Fatalf("capability-off Paths = %v", off.Paths)
			}

			snapshot, err := Load(Config{Root: root, FollowDirectorySymlinks: true})
			if snapshot != nil {
				t.Fatalf("a failed load published a snapshot with Paths = %v", snapshot.Paths)
			}
			if err == nil {
				t.Fatal("want a project-load error")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %q, want it to name %q", err, testCase.want)
			}
			if testCase.composed && strings.Contains(err.Error(), root) {
				t.Fatalf("a composed error leaked an absolute path: %q", err)
			}
		})
	}
}

// TestFollowDirectorySymlinksLeavesProvenNonDirectoriesAlone covers the rows
// the capability deliberately does not widen. A link proven to name a regular
// file or a special node keeps the pre-capability behavior exactly, including
// the identity evidence a skipped entry marks incomplete.
func TestFollowDirectorySymlinksLeavesProvenNonDirectoriesAlone(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"own.gd": "var own := 1\n"})
	external := t.TempDir()
	writeFiles(t, external, map[string]string{"outside.gd": "class_name Outside\n"})
	if err := os.Symlink(filepath.Join(external, "outside.gd"), filepath.Join(root, "linked.gd")); err != nil {
		t.Skipf("this platform cannot create a symlink: %v", err)
	}
	if err := os.Symlink("/dev/null", filepath.Join(root, "device.gd")); err != nil {
		t.Skipf("this platform cannot create a symlink: %v", err)
	}

	for _, identities := range []bool{false, true} {
		snapshot, err := Load(Config{Root: root, FollowDirectorySymlinks: true, Identities: identities})
		if err != nil {
			t.Fatalf("Identities=%v: %v", identities, err)
		}
		if !slices.Equal(snapshot.Paths, []string{"own.gd"}) {
			t.Fatalf("Identities=%v: Paths = %v, want only the project's own script", identities, snapshot.Paths)
		}
		if identities && !snapshot.IdentityIncomplete {
			t.Error("a skipped symlink must keep identity evidence from calling itself exhaustive")
		}
	}
}

// TestFollowDirectorySymlinksRespectsPruningBeforeResolution is what keeps the
// capability from newly failing a load. A broken link the project already
// excludes, or one inside generated metadata, is never resolved, because the
// walk would not have entered the directory it names.
func TestFollowDirectorySymlinksRespectsPruningBeforeResolution(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"own.gd":          "var own := 1\n",
		".gdkitignore":    "hidden/\nignored_mount/\n",
		"hidden/keep.gd":  "var keep := 1\n",
		"present/here.gd": "var here := 1\n",
	})
	// Two shapes have to hold. A link inside a directory the walk prunes is
	// never reached at all, and a link that is *itself* the pruned path is
	// reached and must be skipped before it is resolved — that second one is
	// the only case the walk's own prune gate decides, so without it a broken
	// link a project already excluded would fail a load that passes today.
	for _, mount := range []string{
		"vendor/broken", ".godot/broken", "hidden/broken",
		"addons/worldmap_runtime", "ignored_mount",
	} {
		absolute := filepath.Join(root, filepath.FromSlash(mount))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, "absent"), absolute); err != nil {
			t.Skipf("this platform cannot create a symlink: %v", err)
		}
	}

	snapshot, err := Load(Config{
		Root:                    root,
		FollowDirectorySymlinks: true,
		HonorIgnoreFile:         true,
		Exclude:                 []string{"vendor/**", "addons/worldmap_runtime/**"},
	})
	if err != nil {
		t.Fatalf("a pruned broken link must not fail the load: %v", err)
	}
	want := []string{"own.gd", "present/here.gd"}
	if !slices.Equal(snapshot.Paths, want) {
		t.Fatalf("Paths = %v, want %v", snapshot.Paths, want)
	}
}

// TestFollowDirectorySymlinksDiscoversASourceRootReachedThroughALink closes
// the asymmetry #81 found. A semantic run moves lint's roots into Selection and
// walks the project root, so an intermediate mount that a filtered walk
// resolves through its own root path is an ordinary entry below the walked
// root. With the capability on both shapes discover the same tree, and
// Selected still equals the filtered Paths for that layout.
func TestFollowDirectorySymlinksDiscoversASourceRootReachedThroughALink(t *testing.T) {
	root := t.TempDir()
	mountExternal(t, root, "mounted", map[string]string{
		"src/player.gd":     "class_name Player\n",
		"src/skip/other.gd": "class_name Other\n",
		"docs/notes.gd":     "class_name Notes\n",
	})
	selection := &Selection{SourceRoots: []string{"mounted/src"}, Exclude: []string{"**/skip/**"}}

	filtered, err := Load(Config{
		Root:        root,
		SourceRoots: selection.SourceRoots,
		Exclude:     selection.Exclude,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(filtered.Paths, []string{"mounted/src/player.gd"}) {
		t.Fatalf("the filtered walk through the mount discovered %v", filtered.Paths)
	}

	off, err := Load(Config{Root: root, Selection: selection})
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Selected) != 0 {
		t.Fatalf("capability-off Selected = %v, want the documented pre-change emptiness", off.Selected)
	}

	broad, err := Load(Config{Root: root, FollowDirectorySymlinks: true, Selection: selection})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(broad.Selected, filtered.Paths) {
		t.Fatalf("Selected = %v, want the filtered walk's Paths %v", broad.Selected, filtered.Paths)
	}
	if !slices.Contains(broad.Paths, "mounted/src/skip/other.gd") {
		t.Errorf("the excluded dependency left the universe: %v", broad.Paths)
	}
	if slices.Contains(broad.Selected, "mounted/src/skip/other.gd") {
		t.Errorf("an excluded path entered Selected: %v", broad.Selected)
	}
}

// TestFollowDirectorySymlinksWalksASourceRootThatIsItselfALink records a second
// gap the capability closes. filepath.WalkDir Lstats the walked root, so a
// source root that is itself a link is reported as a non-directory entry and
// its target is never read: the load silently discovers nothing, even though
// Load's own os.Stat check passed. Resolving the root is part of the walk.
func TestFollowDirectorySymlinksWalksASourceRootThatIsItselfALink(t *testing.T) {
	root := t.TempDir()
	mountExternal(t, root, "src", map[string]string{"player.gd": "class_name Player\n"})

	off, err := Load(Config{Root: root, SourceRoots: []string{"src"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Paths) != 0 {
		t.Fatalf("capability-off Paths = %v, want the documented pre-change emptiness", off.Paths)
	}

	snapshot, err := Load(Config{Root: root, SourceRoots: []string{"src"}, FollowDirectorySymlinks: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(snapshot.Paths, []string{"src/player.gd"}) {
		t.Fatalf("Paths = %v", snapshot.Paths)
	}
}

// TestFollowDirectorySymlinksIsDeterministic pins the idempotency the snapshot
// promises: the same bytes and the same symlink graph produce the same
// snapshot, however many times they are loaded and however many loads run at
// once. The concurrent half is what proves the walk's ancestry and seam are
// per-load state rather than anything shared.
func TestFollowDirectorySymlinksIsDeterministic(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"own.gd": "var own := 1\n"})
	external := mountExternal(t, root, "addons/mount", map[string]string{
		"b.gd":       "class_name B\n",
		"a.gd":       "class_name A\n",
		"sub/c.gd":   "class_name C\n",
		"scene.tscn": "[gd_scene format=3 uid=\"uid://det001\"]\n",
	})
	if err := os.Symlink(external, filepath.Join(root, "addons", "twin")); err != nil {
		t.Skipf("this platform cannot create a directory symlink: %v", err)
	}
	config := Config{Root: root, FollowDirectorySymlinks: true, Identities: true}

	first, err := Load(config)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		again, err := Load(config)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(first.Paths, again.Paths) {
			t.Fatalf("Paths differed between loads: %v vs %v", first.Paths, again.Paths)
		}
		if !maps.Equal(first.UIDs, again.UIDs) {
			t.Fatalf("UIDs differed between loads: %v vs %v", first.UIDs, again.UIDs)
		}
		if !slices.Equal(first.Resources, again.Resources) {
			t.Fatalf("Resources differed between loads")
		}
		if !slices.Equal(first.Claims, again.Claims) {
			t.Fatalf("Claims differed between loads")
		}
	}

	results := make([][]string, 4)
	errs := make([]error, len(results))
	done := make(chan int, len(results))
	for index := range results {
		go func() {
			snapshot, err := Load(config)
			if err == nil {
				results[index] = snapshot.Paths
			}
			errs[index] = err
			done <- index
		}()
	}
	for range results {
		<-done
	}
	for index := range results {
		if errs[index] != nil {
			t.Fatalf("concurrent load %d: %v", index, errs[index])
		}
		if !slices.Equal(results[index], first.Paths) {
			t.Fatalf("concurrent load %d produced %v, want %v", index, results[index], first.Paths)
		}
	}
}

// TestLoadFailsClosedWhenAnAcceptedObjectChangesUnderTheWalk drives the two
// boundaries at which the loader has accepted a fact but not yet read the
// bytes it implies. Each case substitutes the object at exactly that instant
// through the instance-scoped seam, so there is no sleep and no dependence on
// the scheduler, and asserts no partial snapshot escapes.
func TestLoadFailsClosedWhenAnAcceptedObjectChangesUnderTheWalk(t *testing.T) {
	cases := []struct {
		name  string
		hooks func(root, external string) loaderHooks
		want  string
	}{
		{
			name: "the accepted target is removed",
			hooks: func(root, external string) loaderHooks {
				return loaderHooks{afterDirectoryAccepted: func(logical string) {
					if logical == "addons/mount" {
						if err := os.RemoveAll(external); err != nil {
							panic(err)
						}
					}
				}}
			},
			want: "read directory addons/mount",
		},
		{
			name: "the accepted target becomes a file",
			hooks: func(root, external string) loaderHooks {
				return loaderHooks{afterDirectoryAccepted: func(logical string) {
					if logical != "addons/mount" {
						return
					}
					if err := os.RemoveAll(external); err != nil {
						panic(err)
					}
					if err := os.WriteFile(external, []byte("not a directory\n"), 0o644); err != nil {
						panic(err)
					}
				}}
			},
			want: "read directory addons/mount",
		},
		{
			name: "the link is retargeted after its target was accepted",
			hooks: func(root, external string) loaderHooks {
				return loaderHooks{afterDirectoryAccepted: func(logical string) {
					if logical != "addons/mount" {
						return
					}
					other, err := os.MkdirTemp("", "retarget")
					if err != nil {
						panic(err)
					}
					mount := filepath.Join(root, "addons", "mount")
					if err := os.Remove(mount); err != nil {
						panic(err)
					}
					if err := os.Symlink(other, mount); err != nil {
						panic(err)
					}
				}}
			},
			want: "read directory addons/mount: target changed during the load",
		},
		{
			// Every file is read by its logical path, so the kernel resolves
			// the mount again at read time. A retarget after the mounted file
			// was read would otherwise leave a snapshot whose paths were
			// attributed to one object and whose bytes came from another.
			name: "the link is retargeted after the load read through it",
			hooks: func(root, external string) loaderHooks {
				return loaderHooks{beforeFileRead: func(logical string) {
					if logical != "own.gd" {
						return
					}
					other, err := os.MkdirTemp("", "retarget")
					if err != nil {
						panic(err)
					}
					mount := filepath.Join(root, "addons", "mount")
					if err := os.Remove(mount); err != nil {
						panic(err)
					}
					if err := os.Symlink(other, mount); err != nil {
						panic(err)
					}
				}}
			},
			want: "verify mount addons/mount: target changed during the load",
		},
		{
			name: "the mount disappears after the load read through it",
			hooks: func(root, external string) loaderHooks {
				return loaderHooks{beforeFileRead: func(logical string) {
					if logical != "own.gd" {
						return
					}
					if err := os.Remove(filepath.Join(root, "addons", "mount")); err != nil {
						panic(err)
					}
				}}
			},
			want: "verify mount addons/mount",
		},
		{
			name: "a discovered script is removed before it is read",
			hooks: func(root, external string) loaderHooks {
				return loaderHooks{beforeFileRead: func(logical string) {
					if logical == "addons/mount/mounted.gd" {
						if err := os.Remove(filepath.Join(external, "mounted.gd")); err != nil {
							panic(err)
						}
					}
				}}
			},
			want: "read addons/mount/mounted.gd",
		},
		{
			name: "a discovered script becomes a directory before it is read",
			hooks: func(root, external string) loaderHooks {
				return loaderHooks{beforeFileRead: func(logical string) {
					if logical != "addons/mount/mounted.gd" {
						return
					}
					target := filepath.Join(external, "mounted.gd")
					if err := os.Remove(target); err != nil {
						panic(err)
					}
					if err := os.Mkdir(target, 0o755); err != nil {
						panic(err)
					}
				}}
			},
			want: "read addons/mount/mounted.gd",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			writeFiles(t, root, map[string]string{"own.gd": "var own := 1\n"})
			external := mountExternal(t, root, "addons/mount", map[string]string{
				"mounted.gd": "class_name Mounted\n",
			})

			snapshot, err := load(
				Config{Root: root, FollowDirectorySymlinks: true},
				testCase.hooks(root, external),
			)
			if snapshot != nil {
				t.Fatalf("a failed load published a snapshot with Paths = %v", snapshot.Paths)
			}
			if err == nil {
				t.Fatal("want a project-load error")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %q, want it to name %q", err, testCase.want)
			}
		})
	}
}

// TestLoadHooksDoNotAffectAnUnaffectedLoad keeps the seam honest: it changes
// when a load observes the filesystem, not what a load that nothing disturbs
// produces.
func TestLoadHooksDoNotAffectAnUnaffectedLoad(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"own.gd": "var own := 1\n"})
	mountExternal(t, root, "addons/mount", map[string]string{"mounted.gd": "class_name Mounted\n"})
	config := Config{Root: root, FollowDirectorySymlinks: true}

	var directories, files []string
	seamed, err := load(config, loaderHooks{
		afterDirectoryAccepted: func(logical string) { directories = append(directories, logical) },
		beforeFileRead:         func(logical string) { files = append(files, logical) },
	})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Load(config)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seamed.Paths, plain.Paths) {
		t.Fatalf("Paths = %v, want %v", seamed.Paths, plain.Paths)
	}
	if !slices.Contains(directories, "addons/mount") {
		t.Errorf("the directory boundary did not report the mount: %v", directories)
	}
	if !slices.Equal(files, plain.Paths) {
		t.Errorf("the file boundary reported %v, want every published path %v", files, plain.Paths)
	}
}
