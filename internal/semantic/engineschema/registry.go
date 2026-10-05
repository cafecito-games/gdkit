package engineschema

import (
	_ "embed"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrUnsupportedVersion reports an exact major/minor registry miss. The
// registry deliberately has no nearest-version or newest-version fallback.
var ErrUnsupportedVersion = errors.New("unsupported Godot engine schema version")

//go:embed data/godot_4_7.json.gz
var godot47Artifact []byte

type artifactLoader func([]byte) (*Loaded, error)

type lazyArtifact struct {
	once   sync.Once
	blob   []byte
	loader artifactLoader
	loaded *Loaded
	err    error
}

func newLazyArtifact(blob []byte, loader artifactLoader) *lazyArtifact {
	// Retain the read-only embedded view without copying it at package startup.
	// The first actual load takes a private copy before handing bytes to the
	// loader, so ordinary lint runs neither allocate for nor decompress it.
	return &lazyArtifact{blob: blob, loader: loader}
}

func (l *lazyArtifact) Load() (*Loaded, error) {
	l.once.Do(func() {
		if l.loader == nil {
			l.err = errors.New("engine schema artifact has no loader")
			return
		}
		blob := append([]byte(nil), l.blob...)
		l.blob = nil
		loaded, err := l.loader(blob)
		if err != nil {
			l.err = err
			return
		}
		if loaded == nil || loaded.Engine == nil {
			l.err = errors.New("engine schema loader returned an incomplete value")
			return
		}
		l.loaded = loaded
	})
	return l.loaded, l.err
}

type registryIdentity struct {
	version      Version
	rawSHA256    string
	sourceCommit string
	schemaSHA256 string
}

type registryRecord struct {
	artifact *lazyArtifact
}

var godot47Identity = registryIdentity{
	version:      Version{Major: 4, Minor: 7, Patch: 2},
	rawSHA256:    "d0e4c08c03b165156dabe6bfb6a906baf0069189f62035341230a246c86d6986",
	sourceCommit: "ed1daf0bf",
	schemaSHA256: "2b38249d74e48221e7fcc655592929e5c1bf4ab8d34c5c8b98ce8655a18c0a66",
}

var embeddedRegistry = map[MinorVersion]registryRecord{
	{Major: 4, Minor: 7}: {
		artifact: newLazyArtifact(godot47Artifact, func(data []byte) (*Loaded, error) {
			return loadRegistryArtifact(data, godot47Identity)
		}),
	},
}

// SupportedMinors returns a sorted copy of the exact embedded registry keys.
func SupportedMinors() []MinorVersion {
	versions := make([]MinorVersion, 0, len(embeddedRegistry))
	for version := range embeddedRegistry {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Major < versions[j].Major ||
			(versions[i].Major == versions[j].Major && versions[i].Minor < versions[j].Minor)
	})
	return versions
}

// LoadEmbedded loads the schema registered for exactly major.minor.
func LoadEmbedded(major, minor int) (*Loaded, error) {
	key := MinorVersion{Major: major, Minor: minor}
	record, ok := embeddedRegistry[key]
	if !ok {
		return nil, fmt.Errorf("%w: %d.%d", ErrUnsupportedVersion, major, minor)
	}
	loaded, err := record.artifact.Load()
	if err != nil {
		return nil, fmt.Errorf("load embedded Godot %d.%d schema: %w", major, minor, err)
	}
	copy := *loaded
	return &copy, nil
}

func loadRegistryArtifact(data []byte, identity registryIdentity) (*Loaded, error) {
	loaded, err := LoadArtifact(data)
	if err != nil {
		return nil, err
	}
	provenance := loaded.Provenance
	if provenance.Source != SourceEmbedded || provenance.Version != identity.version ||
		provenance.RawSHA256 != identity.rawSHA256 || provenance.SourceCommit != identity.sourceCommit ||
		provenance.SchemaSHA256 != identity.schemaSHA256 {
		return nil, errors.New("embedded engine schema provenance does not match its registry identity")
	}
	return loaded, nil
}
