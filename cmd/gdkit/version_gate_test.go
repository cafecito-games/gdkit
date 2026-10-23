package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/internal/versiongate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			require.Equal(t, want, got)
			require.Equal(t, plainOut.String(), stdout.String())
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
			require.Equal(t, 2, code)
			require.Empty(t, stdout.String())
			for _, want := range []string{"--minimum-version", "0.3.0", "0.2.0"} {
				assert.Contains(t, stderr.String(), want)
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
			require.Equal(t, 2, code)
			require.Contains(t, stderr.String(), "major.minor.patch")
		})
	}
}

// The gate runs before any config is resolved, so it holds in a project that
// has no configuration file at all.
func TestMinimumVersionGatesBeforeConfigResolution(t *testing.T) {
	pretendVersion(t, "0.2.0")
	root := gateProject(t)
	_, err := os.Stat(filepath.Join(root, ".gdkit"))
	require.ErrorIs(t, err, os.ErrNotExist)
	var stdout, stderr bytes.Buffer
	code := run([]string{"arch", "check", "--minimum-version", "0.3.0", root}, &stdout, &stderr)
	require.Equal(t, 2, code)
	require.Contains(t, stderr.String(), "0.3.0")
}

// A binary too old to understand the config is reported as too old, not as a
// binary that found a key it does not know.
func TestMinimumVersionGatesBeforeConfigErrors(t *testing.T) {
	pretendVersion(t, "0.2.0")
	root := gateProject(t)
	writeCLIFile(t, root, ".gdkit/architecture.json", `{"version": 1, "zones": ["anything"]}`)
	var stdout, stderr bytes.Buffer
	code := run([]string{"arch", "check", "--minimum-version", "0.3.0", root}, &stdout, &stderr)
	require.Equal(t, 2, code)
	require.NotContains(t, stderr.String(), "zones")
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
			code := run(arguments, &stdout, &stderr)
			require.Equal(t, 2, code)
			require.Empty(t, stdout.String())
			require.Contains(t, stderr.String(), "0.3.0")
			// A write command must not have touched the project either.
			after := projectFingerprint(t, root)
			require.Equal(t, before, after)

			// A satisfied floor leaves the command running as before.
			pretendVersion(t, "0.3.0")
			stdout.Reset()
			stderr.Reset()
			code = run(arguments, &stdout, &stderr)
			require.NotEqual(t, 2, code)
		})
	}
}

func TestMinimumVersionFailsClosedOnADevelopmentBuild(t *testing.T) {
	// The test binary reports a development build, so no seam is needed here.
	root := gateProject(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"arch", "check", "--minimum-version", "0.3.0", root}, &stdout, &stderr)
	require.Equal(t, 2, code)
	require.Contains(t, stderr.String(), versiongate.AllowDevelopmentEnvironmentVariable)

	t.Setenv(versiongate.AllowDevelopmentEnvironmentVariable, "1")
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"arch", "check", "--minimum-version", "0.3.0", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
}

// The config field is enforced through the CLI too, with the same bypass.
func TestConfigMinimumVersionIsEnforcedThroughTheCLI(t *testing.T) {
	root := gateProject(t)
	writeCLIFile(t, root, ".gdkit/architecture.json", `{"version": 1, "minimum_gdkit_version": "0.3.0"}`)
	var stdout, stderr bytes.Buffer
	code := run([]string{"arch", "check", root}, &stdout, &stderr)
	require.Equal(t, 2, code)
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), "0.3.0")

	t.Setenv(versiongate.AllowDevelopmentEnvironmentVariable, "1")
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"arch", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
}

// A generated config must not pin itself to whichever binary generated it.
func TestArchInitWritesNoMinimumVersion(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"arch", "init", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(".gdkit/architecture.json")))
	require.NoError(t, err)
	require.NotContains(t, string(contents), "minimum_gdkit_version")
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
	require.NoError(t, err)
	return builder.String()
}
