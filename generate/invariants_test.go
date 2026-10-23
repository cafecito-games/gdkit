package generate

import (
	"testing"

	"github.com/cafecito-games/gdkit/format"
	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser"
	gdformat "github.com/cafecito-games/gdparser/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtures is every source the invariants run over. Each must opt in, so a
// candidate is actually produced.
var fixtures = map[string]map[string]string{
	"to_string only": {
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\nvar r: int\n",
	},
	"equals only": {
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
	},
	"both": {
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string, equals\nvar q: int\nvar r: int\n",
	},
	"no fields": {
		"a.gd": "class_name Marker\nextends RefCounted\n\n# gdkit:generate = to_string, equals\n",
	},
	"many fields wrapping past line_width": {
		"a.gd": "class_name Wide\nextends RefCounted\n\n# gdkit:generate = equals\n" +
			"var alpha: int\nvar bravo: int\nvar charlie: int\nvar delta: int\nvar echo: int\n" +
			"var foxtrot: int\nvar golf: int\nvar hotel: int\nvar india: int\nvar juliet: int\n",
	},
	"composing subclass": {
		"base.gd": "class_name Base\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n\n# gdkit:generate = equals\nvar r: int\n",
	},
	"no class_name": {
		"a.gd": "extends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n",
	},
	"field named like the parameter": {
		"a.gd": "class_name Odd\nextends RefCounted\n\n# gdkit:generate = to_string, equals\nvar p_other: int\n",
	},
	"field opted out": {
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\n" +
			"var q: int\nvar _cache: Dictionary  # gdkit:generate:ignore-field\n",
	},
	"inner class alongside a generated top level": {
		// Two blank lines before a major declaration: the fixture has to be
		// canonical itself, or the format oracle has nothing to assert.
		"a.gd": "class_name Holder\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n\n\n" +
			"class Nested:\n\tvar r: int\n",
	},
	"deep_equals over a value-object field": withCompanionAddon(map[string]string{
		"coordinate.gd": "class_name Coordinate\nextends RefCounted\n\n# gdkit:generate = equals, deep_equals\nvar q: int\nvar r: int\n",
		"unit.gd":       "class_name Unit\nextends RefCounted\n\n# gdkit:generate = to_string, deep_equals\nvar name: String\nvar position: Coordinate\n",
	}),
	"deep_equals with an untyped field": withCompanionAddon(map[string]string{
		"a.gd": "class_name Bag\nextends RefCounted\n\n# gdkit:generate = deep_equals\nvar loose\nvar tagged: String\n",
	}),
	"property with accessors": {
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\n" +
			"var q: int\nvar doubled: int:\n\tget:\n\t\treturn self.q * 2\n",
	},
}

func TestWriteIsIdempotent(t *testing.T) {
	for name, files := range fixtures {
		t.Run(name, func(t *testing.T) {
			once := applyOnce(t, DefaultConfig(), files)
			twice := applyOnce(t, DefaultConfig(), once)
			assert.Equal(t, once, twice, "a second write changed the files")
		})
	}
}

func TestWriteThenCheckIsClean(t *testing.T) {
	for name, files := range fixtures {
		t.Run(name, func(t *testing.T) {
			written := applyOnce(t, DefaultConfig(), files)
			report := checkProject(t, DefaultConfig(), written)
			assert.False(t, report.HasChanges(), "check after write is not clean")
			assert.False(t, report.HasDiagnostics(), "check after write is not clean")
		})
	}
}

// The oracle as a corpus check, on top of its per-candidate use in verify: a
// generated file must be canonical whenever its source was.
func TestGeneratedFilesAreFormatClean(t *testing.T) {
	options, err := format.DefaultConfig().Options()
	require.NoError(t, err)
	for name, files := range fixtures {
		t.Run(name, func(t *testing.T) {
			written := applyOnce(t, DefaultConfig(), files)
			for path, contents := range written {
				file, err := gdparser.ParseFile(path, []byte(contents))
				require.NoError(t, err, path+" does not parse after generation")
				formatted := gdformat.FileWithOptions(file, options)
				assert.Equal(t, contents, formatted, path+" is not formatted after generation")
			}
		})
	}
}

func TestRemovingTheRegionRegeneratesTheSameBytes(t *testing.T) {
	for name, files := range fixtures {
		t.Run(name, func(t *testing.T) {
			written := applyOnce(t, DefaultConfig(), files)
			stripped := map[string]string{}
			for path, contents := range written {
				span, found, err := FindRegion([]byte(contents))
				require.NoError(t, err)
				if !found {
					stripped[path] = contents
					continue
				}
				stripped[path] = contents[:span.Start] + contents[span.End:]
			}
			again := applyOnce(t, DefaultConfig(), stripped)
			assert.Equal(t, written, again, "regeneration differed")
		})
	}
}

// Every generated file must load in Godot's eyes, which here means it reparses.
func TestGeneratedFilesReparse(t *testing.T) {
	for name, files := range fixtures {
		t.Run(name, func(t *testing.T) {
			for path, contents := range applyOnce(t, DefaultConfig(), files) {
				_, err := gdparser.ParseFile(path, []byte(contents))
				assert.NoError(t, err, path+" does not parse")
			}
		})
	}
}

// A project with no config and no markers must be left completely alone.
func TestAProjectThatOptedIntoNothingIsUntouched(t *testing.T) {
	files := map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\nvar q: int\n",
		"b.gd": "extends Node\n\nfunc _ready() -> void:\n\tpass\n",
	}
	written := applyOnce(t, DefaultConfig(), files)
	assert.Equal(t, files, written, "files were modified without opting in")
}

