package main

import (
	"os"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/internal/semantic/engineschema"
)

func TestArtifactSummaryReportsEveryPinnedIdentity(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/extension_api_4_7_2_sample.json")
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
