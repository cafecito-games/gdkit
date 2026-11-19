package cfg_test

import (
	"context"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdparser/configfile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/gdkit/cfg"
	"github.com/cafecito-games/gdkit/internal/failure"
)

func TestParseAndQueryProjectGodot(t *testing.T) {
	source := `; Engine configuration file.
config_version=5

[animation]

compatibility/default_parent_skeleton_in_mesh_instance_3d=true

[application]

config/name="Uzir"
config/version="0.1.0+dev"
config/features=PackedStringArray("4.7", "GL Compatibility")
boot_splash/bg_color=Color(0.035, 0.028, 0.022, 1)
boot_splash/show_image=false

[audio]

buses/default_bus_layout="res://assets/audio/default_bus_layout.tres"
general/ios/session_category=3
general/ios/mix_with_others=true

[autoload]

Log="*res://core/log.gd"
Audio="*res://core/audio/app_audio.gd"
`

	document, err := cfg.Parse("project.godot", []byte(source), cfg.Options{})
	require.NoError(t, err)

	cases := map[string]struct {
		filter string
		want   []any
	}{
		"preamble key": {
			filter: `.config_version`,
			want:   []any{5},
		},
		"bracketed section and nested slash keys": {
			filter: `.["animation"].compatibility.default_parent_skeleton_in_mesh_instance_3d`,
			want:   []any{true},
		},
		"dot section and nested slash keys": {
			filter: `.application.config.name`,
			want:   []any{"Uzir"},
		},
		"packed string array as json array": {
			filter: `.application.config.features`,
			want:   []any{[]any{"4.7", "GL Compatibility"}},
		},
		"packed string array iteration": {
			filter: `.application.config.features[]`,
			want:   []any{"4.7", "GL Compatibility"},
		},
		"color constructor as structured object": {
			filter: `.application.boot_splash.bg_color`,
			want: []any{map[string]any{
				"$type": "Color",
				"args":  []any{0.035, 0.028, 0.022, 1},
			}},
		},
		"deeply nested slash keys": {
			filter: `.audio.general.ios`,
			want: []any{map[string]any{
				"mix_with_others":  true,
				"session_category": 3,
			}},
		},
		"jq pipe and keys": {
			filter: `.autoload | keys`,
			want:   []any{[]any{"Audio", "Log"}},
		},
		"jq select and map": {
			filter: `.autoload | to_entries | map(select(.value | contains("audio")) | .key)`,
			want:   []any{[]any{"Audio"}},
		},
		"missing key yields null": {
			filter: `.application.does_not_exist`,
			want:   []any{nil},
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			query, err := cfg.Compile(testCase.filter)
			require.NoError(t, err)
			results, err := query.Run(context.Background(), document)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, results)
		})
	}
}

