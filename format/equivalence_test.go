package format

import (
	"strings"
	"testing"

	gdformat "github.com/cafecito-games/gdparser/format"
)

func TestDelimiterNeutralBody(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"plain":                {`abc`, `abc`},
		"bare quotes":          {`it's "x"`, `it's "x"`},
		"escaped quotes":       {`it\'s \"x\"`, `it's "x"`},
		"other escapes kept":   {`\a\b\f\n\r\t\v`, `\a\b\f\n\r\t\v`},
		"unicode escapes kept": {`\u00e9\U01F600`, `\u00e9\U01F600`},
		"escaped backslash":    {`\\'`, `\\'`},
		"backslash then quote": {`\\\'`, `\\'`},
		"continuation kept":    {"a\\\nb", "a\\\nb"},
		"real newline":         {"a\nb", "a\nb"},
		"dangling backslash":   {`a\`, `a\`},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if got := delimiterNeutralBody(testCase.body); got != testCase.want {
				t.Fatalf("delimiterNeutralBody(%q) = %q, want %q", testCase.body, got, testCase.want)
			}
		})
	}
}

func TestNormalizedNumber(t *testing.T) {
	for raw, want := range map[string]string{
		"10":          "10",
		"1_000":       "1_000",
		"1.":          "1.0",
		".5":          "0.5",
		"1.5":         "1.5",
		"1.e5":        "1.0e5",
		".5E-3":       "0.5E-3",
		"1E5":         "1E5",
		"0X1F":        "0x1f",
		"0xDEAD_beef": "0xdead_beef",
		"0B101":       "0b101",
		"0x1E":        "0x1e",
	} {
		if got := normalizedNumber(raw); got != want {
			t.Errorf("normalizedNumber(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestNormalizedComment(t *testing.T) {
	for text, want := range map[string]string{
		"#note":         "# note",
		"# note":        "# note",
		"#  note":       "#  note",
		"##doc":         "## doc",
		"## doc":        "## doc",
		"#":             "#",
		"##":            "##",
		"#region Name":  "#region Name",
		"#endregion":    "#endregion",
		"##region Name": "##region Name",
		"#\tnote":       "# \tnote",
		"###x":          "## #x",
		"#!x":           "# !x",
	} {
		if got := normalizedComment(text); got != want {
			t.Errorf("normalizedComment(%q) = %q, want %q", text, got, want)
		}
	}
}

// trickyLiterals is a set of literals whose spelling the formatter may change, written
// so that every one of them is reformatted at least by the spacing around "=".
var trickyLiterals = []string{
	`'say "hi"'`,
	`"it's"`,
	`'it\'s'`,
	`"a\"b"`,
	`'a\"b'`,
	`"a\'b"`,
	`'both \' and "'`,
	`"both ' and \""`,
	`r"\n"`,
	`r'\''`,
	`r'plain'`,
	`r'say "hi"'`,
	`'\\'`,
	`'é\U01F600😀'`,
	`'tab\there'`,
	"\"\"\"triple \"quoted\" 'text'\nover lines\"\"\"",
	"'''triple \"quoted\" 'text'\nover lines'''",
	`""""quote first"""`,
	`r'''raw "triple" \n'''`,
	`&'name'`,
	`&"it's"`,
	`^'path/to'`,
	`$'A/B'`,
	`$"A/B"`,
	`$A/B`,
	`%Name`,
	`%'Unique Name'`,
	`""`,
	`''`,
	"'continued \\\nline'",
	`0X1F`,
	`0B101`,
	`1.`,
	`.5`,
	`1E5`,
	`1.E5`,
	`1_000`,
	`0xDEAD_beef`,
	`1_0.5_0`,
}

func TestFormatAcceptsItsOwnOutputForTrickyLiterals(t *testing.T) {
	configurations := map[string]func(*Config){
		"default":  func(*Config) {},
		"single":   func(config *Config) { config.QuoteStyle = "single" },
		"preserve": func(config *Config) { config.QuoteStyle = "preserve"; config.Numbers = "preserve" },
	}
	for configurationName, configure := range configurations {
		for _, literal := range trickyLiterals {
			t.Run(configurationName+"/"+literal, func(t *testing.T) {
				config := DefaultConfig()
				configure(&config)
				source := "var a=" + literal + "\nfunc f(x=" + literal + "):\n\treturn [" + literal + ",{" + literal + ":x}]\n"
				report, _ := formatProject(t, config, map[string]string{"a.gd": source})
				if report.HasDiagnostics() {
					t.Fatalf("diagnostic for real formatter output: %s", report.Diagnostics[0])
				}
				if len(report.Results) != 1 || !report.Results[0].Changed {
					t.Fatalf("results = %+v, want one changed result", report.Results)
				}
			})
		}
	}
}

func TestFormatSpellsTrickyLiterals(t *testing.T) {
	cases := map[string]struct {
		configure func(*Config)
		source    string
		want      string
	}{
		"fewest escapes":      {func(*Config) {}, `var a='say "hi"'` + "\n", `var a = 'say "hi"'` + "\n"},
		"drops an escape":     {func(*Config) {}, `var a='it\'s'` + "\n", `var a = "it's"` + "\n"},
		"single drops escape": {func(config *Config) { config.QuoteStyle = "single" }, `var a="a\"b"` + "\n", `var a = 'a"b'` + "\n"},
		"raw requoted":        {func(*Config) {}, `var a=r'\''` + "\n", `var a = r"\'"` + "\n"},
		"raw single":          {func(config *Config) { config.QuoteStyle = "single" }, `var a=r'\''` + "\n", `var a = r"\'"` + "\n"},
		"string name":         {func(*Config) {}, "var a=&'name'\n", "var a = &\"name\"\n"},
		"numbers":             {func(*Config) {}, "var a=[0X1F,1.,.5,1E5,0xDEAD_beef]\n", "var a = [0x1f, 1.0, 0.5, 1E5, 0xdead_beef]\n"},
		"triple kept":         {func(config *Config) { config.QuoteStyle = "single" }, "var a=\"\"\"a'b\"\"\"\n", "var a = \"\"\"a'b\"\"\"\n"},
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

// The formatter trims trailing whitespace from every line it emits. That is
// harmless at the end of a comment and changes the value of a string literal
// that spans lines.
func TestFormatAcceptsACommentThatLosesTrailingWhitespace(t *testing.T) {
	for name, style := range map[string]string{"normalize": "normalize", "preserve": "preserve"} {
		t.Run(name, func(t *testing.T) {
			config := DefaultConfig()
			config.CommentSpacing = style
			source := "# note \t \nvar a = 1  # why  \nvar b = [\n\t1,  # one \n]\n"
			want := "# note\nvar a = 1  # why\nvar b = [\n\t1,  # one\n]\n"
			if got := formatSource(t, config, source); got != want {
				t.Fatalf("formatted = %q, want %q", got, want)
			}
		})
	}
}

func TestFormatRefusesToTrimWhitespaceInsideAString(t *testing.T) {
	for name, source := range map[string]string{
		"triple quoted": "var a = \"\"\"one  \ntwo\"\"\"\n",
		"single quoted": "var a = 'one\t\ntwo'\n",
		"blank line":    "var a = \"\"\"one\n  \ntwo\"\"\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			report, _ := formatProject(t, DefaultConfig(), map[string]string{"a.gd": source})
			if len(report.Results) != 0 {
				t.Fatalf("results = %+v (%q), want none", report.Results, report.Results[0].Formatted)
			}
			want := Diagnostic{Rule: "format.unsafe", Message: "formatting changed the syntax tree", Path: "a.gd", Line: 1, Column: 1}
			if len(report.Diagnostics) != 1 || report.Diagnostics[0] != want {
				t.Fatalf("diagnostics = %+v, want %+v", report.Diagnostics, want)
			}
		})
	}
}

func TestVerifyComparesLiteralValues(t *testing.T) {
	single := gdformat.GodotStyle()
	single.QuoteStyle = gdformat.SingleQuotes
	cases := map[string]struct {
		source, formatted string
		options           gdformat.Options
		accepted          bool
	}{
		"requoted with escape dropped": {`var a = 'it\'s'`, `var a = "it's"`, gdformat.GodotStyle(), true},
		"requoted with escape added":   {`var a = "it's"`, `var a = 'it\'s'`, single, true},
		"requoted keeping an escape":   {`var a = 'a\tb'`, `var a = "a\tb"`, gdformat.GodotStyle(), true},
		"raw requoted":                 {`var a = r'\n'`, `var a = r"\n"`, gdformat.GodotStyle(), true},

		"unicode escape decoded":    {`var a = '\u0041'`, `var a = "A"`, gdformat.GodotStyle(), false},
		"unicode escape encoded":    {`var a = 'A'`, `var a = "\u0041"`, gdformat.GodotStyle(), false},
		"tab escape decoded":        {`var a = '\t'`, "var a = \"\t\"", gdformat.GodotStyle(), false},
		"newline escape decoded":    {`var a = 'a\nb'`, "var a = \"a\nb\"", gdformat.GodotStyle(), false},
		"long escape as surrogates": {`var a = '\U01F600'`, `var a = "\uD83D\uDE00"`, gdformat.GodotStyle(), false},
		"escape digits recased":     {`var a = '\u00e9'`, `var a = "\u00E9"`, gdformat.GodotStyle(), false},
		"continuation joined":       {"var a = 'a\\\nb'", `var a = "ab"`, gdformat.GodotStyle(), false},
		"same spelling unescaped":   {`var a = '\u0041'`, `var a = '\u0041'`, gdformat.GodotStyle(), true},

		"escape left behind":        {`var a = 'it\'s'`, `var a = "it\\'s"`, gdformat.GodotStyle(), false},
		"escape changed":            {`var a = 'a\tb'`, `var a = "a\nb"`, gdformat.GodotStyle(), false},
		"escape made literal":       {`var a = 'a\\tb'`, `var a = "a\tb"`, gdformat.GodotStyle(), false},
		"raw body re-escaped":       {`var a = r'\''`, `var a = r"'"`, gdformat.GodotStyle(), false},
		"raw prefix dropped":        {`var a = r'\n'`, `var a = "\n"`, gdformat.GodotStyle(), false},
		"raw prefix added":          {`var a = '\\n'`, `var a = r"\n"`, gdformat.GodotStyle(), false},
		"string became name":        {`var a = 'x'`, `var a = &"x"`, gdformat.GodotStyle(), false},
		"name became path":          {`var a = &'x'`, `var a = ^"x"`, gdformat.GodotStyle(), false},
		"triple became single":      {`var a = '''x'''`, `var a = "x"`, gdformat.GodotStyle(), false},
		"triple requoted":           {`var a = '''x'''`, `var a = """x"""`, gdformat.GodotStyle(), false},
		"continuation dropped":      {"var a = 'a\\\nb'", "var a = \"a\nb\"", gdformat.GodotStyle(), false},
		"separator added":           {`var a = 10`, `var a = 1_0`, gdformat.GodotStyle(), false},
		"separator dropped":         {`var a = 1_000`, `var a = 1000`, gdformat.GodotStyle(), false},
		"hexadecimal digit changed": {`var a = 0X1F`, `var a = 0x1e`, gdformat.GodotStyle(), false},
		"zero appended":             {`var a = 1.`, `var a = 1.00`, gdformat.GodotStyle(), false},
		"exponent case changed":     {`var a = 1E5`, `var a = 1e5`, gdformat.GodotStyle(), false},
		"integer became float":      {`var a = 1`, `var a = 1.0`, gdformat.GodotStyle(), false},
		"comment text respaced":     {`#a  b`, `# a b`, gdformat.GodotStyle(), false},
		"comment space removed":     {`#  a`, `# a`, gdformat.GodotStyle(), false},
		"region spaced":             {`#region A`, `# region A`, gdformat.GodotStyle(), false},
		"comment became docs":       {`#a`, `## a`, gdformat.GodotStyle(), false},
		"comment lost a letter":     {`# ab`, `# a`, gdformat.GodotStyle(), false},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			err := verify("a.gd", []byte(testCase.source+"\n"), []byte(testCase.formatted+"\n"), testCase.options)
			if testCase.accepted && err != nil {
				t.Fatalf("verify rejected an equal value: %v", err)
			}
			if !testCase.accepted && (err == nil || !strings.HasPrefix(err.Error(), "formatting changed the syntax tree")) {
				t.Fatalf("error = %v, want a changed syntax tree", err)
			}
		})
	}
}
