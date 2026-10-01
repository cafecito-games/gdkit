package format

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	gdformat "github.com/cafecito-games/gdparser/format"
)

func writeConfig(t *testing.T, root, name, contents string) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultConfigIsGodotStyle(t *testing.T) {
	config := DefaultConfig()
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	options, err := config.options()
	if err != nil {
		t.Fatal(err)
	}
	if options != gdformat.GodotStyle() {
		t.Fatalf("options = %+v, want %+v", options, gdformat.GodotStyle())
	}
	if !reflect.DeepEqual(config.SourceRoots, []string{"."}) {
		t.Fatalf("source roots = %v", config.SourceRoots)
	}
	if !reflect.DeepEqual(config.Exclude, []string{".git/**", ".godot/**", ".gdkit/**", "addons/**"}) {
		t.Fatalf("exclude = %v", config.Exclude)
	}
}

func TestDefaultConfigRoundTripsThroughJSON(t *testing.T) {
	data, err := json.Marshal(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var decoded Config
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, DefaultConfig()) {
		t.Fatalf("round trip = %+v, want %+v", decoded, DefaultConfig())
	}
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
	if err != nil {
		t.Fatal(err)
	}
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
	if options != want {
		t.Fatalf("options = %+v, want %+v", options, want)
	}

	config.QuoteStyle = "preserve"
	options, err = config.options()
	if err != nil {
		t.Fatal(err)
	}
	if options.QuoteStyle != gdformat.PreserveQuotes {
		t.Fatalf("quote style = %v, want preserve", options.QuoteStyle)
	}
}

func TestLoadConfigReturnsDefaultsWhenDefaultFileIsMissing(t *testing.T) {
	config, err := LoadConfig(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config, DefaultConfig()) {
		t.Fatalf("config = %+v, want defaults", config)
	}
}

func TestLoadConfigFailsWhenNamedFileIsMissing(t *testing.T) {
	_, err := LoadConfig(t.TempDir(), "missing.json")
	if err == nil || !strings.HasPrefix(err.Error(), "read format config:") {
		t.Fatalf("error = %v, want a read format config error", err)
	}
}

func TestLoadConfigInheritsOmittedFields(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, DefaultConfigPath, `{"line_width": 80, "quote_style": "single", "blank_lines": {"nested": 2}}`)
	config, err := LoadConfig(root, "")
	if err != nil {
		t.Fatal(err)
	}
	want := DefaultConfig()
	want.LineWidth = 80
	want.QuoteStyle = "single"
	want.BlankLines.Nested = 2
	if !reflect.DeepEqual(config, want) {
		t.Fatalf("config = %+v, want %+v", config, want)
	}
}

func TestLoadConfigReadsNamedFileRelativeToRoot(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "style/format.json", `{"indent": "spaces"}`)
	config, err := LoadConfig(root, "style/format.json")
	if err != nil {
		t.Fatal(err)
	}
	if config.Indent != "spaces" {
		t.Fatalf("indent = %q, want spaces", config.Indent)
	}
	absolute, err := LoadConfig(t.TempDir(), filepath.Join(root, "style", "format.json"))
	if err != nil {
		t.Fatal(err)
	}
	if absolute.Indent != "spaces" {
		t.Fatalf("indent = %q, want spaces", absolute.Indent)
	}
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
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want it to contain %q", err, testCase.want)
			}
		})
	}
}

func TestOptionsRejectsUnknownEnumeration(t *testing.T) {
	config := DefaultConfig()
	config.Operators = "symbols"
	if _, err := config.options(); err == nil {
		t.Fatal("options accepted an unknown operators value")
	}
}
