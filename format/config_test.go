package format

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	gdformat "github.com/cafecito-games/gdparser/format"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, root, name, contents string) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(name))
	err := os.MkdirAll(filepath.Dir(absolute), 0o755)
	require.NoError(t, err)
	err = os.WriteFile(absolute, []byte(contents), 0o644)
	require.NoError(t, err)
}

func TestDefaultConfigIsGodotStyle(t *testing.T) {
	config := DefaultConfig()
	err := config.Validate()
	require.NoError(t, err)
	options, err := config.options()
	require.NoError(t, err)
	require.Equal(t, gdformat.GodotStyle(), options)
	require.Equal(t, []string{"."}, config.SourceRoots)
	require.Equal(t, []string{".git/**", ".godot/**", ".gdkit/**", "addons/**"}, config.Exclude)
}

func TestDefaultConfigRoundTripsThroughJSON(t *testing.T) {
	data, err := json.Marshal(DefaultConfig())
	require.NoError(t, err)
	var decoded Config
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	require.Equal(t, DefaultConfig(), decoded)
}

func TestConfigOptionsMapsEveryField(t *testing.T) {
	config := DefaultConfig()
	config.LineWidth = 80
	config.TabWidth = 2
	config.Indent = "spaces"
	config.QuoteStyle = "single"
	config.CommentSpacing = "preserve"
	config.Operators = "preserve"
	config.Numbers = "preserve"
	config.TrailingCommas = "never"
	config.BlankLines = BlankLines{TopLevel: 3, Nested: 2}
	options, err := config.options()
	require.NoError(t, err)
	want := gdformat.Options{
		LineWidth:      80,
		TabWidth:       2,
		Indent:         gdformat.Spaces,
		QuoteStyle:     gdformat.SingleQuotes,
		CommentSpacing: gdformat.PreserveComments,
		Operators:      gdformat.PreserveOperators,
		Numbers:        gdformat.PreserveNumbers,
		TrailingCommas: gdformat.NoTrailingCommas,
		BlankLines:     gdformat.BlankLineLimits{TopLevel: 3, Nested: 2},
	}
	require.Equal(t, want, options)

	config.QuoteStyle = "preserve"
	options, err = config.options()
	require.NoError(t, err)
	require.Equal(t, gdformat.PreserveQuotes, options.QuoteStyle)
}

func TestLoadConfigReturnsDefaultsWhenDefaultFileIsMissing(t *testing.T) {
	config, err := LoadConfig(t.TempDir(), "")
	require.NoError(t, err)
	require.Equal(t, DefaultConfig(), config)
}

func TestLoadConfigFailsWhenNamedFileIsMissing(t *testing.T) {
	_, err := LoadConfig(t.TempDir(), "missing.json")
	require.Error(t, err)
	require.ErrorContains(t, err, "read format config:")
}

func TestLoadConfigInheritsOmittedFields(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, DefaultConfigPath, `{"line_width": 80, "quote_style": "single", "blank_lines": {"nested": 2}}`)
	config, err := LoadConfig(root, "")
	require.NoError(t, err)
	want := DefaultConfig()
	want.LineWidth = 80
	want.QuoteStyle = "single"
	want.BlankLines.Nested = 2
	require.Equal(t, want, config)
}

func TestLoadConfigReadsNamedFileRelativeToRoot(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "style/format.json", `{"indent": "spaces"}`)
	config, err := LoadConfig(root, "style/format.json")
	require.NoError(t, err)
	require.Equal(t, "spaces", config.Indent)
	absolute, err := LoadConfig(t.TempDir(), filepath.Join(root, "style", "format.json"))
	require.NoError(t, err)
	require.Equal(t, "spaces", absolute.Indent)
}

func TestLoadConfigRejectsInvalidFiles(t *testing.T) {
	cases := map[string]struct {
		contents string
		want     string
	}{
		"unknown field":         {`{"line_length": 80}`, "parse format config:"},
		"malformed":             {`{`, "parse format config:"},
		"unsupported version":   {`{"version": 2}`, "unsupported format config version 2"},
		"no source roots":       {`{"source_roots": []}`, "at least one source_root"},
		"escaping source root":  {`{"source_roots": ["../other"]}`, `source_root "../other" must be a project-relative path`},
		"absolute source root":  {`{"source_roots": ["/etc"]}`, "must be a project-relative path"},
		"empty source root":     {`{"source_roots": [""]}`, "must be a project-relative path"},
		"zero line width":       {`{"line_width": 0}`, "line_width must be at least 1, got 0"},
		"negative tab width":    {`{"tab_width": -4}`, "tab_width must be at least 1, got -4"},
		"zero top level blanks": {`{"blank_lines": {"top_level": 0}}`, "blank_lines.top_level must be at least 1, got 0"},
		"zero nested blanks":    {`{"blank_lines": {"nested": 0}}`, "blank_lines.nested must be at least 1, got 0"},
		"indent":                {`{"indent": "x"}`, `indent must be "tabs" or "spaces", got "x"`},
		"quote style":           {`{"quote_style": "x"}`, `quote_style must be "double", "single", or "preserve", got "x"`},
		"comment spacing":       {`{"comment_spacing": "x"}`, `comment_spacing must be "normalize" or "preserve", got "x"`},
		"operators":             {`{"operators": "symbols"}`, `operators must be "words" or "preserve", got "symbols"`},
		"numbers":               {`{"numbers": ""}`, `numbers must be "normalize" or "preserve", got ""`},
		"trailing commas":       {`{"trailing_commas": "always"}`, `trailing_commas must be "when-broken" or "never", got "always"`},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeConfig(t, root, DefaultConfigPath, testCase.contents)
			_, err := LoadConfig(root, "")
			require.Error(t, err)
			require.ErrorContains(t, err, testCase.want)
		})
	}
}

func TestOptionsRejectsUnknownEnumeration(t *testing.T) {
	config := DefaultConfig()
	config.Operators = "symbols"
	_, err := config.options()
	require.Error(t, err)
}
