package semantic_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cafecito-games/gdkit/internal/semantic"
	"github.com/cafecito-games/gdkit/internal/semantic/engineschema"
	"github.com/cafecito-games/gdkit/internal/semanticsource"
	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser/ast"
)

func TestResourceReductionUsesRealProjectAndEngineProducers(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		".gdkitignore":        "ignored/\n",
		"project.godot":       "[application]\nconfig/name=\"resource fixture\"\n",
		"actors/enemy.gd":     "class_name Enemy\n",
		"actors/enemy.gd.uid": "uid://a\n",
		"nested/loader.gd":    "class_name Loader\nfunc run():\n\tvar script_path := preload(\"../actors/enemy.gd\")\n\tvar script_escaped := load(\"res://actors/\\u0065nemy.gd\")\n\tvar script_uid := load(\"uid://a\")\n\tvar created := load(\"uid://a\").new()\n\tvar scene_path := load(\"res://scenes/main.tscn\")\n\tvar scene_uid := load(\"uid://b\")\n\tvar scene_instance := load(\"res://scenes/main.tscn\").instantiate()\n\tvar text_path := load(\"res://assets/theme.tres\")\n\tvar text_uid := load(\"uid://c\")\n\tvar import_asset := load(\"res://art/icon.png\")\n\tvar import_uid := load(\"uid://d\")\n\tvar missing := load(\"res://missing.gd\")\n\tvar user := load(\"user://save.gd\")\n\tvar foreign := load(\"custom://thing.gd\")\n\tvar escapes := load(\"../../outside.gd\")\n\tvar ambiguous := load(\"uid://e\")\n\tvar malformed := load(\"uid://z\")\n",
		"scenes/main.tscn":    "[gd_scene format=3 uid=\"uid://b\"]\n",
		"assets/theme.tres":   "[gd_resource type=\"Theme\" format=3 uid=\"uid://c\"]\n",
		"art/icon.png":        "not-a-real-png-but-a-real-import-owner",
		"art/icon.png.import": "[remap]\nuid=\"uid://d\"\n",
		"ignored/one.gd":      "class_name IgnoredOne\n",
		"ignored/one.gd.uid":  "uid://e\n",
		"ignored/two.gd":      "class_name IgnoredTwo\n",
		"ignored/two.gd.uid":  "uid://e\n",
	}
	writeResourceFixture(t, root, files)

	snapshot, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(snapshot.Scripts["nested/loader.gd"].Source); got != files["nested/loader.gd"] {
		t.Fatalf("loader bytes changed while loading: %q", got)
	}
	source := semanticsource.NewSnapshot(snapshot)
	loaded, err := engineschema.LoadEmbedded(4, 7)
	if err != nil {
		t.Fatal(err)
	}
	analyzer := semantic.NewAnalyzer(source, loaded.Engine)
	file := source.File("nested/loader.gd")
	if file == nil {
		t.Fatal("real project loader did not retain nested/loader.gd")
	}

	for _, name := range []string{"script_path", "script_escaped", "script_uid"} {
		t.Run(name, func(t *testing.T) {
			assertResourceClass(t, analyzer.TypeOf(resourceVariableValue(t, file, name)), "actors/enemy.gd", true)
		})
	}
	assertResourceClass(t, analyzer.TypeOf(resourceVariableValue(t, file, "created")), "actors/enemy.gd", false)
	for _, name := range []string{"scene_path", "scene_uid"} {
		t.Run(name, func(t *testing.T) {
			assertResourceClass(t, analyzer.TypeOf(resourceVariableValue(t, file, name)), "PackedScene", false)
		})
	}
	assertResourceClass(t, analyzer.TypeOf(resourceVariableValue(t, file, "scene_instance")), "Node", false)
	for _, name := range []string{"text_path", "text_uid"} {
		t.Run(name, func(t *testing.T) {
			assertResourceClass(t, analyzer.TypeOf(resourceVariableValue(t, file, name)), "Resource", false)
		})
	}
	for _, name := range []string{"import_asset", "import_uid", "missing", "user", "foreign", "escapes", "ambiguous", "malformed"} {
		t.Run(name, func(t *testing.T) {
			got := analyzer.TypeOf(resourceVariableValue(t, file, name))
			if got.Kind() != semantic.KindUnknown || strings.TrimSpace(got.Reason()) == "" {
				t.Fatalf("TypeOf(%s) = %s (%q), want reasoned Unknown", name, got, got.Reason())
			}
			if strings.Contains(got.Reason(), root) {
				t.Fatalf("TypeOf(%s) leaked host root in %q", name, got.Reason())
			}
		})
	}

	// Resource answers enter the same completed-result cache as all other
	// reductions. Exercise a cold analyzer concurrently so the immutable
	// snapshot resolver is covered by the race build and by first-publication.
	const readers = 24
	cold := semantic.NewAnalyzer(source, loaded.Engine)
	start := make(chan struct{})
	problems := make(chan string, readers)
	var done sync.WaitGroup
	done.Add(readers)
	expression := resourceVariableValue(t, file, "script_uid")
	for reader := 0; reader < readers; reader++ {
		go func() {
			defer done.Done()
			<-start
			got := cold.TypeOf(expression)
			if got.Kind() != semantic.KindClass || got.Name() != "actors/enemy.gd" || !got.Meta() {
				problems <- got.String()
			}
		}()
	}
	close(start)
	done.Wait()
	close(problems)
	for problem := range problems {
		t.Errorf("concurrent resource reduction = %s, want actors/enemy.gd meta class", problem)
	}
}

