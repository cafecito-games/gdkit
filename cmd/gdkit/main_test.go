package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInitAndCleanCheck(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "init", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("init exit %d: %s", code, stderr.String())
	}
	for _, name := range []string{".gdkit/architecture.json", ".gdkit/allowlist.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Errorf("starter %s: %v", name, err)
		}
	}
	writeCLIFile(t, root, "features/combat/domain/health.gd", "class_name Health\n")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"arch", "check", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("check exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "architecture check passed") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestRunCheckViolationExit(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "features/combat/domain/health.gd", "class_name Health extends Node\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "check", root}, &stdout, &stderr); code != 1 {
		t.Fatalf("check exit %d: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[engine.reference]") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version", "--format", "json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("version exit %d: %s", code, stderr.String())
	}
	for _, field := range []string{`"version"`, `"dirty"`, `"go_version"`} {
		if !strings.Contains(stdout.String(), field) {
			t.Errorf("version JSON does not contain %s: %s", field, stdout.String())
		}
	}

	stdout.Reset()
	if code := run([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("--version exit %d: %s", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "gdkit version ") {
		t.Fatalf("unexpected version output: %s", stdout.String())
	}
}

func writeCLIFile(t *testing.T, root, name, contents string) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