// Snapshot the emitted region for the headline fixture, so a change to the
// generated text is a deliberate decision rather than a surprise.
func TestGeneratedRegionForTheHeadlineFixture(t *testing.T) {
	written := applyOnce(t, DefaultConfig(), fixtures["both"])
	want := "class_name Hex\n" +
		"extends RefCounted\n" +
		"\n" +
		"# gdkit:generate = to_string, equals\n" +
		"var q: int\n" +
		"var r: int\n" +
		"\n" +
		"\n" +
		"# gdkit:generated:begin\n" +
		"func _to_string() -> String:\n" +
		"\treturn \"Hex(q=%s, r=%s)\" % [self.q, self.r]\n" +
		"\n" +
		"\n" +
		"func equals(p_other: Variant) -> bool:\n" +
		// The formatter drops the redundant parentheses: "is" binds tighter
		// than "not", so this is the same expression, and a parenthesis change
		// is exactly what format's own safety net permits.
		"\tif not p_other is Object:\n" +
		"\t\treturn false\n" +
		"\tif p_other.get_script() != get_script():\n" +
		"\t\treturn false\n" +
		"\treturn self.q == p_other.q and self.r == p_other.r\n" +
		"\n" +
		"\n" +
		"# gdkit:generated:end\n"
	got := written["a.gd"]
	assert.Equal(t, want, got)
}

// Apply must not touch a file that changed under the run, and must not lose
// the concurrent edit.
func TestApplyRefusesAFileThatChangedOnDisk(t *testing.T) {
	root := t.TempDir()
	source := "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n"
	writeInto(t, root, map[string]string{"a.gd": source})
	snapshot, err := project.Load(project.Config{
		Root:      root,
		Selection: &project.Selection{SourceRoots: []string{"."}},
	})
	require.NoError(t, err)
	generator, err := New(DefaultConfig(), format.DefaultConfig())
	require.NoError(t, err)
	plan := generator.Check(snapshot)
	writeInto(t, root, map[string]string{"a.gd": source + "var late: int\n"})
	_, err = Apply(snapshot, plan, false)
	assert.Error(t, err, "Apply overwrote a file that changed under it")
	contents, err := osReadFile(root, "a.gd")
	require.NoError(t, err)
	assert.Contains(t, contents, "var late: int", "the concurrent edit was lost")
}

func TestApplyPrunesAnOrphanedRegionWithItsLeadingGap(t *testing.T) {
	root := t.TempDir()
	source := "class_name Hex\nextends RefCounted\n\nvar q: int\n\n\n" +
		"# gdkit:generated:begin\nfunc _to_string() -> String:\n\treturn \"Hex()\"\n\n\n# gdkit:generated:end\n"
	writeInto(t, root, map[string]string{"a.gd": source})
	snapshot, err := project.Load(project.Config{
		Root:      root,
		Selection: &project.Selection{SourceRoots: []string{"."}},
	})
	require.NoError(t, err)
	generator, err := New(DefaultConfig(), format.DefaultConfig())
	require.NoError(t, err)
	_, err = Apply(snapshot, generator.Check(snapshot), true)
	require.NoError(t, err)
	contents, err := osReadFile(root, "a.gd")
	require.NoError(t, err)
	want := "class_name Hex\nextends RefCounted\n\nvar q: int\n"
	assert.Equal(t, want, contents)
}

// Without --prune an orphan is reported and left exactly as it was.
func TestApplyLeavesAnOrphanAloneWithoutPrune(t *testing.T) {
	source := "class_name Hex\nextends RefCounted\n\nvar q: int\n\n\n" +
		"# gdkit:generated:begin\nfunc _to_string() -> String:\n\treturn \"Hex()\"\n\n\n# gdkit:generated:end\n"
	written := applyOnce(t, DefaultConfig(), map[string]string{"a.gd": source})
	assert.Equal(t, source, written["a.gd"], "an orphan was modified without --prune")
}
