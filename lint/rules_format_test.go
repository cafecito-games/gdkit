package lint

import (
	"strings"
	"testing"
)

func TestFormatMaxLineLengthCountsRunesNotBytes(t *testing.T) {
	// 61 accented words is 122 runes and 183 bytes, and every word is one
	// rune wide, so the line is both over the limit and breakable.
	longAccented := "# " + strings.Repeat("é ", 60) + "é\n"
	assertRule(t, "max-line-length", longAccented, 1)

	// 100 accented runes is 200 bytes but exactly at the limit.
	atLimit := "#" + strings.Repeat("é", 99) + "\n"
	if found := lintSource(t, "max-line-length", atLimit); len(found) != 0 {
		t.Errorf("a 100-rune line must not fire: %v", found)
	}
}

// TestFormatMaxLineLengthSparesIrreducibleLines pins the measurement that
// decides whether a long line has a shorter form at all.
func TestFormatMaxLineLengthSparesIrreducibleLines(t *testing.T) {
	longName := strings.Repeat("Generated", 12)
	longWord := strings.Repeat("word", 30)

	// A name, a string literal, and one run of a comment cannot be broken.
	assertRule(t, "max-line-length", "var a := "+longName+".new()\n")
	assertRule(t, "max-line-length", "var a := \"res://"+longWord+".tscn\"\n")
	assertRule(t, "max-line-length", "# see "+longWord+"\n")

	// Prose wraps, so a comment of ordinary words is reducible.
	assertRule(t, "max-line-length", "# "+strings.Repeat("word ", 30)+"\n", 1)

	// So is a line that is long only because it packs short things together.
	assertRule(t, "max-line-length", "var a := ["+strings.Repeat("1, ", 50)+"]\n", 1)

	// Indentation counts towards the width a rewrite cannot avoid, but a line
	// of nothing but whitespace is all width a rewrite can simply drop.
	assertRule(t, "max-line-length", "func f():\n\tvar a := "+longName+"\n")
	assertRule(t, "max-line-length", strings.Repeat(" ", 120)+"\n", 1)
}

// TestFormatMaxLineLengthAsksOnlyForBreakPointsTheCodeAlreadyHas pins which
// long lines still have a shorter form. GDScript continues a line implicitly
// inside an unclosed bracket, so a bracketed construct can be broken and a
// line that brackets nothing cannot be without adding syntax. None of these
// names is long enough to be irreducible on its own.
func TestFormatMaxLineLengthAsksOnlyForBreakPointsTheCodeAlreadyHas(t *testing.T) {
	longName := strings.Repeat("Generated", 6)

	// An argument list is already bracketed, so the call can be broken.
	assertRule(t, "max-line-length", "var a := "+longName+".new("+strings.Repeat("1, ", 15)+"2)\n", 1)

	// Nothing here brackets anything, so breaking the line means introducing
	// parentheses the code does not have.
	assertRule(t, "max-line-length", "var a := "+longName+" + "+longName+"\n")

	// The call's parentheses hold nothing, so breaking inside them leaves the
	// first line exactly as long as it was.
	assertRule(t, "max-line-length", "var a: "+longName+" = "+longName+"Factory.create()\n")
}

// TestFormatMaxLineLengthMeasuresEachLineOfAMultiLineString checks that a
// token spanning several lines contributes to each line only the part of it
// that lies on that line. Inside a multi-line string every line is the value,
// so none of them has a shorter form and none is reported; without clipping
// the literal to each line, the lines after the first would carry no atom at
// all and would be.
func TestFormatMaxLineLengthMeasuresEachLineOfAMultiLineString(t *testing.T) {
	source := "var a := \"\"\"\n" +
		strings.Repeat("word ", 30) + "\n" +
		strings.Repeat("word", 30) + "\n" +
		"\"\"\"\n"
	assertRule(t, "max-line-length", source)
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
	source := "func a():\n\tvar x := [\n    1,\n\t]\n"
	assertRule(t, "mixed-tabs-and-spaces", source)
}

func TestFormatMixedTabsAndSpacesFiresPerMixedLine(t *testing.T) {
	assertRule(t, "mixed-tabs-and-spaces", "func a():\n\tvar x := [\n\t 1,\n\t 2,\n\t]\n", 3, 4)
	assertRule(t, "mixed-tabs-and-spaces", "func a():\n\tvar x := [\n \t1,\n\t]\n", 3)
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

// These forms were rejected by gdparser until cafecito-games/gdparser#12, #13,
// #14, #15, #34 and #76. Godot and gdlint accept all of them. A regression would
// make gdkit lint report source-parse and silently skip every rule on the file,
// so this guards the dependency rather than any one rule.
func TestFormerlyUnparseableFormsStillParse(t *testing.T) {
	sources := map[string]string{
		"variadic.gd":        "func a(first, ...rest):\n\tpass\n",
		"named_lambda.gd":    "func a():\n\tvar callback = func named(): pass\n",
		"match_binding.gd":   "func a(value):\n\tmatch value:\n\t\tvar captured:\n\t\t\tprint(captured)\n",
		"untyped_setter.gd":  "var stored:\n\tset(value):\n\t\tstored = value\n",
		"lua_dictionary.gd":  "func a():\n\tvar entries = {first = 1, second = 2}\n\tprint(entries)\n",
		"tool_extends.gd":    "@tool extends Node\n",
		"icon_class_name.gd": "@icon(\"res://icon.svg\") class_name Icon extends Node\n",
		"two_annotations.gd": "@tool @icon(\"res://icon.svg\") extends Node\n",
		"static_unload.gd":   "@static_unload extends Node\n",
		"lambda_block.gd":    "var handler = func(): if true: pass\n",
	}
	report := lintProject(t, DefaultConfig(), sources)
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == "source-parse" {
			t.Errorf("%s failed to parse: %s", diagnostic.Path, diagnostic.Message)
		}
	}
}
