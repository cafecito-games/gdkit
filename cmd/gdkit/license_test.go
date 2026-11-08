package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/cafecito-games/gdkit/licenseheader"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunLicenseIsInertUntilConfigured(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"license", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Equal(t, "license check passed (1 files)\n", stdout.String())
	require.Empty(t, stderr.String())
	require.Equal(t, "extends Node\n", readCLIFile(t, root, "player.gd"))
}

func TestRunLicenseCheckWriteAndUpdate(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	writeLicenseConfig(t, root, []string{"Copyright (c) 2026 Cafecito Games", "", "SPDX-License-Identifier: MIT"})

	var stdout, stderr bytes.Buffer
	code := run([]string{"license", "check", "--diff", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	for _, want := range []string{
		"would update license header player.gd",
		"--- a/player.gd",
		"+++ b/player.gd",
		"+# SPDX-License-Identifier: MIT",
		"license check failed (1 to update, 0 diagnostics)",
	} {
		assert.Contains(t, stdout.String(), want)
	}
	require.Equal(t, "extends Node\n", readCLIFile(t, root, "player.gd"))

	stdout.Reset()
	code = run([]string{"license", "write", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Equal(t, "updated license header player.gd\nlicense write: 1 updated, 0 unchanged\n", stdout.String())
	require.Contains(t, readCLIFile(t, root, "player.gd"), "# Copyright (c) 2026 Cafecito Games\n")

	stdout.Reset()
	code = run([]string{"license", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Equal(t, "license check passed (1 files)\n", stdout.String())

	writeLicenseConfig(t, root, []string{"Copyright (c) 2027 Cafecito Games", "SPDX-License-Identifier: Apache-2.0"})
	stdout.Reset()
	code = run([]string{"license", "write", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	contents := readCLIFile(t, root, "player.gd")
	require.Contains(t, contents, "# Copyright (c) 2027 Cafecito Games\n")
	require.Contains(t, contents, "# SPDX-License-Identifier: Apache-2.0\n")
	require.NotContains(t, contents, "2026")
}

func TestRunBareLicenseRunsCheckAndHonoursIgnore(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "included.gd", "extends Node\n")
	writeCLIFile(t, root, "ignored.gd", "extends Node\n")
	writeCLIFile(t, root, ".gdkitignore", "ignored.gd\n")
	writeLicenseConfig(t, root, []string{"SPDX-License-Identifier: MIT"})

	previousRoot, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(previousRoot) })
	var stdout, stderr bytes.Buffer
	code := run([]string{"license"}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stdout.String(), "would update license header included.gd")
	require.NotContains(t, stdout.String(), "ignored.gd")
}

func TestRunLicenseReportsMalformedMarkerWithoutWritingIt(t *testing.T) {
	root := t.TempDir()
	broken := "# gdkit:license:begin\nextends Node\n"
	writeCLIFile(t, root, "broken.gd", broken)
	writeCLIFile(t, root, "clean.gd", "extends Node\n")
	writeLicenseConfig(t, root, []string{"SPDX-License-Identifier: MIT"})

	var stdout, stderr bytes.Buffer
	code := run([]string{"license", "write", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stdout.String(), "broken.gd:1: Error:")
	require.Contains(t, stdout.String(), "(license.marker)")
	require.Contains(t, stdout.String(), "updated license header clean.gd")
	require.Equal(t, broken, readCLIFile(t, root, "broken.gd"))
}

func TestRunLicenseJSON(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "player.gd", "extends Node\n")
	writeLicenseConfig(t, root, []string{"SPDX-License-Identifier: MIT"})
	var stdout, stderr bytes.Buffer
	code := run([]string{"license", "write", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	var report struct {
		Results []struct {
			Path    string `json:"path"`
			Changed bool   `json:"changed"`
		} `json:"results"`
		Diagnostics []licenseheader.Diagnostic `json:"diagnostics"`
		Written     []string                   `json:"written"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	require.Equal(t, []string{"player.gd"}, report.Written)
	require.True(t, report.Results[0].Changed)
	require.Empty(t, report.Diagnostics)
}

func TestRunLicenseInitAndUsage(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"license", "init", root}, &stdout, &stderr)
	require.Equal(t, 0, code)
	require.Equal(t, "wrote .gdkit/license.json\n", stdout.String())
	config, err := licenseheader.LoadConfig(root, "")
	require.NoError(t, err)
	require.Empty(t, config.Header)

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"license", "check", "--diff", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 2, code)
	require.Contains(t, stderr.String(), "--diff cannot be combined")

	for _, args := range [][]string{
		{"license", "write", "--diff", root},
		{"license", "check", root, root},
		{"license", "write", root, root},
		{"license", "init", root, root},
		{"license", "bogus"},
	} {
		stdout.Reset()
		stderr.Reset()
		require.Equal(t, 2, run(args, &stdout, &stderr), args)
		require.NotEmpty(t, stderr.String(), args)
	}
}

func TestRunHelpMentionsLicenseCommands(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"license", "help"}, {"license", "--help"}, {"license", "-h"}} {
		var stdout, stderr bytes.Buffer
		require.Equal(t, 0, run(args, &stdout, &stderr))
		for _, command := range []string{"license check", "license write", "license init"} {
			assert.Contains(t, stdout.String(), command)
		}
	}
}

func writeLicenseConfig(t *testing.T, root string, header []string) {
	t.Helper()
	data, err := json.Marshal(struct {
		Header []string `json:"header"`
	}{Header: header})
	require.NoError(t, err)
	writeCLIFile(t, root, ".gdkit/license.json", string(data))
}
