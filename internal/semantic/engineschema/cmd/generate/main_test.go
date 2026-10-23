package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/internal/semantic/engineschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtifactSummaryReportsEveryPinnedIdentity(t *testing.T) {
	raw, err := readRawInput("../../testdata/extension_api_4_7_2_sample.json")
	require.NoError(t, err)
	artifact, err := engineschema.GenerateArtifact(raw, "ed1daf0bf")
	require.NoError(t, err)
	summary, err := summarizeArtifact(strings.Repeat("0", 64), artifact)
	require.NoError(t, err)
	for _, field := range []string{"raw_sha256=", "schema_sha256=", "artifact_sha256=", "artifact_bytes="} {
		assert.Contains(t, summary, field)
	}
}

func TestReadRawInputRejectsOversizedDump(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extension_api.json")
	file, err := os.Create(path)
	require.NoError(t, err)
	err = file.Truncate(engineschema.MaxRawJSONBytes + 1)
	require.NoError(t, err)
	err = file.Close()
	require.NoError(t, err)
	raw, err := readRawInput(path)
	require.Error(t, err)
	require.Nil(t, raw)
	require.ErrorIs(t, err, engineschema.ErrRawInvalid)
}
