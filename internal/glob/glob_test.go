package glob

import "testing"

func TestMatchHandlesSegmentsAndFeatureCaptures(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		matches bool
		feature string
	}{
		{"**/features/{feature}/domain/**", "src/features/combat/domain/health.gd", true, "combat"},
		{"**/features/{feature}/domain/**", "src/features/combat/application/x.gd", false, ""},
		{"*.gd", "player.gd", true, ""},
		{"*.gd", "nested/player.gd", false, ""},
		{"**/*_test.gd", "a/b/thing_test.gd", true, ""},
		{"addons/**", "addons/plugin/x.gd", true, ""},
		{"./src/**", "src/x.gd", true, ""},
	}
	for _, testCase := range cases {
		matches, captures := Match(testCase.pattern, testCase.name)
		if matches != testCase.matches {
			t.Errorf("Match(%q, %q) = %v, want %v", testCase.pattern, testCase.name, matches, testCase.matches)
		}
		if testCase.feature != "" && captures["feature"] != testCase.feature {
			t.Errorf("Match(%q, %q) feature = %q, want %q", testCase.pattern, testCase.name, captures["feature"], testCase.feature)
		}
	}
}

func TestConsumerContracts(t *testing.T) {
	cases := []struct {
		pattern      string
		name         string
		wantMatch    bool
		wantCaptures map[string]string
		description  string
	}{
		// Trailing-slash directory matching (analyzer.go:164 calls with relative+"/")
		{"addons/**", "addons/", true, map[string]string{}, "trailing slash matches"},

		// Anchoring
		{"*.gd", "a.gd.bak", false, nil, "*.gd does not match a.gd.bak"},
		{"src/**", "xsrc/a.gd", false, nil, "src/** does not match xsrc/a.gd"},

		// Regex metacharacters are literal
		{"*.gd", "xgd", false, nil, "dot in *.gd is literal, not any char"},
		{"a.b.c", "axbxc", false, nil, "dots are literal in middle of pattern"},
		{"+file", "+file", true, map[string]string{}, "+ is literal"},
		{"[ab]", "[ab]", true, map[string]string{}, "[ and ] are literal"},
		{"(test)", "(test)", true, map[string]string{}, "parens are literal"},

		// **/ matches zero directories (allowing **/*_test.gd to match x_test.gd)
		{"**/*_test.gd", "x_test.gd", true, map[string]string{}, "**/ matches zero directories"},
		{"**/*_test.gd", "a/b/x_test.gd", true, map[string]string{}, "**/ matches multiple directories"},

		// Bare ** crosses slashes
		{"src/**", "src/a/b/c.gd", true, map[string]string{}, "bare ** crosses slashes"},
		{"src/**", "src/file.gd", true, map[string]string{}, "bare ** also matches single segment"},

		// ? matches exactly one non-slash character
		{"?.gd", "a.gd", true, map[string]string{}, "? matches one char"},
		{"?.gd", "ab.gd", false, nil, "? does not match two chars"},
		{"?.gd", ".gd", false, nil, "? does not match zero chars"},
		{"a/?.gd", "a/x.gd", true, map[string]string{}, "? matches one char after slash"},
		{"a/?.gd", "a/x/y.gd", false, nil, "? does not match slash"},

		// {feature} captures exactly one segment
		{"{feature}/x", "a/x", true, map[string]string{"feature": "a"}, "{feature} captures one segment"},
		{"{feature}/x", "a/b/x", false, nil, "{feature} does not match multiple segments"},
		{"{feature}", "combat", true, map[string]string{"feature": "combat"}, "sole {feature} captures whole segment"},
		{"{feature}", "com/bat", false, nil, "{feature} does not match across slash"},

		// Captures map on a no-capture match
		{"*.gd", "file.gd", true, map[string]string{}, "no captures returns empty map (not nil)"},

		// Cache key normalization
		{"src/**", "src/x.gd", true, map[string]string{}, "normalized path matches"},
		{"./src/**", "src/x.gd", true, map[string]string{}, "./src/** normalizes to src/**"},

		// ** matches zero segments at start/end
		{"**/file.gd", "file.gd", true, map[string]string{}, "**/ at start matches zero dirs"},
		{"file/**", "file/", true, map[string]string{}, "** at end matches zero dirs"},
	}

	for _, testCase := range cases {
		matches, captures := Match(testCase.pattern, testCase.name)
		if matches != testCase.wantMatch {
			t.Errorf("Match(%q, %q): got match=%v, want %v (%s)", testCase.pattern, testCase.name, matches, testCase.wantMatch, testCase.description)
		}

		// Check captures behavior
		if testCase.wantMatch {
			if testCase.wantCaptures == nil {
				// Should not reach here - when match is true, wantCaptures should be specified
				t.Errorf("Match(%q, %q): test case missing wantCaptures (%s)", testCase.pattern, testCase.name, testCase.description)
			} else {
				// Compare maps
				for key, wantValue := range testCase.wantCaptures {
					if captures[key] != wantValue {
						t.Errorf("Match(%q, %q): captures[%q] = %q, want %q (%s)", testCase.pattern, testCase.name, key, captures[key], wantValue, testCase.description)
					}
				}
				// Check no extra keys
				if len(captures) != len(testCase.wantCaptures) {
					t.Errorf("Match(%q, %q): captures has %d keys, want %d (%s)", testCase.pattern, testCase.name, len(captures), len(testCase.wantCaptures), testCase.description)
				}
			}
		} else {
			if len(captures) > 0 {
				t.Errorf("Match(%q, %q): expected no match but got captures=%v (%s)", testCase.pattern, testCase.name, captures, testCase.description)
			}
		}
	}
}

func TestPatternMatchMethod(t *testing.T) {
	pattern, err := Compile("**/*.gd")
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	cases := []struct {
		name    string
		matches bool
	}{
		{"file.gd", true},
		{"a/b/file.gd", true},
		{"file.txt", false},
	}

	for _, testCase := range cases {
		matches, _ := pattern.Match(testCase.name)
		if matches != testCase.matches {
			t.Errorf("pattern.Match(%q) = %v, want %v", testCase.name, matches, testCase.matches)
		}
	}
}

func TestMatchAnyAndCompile(t *testing.T) {
	if !MatchAny([]string{"a/**", "b/**"}, "b/c.gd") {
		t.Error("MatchAny should match the second pattern")
	}
	if MatchAny(nil, "b/c.gd") {
		t.Error("MatchAny on no patterns should be false")
	}
	_, err := Compile("a/**")
	if err != nil {
		t.Errorf("Compile should succeed for valid glob pattern: %v", err)
	}
}
