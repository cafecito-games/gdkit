package format

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"testing"

	"github.com/cafecito-games/gdparser/ast"
	gdformat "github.com/cafecito-games/gdparser/format"
	"github.com/cafecito-games/gdparser/parser"
	"github.com/stretchr/testify/require"
)

const unformatted = "extends Node\nvar a=1\nfunc f( x ):\n\treturn x+a\n"

func TestNewRejectsInvalidConfig(t *testing.T) {
	config := DefaultConfig()
	config.LineWidth = 0
	_, err := New(config)
	require.Error(t, err)
}

func TestFormatReportsChangedFileWithFormatterOutput(t *testing.T) {
	report, snapshot := formatProject(t, DefaultConfig(), map[string]string{"a.gd": unformatted})
	require.False(t, report.HasDiagnostics())
	require.Len(t, report.Results, 1)
	result := report.Results[0]
	want := gdformat.FileWithOptions(snapshot.Scripts["a.gd"].File, gdformat.GodotStyle())
	require.Equal(t, "a.gd", result.Path)
	require.True(t, result.Changed)
	require.Equal(t, want, string(result.Formatted))
	require.NotEqual(t, unformatted, want)
	require.True(t, report.HasChanges())
	require.Len(t, report.Changed(), 1)

	again, _ := formatProject(t, DefaultConfig(), map[string]string{"a.gd": want})
	require.False(t, again.HasDiagnostics())
	require.False(t, again.HasChanges())
	require.Len(t, again.Results, 1)
	require.False(t, again.Results[0].Changed)
	require.Nil(t, again.Results[0].Formatted)
	require.NotNil(t, again.Changed())
	require.Empty(t, again.Changed())
}

func TestFormatHonoursEveryOption(t *testing.T) {
	cases := map[string]struct {
		configure func(*Config)
		source    string
		want      string
	}{
		"default indent":    {func(*Config) {}, "func f():\n    pass\n", "func f():\n\tpass\n"},
		"spaces indent":     {func(c *Config) { c.Indent = "spaces"; c.TabWidth = 2 }, "func f():\n\tpass\n", "func f():\n  pass\n"},
		"double quotes":     {func(*Config) {}, "var a = 'x'\n", "var a = \"x\"\n"},
		"single quotes":     {func(c *Config) { c.QuoteStyle = "single" }, "var a = \"x\"\n", "var a = 'x'\n"},
		"preserved quotes":  {func(c *Config) { c.QuoteStyle = "preserve" }, "var a = 'x'\nvar b = \"y\"\n", "var a = 'x'\nvar b = \"y\"\n"},
		"word operators":    {func(*Config) {}, "var a = b && !c || d\n", "var a = b and not c or d\n"},
		"kept operators":    {func(c *Config) { c.Operators = "preserve" }, "var a = b && !c || d\n", "var a = b && !c || d\n"},
		"comment spacing":   {func(*Config) {}, "#note\nvar a = 1  #why\n", "# note\nvar a = 1  # why\n"},
		"kept comments":     {func(c *Config) { c.CommentSpacing = "preserve" }, "#note\nvar a = 1\n", "#note\nvar a = 1\n"},
		"numbers":           {func(*Config) {}, "var a = .5\nvar b = 0XFF\n", "var a = 0.5\nvar b = 0xff\n"},
		"kept numbers":      {func(c *Config) { c.Numbers = "preserve" }, "var a = .5\nvar b = 0XFF\n", "var a = .5\nvar b = 0XFF\n"},
		"wide line":         {func(*Config) {}, "var a = [alpha, beta, gamma]\n", "var a = [alpha, beta, gamma]\n"},
		"narrow line":       {func(c *Config) { c.LineWidth = 20 }, "var a = [alpha, beta, gamma]\n", "var a = [\n\talpha,\n\tbeta,\n\tgamma,\n]\n"},
		"no trailing comma": {func(c *Config) { c.LineWidth = 20; c.TrailingCommas = "never" }, "var a = [alpha, beta, gamma]\n", "var a = [\n\talpha,\n\tbeta,\n\tgamma\n]\n"},
		"top level blanks":  {func(c *Config) { c.BlankLines.TopLevel = 1 }, "var a = 1\nfunc f():\n\tpass\n", "var a = 1\n\nfunc f():\n\tpass\n"},
		"nested blanks":     {func(c *Config) { c.BlankLines.Nested = 2 }, "var a = 1\n\n\n\nvar b = 2\n", "var a = 1\n\n\nvar b = 2\n"},
		"default blanks":    {func(*Config) {}, "var a = 1\n\n\n\nvar b = 2\nfunc f():\n\tpass\n", "var a = 1\n\nvar b = 2\n\n\nfunc f():\n\tpass\n"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			config := DefaultConfig()
			testCase.configure(&config)
			got := formatSource(t, config, testCase.source)
			require.Equal(t, testCase.want, got)
		})
	}
}

