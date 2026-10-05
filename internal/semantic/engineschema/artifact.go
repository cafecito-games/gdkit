package engineschema

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const maxArtifactJSONBytes = 64 << 20

// GenerateArtifact distils an official raw extension API dump into the
// deterministic compressed format embedded by the registry.
func GenerateArtifact(raw []byte, sourceCommit string) ([]byte, error) {
	document, err := distillRaw(raw, SourceEmbedded, sourceCommit)
	if err != nil {
		return nil, err
	}
	if _, err := compileDocument(document); err != nil {
		return nil, fmt.Errorf("%w: validate engine schema before generation: %v", ErrRawInvalid, err)
	}
	digest, err := schemaDigest(document)
	if err != nil {
		return nil, fmt.Errorf("calculate engine schema digest: %w", err)
	}
	payload, err := json.Marshal(artifact{Schema: document, Digest: digest})
	if err != nil {
		return nil, fmt.Errorf("encode engine schema artifact: %w", err)
	}

	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("create engine schema compressor: %w", err)
	}
	writer.Header.ModTime = time.Time{}
	writer.Header.OS = 255
	writer.Header.Name = ""
	writer.Header.Comment = ""
	writer.Header.Extra = nil
	if _, err := writer.Write(payload); err != nil {
		return nil, fmt.Errorf("compress engine schema artifact: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finish engine schema artifact: %w", err)
	}
	return compressed.Bytes(), nil
}

// LoadArtifact validates and compiles one deterministic embedded artifact. It
// does not publish an Engine unless decompression, framing, schema validation,
// digest verification, and type compilation all succeed.
func LoadArtifact(compressed []byte) (*Loaded, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("open engine schema artifact: %w", err)
	}
	payload, readErr := io.ReadAll(io.LimitReader(reader, maxArtifactJSONBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("decompress engine schema artifact: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close engine schema artifact: %w", closeErr)
	}
	if len(payload) > maxArtifactJSONBytes {
		return nil, fmt.Errorf("engine schema artifact exceeds %d bytes", maxArtifactJSONBytes)
	}

	document, digest, err := decodeArtifactJSON(payload)
	if err != nil {
		return nil, err
	}
	engine, err := compileDocument(document)
	if err != nil {
		return nil, fmt.Errorf("compile engine schema artifact: %w", err)
	}
	provenance := document.Provenance
	provenance.SchemaSHA256 = digest
	return &Loaded{Engine: engine, Provenance: provenance}, nil
}

func decodeArtifactJSON(payload []byte) (document, string, error) {
	if !json.Valid(payload) {
		return document{}, "", errors.New("engine schema artifact contains malformed JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var envelope artifact
	if err := decoder.Decode(&envelope); err != nil {
		return document{}, "", fmt.Errorf("decode engine schema artifact: %w", err)
	}
	if err := validateDocument(envelope.Schema, true); err != nil {
		return document{}, "", fmt.Errorf("validate engine schema artifact: %w", err)
	}
	if !validDigest(envelope.Digest) {
		return document{}, "", errors.New("engine schema artifact has an invalid digest")
	}
	want, err := schemaDigest(envelope.Schema)
	if err != nil {
		return document{}, "", fmt.Errorf("calculate engine schema artifact digest: %w", err)
	}
	if envelope.Digest != want {
		return document{}, "", errors.New("engine schema artifact digest does not match its contents")
	}
	return envelope.Schema, envelope.Digest, nil
}
