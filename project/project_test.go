package project

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

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