func TestFormatReportsParseFailureWithoutResult(t *testing.T) {
	report, snapshot := formatProject(t, DefaultConfig(), map[string]string{
		"broken.gd": "var a = 1\nvar b = 2\nfunc (:\n",
	})
	require.Empty(t, report.Results)
	require.Len(t, report.Diagnostics, 1)
	var parseError *parser.Error
	require.True(t, errors.As(snapshot.Scripts["broken.gd"].ParseError, &parseError), "fixture did not fail with a parser error")
	diagnostic := report.Diagnostics[0]
	start := parseError.Token.Span.Start
	require.Equal(t, "source-parse", diagnostic.Rule)
	require.Equal(t, "broken.gd", diagnostic.Path)
	require.Equal(t, start.Line, diagnostic.Line)
	require.Equal(t, start.Column, diagnostic.Column)
	require.Equal(t, 3, diagnostic.Line)
	require.Equal(t, parseError.Message, diagnostic.Message)
	want := "broken.gd:3: Error: " + parseError.Message + " (source-parse)"
	require.Equal(t, want, diagnostic.String())
	require.True(t, report.HasDiagnostics())
	require.False(t, report.HasChanges())
}

func TestFormatRefusesOutputThatFailsVerification(t *testing.T) {
	snapshot := loadProject(t, DefaultConfig(), map[string]string{
		"a.gd": "var a=1\n",
		"b.gd": "var b = 2\n",
	})
	formatter, err := New(DefaultConfig())
	require.NoError(t, err)
	formatter.emit = func(*ast.File, gdformat.Options) string { return "var renamed = 1\n" }
	report := formatter.Format(snapshot)
	require.Empty(t, report.Results)
	require.Len(t, report.Diagnostics, 2)
	want := Diagnostic{Rule: "format.unsafe", Message: "formatting changed the syntax tree", Path: "a.gd", Line: 1, Column: 1}
	require.Equal(t, want, report.Diagnostics[0])
}

func TestFormatDoesNotMutateSnapshot(t *testing.T) {
	config := DefaultConfig()
	snapshot := loadProject(t, config, map[string]string{
		"a.gd": "#c\nvar a = 'x'  #d\nvar b = .5 if a && !a else [1, #e\n2]\nfunc f( x ):\n\treturn x\n",
	})
	script := snapshot.Scripts["a.gd"]
	treeBefore := ast.JSONValue(script.File)
	sourceBefore := string(script.Source)
	formatter, err := New(config)
	require.NoError(t, err)
	report := formatter.Format(snapshot)
	require.True(t, report.HasChanges())
	require.Equal(t, treeBefore, ast.JSONValue(script.File))
	require.Equal(t, sourceBefore, string(script.Source))
}

func TestFormatReportIsSortedAndDeterministic(t *testing.T) {
	files := map[string]string{
		"b/z.gd":   unformatted,
		"a/y.gd":   "var a = 1\n",
		"c.gd":     unformatted,
		"b/bad.gd": "func (:\n",
		"a/bad.gd": "func (:\n",
	}
	config := DefaultConfig()
	snapshot := loadProject(t, config, files)
	formatter, err := New(config)
	require.NoError(t, err)
	first := formatter.Format(snapshot)
	second := formatter.Format(snapshot)

	var resultPaths, diagnosticPaths []string
	for _, result := range first.Results {
		resultPaths = append(resultPaths, result.Path)
	}
	for _, diagnostic := range first.Diagnostics {
		diagnosticPaths = append(diagnosticPaths, diagnostic.Path)
	}
	require.Equal(t, []string{"a/y.gd", "b/z.gd", "c.gd"}, resultPaths)
	require.Equal(t, []string{"a/bad.gd", "b/bad.gd"}, diagnosticPaths)
	firstJSON, err := json.Marshal(first)
	require.NoError(t, err)
	secondJSON, err := json.Marshal(second)
	require.NoError(t, err)
	require.Equal(t, string(firstJSON), string(secondJSON))
	require.NotContains(t, string(firstJSON), "var a")
}

