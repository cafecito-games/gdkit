package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/project"
)

const uncleanScript = "extends Node\nvar a=1\nfunc doThing( x ):\n\treturn x+a\n"

// writeIgnoreProject builds a project whose only problem file sits in a
// nested addons directory, which the default exclude globs do not cover.
func writeIgnoreProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", formattedScript)
	writeCLIFile(t, root, "apps/editor/addons/tool/tool.gd", uncleanScript)
	return root
}

func TestRunLintAndFormatHonourIgnoreFile(t *testing.T) {
	commands := [][]string{{"lint", "check"}, {"format", "check"}}
	for _, command := range commands {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			root := writeIgnoreProject(t)
			var stdout, stderr bytes.Buffer
			if code := run(append(command, root), &stdout, &stderr); code != 1 {
				t.Fatalf("without an ignore file: exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), "apps/editor/addons/tool/tool.gd") {
				t.Fatalf("unexpected output: %s", stdout.String())
			}

			writeCLIFile(t, root, project.IgnoreFileName, "addons/\n")
			stdout.Reset()
			stderr.Reset()
			if code := run(append(command, root), &stdout, &stderr); code != 0 {
				t.Fatalf("with an ignore file: exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if strings.Contains(stdout.String(), "addons") {
				t.Fatalf("ignored file appears in output: %s", stdout.String())
			}
		})
	}
}

func TestRunFormatWriteLeavesIgnoredFilesUntouched(t *testing.T) {
	root := writeIgnoreProject(t)
	writeCLIFile(t, root, "scripts/enemy.gd", unformattedScript)
	writeCLIFile(t, root, project.IgnoreFileName, "addons/\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"format", "write", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if got := readCLIFile(t, root, "apps/editor/addons/tool/tool.gd"); got != uncleanScript {
		t.Fatalf("format write rewrote an ignored file: %q", got)
	}
	if got := readCLIFile(t, root, "scripts/enemy.gd"); got == unformattedScript {
		t.Fatal("format write did not rewrite a file that is not ignored")
	}
	want := "reformatted scripts/enemy.gd\nformat write: 1 reformatted, 1 unchanged\n"
	if stdout.String() != want {
		t.Fatalf("output = %q, want %q", stdout.String(), want)
	}
}

func TestRunFormatWriteWithoutIgnoreFileRewritesNestedAddons(t *testing.T) {
	root := writeIgnoreProject(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"format", "write", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if got := readCLIFile(t, root, "apps/editor/addons/tool/tool.gd"); got == uncleanScript {
		t.Fatal("format write should rewrite the file when nothing ignores it")
	}
}

func TestRunRejectsMalformedIgnoreFile(t *testing.T) {
	commands := [][]string{{"lint", "check"}, {"format", "check"}, {"format", "write"}}
	for _, command := range commands {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			root := writeIgnoreProject(t)
			writeCLIFile(t, root, project.IgnoreFileName, "addons/\n[unterminated\n")
			var stdout, stderr bytes.Buffer
			if code := run(append(command, root), &stdout, &stderr); code != 2 {
				t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if !strings.HasPrefix(stderr.String(), "gdkit: parse .gdkitignore: line 2: ") {
				t.Fatalf("unexpected error: %s", stderr.String())
			}
			if got := readCLIFile(t, root, "apps/editor/addons/tool/tool.gd"); got != uncleanScript {
				t.Fatalf("a failed run rewrote a file: %q", got)
			}
		})
	}
}

// Hiding a file from the architecture analyzer would drop its class from the
// index, so arch check must see every file whatever the ignore file says.
func TestRunArchCheckDoesNotReadIgnoreFile(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "features/combat/domain/health.gd", "class_name Health extends Node\n")
	arguments := []string{"arch", "check", "--format", "json", "--show-edges", root}
	var before, stderr bytes.Buffer
	if code := run(arguments, &before, &stderr); code != 1 {
		t.Fatalf("exit %d: stdout=%s stderr=%s", code, before.String(), stderr.String())
	}

	for _, contents := range []string{"features/\n*.gd\n", "[unterminated\n"} {
		writeCLIFile(t, root, project.IgnoreFileName, contents)
		var after bytes.Buffer
		stderr.Reset()
		if code := run(arguments, &after, &stderr); code != 1 {
			t.Fatalf("with ignore file %q: exit %d: stdout=%s stderr=%s", contents, code, after.String(), stderr.String())
		}
		if after.String() != before.String() {
			t.Fatalf("ignore file %q changed arch check output:\n%s\nwant:\n%s", contents, after.String(), before.String())
		}
	}
}
