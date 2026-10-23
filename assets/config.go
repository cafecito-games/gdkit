package assets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cafecito-games/gdkit/internal/buildinfo"
	"github.com/cafecito-games/gdkit/internal/failure"
	"github.com/cafecito-games/gdkit/internal/glob"
	"github.com/cafecito-games/gdkit/internal/versiongate"
)

// DefaultConfigPath is where assets init writes and assets check reads.
const DefaultConfigPath = ".gdkit/assets.json"

// Keep names files the analysis cannot prove are in use but that are. A kept
// file is an additional root, so what it depends on is reachable too: pinning
// a scene only to be told its sprites are dead would make the key useless.
type Keep struct {
	Pattern string `json:"pattern"`
	// Reason is required. Unlike an architecture allowlist exception this
	// needs no ADR — "a C# caller loads this" is too small a fact for a
	// document — but a bare glob with no justification is how a keep list
	// rots into a junk drawer nobody dares shorten.
	Reason string `json:"reason"`
}

// Config is the unused-asset policy.
type Config struct {
	Version int `json:"version"`
	// SourceRoots and Exclude narrow what may be reported, never what is
	// read. Hiding a directory from the walk would delete its outgoing
	// references, and every asset it was the only user of would immediately
	// report as unused. The whole project is always the universe.
	SourceRoots []string `json:"source_roots"`
	Exclude     []string `json:"exclude"`
	// AssetExtensions decides which files may appear in a finding. A file
	// outside the list is still a node and still carries its references — a
	// JSON data table naming a scene keeps that scene alive — it simply is
	// never reported, because a repository holds a README and a CI workflow
	// that no Godot project references and no one wants listed.
	AssetExtensions []string `json:"asset_extensions"`
	// Keep pins additional roots.
	Keep []Keep `json:"keep,omitempty"`
	// Severity overrides a rule's severity.
	Severity map[string]Severity `json:"severity,omitempty"`
	// MinimumGdkitVersion is the floor this config needs.
	MinimumGdkitVersion string `json:"minimum_gdkit_version,omitempty"`
}

// defaultSeverity is each rule's severity when the config overrides none.
//
// Only asset.unreferenced fails a run. The other three are warnings, each for
// its own reason.
//
// asset.unreachable is the second tier by construction, so gating on it means
// gating on the findings a project has not reviewed yet. asset.dynamic is a
// disclosure and not a defect: a single data-driven load would otherwise fail
// a project's CI permanently and get the tool switched off in a week.
//
// asset.missing is a warning because it is a side observation rather than
// this tool's job, and because the references that dangle most often are the
// deliberate ones — a test asserting that loading res://does/not/exist.tres
// fails names a path that really is absent, and it is right to. A project
// whose production code should have none promotes it.
var defaultSeverity = map[string]Severity{
	RuleUnreferenced: SeverityError,
	RuleMissing:      SeverityWarning,
	RuleUnreachable:  SeverityWarning,
	RuleDynamic:      SeverityWarning,
}

// defaultAssetExtensions is every extension a Godot 4 project ships that
// someone would want told about. It is deliberately a list of what to report
// rather than of what to ignore: an extension nobody thought of goes
// unreported, which is the safe direction for a tool whose output people act
// on by deleting files.
//
// .json is absent although Godot reads JSON data tables, because a
// repository holds far more tooling JSON than game data — a package-lock,
// a tsconfig, a CI matrix — and reporting one of those is how a reader
// decides the tool does not understand their project. A JSON file is still
// read for the references inside it, so a data table keeps what it names
// alive; a project whose data really is JSON adds the extension back.
var defaultAssetExtensions = []string{
	".escn", ".gd", ".gdshader", ".gdshaderinc", ".res", ".scn", ".tres", ".tscn",
	".bmp", ".dds", ".exr", ".hdr", ".jpeg", ".jpg", ".ktx", ".png", ".svg", ".tga", ".webp",
	".aac", ".flac", ".mp3", ".ogg", ".wav",
	".fnt", ".otf", ".ttf", ".woff", ".woff2",
	".dae", ".fbx", ".glb", ".gltf", ".obj",
	".csv", ".po", ".translation",
	".ogv", ".theme",
}

// DefaultConfig is both the zero-config policy and what assets init writes.
//
// Exclude starts wider than the other tools'. They walk only .gd files, so a
// repository that also holds a website or a tool chain is invisible to them;
// this one inventories every file there is, and would otherwise report a
// vendored JavaScript dependency's icons as unused Godot assets. addons/** is
// excluded for the same reason it is in format and generate: a third-party
// addon is not code the project should be told to delete.
func DefaultConfig() Config {
	extensions := make([]string, len(defaultAssetExtensions))
	copy(extensions, defaultAssetExtensions)
	return Config{
		Version:         1,
		SourceRoots:     []string{"."},
		Exclude:         []string{".git/**", ".godot/**", ".gdkit/**", "addons/**", "node_modules/**", "**/node_modules/**"},
		AssetExtensions: extensions,
		Keep: []Keep{{
			Pattern: "script_templates/**",
			Reason:  "Godot reads the editor's script templates by scanning this directory",
		}},
	}
}

