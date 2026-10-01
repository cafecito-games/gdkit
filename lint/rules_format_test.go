package lint

import (
	"strings"
	"testing"
)

func TestFormatMaxLineLengthCountsRunesNotBytes(t *testing.T) {
	longAscii := "var a := \"" + strings.Repeat("x", 120) + "\"\n"
	assertRule(t, "max-line-length", longAscii, 1)

	// 100 accented runes is 200 bytes but exactly at the limit.
	atLimit := strings.Repeat("é", 100) + "\n"
	if found := lintSource(t, "max-line-length", atLimit); len(found) != 0 {
		t.Errorf("a 100-rune line must not fire: %v", found)
	}
}

func TestFormatMaxFileLinesFiresOnceOnTheLastLine(t *testing.T) {
	config := DefaultConfig()
	config.MaxFileLines = 3
	report := lintProject(t, config, map[string]string{"a.gd": "var a := 1\nvar b := 2\nvar c := 3\nvar d := 4\nvar e := 5\n"})
	var found []Diagnostic
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == "max-file-lines" {
			found = append(found, diagnostic)
		}
	}
	if len(found) != 1 {
		t.Fatalf("max-file-lines fired %d times, want 1: %v", len(found), found)
	}
	if found[0].Line != 5 {
		t.Errorf("Line = %d, want 5", found[0].Line)
	}
}

func TestFormatMixedTabsAndSpacesAllowsAFilePerStyle(t *testing.T) {
	source := "func a():\n\tvar x := 1\nfunc b():\n    var y := 2\n"
	assertRule(t, "mixed-tabs-and-spaces", source)
}

func TestFormatMixedTabsAndSpacesFiresPerMixedLine(t *testing.T) {
	assertRule(t, "mixed-tabs-and-spaces", "func a():\n\t var x := 1\n\t var y := 2\n", 2, 3)
	assertRule(t, "mixed-tabs-and-spaces", "func a():\n \tvar x := 1\n", 2)
}

func TestFormatMaxLineLengthExpandsTabsToTheConfiguredWidth(t *testing.T) {
	config := DefaultConfig()
	config.MaxLineLength = 20
	config.TabCharacters = 4
	// Three tabs expand to 12 columns for 25 in total; counted as one character
	// each the line would be only 16 long.
	report := lintProject(t, config, map[string]string{"a.gd": "var a := 1\t\t\t# x\n"})
	var found []Diagnostic
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == "max-line-length" {
			found = append(found, diagnostic)
		}
	}
	if len(found) != 1 || found[0].Line != 1 {
		t.Fatalf("max-line-length = %v, want one diagnostic on line 1", found)
	}
}

func TestFormatTabCharactersIsNotARule(t *testing.T) {
	if IsRule("tab-characters") {
		t.Error("tab-characters is a max-line-length setting, not a rule")
	}
}

// The whitespace sits inside comments so the parser accepts it. gdlint splits
// source with str.splitlines, which treats form feed and vertical tab as line
// breaks and therefore never reports them as trailing whitespace; a no-break
// space is not a line break and is reported.
func TestFormatTrailingWhitespaceMatchesUnicodeSpace(t *testing.T) {
	source := "# a\u00a0\n# b\u2003\nvar d := 4\n"
	assertRule(t, "trailing-whitespace", source, 1, 2)
}

func TestTrailingWhitespaceFollowsPythonLineBreakSemantics(t *testing.T) {
	cases := []struct {
		name   string
		suffix string
		fires  bool
	}{
		{"space", " ", true},
		{"tab", "\t", true},
		{"no-break space", " ", true},
		{"em space", " ", true},
		{"form feed", "\f", false},
		{"vertical tab", "\v", false},
		{"file separator", "\x1c", false},
		{"next line", "\u0085", false},
		{"line separator", " ", false},
		{"paragraph separator", " ", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			source := "# comment" + testCase.suffix + "\n"
			if testCase.fires {
				assertRule(t, "trailing-whitespace", source, 1)
			} else {
				assertNoRule(t, "trailing-whitespace", source)
			}
		})
	}
}

// A comment may sit deeper than the block containing it. Godot and gdlint both
// accept this, and gdparser rejected it until cafecito-games/gdparser#5. If the
// parser regresses, every rule silently stops running on the affected file, so
// this guards the dependency rather than any one rule.
func TestDeeperIndentedCommentsStillParse(t *testing.T) {
	sources := map[string]string{
		"deeper.gd":      "func a():\n\tpass\n\t\t# deeper than the body\n",
		"between.gd":     "func a():\n\tvar first := 1\n\t\t# deeper\n\tvar second := 2\n",
		"nested.gd":      "func a():\n\tif true:\n\t\tpass\n\t\t\t# deeper\n",
		"class_level.gd": "var value := 1\n\t# deeper than class level\n",
	}
	report := lintProject(t, DefaultConfig(), sources)
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == "source-parse" {
			t.Errorf("%s failed to parse: %s", diagnostic.Path, diagnostic.Message)
		}
	}
}
