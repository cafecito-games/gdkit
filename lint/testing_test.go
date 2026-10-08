package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/project"
)

// lintProject writes files into a temp project and lints it with config.
func lintProject(t *testing.T, config Config, files map[string]string) Report {
	t.Helper()
	root := writeTree(t, files)
	linter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := project.Load(lintLoadConfig(root, config, linter))
	if err != nil {
		t.Fatal(err)
	}
	return linter.Lint(snapshot)
}

// lintSource lints one file named a.gd with the default configuration and
// returns only the diagnostics for the named rule. It fails the test when the
// fixture does not parse, unless the caller asked for source-parse itself, so
// an unparseable fixture cannot pass as a rule correctly staying silent.
func lintSource(t *testing.T, rule, source string) []Diagnostic {
	t.Helper()
	return lintSourceWithConfig(t, DefaultConfig(), rule, source)
}

func lintSourceWithConfig(t *testing.T, config Config, rule, source string) []Diagnostic {
	t.Helper()
	report := lintProject(t, config, map[string]string{"a.gd": source})
	var out []Diagnostic
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == "source-parse" && rule != "source-parse" {
			t.Fatalf("fixture does not parse: %s", diagnostic.Message)
		}
		if diagnostic.Rule == rule {
			out = append(out, diagnostic)
		}
	}
	return out
}

// assertRule fails unless the named rule fired exactly on the given lines,
// under the default configuration.
func assertRule(t *testing.T, rule, source string, lines ...int) {
	t.Helper()
	assertRuleWithConfig(t, DefaultConfig(), rule, source, lines...)
}

// assertRuleWithConfig is assertRule with a caller-supplied configuration.
func assertRuleWithConfig(t *testing.T, config Config, rule, source string, lines ...int) {
	t.Helper()
	found := lintSourceWithConfig(t, config, rule, source)
	got := make([]int, len(found))
	for index, diagnostic := range found {
		got[index] = diagnostic.Line
	}
	if len(got) == len(lines) {
		same := true
		for index := range got {
			if got[index] != lines[index] {
				same = false
			}
		}
		if same {
			return
		}
	}
	var detail strings.Builder
	for _, diagnostic := range found {
		fmt.Fprintf(&detail, "\n  %d:%d %s", diagnostic.Line, diagnostic.Column, diagnostic.Message)
	}
	t.Fatalf("%s fired on lines %v, want %v%s", rule, got, lines, detail.String())
}

// assertNoRule fails if the named rule fires at all under the default
// configuration.
func assertNoRule(t *testing.T, rule, source string) {
	t.Helper()
	assertRuleWithConfig(t, DefaultConfig(), rule, source)
}

// writeTree writes files into a fresh temp project root and returns the root.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, contents := range files {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// lintLoadConfig mirrors cmd/gdkit's lint load decision. A run whose resolved
// rules need semantic analysis walks the whole project and moves the lint
// filters into Selection; a run that does not keeps them on discovery, so an
// excluded file is never read. Tests that stand in for the CLI must choose the
// same way, or they would prove nothing about what the CLI does.
func lintLoadConfig(root string, config Config, linter *Linter) project.Config {
	if linter.NeedsSemanticAnalysis() {
		return project.Config{Root: root, Selection: &project.Selection{
			SourceRoots:     config.SourceRoots,
			Exclude:         config.Exclude,
			HonorIgnoreFile: true,
		}}
	}
	return project.Config{
		Root:            root,
		SourceRoots:     config.SourceRoots,
		Exclude:         config.Exclude,
		HonorIgnoreFile: true,
	}
}

// lintTree lints a temp project the way the CLI does: the linter is built
// first, and its resolved semantic capability decides the load shape.
func lintTree(t *testing.T, config Config, rules []Rule, files map[string]string) (*project.Snapshot, Report) {
	t.Helper()
	root := writeTree(t, files)
	linter, err := newLinterForProject(root, config, rules)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := project.Load(lintLoadConfig(root, config, linter))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, linter.Lint(snapshot)
}

// collectionConfig enables the one semantic rule, with generated/** excluded
// from lint actions the way a project excludes vendored or generated code.
func collectionConfig() Config {
	config := DefaultConfig()
	config.Enable = []string{ruleRequireTypedCollection}
	config.Exclude = append(config.Exclude, "generated/**")
	return config
}