// LoadConfig loads an assets config. When name is empty it uses the default
// path and returns defaults if that file does not exist; an explicitly named
// file that is missing is an error.
func LoadConfig(root, name string) (Config, error) {
	implicit := name == ""
	if implicit {
		name = DefaultConfigPath
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(root, filepath.FromSlash(name))
	}
	data, err := os.ReadFile(name)
	if implicit && errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, failure.WrapPath(failure.ConfigRead, name, fmt.Errorf("read assets config: %w", err))
	}
	// Both whole-document checks below unmarshal the file, which requires it
	// to be exactly one JSON value. A decoder reads only the first value and
	// never looks at what follows, so without this a config trailed by
	// anything else would load with its version floor silently unapplied.
	if !json.Valid(data) {
		return Config{}, failure.WrapPath(failure.ConfigParse, name,
			errors.New("parse assets config: file must contain exactly one JSON value"))
	}
	// The floor is checked before unknown keys are rejected, and the ordering
	// is load-bearing: a config written for a newer gdkit carries both the
	// floor and the syntax that needed it, so checking later would report an
	// unknown key and point the reader at a typo instead of at a binary that
	// is too old.
	if err := checkMinimumVersion(data, name); err != nil {
		return Config{}, failure.WrapKey(failure.ConfigVersionFloor, name, "minimum_gdkit_version", err)
	}
	config := DefaultConfig()
	// Every list field is cleared before decoding and restored afterwards
	// only when the file omits its key: encoding/json decodes an array
	// element onto whatever the slice already holds at that index, so a
	// declared keep entry would otherwise inherit a default entry's fields.
	defaults := config
	config.SourceRoots, config.Exclude, config.AssetExtensions, config.Keep = nil, nil, nil, nil
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, failure.WrapPath(failure.ConfigParse, name, fmt.Errorf("parse assets config: %w", err))
	}
	if config.SourceRoots == nil {
		config.SourceRoots = defaults.SourceRoots
	}
	if config.Exclude == nil {
		config.Exclude = defaults.Exclude
	}
	if config.AssetExtensions == nil {
		config.AssetExtensions = defaults.AssetExtensions
	}
	if config.Keep == nil {
		config.Keep = defaults.Keep
	}
	if err := config.Validate(); err != nil {
		return Config{}, failure.WrapPath(failure.ConfigInvalid, name, err)
	}
	return config, nil
}

// detectedVersion is the running binary's version, as a package variable so a
// test can load a config as an arbitrary release would.
var detectedVersion = func() string { return buildinfo.Current().Version }

// checkMinimumVersion enforces the config's own floor against this binary. It
// decodes the one key with a non-strict unmarshal of its own so it can run
// before the strict decode rejects a key written for a newer release.
func checkMinimumVersion(data []byte, configPath string) error {
	var document struct {
		MinimumGdkitVersion string `json:"minimum_gdkit_version"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil
	}
	return versiongate.CheckFloor(configPath, document.MinimumGdkitVersion, detectedVersion())
}

// compiledConfig holds everything Validate compiles once, so the check
// compiles no glob per file.
type compiledConfig struct {
	keep       []glob.Pattern
	extensions map[string]bool
	severity   map[string]Severity
}

// Validate compiles every glob and checks every rule name, so a typo fails as
// a configuration error rather than silently reporting nothing.
func (c Config) Validate() error {
	_, err := c.validate()
	return err
}

func (c Config) validate() (*compiledConfig, error) {
	if c.Version != 1 {
		return nil, fmt.Errorf("unsupported assets config version %d", c.Version)
	}
	if len(c.SourceRoots) == 0 {
		return nil, errors.New("assets config needs at least one source_root")
	}
	for _, root := range c.SourceRoots {
		clean := filepath.Clean(filepath.FromSlash(root))
		if root == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(filepath.ToSlash(clean), "../") {
			return nil, fmt.Errorf("source_root %q must be a project-relative path", root)
		}
	}
	for _, pattern := range c.Exclude {
		if _, err := glob.Compile(pattern); err != nil {
			return nil, fmt.Errorf("exclude pattern %q: %w", pattern, err)
		}
	}
	compiled := &compiledConfig{
		extensions: make(map[string]bool, len(c.AssetExtensions)),
		severity:   make(map[string]Severity, len(Rules)),
	}
	for _, entry := range c.Keep {
		if entry.Pattern == "" {
			return nil, errors.New("keep entry needs a pattern")
		}
		if strings.TrimSpace(entry.Reason) == "" {
			return nil, fmt.Errorf("keep pattern %q needs a reason", entry.Pattern)
		}
		pattern, err := glob.Compile(entry.Pattern)
		if err != nil {
			return nil, fmt.Errorf("keep pattern %q: %w", entry.Pattern, err)
		}
		compiled.keep = append(compiled.keep, pattern)
	}
	for _, extension := range c.AssetExtensions {
		if !strings.HasPrefix(extension, ".") || extension == "." {
			return nil, fmt.Errorf("asset_extensions entry %q must start with a dot", extension)
		}
		compiled.extensions[strings.ToLower(extension)] = true
	}
	for rule, severity := range defaultSeverity {
		compiled.severity[rule] = severity
	}
	for _, rule := range sortedKeys(c.Severity) {
		if !IsRule(rule) {
			return nil, fmt.Errorf("severity names unknown rule %q", rule)
		}
		switch c.Severity[rule] {
		case SeverityError, SeverityWarning:
			compiled.severity[rule] = c.Severity[rule]
		default:
			return nil, fmt.Errorf("rule %q has unknown severity %q", rule, c.Severity[rule])
		}
	}
	return compiled, nil
}

// sortedKeys returns a map's keys in order, so a config with two bad entries
// always fails on the same one.
func sortedKeys(values map[string]Severity) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
