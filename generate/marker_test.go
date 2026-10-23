package generate

import (
	"testing"

	"github.com/cafecito-games/gdkit/internal/suppression"
	"github.com/stretchr/testify/assert"
)

func TestMarkerGenerate(t *testing.T) {
	for line, want := range map[string][]string{
		"# gdkit:generate = to_string, equals":  {"to_string", "equals"},
		"\t# gdkit : generate=equals":           {"equals"},
		"# gdlint:generate = to_string":         {"to_string"},
		"# gdkit:generate=  equals ,to_string ": {"equals", "to_string"},
	} {
		got, err := MatchGenerate(line)
		if !assert.NoError(t, err, "MatchGenerate(%q) errored", line) {
			continue
		}
		assert.Equal(t, want, got, "MatchGenerate(%q)", line)
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
		_, err := MatchGenerate(line)
		assert.Error(t, err, "MatchGenerate(%q) accepted a bad list", line)
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
		assert.NoError(t, err, "MatchGenerate(%q)", line)
		assert.Nil(t, names, "MatchGenerate(%q)", line)
	}
}

func TestMarkerIgnoreFormsAreDistinct(t *testing.T) {
	assert.True(t, MatchIgnore("# gdkit:generate:ignore"), "the class form did not match")
	assert.False(t, MatchIgnore("# gdkit:generate:ignore-field"), "the class form matched the field form")
	assert.True(t, MatchIgnoreField("var x = 1  # gdkit:generate:ignore-field"), "the field form did not match trailing")
	assert.False(t, MatchIgnoreField("# gdkit:generate:ignore"), "the field form matched the class form")
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
			_, _, ok := suppression.Match(kind, line)
			assert.False(t, ok, "suppression.Match(%d, %q) matched", kind, line)
		}
	}
}

func TestStandsAlone(t *testing.T) {
	for line, want := range map[string]bool{
		"# gdkit:generate:ignore":            true,
		"\t\t# gdkit:generate:ignore":        true,
		"var x = 1  # gdkit:generate:ignore": false,
	} {
		got := standsAlone(line)
		assert.Equal(t, want, got, "standsAlone(%q)", line)
	}
}