func TestResourceReductionDistinguishesRelativePreloadAndLoad(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"project.godot":    "[application]\nconfig/name=\"relative resource fixture\"\n",
		"target.gd":        "class_name RootTarget\n",
		"nested/target.gd": "class_name NestedTarget\n",
		"nested/loader.gd": "class_name Loader\nfunc run():\n\tvar preloaded := preload(\"target.gd\")\n\tvar loaded := load(\"target.gd\")\n",
	}
	writeResourceFixture(t, root, files)

	snapshot, err := project.Load(project.Config{Root: root, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	source := semanticsource.NewSnapshot(snapshot)
	loaded, err := engineschema.LoadEmbedded(4, 7)
	if err != nil {
		t.Fatal(err)
	}
	analyzer := semantic.NewAnalyzer(source, loaded.Engine)
	file := source.File("nested/loader.gd")
	if file == nil {
		t.Fatal("real project loader did not retain nested/loader.gd")
	}
	assertResourceClass(t, analyzer.TypeOf(resourceVariableValue(t, file, "preloaded")), "nested/target.gd", true)
	assertResourceClass(t, analyzer.TypeOf(resourceVariableValue(t, file, "loaded")), "target.gd", true)
}

func TestResourceReductionFailsClosedForRealParseFailedScriptTarget(t *testing.T) {
	root := t.TempDir()
	writeResourceFixture(t, root, map[string]string{
		"loader.gd": "class_name Loader\nfunc run():\n\tvar broken := load(\"res://broken.gd\")\n",
		"broken.gd": "func (((\n",
	})
	snapshot, err := project.Load(project.Config{Root: root, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	foundInventory := false
	for _, resource := range snapshot.Resources {
		if resource.Path == "broken.gd" && resource.Kind == project.ResourceScript {
			foundInventory = true
		}
	}
	if !foundInventory || snapshot.Scripts["broken.gd"].ParseError == nil {
		t.Fatalf("fixture did not retain parse-failed script evidence: resources=%v script=%#v", snapshot.Resources, snapshot.Scripts["broken.gd"])
	}
	loaded, err := engineschema.LoadEmbedded(4, 7)
	if err != nil {
		t.Fatal(err)
	}
	source := semanticsource.NewSnapshot(snapshot)
	file := source.File("loader.gd")
	resolved := source.ResolveLoadResource("loader.gd", "res://broken.gd")
	if resolved.State() != semantic.ResourceFound || resolved.Kind() != semantic.ResourceScript || resolved.Path() != "broken.gd" {
		t.Fatalf("parse-failed resource inventory = state=%s kind=%s path=%q reason=%q", resolved.State(), resolved.Kind(), resolved.Path(), resolved.Reason())
	}
	got := semantic.NewAnalyzer(source, loaded.Engine).TypeOf(resourceVariableValue(t, file, "broken"))
	if got.Kind() != semantic.KindUnknown || strings.TrimSpace(got.Reason()) == "" {
		t.Fatalf("parse-failed script target = %s (%q), want reasoned Unknown", got, got.Reason())
	}
}

func writeResourceFixture(t *testing.T, root string, files map[string]string) {
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

func resourceVariableValue(t *testing.T, file *ast.File, name string) ast.Expression {
	t.Helper()
	var declarations []*ast.VariableDeclaration
	ast.Inspect(file, func(node ast.Node) bool {
		declaration, ok := node.(*ast.VariableDeclaration)
		if ok && declaration.Name == name {
			declarations = append(declarations, declaration)
		}
		return true
	})
	if len(declarations) != 1 || declarations[0].Value == nil {
		t.Fatalf("variable %q declarations = %#v, want one initialized declaration", name, declarations)
	}
	return declarations[0].Value
}

func assertResourceClass(t *testing.T, got semantic.Type, name string, meta bool) {
	t.Helper()
	if got.Kind() != semantic.KindClass || got.Name() != name || got.Meta() != meta {
		t.Fatalf("type = %s (%q), want class %q meta=%t", got, got.Reason(), name, meta)
	}
}
