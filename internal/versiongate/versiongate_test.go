package versiongate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseReadsTripleAndIgnoresSuffixes(t *testing.T) {
	for input, want := range map[string]Version{
		"0.2.0":                  {0, 2, 0},
		"v0.2.0":                 {0, 2, 0},
		"1.10.2":                 {1, 10, 2},
		"0.2.1-SNAPSHOT-9060007": {0, 2, 1},
		"0.2.0+meta":             {0, 2, 0},
		"v1.0.0-rc.1+build.9":    {1, 0, 0},
		"10.20.30":               {10, 20, 30},
		"0.0.0":                  {0, 0, 0},
	} {
		got, err := Parse(input)
		if assert.NoError(t, err) {
			assert.Equal(t, want, got)
		}
	}
}

func TestParseRejectsNonReleaseVersions(t *testing.T) {
	for _, input := range []string{"", "dev", "0.2", "0.2.0.1", "a.b.c", "-1.0.0", "0..0", "1.2.x", "+1.2.3", "0.2.0 "} {
		_, err := Parse(input)
		assert.Error(t, err)
	}
}

func TestParseRequirementIsStricterThanParse(t *testing.T) {
	got, err := ParseRequirement("0.3.0")
	require.NoError(t, err)
	require.Equal(t, Version{0, 3, 0}, got)
	// A floor comes from a config file or a command line, where a leading "v"
	// or a prerelease suffix is a mistake worth naming.
	for _, input := range []string{"v0.3.0", "0.2.1-SNAPSHOT-9060007", "0.2.0+meta", "0.3", "", "dev"} {
		_, err := ParseRequirement(input)
		assert.Error(t, err)
	}
}

func TestLessOrdersNumerically(t *testing.T) {
	cases := []struct {
		left  Version
		right Version
		want  bool
	}{
		{Version{0, 2, 0}, Version{0, 2, 0}, false},
		{Version{0, 2, 0}, Version{0, 2, 1}, true},
		{Version{0, 2, 1}, Version{0, 2, 0}, false},
		{Version{0, 2, 0}, Version{0, 3, 0}, true},
		{Version{0, 3, 0}, Version{0, 2, 0}, false},
		{Version{0, 9, 0}, Version{1, 0, 0}, true},
		{Version{1, 0, 0}, Version{0, 9, 0}, false},
		// String comparison would order 1.10.0 before 1.9.0.
		{Version{1, 9, 0}, Version{1, 10, 0}, true},
		{Version{1, 10, 0}, Version{1, 9, 0}, false},
		{Version{1, 2, 9}, Version{1, 2, 10}, true},
	}
	for _, test := range cases {
		got := test.left.Less(test.right)
		assert.Equal(t, test.want, got)
	}
}

func TestIsDevelopment(t *testing.T) {
	for _, input := range []string{"dev", ""} {
		assert.True(t, IsDevelopment(input))
	}
	for _, input := range []string{"0.2.0", "v0.2.0", "0.2.1-SNAPSHOT-9060007", "1.10.2"} {
		assert.False(t, IsDevelopment(input))
	}
}

// ParseEngineVersion exists because neither existing entry point fits a Godot
// version written by hand: ParseRequirement demands all three components, and
// nobody writes "4.7.0", while Parse tolerates a leading "v" and a prerelease
// suffix because it reads what a binary reports about itself.
func TestParseEngineVersion(t *testing.T) {
	tests := []struct {
		value   string
		want    Version
		wantErr bool
	}{
		{value: "4.7", want: Version{Major: 4, Minor: 7}},
		{value: "4.7.0", want: Version{Major: 4, Minor: 7}},
		{value: "4.7.1", want: Version{Major: 4, Minor: 7, Patch: 1}},
		{value: "4.0", want: Version{Major: 4}},
		{value: "04.07", want: Version{Major: 4, Minor: 7}},
		{value: "1234567890.1", wantErr: true},
		{value: "4", wantErr: true},
		{value: "v4.7", wantErr: true},
		{value: "4.7.0-beta1", wantErr: true},
		{value: "4.7.", wantErr: true},
		{value: "", wantErr: true},
		{value: "4.x", wantErr: true},
	}
	for _, test := range tests {
		got, err := ParseEngineVersion(test.value)
		if test.wantErr {
			if !assert.Error(t, err) {
				continue
			}
			// The message names the two forms this function accepts.
			// parseTriple's own message names only the three-component form,
			// which would tell a reader to write a patch level that is
			// optional here.
			assert.Contains(t, err.Error(), "major.minor or major.minor.patch")
			continue
		}
		if !assert.NoError(t, err) {
			continue
		}
		assert.Equal(t, test.want, got)
	}
}
