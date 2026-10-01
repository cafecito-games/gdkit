package format

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/cafecito-games/gdparser/ast"
	gdformat "github.com/cafecito-games/gdparser/format"
	"github.com/cafecito-games/gdparser/parser"
)

const unformatted = "extends Node\nvar a=1\nfunc f( x ):\n\treturn x+a\n"

func TestNewRejectsInvalidConfig(t *testing.T) {
	config := DefaultConfig()
	config.LineWidth = 0
	if _, err := New(config); err == nil {
		t.Fatal("New accepted an invalid config")
	}
}

func TestFormatReportsChangedFileWithFormatterOutput(t *testing.T) {
	report, snapshot := formatProject(t, DefaultConfig(), map[string]string{"a.gd": unformatted})
	if report.HasDiagnostics() {
		t.Fatalf("unexpected diagnostics: %v", report.Diagnostics)
	}
	if len(report.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(report.Results))
	}
	result := report.Results[0]
	want := gdformat.FileWithOptions(snapshot.Scripts["a.gd"].File, gdformat.GodotStyle())
	if result.Path != "a.gd" || !result.Changed || string(result.Formatted) != want {
		t.Fatalf("result = %+v (%q), want changed with %q", result, result.Formatted, want)
	}
	if want == unformatted {
		t.Fatal("fixture is already formatted")
	}
	if !report.HasChanges() || len(report.Changed()) != 1 {
		t.Fatalf("HasChanges = %v, Changed = %v", report.HasChanges(), report.Changed())
	}

	again, _ := formatProject(t, DefaultConfig(), map[string]string{"a.gd": want})
	if again.HasDiagnostics() || again.HasChanges() {
		t.Fatalf("formatted output is not stable: %+v", again)
	}
	if len(again.Results) != 1 || again.Results[0].Changed || again.Results[0].Formatted != nil {
		t.Fatalf("results = %+v, want one unchanged result", again.Results)
	}
	if again.Changed() == nil || len(again.Changed()) != 0 {
		t.Fatalf("Changed = %#v, want an empty non-nil slice", again.Changed())
	}
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
			if got := formatSource(t, config, testCase.source); got != testCase.want {
				t.Fatalf("formatted = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestFormatReportsParseFailureWithoutResult(t *testing.T) {
	report, snapshot := formatProject(t, DefaultConfig(), map[string]string{
		"broken.gd": "var a = 1\nvar b = 2\nfunc (:\n",
	})
	if len(report.Results) != 0 {
		t.Fatalf("results = %+v, want none", report.Results)
	}
	if len(report.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %v, want one", report.Diagnostics)
	}
	var parseError *parser.Error
	if !errors.As(snapshot.Scripts["broken.gd"].ParseError, &parseError) {
		t.Fatal("fixture did not fail with a parser error")
	}
	diagnostic := report.Diagnostics[0]
	start := parseError.Token.Span.Start
	if diagnostic.Rule != "source-parse" || diagnostic.Path != "broken.gd" || diagnostic.Line != start.Line || diagnostic.Column != start.Column {
		t.Fatalf("diagnostic = %+v, want source-parse at %d:%d", diagnostic, start.Line, start.Column)
	}
	if diagnostic.Line != 3 {
		t.Fatalf("line = %d, want 3", diagnostic.Line)
	}
	if diagnostic.Message != parseError.Message {
		t.Fatalf("message = %q, want %q", diagnostic.Message, parseError.Message)
	}
	want := "broken.gd:3: Error: " + parseError.Message + " (source-parse)"
	if diagnostic.String() != want {
		t.Fatalf("String() = %q, want %q", diagnostic.String(), want)
	}
	if !report.HasDiagnostics() || report.HasChanges() {
		t.Fatalf("HasDiagnostics = %v, HasChanges = %v", report.HasDiagnostics(), report.HasChanges())
	}
}

func TestFormatRefusesOutputThatFailsVerification(t *testing.T) {
	snapshot := loadProject(t, DefaultConfig(), map[string]string{
		"a.gd": "var a=1\n",
		"b.gd": "var b = 2\n",
	})
	formatter, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	formatter.emit = func(*ast.File, gdformat.Options) string { return "var renamed = 1\n" }
	report := formatter.Format(snapshot)
	if len(report.Results) != 0 {
		t.Fatalf("results = %+v, want none", report.Results)
	}
	if len(report.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %v, want two", report.Diagnostics)
	}
	want := Diagnostic{Rule: "format.unsafe", Message: "formatting changed the syntax tree", Path: "a.gd", Line: 1, Column: 1}
	if report.Diagnostics[0] != want {
		t.Fatalf("diagnostic = %+v, want %+v", report.Diagnostics[0], want)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	report := formatter.Format(snapshot)
	if !report.HasChanges() {
		t.Fatalf("fixture was not reformatted: %+v", report)
	}
	if !reflect.DeepEqual(ast.JSONValue(script.File), treeBefore) {
		t.Fatal("Format mutated the snapshot's syntax tree")
	}
	if string(script.Source) != sourceBefore {
		t.Fatal("Format mutated the snapshot's source")
	}
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
	if err != nil {
		t.Fatal(err)
	}
	first := formatter.Format(snapshot)
	second := formatter.Format(snapshot)

	var resultPaths, diagnosticPaths []string
	for _, result := range first.Results {
		resultPaths = append(resultPaths, result.Path)
	}
	for _, diagnostic := range first.Diagnostics {
		diagnosticPaths = append(diagnosticPaths, diagnostic.Path)
	}
	if !reflect.DeepEqual(resultPaths, []string{"a/y.gd", "b/z.gd", "c.gd"}) {
		t.Fatalf("result paths = %v", resultPaths)
	}
	if !reflect.DeepEqual(diagnosticPaths, []string{"a/bad.gd", "b/bad.gd"}) {
		t.Fatalf("diagnostic paths = %v", diagnosticPaths)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("runs differ:\n%s\n%s", firstJSON, secondJSON)
	}
	if strings.Contains(string(firstJSON), "var a") {
		t.Fatalf("formatted bytes leaked into JSON: %s", firstJSON)
	}
}

func TestEmptyReportMarshalsEmptyArrays(t *testing.T) {
	report, _ := formatProject(t, DefaultConfig(), map[string]string{"notes.txt": "x"})
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"results":[],"diagnostics":[]}` {
		t.Fatalf("JSON = %s", data)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	first := formatter.Format(snapshot)
	if len(first.Results) != 160 || len(first.Changed()) != 140 || len(first.Diagnostics) != 40 {
		t.Fatalf("got %d results, %d changed, %d diagnostics", len(first.Results), len(first.Changed()), len(first.Diagnostics))
	}
	if !sort.SliceIsSorted(first.Results, func(i, j int) bool { return first.Results[i].Path < first.Results[j].Path }) {
		t.Fatal("results are not in path order")
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		again := formatter.Format(snapshot)
		againJSON, err := json.Marshal(again)
		if err != nil {
			t.Fatal(err)
		}
		if string(againJSON) != string(firstJSON) {
			t.Fatal("report differs between runs")
		}
		if !reflect.DeepEqual(again, first) {
			t.Fatal("formatted contents differ between runs")
		}
	}
}

func TestFormatReportsLexerErrorAtItsPosition(t *testing.T) {
	report, _ := formatProject(t, DefaultConfig(), map[string]string{"a.gd": "var a = 1\nvar s = \"\\x\"\n"})
	want := Diagnostic{Rule: "source-parse", Message: `invalid escape "\x" in string`, Path: "a.gd", Line: 2, Column: 11}
	if len(report.Diagnostics) != 1 || report.Diagnostics[0] != want {
		t.Fatalf("diagnostics = %+v, want %+v", report.Diagnostics, want)
	}
	if got := report.Diagnostics[0].String(); got != `a.gd:2: Error: invalid escape "\x" in string (source-parse)` {
		t.Fatalf("String() = %q", got)
	}
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
			if report.HasDiagnostics() || len(report.Results) != 1 {
				t.Fatalf("report = %+v, want one result", report)
			}
			result := report.Results[0]
			if result.Changed != testCase.changed || string(result.Formatted) != testCase.want {
				t.Fatalf("changed = %v, formatted = %q; want %v, %q", result.Changed, result.Formatted, testCase.changed, testCase.want)
			}
		})
	}
}
