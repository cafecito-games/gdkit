// Package engineschema distils Godot's extension_api.json into the immutable
// semantic.Engine index used by semantic-aware lint rules.
package engineschema

import "github.com/cafecito-games/gdkit/internal/semantic"

const schemaVersion = 1

// SourceKind identifies who controls the selected engine facts.
type SourceKind string

const (
	// SourceEmbedded is an official schema bundled with this gdkit release.
	SourceEmbedded SourceKind = "embedded"
	// SourceOverride is a project-supplied, wholesale extension_api override.
	SourceOverride SourceKind = "override"
)

func (s SourceKind) valid() bool {
	return s == SourceEmbedded || s == SourceOverride
}

// Version is the numeric engine version reported by the raw dump header.
type Version struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
	Patch int `json:"patch"`
}

// MinorVersion is an exact embedded-registry key. Selection never compares or
// orders keys to find a nearest or newest schema.
type MinorVersion struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

// Provenance identifies both the raw producer input and the semantic artifact
// compiled from it. SchemaSHA256 is calculated over the canonical schema that
// contains every other field here plus all retained records; paths and
// timestamps are deliberately absent.
type Provenance struct {
	Source       SourceKind `json:"source"`
	Version      Version    `json:"version"`
	Status       string     `json:"status"`
	Build        string     `json:"build"`
	FullName     string     `json:"full_name"`
	SourceCommit string     `json:"source_commit,omitempty"`
	RawSHA256    string     `json:"raw_sha256"`
	SchemaSHA256 string     `json:"schema_sha256,omitempty"`
}

// Loaded is one complete schema publication. Engine is immutable and
// Provenance is a value copy, so callers cannot contaminate a registry entry.
type Loaded struct {
	Engine     *semantic.Engine
	Provenance Provenance
}

// document is the single serialized schema definition shared by the
// generator and runtime artifact loader.
type document struct {
	Version    int               `json:"schema_version"`
	Provenance Provenance        `json:"provenance"`
	Builtins   []builtinRecord   `json:"builtins"`
	Classes    []classRecord     `json:"classes"`
	Methods    []methodRecord    `json:"methods"`
	Properties []propertyRecord  `json:"properties"`
	Operators  []operatorRecord  `json:"operators"`
	Singletons []singletonRecord `json:"singletons"`
	Utilities  []utilityRecord   `json:"utilities"`
}

type builtinRecord struct {
	Name string `json:"name"`
}

type classRecord struct {
	Name     string `json:"name"`
	Inherits string `json:"inherits,omitempty"`
}

type argumentRecord struct {
	Type       string `json:"type"`
	HasDefault bool   `json:"has_default,omitempty"`
}

type methodRecord struct {
	Owner      string           `json:"owner"`
	Name       string           `json:"name"`
	ReturnType string           `json:"return_type"`
	Arguments  []argumentRecord `json:"arguments"`
	Static     bool             `json:"static,omitempty"`
	Vararg     bool             `json:"vararg,omitempty"`
}

type propertyRecord struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
	Type  string `json:"type"`
}

type operatorRecord struct {
	Left       string `json:"left"`
	Operator   string `json:"operator"`
	Right      string `json:"right,omitempty"`
	ReturnType string `json:"return_type"`
}

type singletonRecord struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type utilityRecord struct {
	Name       string           `json:"name"`
	ReturnType string           `json:"return_type"`
	Arguments  []argumentRecord `json:"arguments"`
	Vararg     bool             `json:"vararg,omitempty"`
}

type artifact struct {
	Schema document `json:"schema"`
	Digest string   `json:"digest"`
}
