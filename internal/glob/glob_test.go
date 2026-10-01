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