func TestParseAllVariantExpressions(t *testing.T) {
	source := `[variants]
nothing=null
yes=true
no=false
decimal=42
underscored=1_000_000
hex=0xff
binary=0b1010
huge=999999999999999999999999999999
neg_int=-17
pos_int=+9
neg_huge=-999999999999999999999999999999
float_val=3.5
neg_float=-2.25
pos_float=+1.5
inf_val=inf
neg_inf=-inf
inf_neg_val=inf_neg
neg_inf_neg=-inf_neg
nan_val=nan
plain="hello"
string_name=&"MySignal"
node_path=^"Root/Player"
symbol=KEY_SPACE
neg_symbol=-SOME_FLAG
pos_symbol=+SOME_FLAG
empty_array=[]
empty_packed=PackedStringArray()
packed_ints=PackedInt32Array(1, 2, 3)
typed_array=Array[int]([10, 20])
typed_dict=Dictionary[String, int]({"a": 1, "b": 2})
int_key_dict={0: "zero", 1: "one"}
vector=Vector2(1.5, -2.5)
empty_ctor=Vector2()
input_action={
"deadzone": 0.5,
"events": [Object(InputEventKey, "resource_local_to_scene": false, "device": -1, "physical_keycode": 65)],
}
bare_object=Object(Node)
`

	document, err := cfg.Parse("variants.cfg", []byte(source), cfg.Options{})
	require.NoError(t, err)

	variants := document["variants"].(map[string]any)
	assert.Nil(t, variants["nothing"])
	assert.Equal(t, true, variants["yes"])
	assert.Equal(t, false, variants["no"])
	assert.Equal(t, 42, variants["decimal"])
	assert.Equal(t, 1000000, variants["underscored"])
	assert.Equal(t, 255, variants["hex"])
	assert.Equal(t, 10, variants["binary"])

	wantHuge, ok := new(big.Int).SetString("999999999999999999999999999999", 10)
	require.True(t, ok)
	assert.Equal(t, wantHuge, variants["huge"])
	assert.Equal(t, -17, variants["neg_int"])
	assert.Equal(t, 9, variants["pos_int"])
	assert.Equal(t, new(big.Int).Neg(wantHuge), variants["neg_huge"])

	assert.Equal(t, 3.5, variants["float_val"])
	assert.Equal(t, -2.25, variants["neg_float"])
	assert.Equal(t, 1.5, variants["pos_float"])
	assert.True(t, math.IsInf(variants["inf_val"].(float64), 1))
	assert.True(t, math.IsInf(variants["neg_inf"].(float64), -1))
	assert.True(t, math.IsInf(variants["inf_neg_val"].(float64), -1))
	assert.True(t, math.IsInf(variants["neg_inf_neg"].(float64), 1))
	assert.True(t, math.IsNaN(variants["nan_val"].(float64)))

	assert.Equal(t, "hello", variants["plain"])
	assert.Equal(t, "MySignal", variants["string_name"])
	assert.Equal(t, "Root/Player", variants["node_path"])
	assert.Equal(t, "KEY_SPACE", variants["symbol"])
	assert.Equal(t, "-SOME_FLAG", variants["neg_symbol"])
	assert.Equal(t, "+SOME_FLAG", variants["pos_symbol"])

	assert.Equal(t, []any{}, variants["empty_array"])
	assert.Equal(t, []any{}, variants["empty_packed"])
	assert.Equal(t, []any{1, 2, 3}, variants["packed_ints"])
	assert.Equal(t, []any{10, 20}, variants["typed_array"])
	assert.Equal(t, map[string]any{"a": 1, "b": 2}, variants["typed_dict"])
	assert.Equal(t, map[string]any{"0": "zero", "1": "one"}, variants["int_key_dict"])
	assert.Equal(t, map[string]any{"$type": "Vector2", "args": []any{1.5, -2.5}}, variants["vector"])
	assert.Equal(t, map[string]any{"$type": "Vector2", "args": []any{}}, variants["empty_ctor"])
	assert.Equal(t, map[string]any{
		"$type":      "Object",
		"class":      "Node",
		"properties": map[string]any{},
	}, variants["bare_object"])

	query, err := cfg.Compile(`.variants.input_action.events[] | select(.class == "InputEventKey") | .properties.physical_keycode`)
	require.NoError(t, err)
	results, err := query.Run(context.Background(), document)
	require.NoError(t, err)
	assert.Equal(t, []any{65}, results)
}

func TestFlatOptionPreservesSlashKeys(t *testing.T) {
	source := `[animation]
compatibility/default_parent_skeleton_in_mesh_instance_3d=true
`
	document, err := cfg.Parse("project.godot", []byte(source), cfg.Options{Flat: true})
	require.NoError(t, err)

	query, err := cfg.Compile(`.animation["compatibility/default_parent_skeleton_in_mesh_instance_3d"]`)
	require.NoError(t, err)
	results, err := query.Run(context.Background(), document)
	require.NoError(t, err)
	assert.Equal(t, []any{true}, results)
}

func TestSectionNamesWithDotsRemainLiteral(t *testing.T) {
	source := `[preset.0]
name="iOS"

[preset.0.options]
application/bundle_identifier="net.uzir.client"
`
	document, err := cfg.Parse("export_presets.cfg", []byte(source), cfg.Options{})
	require.NoError(t, err)

	query, err := cfg.Compile(`{name: .["preset.0"].name, bundle: .["preset.0.options"].application.bundle_identifier}`)
	require.NoError(t, err)
	results, err := query.Run(context.Background(), document)
	require.NoError(t, err)
	assert.Equal(t, []any{map[string]any{
		"name":   "iOS",
		"bundle": "net.uzir.client",
	}}, results)
}

