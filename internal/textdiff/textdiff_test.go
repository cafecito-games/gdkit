package textdiff

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func numbered(count int) []string {
	lines := make([]string, count)
	for index := range lines {
		lines[index] = fmt.Sprintf("line %d\n", index+1)
	}
	return lines
}

func TestUnified(t *testing.T) {
	twenty := numbered(20)
	distant := append([]string{}, twenty...)
	distant[1] = "changed 2\n"
	distant[17] = "changed 18\n"
	adjacent := append([]string{}, twenty...)
	adjacent[7] = "changed 8\n"
	adjacent[12] = "changed 13\n"

	cases := map[string]struct {
		old, new string
		want     string
	}{
		"identical":  {"a\nb\n", "a\nb\n", ""},
		"both empty": {"", "", ""},
		"single line change": {
			"a\nb\nc\n", "a\nB\nc\n",
			"--- old\n+++ new\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n",
		},
		"insertion at start": {
			"a\nb\n", "first\na\nb\n",
			"--- old\n+++ new\n@@ -1,2 +1,3 @@\n+first\n a\n b\n",
		},
		"deletion at end": {
			"a\nb\nc\nd\ne\n", "a\nb\nc\nd\n",
			"--- old\n+++ new\n@@ -2,4 +2,3 @@\n b\n c\n d\n-e\n",
		},
		"two distant changes": {
			strings.Join(twenty, ""), strings.Join(distant, ""),
			"--- old\n+++ new\n" +
				"@@ -1,5 +1,5 @@\n line 1\n-line 2\n+changed 2\n line 3\n line 4\n line 5\n" +
				"@@ -15,6 +15,6 @@\n line 15\n line 16\n line 17\n-line 18\n+changed 18\n line 19\n line 20\n",
		},
		"adjacent changes share a hunk": {
			strings.Join(twenty, ""), strings.Join(adjacent, ""),
			"--- old\n+++ new\n@@ -5,12 +5,12 @@\n line 5\n line 6\n line 7\n-line 8\n+changed 8\n" +
				" line 9\n line 10\n line 11\n line 12\n-line 13\n+changed 13\n line 14\n line 15\n line 16\n",
		},
		"new lacks trailing newline": {
			"a\nb\n", "a\nb",
			"--- old\n+++ new\n@@ -1,2 +1,2 @@\n a\n-b\n+b\n\\ No newline at end of file\n",
		},
		"old lacks trailing newline": {
			"a\nb", "a\nb\n",
			"--- old\n+++ new\n@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+b\n",
		},
		"neither has trailing newline": {
			"a\nb", "A\nb",
			"--- old\n+++ new\n@@ -1,2 +1,2 @@\n-a\n+A\n b\n\\ No newline at end of file\n",
		},
		"empty old": {
			"", "a\nb\n",
			"--- old\n+++ new\n@@ -0,0 +1,2 @@\n+a\n+b\n",
		},
		"empty new": {
			"a\nb\n", "",
			"--- old\n+++ new\n@@ -1,2 +0,0 @@\n-a\n-b\n",
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			got := Unified("old", "new", []byte(test.old), []byte(test.new))
			if got != test.want {
				t.Fatalf("Unified:\n%s\nwant:\n%s", got, test.want)
			}
		})
	}
}

func TestUnifiedAppliesWithPatch(t *testing.T) {
	patch, err := exec.LookPath("patch")
	if err != nil {
		t.Skip("patch is not installed")
	}
	twenty := numbered(20)
	edited := append([]string{"inserted\n"}, twenty[:4]...)
	edited = append(edited, "replaced 5\n", "extra\n")
	edited = append(edited, twenty[5:15]...)
	edited = append(edited, twenty[17:]...)
	cases := map[string]struct{ old, new string }{
		"mixed edits":         {strings.Join(twenty, ""), strings.Join(edited, "")},
		"newline removed":     {"a\nb\nc\n", "a\nB\nc"},
		"newline added":       {"a\nb\nc", "a\nb\nc\n"},
		"repeated lines":      {"x\nx\ny\nx\nx\n", "x\ny\ny\nx\nz\nx\n"},
		"everything replaced": {"a\nb\nc\n", "d\ne\n"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, "file.txt")
			patchFile := filepath.Join(directory, "change.patch")
			if err := os.WriteFile(target, []byte(test.old), 0o644); err != nil {
				t.Fatal(err)
			}
			diff := Unified("a/file.txt", "b/file.txt", []byte(test.old), []byte(test.new))
			if err := os.WriteFile(patchFile, []byte(diff), 0o644); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command(patch, "-s", target, patchFile).CombinedOutput(); err != nil {
				t.Fatalf("patch rejected the diff: %v\n%s\n%s", err, output, diff)
			}
			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.new {
				t.Fatalf("patched file = %q, want %q\n%s", got, test.new, diff)
			}
		})
	}
}

func TestUnifiedStaysFastWhenEveryLineChanges(t *testing.T) {
	var old, new strings.Builder
	for index := range 6000 {
		if index%10 == 0 {
			old.WriteString("\n")
			new.WriteString("\n")
			continue
		}
		fmt.Fprintf(&old, "    statement_%d()\n", index)
		fmt.Fprintf(&new, "\tstatement_%d()\n", index)
	}
	started := time.Now()
	diff := Unified("old", "new", []byte(old.String()), []byte(new.String()))
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("diff of 6000 changed lines took %s", elapsed)
	}
	// The "+++" header line is the one extra match for the addition marker.
	removals, additions := strings.Count(diff, "\n-"), strings.Count(diff, "\n+")-1
	if removals != 5400 || additions != 5400 {
		t.Fatalf("diff has %d removals and %d additions, want 5400 of each", removals, additions)
	}
}
