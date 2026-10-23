package buildinfo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCurrentUsesInjectedValues(t *testing.T) {
	oldVersion, oldCommit, oldDate, oldTreeState := version, commit, date, treeState
	t.Cleanup(func() {
		version, commit, date, treeState = oldVersion, oldCommit, oldDate, oldTreeState
	})
	version = "v1.2.3"
	commit = "0123456789abcdef0123456789abcdef01234567"
	date = "2026-09-30T12:00:00Z"
	treeState = "dirty"

	got := Current()
	require.Equal(t, "1.2.3", got.Version)
	require.Equal(t, commit, got.Commit)
	require.Equal(t, date, got.Date)
	require.True(t, got.Dirty)
}

func TestInfoString(t *testing.T) {
	info := Info{
		Version: "1.2.3",
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		Date:    "2026-09-30T12:00:00Z",
		Dirty:   true,
	}
	want := "gdkit version 1.2.3 (commit 0123456789ab, dirty), built 2026-09-30T12:00:00Z"
	got := info.String()
	require.Equal(t, want, got)
}

func TestNormalizeVersion(t *testing.T) {
	for input, want := range map[string]string{
		"v1.2.3":  "1.2.3",
		"1.2.3":   "1.2.3",
		"(devel)": "dev",
		"":        "dev",
	} {
		got := normalizeVersion(input)
		assert.Equal(t, want, got)
	}
}