func TestDuplicateSectionsAndDuplicateKeys(t *testing.T) {
	source := `[section]
a/b=1
a/c=2

[section]
a/b=10
d=4

[empty]
; comment only
`
	document, err := cfg.Parse("dup.cfg", []byte(source), cfg.Options{})
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"section": map[string]any{
			"a": map[string]any{"b": 10, "c": 2},
			"d": 4,
		},
		"empty": map[string]any{},
	}, document)
}

func TestSlashPrefixCollisionsAreLosslessAndOrderIndependent(t *testing.T) {
	forward := `[section]
a/b=1
a/b/c=2
a/d=3
`
	reverse := `[section]
a/b/c=2
a/b=1
a/d=3
`
	first, err := cfg.Parse("forward.cfg", []byte(forward), cfg.Options{})
	require.NoError(t, err)
	second, err := cfg.Parse("reverse.cfg", []byte(reverse), cfg.Options{})
	require.NoError(t, err)

	want := map[string]any{
		"section": map[string]any{
			"a": map[string]any{
				"b":   1,
				"b/c": 2,
				"d":   3,
			},
		},
	}
	assert.Equal(t, want, first)
	assert.Equal(t, want, second)
}

func TestDistinctVariantDictionaryKeyCollisionsArePreserved(t *testing.T) {
	source := `[dicts]
mixed={1: "numeric", "1": "text"}
deduped_and_mixed={1: "old", 1: "new", "1": "text"}
bool_and_string={true: "bool", "true": "string"}
null_and_string={null: "nil", "null": "string"}
nodepath_and_string={^"same": "node", "same": "string"}
stringname_and_string={&"same": "name", "same": "string"}
identifier_and_string={SAME: "ident", "SAME": "string"}
distinct_objects={Object(InputEventKey, "physical_keycode": 65): "first", Object(InputEventKey, "physical_keycode": 65): "second"}
duplicate_same_key={"a": 1, "a": 2}
non_colliding_ints={0: "zero", 1: "one"}
`
	document, err := cfg.Parse("dicts.cfg", []byte(source), cfg.Options{})
	require.NoError(t, err)

	dicts := document["dicts"].(map[string]any)
	assert.Equal(t, map[string]any{
		"$type": "Dictionary",
		"entries": []any{
			map[string]any{"key": 1, "value": "numeric"},
			map[string]any{"key": "1", "value": "text"},
		},
	}, dicts["mixed"])
	assert.Equal(t, map[string]any{
		"$type": "Dictionary",
		"entries": []any{
			map[string]any{"key": 1, "value": "new"},
			map[string]any{"key": "1", "value": "text"},
		},
	}, dicts["deduped_and_mixed"])
	assert.Equal(t, map[string]any{
		"$type": "Dictionary",
		"entries": []any{
			map[string]any{"key": true, "value": "bool"},
			map[string]any{"key": "true", "value": "string"},
		},
	}, dicts["bool_and_string"])
	assert.Equal(t, map[string]any{
		"$type": "Dictionary",
		"entries": []any{
			map[string]any{"key": nil, "value": "nil"},
			map[string]any{"key": "null", "value": "string"},
		},
	}, dicts["null_and_string"])
	assert.Equal(t, map[string]any{
		"$type": "Dictionary",
		"entries": []any{
			map[string]any{"key": map[string]any{"$type": "NodePath", "value": "same"}, "value": "node"},
			map[string]any{"key": "same", "value": "string"},
		},
	}, dicts["nodepath_and_string"])
	assert.Equal(t, map[string]any{
		"$type": "Dictionary",
		"entries": []any{
			map[string]any{"key": map[string]any{"$type": "StringName", "value": "same"}, "value": "name"},
			map[string]any{"key": "same", "value": "string"},
		},
	}, dicts["stringname_and_string"])
	assert.Equal(t, map[string]any{
		"$type": "Dictionary",
		"entries": []any{
			map[string]any{"key": map[string]any{"$type": "Identifier", "value": "SAME"}, "value": "ident"},
			map[string]any{"key": "SAME", "value": "string"},
		},
	}, dicts["identifier_and_string"])
	eventKey := map[string]any{
		"$type":      "Object",
		"class":      "InputEventKey",
		"properties": map[string]any{"physical_keycode": 65},
	}
	assert.Equal(t, map[string]any{
		"$type": "Dictionary",
		"entries": []any{
			map[string]any{"key": eventKey, "value": "first"},
			map[string]any{"key": eventKey, "value": "second"},
		},
	}, dicts["distinct_objects"])
	assert.Equal(t, map[string]any{"a": 2}, dicts["duplicate_same_key"])
	assert.Equal(t, map[string]any{"0": "zero", "1": "one"}, dicts["non_colliding_ints"])

	roundTrips := []struct {
		key  string
		want string
	}{
		{"mixed", `{1: "numeric", "1": "text"}`},
		{"nodepath_and_string", `{^"same": "node", "same": "string"}`},
		{"stringname_and_string", `{&"same": "name", "same": "string"}`},
		{"identifier_and_string", `{SAME: "ident", "SAME": "string"}`},
		{"distinct_objects", `{Object(InputEventKey, "physical_keycode": 65): "first", Object(InputEventKey, "physical_keycode": 65): "second"}`},
	}
	for _, testCase := range roundTrips {
		formatted, err := cfg.FormatGodot(dicts[testCase.key])
		require.NoError(t, err)
		assert.Equal(t, testCase.want, formatted)
		reparsed, err := cfg.Parse("roundtrip.cfg", []byte("value="+formatted+"\n"), cfg.Options{})
		require.NoError(t, err)
		assert.Equal(t, dicts[testCase.key], reparsed["value"], testCase.key)
	}
}

