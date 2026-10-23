package format

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertNoTemporaryFiles fails if a write left its temporary file behind.
func assertNoTemporaryFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		assert.False(t, strings.HasPrefix(entry.Name(), ".gdkit-format-"), "temporary file left behind: %s", name)
		return nil
	})
	require.NoError(t, err)
}

func TestApplyWritesChangedFilesOnly(t *testing.T) {
	const broken = "func (:\n"
	const clean = "var a = 1\n"
	report, snapshot := formatProject(t, DefaultConfig(), map[string]string{
		"b/changed.gd": unformatted,
		"a/changed.gd": unformatted,
		"clean.gd":     clean,
		"broken.gd":    broken,
	})
	cleanPath := filepath.Join(snapshot.Root, "clean.gd")
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	err := os.Chtimes(cleanPath, past, past)
	require.NoError(t, err)

	written, err := Apply(snapshot, report)
	require.NoError(t, err)
	require.Equal(t, []string{"a/changed.gd", "b/changed.gd"}, written)
	for _, result := range report.Changed() {
		got, err := os.ReadFile(filepath.Join(snapshot.Root, filepath.FromSlash(result.Path)))
		require.NoError(t, err)
		require.Equal(t, string(result.Formatted), string(got))
		require.NotEqual(t, unformatted, string(got))
	}
	info, err := os.Stat(cleanPath)
	require.NoError(t, err)
	require.True(t, info.ModTime().Equal(past))
	got, err := os.ReadFile(filepath.Join(snapshot.Root, "broken.gd"))
	require.NoError(t, err)
	require.Equal(t, broken, string(got))
	assertNoTemporaryFiles(t, snapshot.Root)
}

func TestApplyWritesNothingForAnUnchangedProject(t *testing.T) {
	report, snapshot := formatProject(t, DefaultConfig(), map[string]string{"clean.gd": "var a = 1\n"})
	written, err := Apply(snapshot, report)
	require.NoError(t, err)
	require.NotNil(t, written)
	require.Empty(t, written)
}

func TestApplyPreservesFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not preserved on Windows")
	}
	config := DefaultConfig()
	snapshot := loadProject(t, config, map[string]string{"script.gd": unformatted, "private.gd": unformatted})
	modes := map[string]os.FileMode{"script.gd": 0o755, "private.gd": 0o600}
	for name, mode := range modes {
		err := os.Chmod(filepath.Join(snapshot.Root, name), mode)
		require.NoError(t, err)
	}
	formatter, err := New(config)
	require.NoError(t, err)
	_, err = Apply(snapshot, formatter.Format(snapshot))
	require.NoError(t, err)
	for name, mode := range modes {
		info, err := os.Stat(filepath.Join(snapshot.Root, name))
		require.NoError(t, err)
		require.Equal(t, mode, info.Mode().Perm())
	}
	assertNoTemporaryFiles(t, snapshot.Root)
}

func TestApplyStopsAtFirstFailureAndNamesThePath(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not block writes here")
	}
	report, snapshot := formatProject(t, DefaultConfig(), map[string]string{
		"a.gd":        unformatted,
		"locked/b.gd": unformatted,
		"z.gd":        unformatted,
	})
	locked := filepath.Join(snapshot.Root, "locked")
	err := os.Chmod(locked, 0o555)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	written, err := Apply(snapshot, report)
	require.Error(t, err)
	require.ErrorContains(t, err, "write locked/b.gd: ")
	require.Equal(t, []string{"a.gd"}, written)
	for _, name := range []string{"locked/b.gd", "z.gd"} {
		got, readErr := os.ReadFile(filepath.Join(snapshot.Root, filepath.FromSlash(name)))
		require.NoError(t, readErr)
		require.Equal(t, unformatted, string(got))
	}
	assertNoTemporaryFiles(t, snapshot.Root)
}

func TestApplyFailsWhenTargetIsMissing(t *testing.T) {
	report, snapshot := formatProject(t, DefaultConfig(), map[string]string{"a.gd": unformatted})
	err := os.Remove(filepath.Join(snapshot.Root, "a.gd"))
	require.NoError(t, err)
	written, err := Apply(snapshot, report)
	require.Error(t, err)
	require.ErrorContains(t, err, "write a.gd: ")
	require.Empty(t, written)
	assertNoTemporaryFiles(t, snapshot.Root)
}

func TestApplyRefusesToOverwriteAFileChangedSinceItWasRead(t *testing.T) {
	const edited = "extends Node\nvar edited_meanwhile=2\n"
	report, snapshot := formatProject(t, DefaultConfig(), map[string]string{
		"a.gd": unformatted,
		"b.gd": unformatted,
		"c.gd": unformatted,
	})
	err := os.WriteFile(filepath.Join(snapshot.Root, "b.gd"), []byte(edited), 0o644)
	require.NoError(t, err)

	written, err := Apply(snapshot, report)
	require.EqualError(t, err, "write b.gd: file changed on disk since it was read")
	require.Equal(t, []string{"a.gd"}, written)
	for name, want := range map[string]string{"b.gd": edited, "c.gd": unformatted} {
		got, readErr := os.ReadFile(filepath.Join(snapshot.Root, name))
		require.NoError(t, readErr)
		require.Equal(t, want, string(got))
	}
	assertNoTemporaryFiles(t, snapshot.Root)
}
