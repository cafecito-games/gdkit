// Command generate creates the deterministic embedded engine-schema artifact.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cafecito-games/gdkit/internal/semantic/engineschema"
)

func main() {
	input := flag.String("input", "", "path to an official extension_api.json")
	output := flag.String("output", "", "path to the generated .json.gz artifact")
	sourceCommit := flag.String("source-commit", "", "official Godot source commit")
	rawSHA256 := flag.String("raw-sha256", "", "expected SHA-256 of the raw dump")
	flag.Parse()
	if *input == "" || *output == "" || *sourceCommit == "" || *rawSHA256 == "" {
		fatal("-input, -output, -source-commit, and -raw-sha256 are required")
	}

	raw, err := os.ReadFile(*input)
	if err != nil {
		fatal("read input: %v", err)
	}
	digest := sha256.Sum256(raw)
	gotSHA256 := hex.EncodeToString(digest[:])
	if gotSHA256 != *rawSHA256 {
		fatal("raw SHA-256 is %s, want %s", gotSHA256, *rawSHA256)
	}
	artifact, err := engineschema.GenerateArtifact(raw, *sourceCommit)
	if err != nil {
		fatal("generate artifact: %v", err)
	}
	summary, err := summarizeArtifact(gotSHA256, artifact)
	if err != nil {
		fatal("verify generated artifact: %v", err)
	}
	if err := writeAtomically(*output, artifact); err != nil {
		fatal("write artifact: %v", err)
	}
	fmt.Println(summary)
}

func summarizeArtifact(rawSHA256 string, artifact []byte) (string, error) {
	loaded, err := engineschema.LoadArtifact(artifact)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(artifact)
	return fmt.Sprintf("raw_sha256=%s schema_sha256=%s artifact_sha256=%s artifact_bytes=%d",
		rawSHA256, loaded.Provenance.SchemaSHA256, hex.EncodeToString(digest[:]), len(artifact)), nil
}

func writeAtomically(path string, data []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".engine-schema-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(temporaryName)
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return err
	}
	remove = false
	return nil
}

func fatal(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