func TestPreambleKeyCollidingWithSectionNameIsPreserved(t *testing.T) {
	source := `config_version=5
application=1
application/sub=2

[application]
name="Game"
`
	document, err := cfg.Parse("collision.cfg", []byte(source), cfg.Options{})
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"config_version": 5,
		"": map[string]any{
			"application":     1,
			"application/sub": 2,
		},
		"application": map[string]any{
			"name": "Game",
		},
	}, document)
}

func TestQuerySupportsInputAndInputsAcrossDocuments(t *testing.T) {
	first, err := cfg.Parse("a.cfg", []byte("[app]\nname=\"First\"\n"), cfg.Options{})
	require.NoError(t, err)
	second, err := cfg.Parse("b.cfg", []byte("[app]\nname=\"Second\"\n"), cfg.Options{})
	require.NoError(t, err)

	query, err := cfg.Compile(`[., inputs] | map(.app.name)`)
	require.NoError(t, err)
	results, err := query.RunInputs(context.Background(), []any{first, second})
	require.NoError(t, err)
	assert.Equal(t, []any{[]any{"First", "Second"}}, results)

	singleQuery, err := cfg.Compile(`[.app.name, input.app.name]`)
	require.NoError(t, err)
	singleResults, err := singleQuery.RunInputs(context.Background(), []any{first, second})
	require.NoError(t, err)
	assert.Equal(t, []any{[]any{"First", "Second"}}, singleResults)
}

func TestEmptySlashSegmentsStayLiteral(t *testing.T) {
	source := `[section]
"a//b"=1
"/leading"=2
"trailing/"=3
`
	document, err := cfg.Parse("slashes.cfg", []byte(source), cfg.Options{})
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"section": map[string]any{
			"a//b":      1,
			"/leading":  2,
			"trailing/": 3,
		},
	}, document)
}

