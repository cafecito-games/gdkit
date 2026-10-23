package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// referenceTexts returns the references collected from one file, in order.
func referenceTexts(snapshot *Snapshot, path string) []string {
	var texts []string
	for _, link := range snapshot.Links {
		if link.Path == path {
			texts = append(texts, link.Reference)
		}
	}
	return texts
}

func TestInventoryIsOffByDefault(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"project.godot": "[application]\nrun/main_scene=\"res://main.tscn\"\n",
		"main.tscn":     "[gd_scene format=3 uid=\"uid://a\"]\n",
	})

	snapshot, err := Load(Config{Root: root})
	require.NoError(t, err)
	assert.Empty(t, snapshot.Files)
	assert.Empty(t, snapshot.Links)
}

func TestInventoryListsEveryFileAndAttributesSidecars(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"project.godot":       "[application]\n",
		"art/hero.png":        "\x89PNG",
		"art/hero.png.import": "[remap]\nuid=\"uid://b\"\n",
		"art/hero.png.uid":    "uid://b\n",
		"notes.md":            "# notes\n",
		"main.gd":             "extends Node\n",
	})

	snapshot, err := Load(Config{Root: root, Inventory: true})
	require.NoError(t, err)
	// A sidecar belongs to the file beside it: hero.png is one asset, not three.
	assert.Equal(t, []string{"art/hero.png", "main.gd", "notes.md", "project.godot"}, snapshot.Files)
}

func TestInventoryCollectsReferencesByPathEvenWithoutAUID(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"project.godot": "[application]\n",
		"ui/hud.tscn": "[gd_scene load_steps=3 format=3 uid=\"uid://hud\"]\n\n" +
			"[ext_resource type=\"Texture2D\" path=\"res://art/heart.png\" id=\"1_a\"]\n" +
			"[ext_resource type=\"Script\" uid=\"uid://script\" path=\"res://ui/hud.gd\" id=\"2_b\"]\n",
		"art/heart.png": "\x89PNG",
		"ui/hud.gd":     "extends Control\n",
	})

	snapshot, err := Load(Config{Root: root, Inventory: true})
	require.NoError(t, err)
	// The path is canonical where a reference carries one, including the
	// reference that also carries an identity.
	assert.Equal(t, []string{"res://art/heart.png", "res://ui/hud.gd"}, referenceTexts(snapshot, "ui/hud.tscn"))
}

func TestInventoryCollectsResourceStringsOutsideExtResource(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"project.godot": "[application]\n",
		"data/wave.tres": "[gd_resource type=\"Resource\" format=3 uid=\"uid://w\"]\n\n" +
			"[resource]\n" +
			"spawn_table = [\"res://data/goblin.tres\", \"res://data/orc.tres\"]\n",
		"data/goblin.tres": "[gd_resource type=\"Resource\" format=3 uid=\"uid://g\"]\n",
		"data/orc.tres":    "[gd_resource type=\"Resource\" format=3 uid=\"uid://o\"]\n",
	})

	snapshot, err := Load(Config{Root: root, Inventory: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"res://data/goblin.tres", "res://data/orc.tres"},
		referenceTexts(snapshot, "data/wave.tres"))
}

func TestInventoryCollectsScriptStringsOutsideLoadCalls(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"project.godot": "[application]\n",
		"main.gd": "extends Node\n" +
			"const SCENE := preload(\"res://ui/hud.tscn\")\n" +
			"func read() -> void:\n" +
			"\tFileAccess.open(\"res://data/waves.json\", FileAccess.READ)\n",
	})

	snapshot, err := Load(Config{Root: root, Inventory: true})
	require.NoError(t, err)
	// A path handed to something other than load is still a dependency.
	assert.Equal(t, []string{"res://ui/hud.tscn", "res://data/waves.json"},
		referenceTexts(snapshot, "main.gd"))
}

func TestInventoryCollectsManifestRootsWithoutAKeyList(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"project.godot": "[application]\n" +
			"run/main_scene=\"res://main.tscn\"\n" +
			"config/icon=\"res://icon.svg\"\n\n" +
			"[autoload]\n" +
			"Settings=\"*res://autoload/settings.gd\"\n\n" +
			"[audio]\n" +
			"buses/default_bus_layout=\"res://audio/buses.tres\"\n",
		"main.tscn":            "[gd_scene format=3]\n",
		"icon.svg":             "<svg/>",
		"autoload/settings.gd": "extends Node\n",
		"audio/buses.tres":     "[gd_resource type=\"AudioBusLayout\" format=3]\n",
	})

	snapshot, err := Load(Config{Root: root, Inventory: true})
	require.NoError(t, err)
	// The bus layout sits under a key nothing in gdkit names, which is the
	// point: the manifest is read for its values, not for a list of keys.
	assert.Equal(t, []string{
		"res://main.tscn", "res://icon.svg", "res://autoload/settings.gd", "res://audio/buses.tres",
	}, referenceTexts(snapshot, ManifestFileName))
}

func TestInventoryCollectsShaderIncludes(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"project.godot":              "[application]\n",
		"shaders/water.gdshader":     "shader_type spatial;\n#include \"common.gdshaderinc\"\n#include \"res://shaders/noise.gdshaderinc\"\n",
		"shaders/common.gdshaderinc": "// common\n",
		"shaders/noise.gdshaderinc":  "// noise\n",
	})

	snapshot, err := Load(Config{Root: root, Inventory: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"common.gdshaderinc", "res://shaders/noise.gdshaderinc"},
		referenceTexts(snapshot, "shaders/water.gdshader"))
}

func TestInventoryMarksInferredConfigReferencesInexplicit(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"project.godot":           "[application]\n",
		"addons/thing/plugin.cfg": "[plugin]\nname=\"Thing\"\nversion=\"1.0\"\nscript=\"plugin.gd\"\n",
		"addons/thing/plugin.gd":  "@tool\nextends EditorPlugin\n",
	})

	snapshot, err := Load(Config{Root: root, Inventory: true})
	require.NoError(t, err)
	explicit := map[string]bool{}
	for _, link := range snapshot.Links {
		if link.Path == "addons/thing/plugin.cfg" {
			explicit[link.Reference] = link.Explicit
		}
	}
	// A relative script= keeps its target alive, and a version that merely
	// reads as a path must never be the evidence that a file is missing.
	assert.Equal(t, map[string]bool{"plugin.gd": false, "1.0": false}, explicit)
}
