package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/internal/semantic/engineschema"
)

func TestArtifactSummaryReportsEveryPinnedIdentity(t *testing.T) {
	raw, err := readRawInput("../../testdata/extension_api_4_7_2_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := engineschema.GenerateArtifact(raw, "ed1daf0bf")
	if err != nil {
		t.Fatal(err)
	}
	summary, err := summarizeArtifact(strings.Repeat("0", 64), artifact)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"raw_sha256=", "schema_sha256=", "artifact_sha256=", "artifact_bytes="} {
		if !strings.Contains(summary, field) {
			t.Errorf("summary %q is missing %q", summary, field)
		}
	}
}

func TestReadRawInputRejectsOversizedDump(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extension_api.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(engineschema.MaxRawJSONBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if raw, err := readRawInput(path); err == nil || raw != nil || !errors.Is(err, engineschema.ErrRawInvalid) {
		t.Fatalf("readRawInput() = %d bytes, %v", len(raw), err)
	}
}
