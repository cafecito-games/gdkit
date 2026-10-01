package format

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// assertNoTemporaryFiles fails if a write left its temporary file behind.
func assertNoTemporaryFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".gdkit-format-") {
			t.Errorf("temporary file left behind: %s", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
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
	if err := os.Chtimes(cleanPath, past, past); err != nil {
		t.Fatal(err)
	}

	written, err := Apply(snapshot, report)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(written, []string{"a/changed.gd", "b/changed.gd"}) {
		t.Fatalf("written = %v", written)
	}
	for _, result := range report.Changed() {
		got, err := os.ReadFile(filepath.Join(snapshot.Root, filepath.FromSlash(result.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(result.Formatted) || string(got) == unformatted {
			t.Fatalf("%s holds %q, want %q", result.Path, got, result.Formatted)
		}
	}
	info, err := os.Stat(cleanPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(past) {
		t.Fatalf("unchanged file was rewritten: modified %v, want %v", info.ModTime(), past)
	}
	got, err := os.ReadFile(filepath.Join(snapshot.Root, "broken.gd"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != broken {
		t.Fatalf("file with a diagnostic was rewritten: %q", got)
	}
	assertNoTemporaryFiles(t, snapshot.Root)
}

func TestApplyWritesNothingForAnUnchangedProject(t *testing.T) {
	report, snapshot := formatProject(t, DefaultConfig(), map[string]string{"clean.gd": "var a = 1\n"})
	written, err := Apply(snapshot, report)
	if err != nil {
		t.Fatal(err)
	}
	if written == nil || len(written) != 0 {
		t.Fatalf("written = %#v, want an empty non-nil slice", written)
	}
}

func TestApplyPreservesFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not preserved on Windows")
	}
	config := DefaultConfig()
	snapshot := loadProject(t, config, map[string]string{"script.gd": unformatted, "private.gd": unformatted})
	modes := map[string]os.FileMode{"script.gd": 0o755, "private.gd": 0o600}
	for name, mode := range modes {
		if err := os.Chmod(filepath.Join(snapshot.Root, name), mode); err != nil {
			t.Fatal(err)
		}
	}
	formatter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(snapshot, formatter.Format(snapshot)); err != nil {
		t.Fatal(err)
	}
	for name, mode := range modes {
		info, err := os.Stat(filepath.Join(snapshot.Root, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("%s mode = %v, want %v", name, info.Mode().Perm(), mode)
		}
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
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	written, err := Apply(snapshot, report)
	if err == nil || !strings.HasPrefix(err.Error(), "write locked/b.gd: ") {
		t.Fatalf("error = %v, want a write error naming locked/b.gd", err)
	}
	if !reflect.DeepEqual(written, []string{"a.gd"}) {
		t.Fatalf("written = %v, want only a.gd", written)
	}
	for _, name := range []string{"locked/b.gd", "z.gd"} {
		got, readErr := os.ReadFile(filepath.Join(snapshot.Root, filepath.FromSlash(name)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(got) != unformatted {
			t.Fatalf("%s was rewritten after the failure", name)
		}
	}
	assertNoTemporaryFiles(t, snapshot.Root)
}

func TestApplyFailsWhenTargetIsMissing(t *testing.T) {
	report, snapshot := formatProject(t, DefaultConfig(), map[string]string{"a.gd": unformatted})
	if err := os.Remove(filepath.Join(snapshot.Root, "a.gd")); err != nil {
		t.Fatal(err)
	}
	written, err := Apply(snapshot, report)
	if err == nil || !strings.HasPrefix(err.Error(), "write a.gd: ") {
		t.Fatalf("error = %v, want a write error naming a.gd", err)
	}
	if len(written) != 0 {
		t.Fatalf("written = %v, want none", written)
	}
	assertNoTemporaryFiles(t, snapshot.Root)
}