func TestFormatJSONRawAndGodot(t *testing.T) {
	t.Run("json does not escape html characters", func(t *testing.T) {
		encoded, err := cfg.FormatJSON("<key>A & B</key>", false)
		require.NoError(t, err)
		assert.Equal(t, `"<key>A & B</key>"`, string(encoded))
	})

	t.Run("json pretty vs compact", func(t *testing.T) {
		value := map[string]any{"b": 2, "a": []any{1, true}}
		pretty, err := cfg.FormatJSON(value, false)
		require.NoError(t, err)
		assert.Equal(t, "{\n  \"a\": [\n    1,\n    true\n  ],\n  \"b\": 2\n}", string(pretty))

		compact, err := cfg.FormatJSON(value, true)
		require.NoError(t, err)
		assert.Equal(t, `{"a":[1,true],"b":2}`, string(compact))
	})

	t.Run("raw unquotes strings and formats non-strings as json", func(t *testing.T) {
		rawString, err := cfg.FormatRaw("line1\nline2", false)
		require.NoError(t, err)
		assert.Equal(t, "line1\nline2", string(rawString))

		rawBool, err := cfg.FormatRaw(true, false)
		require.NoError(t, err)
		assert.Equal(t, "true", string(rawBool))
	})

	t.Run("godot formatting round-trips through configfile parser", func(t *testing.T) {
		values := []struct {
			input any
			want  string
		}{
			{nil, "null"},
			{true, "true"},
			{false, "false"},
			{42, "42"},
			{-7, "-7"},
			{1.5, "1.5"},
			{float64(2), "2.0"},
			{math.Inf(1), "inf"},
			{math.Inf(-1), "-inf"},
			{math.NaN(), "nan"},
			{"hello\n\"world\"", `"hello\n\"world\""`},
			{[]any{1, "two"}, `[1, "two"]`},
			{map[string]any{"$type": "Color", "args": []any{0.5, 0.25, 0.1, 1}}, "Color(0.5, 0.25, 0.1, 1)"},
			{map[string]any{
				"$type": "Object",
				"class": "InputEventKey",
				"properties": map[string]any{
					"device":  -1,
					"keycode": 32,
				},
			}, `Object(InputEventKey, "device": -1, "keycode": 32)`},
			{map[string]any{"z": 1, "a": "first"}, `{"a": "first", "z": 1}`},
		}

		for _, testCase := range values {
			formatted, err := cfg.FormatGodot(testCase.input)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, formatted)

			_, parseErr := configfile.ParseString("value=" + formatted + "\n")
			assert.NoError(t, parseErr, "godot output should parse cleanly: %s", formatted)
		}
	})
}

func TestFailureKinds(t *testing.T) {
	t.Run("missing file reports config.read", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing.godot")
		_, err := cfg.ParseFile(missing, cfg.Options{})
		require.Error(t, err)
		labelled, ok := failure.Of(err)
		require.True(t, ok)
		assert.Equal(t, failure.ConfigRead, labelled.Kind)
		assert.Equal(t, missing, labelled.Path)
	})

	t.Run("malformed config reports config.parse", func(t *testing.T) {
		broken := filepath.Join(t.TempDir(), "broken.godot")
		require.NoError(t, os.WriteFile(broken, []byte("[unterminated\n"), 0o644))
		_, err := cfg.ParseFile(broken, cfg.Options{})
		require.Error(t, err)
		labelled, ok := failure.Of(err)
		require.True(t, ok)
		assert.Equal(t, failure.ConfigParse, labelled.Kind)
		assert.Equal(t, broken, labelled.Path)
	})

	t.Run("invalid jq filter reports query.parse", func(t *testing.T) {
		_, err := cfg.Compile(".application | [")
		require.Error(t, err)
		labelled, ok := failure.Of(err)
		require.True(t, ok)
		assert.Equal(t, failure.QueryParse, labelled.Kind)
	})

	t.Run("empty jq filter reports query.parse", func(t *testing.T) {
		_, err := cfg.Compile("   ")
		require.Error(t, err)
		labelled, ok := failure.Of(err)
		require.True(t, ok)
		assert.Equal(t, failure.QueryParse, labelled.Kind)
	})

	t.Run("runtime error reports query.eval", func(t *testing.T) {
		query, err := cfg.Compile(`error("boom")`)
		require.NoError(t, err)
		_, err = query.Run(context.Background(), map[string]any{})
		require.Error(t, err)
		labelled, ok := failure.Of(err)
		require.True(t, ok)
		assert.Equal(t, failure.QueryEval, labelled.Kind)
	})

	t.Run("halt with zero stops cleanly", func(t *testing.T) {
		query, err := cfg.Compile(`1, halt, 2`)
		require.NoError(t, err)
		results, err := query.Run(context.Background(), map[string]any{})
		require.NoError(t, err)
		assert.Equal(t, []any{1}, results)
	})
}

func TestIsTruthy(t *testing.T) {
	assert.False(t, cfg.IsTruthy(nil))
	assert.False(t, cfg.IsTruthy(false))
	assert.True(t, cfg.IsTruthy(true))
	assert.True(t, cfg.IsTruthy(0))
	assert.True(t, cfg.IsTruthy(""))
	assert.True(t, cfg.IsTruthy([]any{}))
	assert.True(t, cfg.IsTruthy(map[string]any{}))
}