func TestEmptyReportMarshalsEmptyArrays(t *testing.T) {
	report, _ := formatProject(t, DefaultConfig(), map[string]string{"notes.txt": "x"})
	data, err := json.Marshal(report)
	require.NoError(t, err)
	require.Equal(t, `{"results":[],"diagnostics":[]}`, string(data))
}

func TestFormatIsIdenticalAcrossRunsOfALargeProject(t *testing.T) {
	files := make(map[string]string, 200)
	for index := range 200 {
		name := fmt.Sprintf("group_%d/script_%03d.gd", index%7, index)
		switch index % 10 {
		case 3:
			files[name] = "func (:\n"
		case 5:
			files[name] = "var a = 1\n"
		case 7:
			files[name] = "var BadOne = 1; var BadTwo = 2 # gdlint:ignore=class-variable-name\n"
		default:
			files[name] = fmt.Sprintf("extends Node\nvar value_%d=%d\nfunc f( x ):\n\treturn x+value_%d  #why\n", index, index, index)
		}
	}
	config := DefaultConfig()
	snapshot := loadProject(t, config, files)
	formatter, err := New(config)
	require.NoError(t, err)
	first := formatter.Format(snapshot)
	require.Len(t, first.Results, 160)
	require.Len(t, first.Changed(), 140)
	require.Len(t, first.Diagnostics, 40)
	require.True(t, sort.SliceIsSorted(first.Results, func(i, j int) bool { return first.Results[i].Path < first.Results[j].Path }), "results are not in path order")
	firstJSON, err := json.Marshal(first)
	require.NoError(t, err)
	for range 5 {
		again := formatter.Format(snapshot)
		againJSON, err := json.Marshal(again)
		require.NoError(t, err)
		require.Equal(t, string(firstJSON), string(againJSON))
		require.Equal(t, first, again)
	}
}

func TestFormatReportsLexerErrorAtItsPosition(t *testing.T) {
	report, _ := formatProject(t, DefaultConfig(), map[string]string{"a.gd": "var a = 1\nvar s = \"\\x\"\n"})
	want := Diagnostic{Rule: "source-parse", Message: `invalid escape "\x" in string`, Path: "a.gd", Line: 2, Column: 11}
	require.Len(t, report.Diagnostics, 1)
	require.Equal(t, want, report.Diagnostics[0])
	got := report.Diagnostics[0].String()
	require.Equal(t, `a.gd:2: Error: invalid escape "\x" in string (source-parse)`, got)
}

// These pin how the formatter treats the edges of a file, so a change in
// gdparser that alters them is noticed.
func TestFormatAtTheEdgesOfAFile(t *testing.T) {
	cases := map[string]struct {
		source  string
		changed bool
		want    string
	}{
		"windows line endings":           {"var a=1\r\nvar b = 2\r\n", true, "var a = 1\nvar b = 2\n"},
		"formatted windows line endings": {"var a = 1\r\n# note\r\n", true, "var a = 1\n# note\n"},
		"windows line ending in string":  {"var a = \"\"\"x\r\ny\"\"\"\r\n", true, "var a = \"\"\"x\r\ny\"\"\"\n"},
		"byte order mark":                {"\xef\xbb\xbfvar a=1\n", true, "var a = 1\n"},
		"formatted byte order mark":      {"\xef\xbb\xbfvar a = 1\n", true, "var a = 1\n"},
		"empty file":                     {"", false, ""},
		"blank file":                     {"\n\n  \n", true, ""},
		"no final newline":               {"var a = 1", true, "var a = 1\n"},
		"comment without final newline":  {"#c", true, "# c\n"},
		"extra final newlines":           {"var a = 1\n\n\n", true, "var a = 1\n"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			report, _ := formatProject(t, DefaultConfig(), map[string]string{"a.gd": testCase.source})
			require.False(t, report.HasDiagnostics())
			require.Len(t, report.Results, 1)
			result := report.Results[0]
			require.Equal(t, testCase.changed, result.Changed)
			require.Equal(t, testCase.want, string(result.Formatted))
		})
	}
}
