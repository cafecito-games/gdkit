package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/internal/versiongate"
)

// pretendVersion makes the --minimum-version gate see a fixed binary version
// for one test. The test binary itself always reports a development build.
func pretendVersion(t *testing.T, value string) {
	t.Helper()
	previous := detectedVersion
	t.Cleanup(func() { detectedVersion = previous })
	detectedVersion = func() string { return value }
}

// gateProject lays down a project every check command can run over cleanly.
func gateProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeCLIFile(t, root, "features/combat/domain/health.gd", "class_name Health\n")
	writeCLIFile(t, root, "features/combat/domain/health.gd.uid", "uid://b1gy6duxcey6j\n")
	return root
}

// A satisfied floor must not change the outcome of the run it guards.
func TestMinimumVersionSatisfiedLeavesTheExitCodeAlone(t *testing.T) {
	for _, binary := range []string{"0.3.0", "0.3.1", "1.0.0", "0.3.0-SNAPSHOT-9060007"} {
		t.Run(binary, func(t *testing.T) {
			pretendVersion(t, binary)
			root := gateProject(t)
			var plainOut, plainErr bytes.Buffer
			want := run([]string{"arch", "check", root}, &plainOut, &plainErr)

			var stdout, stderr bytes.Buffer
			got := run([]string{"arch", "check", "--minimum-version", "0.3.0", root}, &stdout, &stderr)
			if got != want {
				t.Fatalf("exit %d with the flag, %d without: %s", got, want, stderr.String())
			}
			if stdout.String() != plainOut.String() {
				t.Fatalf("the flag changed the report:\n%s\n%s", plainOut.String(), stdout.String())
			}
		})
	}
}

func TestMinimumVersionBelowFloorExitsTwoAndEmitsNoReport(t *testing.T) {
	for _, outputFormat := range []string{"text", "json"} {
		t.Run(outputFormat, func(t *testing.T) {
			pretendVersion(t, "0.2.0")
			root := gateProject(t)
			var stdout, stderr bytes.Buffer
			code := run([]string{"arch", "check", "--minimum-version", "0.3.0", "--format", outputFormat, root}, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("exit %d, want 2: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if stdout.String() != "" {
				t.Fatalf("a report was emitted after a failed gate: %s", stdout.String())
			}
			for _, want := range []string{"--minimum-version", "0.3.0", "0.2.0"} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr %q does not contain %q", stderr.String(), want)
				}
			}
		})
	}
}

