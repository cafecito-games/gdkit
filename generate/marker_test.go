package generate

import (
	"slices"
	"testing"

	"github.com/cafecito-games/gdkit/internal/suppression"
)

func TestMarkerGenerate(t *testing.T) {
	for line, want := range map[string][]string{
		"# gdkit:generate = to_string, equals":  {"to_string", "equals"},
		"\t# gdkit : generate=equals":           {"equals"},
		"# gdlint:generate = to_string":         {"to_string"},
		"# gdkit:generate=  equals ,to_string ": {"equals", "to_string"},
	} {
		got, err := MatchGenerate(line)
		if err != nil {
			t.Errorf("MatchGenerate(%q) errored: %v", line, err)
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("MatchGenerate(%q) = %v, want %v", line, got, want)
		}
	}
}

// Unlike internal/suppression, which lets a list run to end of line for gdlint
// compatibility, this directive is strict: a remark is an error rather than
// part of the last name.
func TestMarkerGenerateRejectsRubbish(t *testing.T) {
	for _, line := range []string{
		"# gdkit:generate = equals remark",
		"# gdkit:generate = ",
		"# gdkit:generate = equals,",
		"# gdkit:generate = no_such_generator",
	} {
		if _, err := MatchGenerate(line); err == nil {
			t.Errorf("MatchGenerate(%q) accepted a bad list", line)
		}
	}
}

func TestMarkerGenerateIgnoresOtherDirectives(t *testing.T) {
	for _, line := range []string{
		"# gdkit:generate:ignore",
		"# gdkit:generate:ignore-field",
		"# gdkit:ignore = equals",
		"# generate = equals",
		"var x = 1",
	} {
		names, err := MatchGenerate(line)
		if err != nil || names != nil {
			t.Errorf("MatchGenerate(%q) = %v, %v, want no match", line, names, err)
		}
	}
}

func TestMarkerIgnoreFormsAreDistinct(t *testing.T) {
	if !MatchIgnore("# gdkit:generate:ignore") {
		t.Error("the class form did not match")
	}
	if MatchIgnore("# gdkit:generate:ignore-field") {
		t.Error("the class form matched the field form")
	}
	if !MatchIgnoreField("var x = 1  # gdkit:generate:ignore-field") {
		t.Error("the field form did not match trailing")
	}
	if MatchIgnoreField("# gdkit:generate:ignore") {
		t.Error("the field form matched the class form")
	}
}

// internal/suppression must not see these as suppression directives, or every
// marker in every adopting project becomes an unknown-ignore finding.
func TestMarkersAreNotSuppressionDirectives(t *testing.T) {
	for _, line := range []string{
		"# gdkit:generate = equals",
		"# gdkit:generate:ignore",
		"# gdkit:generate:ignore-field",
	} {
		for _, kind := range suppression.Kinds {
			if _, _, ok := suppression.Match(kind, line); ok {
				t.Errorf("suppression.Match(%v, %q) matched", kind, line)
			}
		}
	}
}

func TestStandsAlone(t *testing.T) {
	for line, want := range map[string]bool{
		"# gdkit:generate:ignore":            true,
		"\t\t# gdkit:generate:ignore":        true,
		"var x = 1  # gdkit:generate:ignore": false,
	} {
		if got := standsAlone(line); got != want {
			t.Errorf("standsAlone(%q) = %v, want %v", line, got, want)
		}
	}
}
