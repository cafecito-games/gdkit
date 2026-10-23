package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/gdkit/assets"
)

// liveProject is a project where everything hangs off the manifest.
func liveProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeCLIFile(t, root, "project.godot", "[application]\nrun/main_scene=\"res://main.tscn\"\n")
	writeCLIFile(t, root, "main.tscn", "[gd_scene load_steps=2 format=3]\n\n"+
		"[ext_resource type=\"Script\" path=\"res://main.gd\" id=\"1\"]\n")
	writeCLIFile(t, root, "main.gd", "extends Node\n")
	return root
}

func TestRunAssetsCheckClean(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"assets", "check", liveProject(t)}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "assets check passed")
}

func TestRunAssetsCheckReportsAnUnreferencedFile(t *testing.T) {
	root := liveProject(t)
	writeCLIFile(t, root, "art/orphan.png", "\x89PNG")

	var stdout, stderr bytes.Buffer
	code := run([]string{"assets", "check", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	assert.Contains(t, stdout.String(), "art/orphan.png:1: Error: nothing in the project references this file (asset.unreferenced)")
}

// A warning prints without failing, which is what lets a project adopt the
// tool before it has reviewed the second tier.
func TestRunAssetsCheckDoesNotFailOnWarningsAlone(t *testing.T) {
	root := liveProject(t)
	writeCLIFile(t, root, "ui/hud.tscn", "[gd_scene load_steps=2 format=3]\n\n"+
		"[ext_resource type=\"Texture2D\" path=\"res://art/gone.png\" id=\"1\"]\n")
	writeCLIFile(t, root, ".gdkit/assets.json",
		`{"version": 1, "severity": {"asset.unreferenced": "warning"}}`)

	var stdout, stderr bytes.Buffer
	code := run([]string{"assets", "check", root}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "Warning: references res://art/gone.png, which does not exist (asset.missing)")
}

func TestRunAssetsCheckJSONIsSortedAndCountsTheInventory(t *testing.T) {
	root := liveProject(t)
	writeCLIFile(t, root, "art/b.png", "\x89PNG")
	writeCLIFile(t, root, "art/a.png", "\x89PNG")

	var stdout, stderr bytes.Buffer
	code := run([]string{"assets", "check", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 1, code)
	var report assets.Report
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	require.Len(t, report.Diagnostics, 2)
	assert.Equal(t, "art/a.png", report.Diagnostics[0].Path)
	assert.Equal(t, "art/b.png", report.Diagnostics[1].Path)
	assert.Equal(t, 5, report.Inventoried)
	// project.godot is inventoried but is not an asset extension.
	assert.Equal(t, 4, report.Reportable)
}

func TestRunAssetsCheckDisablesByRuleName(t *testing.T) {
	root := liveProject(t)
	writeCLIFile(t, root, "art/orphan.png", "\x89PNG")

	var stdout, stderr bytes.Buffer
	code := run([]string{"assets", "check", "--disable", "asset.unreferenced", root}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	assert.NotContains(t, stdout.String(), "asset.unreferenced")
}

func TestRunAssetsCheckRejectsAnUnknownRuleName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"assets", "check", "--disable", "asset.unrefernced", liveProject(t)}, &stdout, &stderr)
	require.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "unknown rule")
}

// Under --format json an exit-2 failure is an envelope on stderr and stdout
// stays empty, so a consumer can tell "no report" from "an empty report".
func TestRunAssetsCheckJSONFailureIsAnEnvelopeOnStderr(t *testing.T) {
	root := liveProject(t)
	writeCLIFile(t, root, ".gdkit/assets.json", `{"version": 2}`)

	var stdout, stderr bytes.Buffer
	code := run([]string{"assets", "check", "--format", "json", root}, &stdout, &stderr)
	require.Equal(t, 2, code)
	assert.Empty(t, stdout.String())
	var envelope struct {
		Error struct {
			Kind string `json:"kind"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(stderr.Bytes(), &envelope))
	assert.Equal(t, "config.invalid", envelope.Error.Kind)
}

func TestRunAssetsCheckRejectsAnUnknownFormatAsText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"assets", "check", "--format", "yaml", liveProject(t)}, &stdout, &stderr)
	require.Equal(t, 2, code)
	assert.NotContains(t, stderr.String(), "{")
}

func TestRunAssetsWithNoSubcommandChecks(t *testing.T) {
	root := liveProject(t)
	writeCLIFile(t, root, "art/orphan.png", "\x89PNG")

	var stdout, stderr bytes.Buffer
	require.Equal(t, 1, run([]string{"assets", root}, &stdout, &stderr))
	assert.Contains(t, stdout.String(), "asset.unreferenced")
}

func TestRunAssetsInitWritesTheDefaultConfig(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, run([]string{"assets", "init", root}, &stdout, &stderr))
	assert.Equal(t, "wrote "+assets.DefaultConfigPath+"\n", stdout.String())

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(assets.DefaultConfigPath)))
	require.NoError(t, err)
	var written assets.Config
	require.NoError(t, json.Unmarshal(data, &written))
	assert.Equal(t, assets.DefaultConfig(), written)
}

func TestRunAssetsInitRefusesToReplaceWithoutForce(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, run([]string{"assets", "init", root}, &stdout, &stderr))
	stdout.Reset()
	stderr.Reset()
	require.Equal(t, 2, run([]string{"assets", "init", root}, &stdout, &stderr))
	stdout.Reset()
	stderr.Reset()
	require.Equal(t, 0, run([]string{"assets", "init", "--force", root}, &stdout, &stderr))
}

func TestRunAssetsCheckRejectsTwoProjectRoots(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"assets", "check", ".", ".."}, &stdout, &stderr)
	require.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "at most one project root")
}

func TestRunAssetsCheckHonorsMinimumVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"assets", "check", "--minimum-version", "99.0.0", liveProject(t)}, &stdout, &stderr)
	require.Equal(t, 2, code)
	assert.True(t, strings.Contains(stderr.String(), "99.0.0"), stderr.String())
}