func TestMinimumVersionMalformedExitsTwo(t *testing.T) {
	for _, value := range []string{"0.3", "v0.3.0", "latest", "dev", "0.3.0-rc.1"} {
		t.Run(value, func(t *testing.T) {
			pretendVersion(t, "0.3.0")
			root := gateProject(t)
			var stdout, stderr bytes.Buffer
			code := run([]string{"arch", "check", "--minimum-version", value, root}, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("exit %d, want 2: stderr=%s", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "major.minor.patch") {
				t.Fatalf("stderr %q does not say what a valid value looks like", stderr.String())
			}
		})
	}
}

// The gate runs before any config is resolved, so it holds in a project that
// has no configuration file at all.
func TestMinimumVersionGatesBeforeConfigResolution(t *testing.T) {
	pretendVersion(t, "0.2.0")
	root := gateProject(t)
	if _, err := os.Stat(filepath.Join(root, ".gdkit")); !os.IsNotExist(err) {
		t.Fatalf("test project unexpectedly has a .gdkit directory: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "check", "--minimum-version", "0.3.0", root}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2: stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "0.3.0") {
		t.Fatalf("stderr %q does not report the required version", stderr.String())
	}
}

// A binary too old to understand the config is reported as too old, not as a
// binary that found a key it does not know.
func TestMinimumVersionGatesBeforeConfigErrors(t *testing.T) {
	pretendVersion(t, "0.2.0")
	root := gateProject(t)
	writeCLIFile(t, root, ".gdkit/architecture.json", `{"version": 1, "zones": ["anything"]}`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "check", "--minimum-version", "0.3.0", root}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2: stderr=%s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "zones") {
		t.Fatalf("stderr %q names the unknown key instead of the version floor", stderr.String())
	}
}

func TestMinimumVersionIsAcceptedByEveryCheckCommand(t *testing.T) {
	commands := map[string][]string{
		"arch check":   {"arch", "check"},
		"lint check":   {"lint", "check"},
		"format check": {"format", "check"},
		"format write": {"format", "write"},
		"uid check":    {"uid", "check"},
		"uid write":    {"uid", "write"},
	}
	for name, command := range commands {
		t.Run(name, func(t *testing.T) {
			pretendVersion(t, "0.2.0")
			root := gateProject(t)
			before := projectFingerprint(t, root)
			var stdout, stderr bytes.Buffer
			arguments := append(append([]string{}, command...), "--minimum-version", "0.3.0", root)
			if code := run(arguments, &stdout, &stderr); code != 2 {
				t.Fatalf("exit %d, want 2: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if stdout.String() != "" {
				t.Fatalf("a report was emitted after a failed gate: %s", stdout.String())
			}
			if !strings.Contains(stderr.String(), "0.3.0") {
				t.Fatalf("stderr %q does not report the required version", stderr.String())
			}
			// A write command must not have touched the project either.
			if after := projectFingerprint(t, root); after != before {
				t.Fatalf("a failed gate still changed the project:\n%s\n%s", before, after)
			}

			// A satisfied floor leaves the command running as before.
			pretendVersion(t, "0.3.0")
			stdout.Reset()
			stderr.Reset()
			if code := run(arguments, &stdout, &stderr); code == 2 {
				t.Fatalf("a satisfied floor failed the run: %s", stderr.String())
			}
		})
	}
}

func TestMinimumVersionFailsClosedOnADevelopmentBuild(t *testing.T) {
	// The test binary reports a development build, so no seam is needed here.
	root := gateProject(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "check", "--minimum-version", "0.3.0", root}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2: stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), versiongate.AllowDevelopmentEnvironmentVariable) {
		t.Fatalf("stderr %q does not name the bypass", stderr.String())
	}

	t.Setenv(versiongate.AllowDevelopmentEnvironmentVariable, "1")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"arch", "check", "--minimum-version", "0.3.0", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("the bypass did not let a development build through: exit %d: %s", code, stderr.String())
	}
}

// The config field is enforced through the CLI too, with the same bypass.
func TestConfigMinimumVersionIsEnforcedThroughTheCLI(t *testing.T) {
	root := gateProject(t)
	writeCLIFile(t, root, ".gdkit/architecture.json", `{"version": 1, "minimum_gdkit_version": "0.3.0"}`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "check", root}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("a report was emitted after a failed gate: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "minimum_gdkit_version") && !strings.Contains(stderr.String(), "0.3.0") {
		t.Fatalf("stderr %q does not explain the floor", stderr.String())
	}

	t.Setenv(versiongate.AllowDevelopmentEnvironmentVariable, "1")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"arch", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("the bypass did not let a development build through: exit %d: %s", code, stderr.String())
	}
}

// A generated config must not pin itself to whichever binary generated it.
func TestArchInitWritesNoMinimumVersion(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "init", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init exit %d: %s", code, stderr.String())
	}
	contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(".gdkit/architecture.json")))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "minimum_gdkit_version") {
		t.Fatalf("arch init pinned the generated config:\n%s", contents)
	}
}

// projectFingerprint lists every file under root with its contents, so a test
// can prove a write command left the project alone.
func projectFingerprint(t *testing.T, root string) string {
	t.Helper()
	var builder strings.Builder
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		contents, readError := os.ReadFile(path)
		if readError != nil {
			return readError
		}
		relative, relativeError := filepath.Rel(root, path)
		if relativeError != nil {
			return relativeError
		}
		fmt.Fprintf(&builder, "%s\x00%s\n", filepath.ToSlash(relative), contents)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return builder.String()
}
