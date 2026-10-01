package lint

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// finding is the comparison key: gdlint reports column 0 for several rules
// where gdkit reports a real column, so columns never take part.
type finding struct {
	path string
	line int
	rule string
}

func (f finding) String() string { return fmt.Sprintf("%s:%d (%s)", f.path, f.line, f.rule) }

// gdkitOnlyRules are reported by gdkit with no gdlint counterpart, so they are
// removed from gdkit's side before comparing:
//   - unknown-ignore: gdkit flags an ignore comment naming a rule that does not
//     exist (or an empty name); gdlint silently ignores those.
var gdkitOnlyRules = map[string]bool{"unknown-ignore": true}

// gdlintBatchSize bounds how many files one gdlint invocation receives, which
// keeps the command line short and lets batches run in parallel.
const gdlintBatchSize = 100

var gdlintLine = regexp.MustCompile(`^(.+\.gd):(\d+):.*\(([a-z0-9-]+)\)$`)

// TestDifferential lints the shared fixture with gdkit and with the real
// gdlint binary and requires the same (file, line, rule) set from both.
func TestDifferential(t *testing.T) {
	requireGdlint(t)
	root, err := filepath.Abs(filepath.Join("testdata", "parity"))
	if err != nil {
		t.Fatal(err)
	}
	ours, theirs := compareWithGdlint(t, root, DefaultConfig())
	assertSameFindings(t, ours, theirs)

	covered := make(map[string]bool)
	for key := range ours {
		covered[key.rule] = true
	}
	for _, rule := range RuleNames() {
		if rule == "source-parse" || rule == "unknown-ignore" {
			continue
		}
		if !covered[rule] {
			t.Errorf("fixture does not violate rule %s", rule)
		}
	}
	t.Logf("compared %d findings", len(ours))
}

// TestDifferentialCorpus compares gdkit with gdlint over a real project. Point
// GDKIT_PARITY_CORPUS at the project root; it is skipped when unset.
func TestDifferentialCorpus(t *testing.T) {
	requireGdlint(t)
	root := os.Getenv("GDKIT_PARITY_CORPUS")
	if root == "" {
		t.Skip("GDKIT_PARITY_CORPUS is not set")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	ours, theirs := compareWithGdlint(t, root, corpusConfig())
	assertSameFindings(t, ours, theirs)
	t.Logf("compared %d findings", len(ours))
}

func requireGdlint(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gdlint"); err != nil {
		t.Skip("gdlint is not on PATH")
	}
}

// compareWithGdlint returns the findings from gdkit and from gdlint for the
// project at root. Both are restricted to the files gdkit discovered, so the
// two tools see the same input regardless of how each excludes paths.
func compareWithGdlint(t *testing.T, root string, config Config) (ours, theirs map[finding]bool) {
	t.Helper()
	// gdlint reads gdlintrc from its working directory and would silently
	// change thresholds, so a stray one makes the comparison meaningless.
	if _, err := os.Stat(filepath.Join(root, "gdlintrc")); err == nil {
		t.Fatalf("%s contains a gdlintrc, which would change gdlint's thresholds", root)
	}
	snapshot := loadCorpus(t, root, config)
	linter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	ours = make(map[finding]bool)
	for _, diagnostic := range linter.Lint(snapshot).Diagnostics {
		if gdkitOnlyRules[diagnostic.Rule] {
			continue
		}
		ours[finding{diagnostic.Path, diagnostic.Line, diagnostic.Rule}] = true
	}
	theirs = runGdlint(t, root, snapshot.Paths)
	return ours, theirs
}

// runGdlint runs gdlint with root as its working directory over the given
// slash-separated, root-relative paths and returns what it reported.
func runGdlint(t *testing.T, root string, paths []string) map[finding]bool {
	t.Helper()
	var batches [][]string
	for start := 0; start < len(paths); start += gdlintBatchSize {
		end := min(start+gdlintBatchSize, len(paths))
		batches = append(batches, paths[start:end])
	}

	var (
		mutex    sync.Mutex
		wait     sync.WaitGroup
		findings = make(map[finding]bool)
		failures []string
	)
	workers := make(chan struct{}, max(1, runtime.NumCPU()/2))
	for _, batch := range batches {
		wait.Add(1)
		workers <- struct{}{}
		go func() {
			defer wait.Done()
			defer func() { <-workers }()
			command := exec.Command("gdlint", batch...)
			command.Dir = root
			output, err := command.CombinedOutput()
			// gdlint exits 1 when it reports problems, which is expected.
			var exitError *exec.ExitError
			if err != nil && !(errors.As(err, &exitError) && exitError.ExitCode() == 1) {
				mutex.Lock()
				failures = append(failures, fmt.Sprintf("gdlint: %v\n%s", err, output))
				mutex.Unlock()
				return
			}
			parsed := parseGdlintOutput(string(output))
			mutex.Lock()
			for key := range parsed {
				findings[key] = true
			}
			mutex.Unlock()
		}()
	}
	wait.Wait()
	if len(failures) > 0 {
		t.Fatal(strings.Join(failures, "\n"))
	}
	return findings
}

func parseGdlintOutput(output string) map[finding]bool {
	findings := make(map[finding]bool)
	for _, line := range strings.Split(output, "\n") {
		match := gdlintLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if match == nil {
			continue
		}
		var number int
		fmt.Sscanf(match[2], "%d", &number)
		findings[finding{strings.TrimPrefix(filepath.ToSlash(match[1]), "./"), number, match[3]}] = true
	}
	return findings
}

// assertSameFindings fails with one line per difference, grouped by rule and
// naming the side that reported it.
func assertSameFindings(t *testing.T, ours, theirs map[finding]bool) {
	t.Helper()
	var differences []string
	for key := range ours {
		if !theirs[key] {
			differences = append(differences, fmt.Sprintf("%s: only gdkit reported %s", key.rule, key))
		}
	}
	for key := range theirs {
		if !ours[key] {
			differences = append(differences, fmt.Sprintf("%s: only gdlint reported %s", key.rule, key))
		}
	}
	if len(differences) == 0 {
		return
	}
	sort.Strings(differences)
	const shown = 100
	extra := 0
	if len(differences) > shown {
		extra = len(differences) - shown
		differences = differences[:shown]
	}
	message := fmt.Sprintf("gdkit and gdlint disagree on %d findings (gdkit %d, gdlint %d):\n  %s",
		len(differences)+extra, len(ours), len(theirs), strings.Join(differences, "\n  "))
	if extra > 0 {
		message += fmt.Sprintf("\n  ... and %d more", extra)
	}
	t.Error(message)
}
