package architecture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/cafecito-games/gdkit/internal/buildinfo"
	"github.com/cafecito-games/gdkit/internal/versiongate"
)

const (
	DefaultConfigPath    = ".gdkit/architecture.json"
	DefaultAllowlistPath = ".gdkit/allowlist.json"
)

// detectedVersion reports the running binary's version. It is a variable so a
// test can load a config as an arbitrary release would.
var detectedVersion = func() string { return buildinfo.Current().Version }

// Config controls discovery, classification, and dependency policy.
type Config struct {
	Version             int                  `json:"version"`
	MinimumGDKitVersion string               `json:"minimum_gdkit_version,omitempty"`
	SourceRoots         []string             `json:"source_roots"`
	Exclude             []string             `json:"exclude"`
	Classifications     []ClassificationRule `json:"classifications"`
	Dependencies        []DependencyRule     `json:"dependencies"`
	RuntimeBoundaries   []string             `json:"runtime_boundaries"`
	TestPatterns        []string             `json:"test_patterns"`
	Allowlist           string               `json:"allowlist"`
	Unclassified        string               `json:"unclassified"`
}

// ClassificationRule assigns a layer and feature. {feature} captures one path segment.
type ClassificationRule struct {
	Pattern string `json:"pattern"`
	Layer   string `json:"layer"`
	Feature string `json:"feature"`
}

// DependencyRule allows matching source files to depend on matching targets.
// "same" in ToFeatures means the source feature; "*" means any feature.
// FromPaths and ToPaths narrow the rule to source and target files matching
// them; an omitted list means any file the layer and feature already allow.
type DependencyRule struct {
	FromLayers    []string `json:"from_layers"`
	FromFeatures  []string `json:"from_features,omitempty"`
	FromPaths     []string `json:"from_paths,omitempty"`
	ToLayers      []string `json:"to_layers"`
	ToFeatures    []string `json:"to_features,omitempty"`
	ToPaths       []string `json:"to_paths,omitempty"`
	ExceptToPaths []string `json:"except_to_paths,omitempty"`
}

// DefaultConfig returns a strict, convention-based layered architecture policy.
func DefaultConfig() Config {
	return Config{
		Version:     1,
		SourceRoots: []string{"."},
		Exclude: []string{
			".git/**", ".godot/**", ".gdkit/**", "addons/**",
		},
		Classifications: []ClassificationRule{
			{Pattern: "**/features/{feature}/domain/**", Layer: "domain", Feature: "{feature}"},
			{Pattern: "**/features/{feature}/application/**", Layer: "application", Feature: "{feature}"},
			{Pattern: "**/features/{feature}/presentation/**", Layer: "presentation", Feature: "{feature}"},
			{Pattern: "**/features/{feature}/infrastructure/**", Layer: "infrastructure", Feature: "{feature}"},
			{Pattern: "**/features/{feature}/bootstrap/**", Layer: "bootstrap", Feature: "{feature}"},
			{Pattern: "**/shared/domain/**", Layer: "domain", Feature: "shared"},
			{Pattern: "**/shared/application/**", Layer: "application", Feature: "shared"},
			{Pattern: "**/shared/presentation/**", Layer: "presentation", Feature: "shared"},
			{Pattern: "**/shared/infrastructure/**", Layer: "infrastructure", Feature: "shared"},
			{Pattern: "**/domain/**", Layer: "domain", Feature: "root"},
			{Pattern: "**/application/**", Layer: "application", Feature: "root"},
			{Pattern: "**/presentation/**", Layer: "presentation", Feature: "root"},
			{Pattern: "**/infrastructure/**", Layer: "infrastructure", Feature: "root"},
			{Pattern: "**/bootstrap/**", Layer: "bootstrap", Feature: "root"},
		},
		Dependencies: []DependencyRule{
			{FromLayers: []string{"domain"}, ToLayers: []string{"domain"}, ToFeatures: []string{"same", "shared"}},
			{FromLayers: []string{"application"}, ToLayers: []string{"domain"}, ToFeatures: []string{"same", "shared"}},
			{FromLayers: []string{"application"}, ToLayers: []string{"application"}, ToFeatures: []string{"same", "shared"}, ToPaths: []string{"**/ports/**", "**/shared/application/**"}},
			{FromLayers: []string{"presentation"}, ToLayers: []string{"application"}, ToFeatures: []string{"same", "shared"}, ToPaths: []string{"**/read_models/**", "**/intents/**", "**/shared/application/**"}},
			{FromLayers: []string{"presentation"}, ToLayers: []string{"presentation"}, ToFeatures: []string{"same", "shared"}},
			{FromLayers: []string{"infrastructure"}, ToLayers: []string{"domain"}, ToFeatures: []string{"same", "shared"}},
			{FromLayers: []string{"infrastructure"}, ToLayers: []string{"application"}, ToFeatures: []string{"same", "shared"}, ToPaths: []string{"**/ports/**", "**/shared/application/**"}},
			{FromLayers: []string{"infrastructure"}, ToLayers: []string{"infrastructure"}, ToFeatures: []string{"same", "shared"}, ToPaths: []string{"**/generated/protocol/**", "**/protocol/generated/**"}},
			{FromLayers: []string{"bootstrap"}, ToLayers: []string{"*"}, ToFeatures: []string{"*"}},
		},
		RuntimeBoundaries: []string{"**/runtime_boundaries/**"},
		TestPatterns:      []string{"**/test/**", "**/tests/**", "**/*_test.gd", "**/test_*.gd"},
		Allowlist:         DefaultAllowlistPath,
		Unclassified:      "error",
	}
}

// LoadConfig loads a config file, or returns defaults when the default path does not exist.
func LoadConfig(root, name string) (Config, error) {
	if name == "" {
		name = DefaultConfigPath
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(root, filepath.FromSlash(name))
	}
	data, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) && filepath.Clean(name) == filepath.Join(filepath.Clean(root), filepath.FromSlash(DefaultConfigPath)) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	// The floor is read before unknown-key reporting and before Validate.
	// A config written for a newer gdkit normally carries both the floor and
	// the syntax that needed it, and naming the unknown key or the validation
	// failure would describe a typo instead of a binary that is too old.
	if err := checkMinimumVersion(data, name); err != nil {
		return Config{}, err
	}
	if err := checkUnknownKeys(data); err != nil {
		return Config{}, err
	}
	config := DefaultConfig()
	defaults := DefaultConfig()
	// encoding/json decodes an array element onto whatever the slice already
	// holds at that index, so leaving the defaults in place would merge a
	// default entry into the one the file declares at the same position — a
	// classification with no layer would inherit the default entry's layer
	// instead of failing. Every list starts empty and the defaults are
	// restored below for the keys the file omits.
	config.SourceRoots = nil
	config.Exclude = nil
	config.Classifications = nil
	config.Dependencies = nil
	config.RuntimeBoundaries = nil
	config.TestPatterns = nil
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	// The decoder reads one value and stops, while checkMinimumVersion and
	// checkUnknownKeys above require the file to be exactly one value and skip
	// themselves when it is not. Without this a config followed by a second
	// value would load with neither check applied, so a declared version floor
	// would silently not apply.
	if decoder.More() {
		return Config{}, errors.New("parse config: unexpected content after the top-level object")
	}
	if config.SourceRoots == nil {
		config.SourceRoots = defaults.SourceRoots
	}
	if config.Exclude == nil {
		config.Exclude = defaults.Exclude
	}
	if config.Classifications == nil {
		config.Classifications = defaults.Classifications
	}
	if config.Dependencies == nil {
		config.Dependencies = defaults.Dependencies
	}
	if config.RuntimeBoundaries == nil {
		config.RuntimeBoundaries = defaults.RuntimeBoundaries
	}
	if config.TestPatterns == nil {
		config.TestPatterns = defaults.TestPatterns
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// checkUnknownKeys rejects any key the config schema does not define, naming
// the key's full JSON path. encoding/json's own strict decoding reports the
// key alone, which does not locate a typo nested inside one of many rules.
func checkUnknownKeys(data []byte) error {
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		// Malformed JSON is reported, with its offset, by the decode that follows.
		return nil
	}
	return unknownKeysIn(document, reflect.TypeOf(Config{}), "")
}

func unknownKeysIn(value any, target reflect.Type, path string) error {
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	switch target.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			field, known := jsonField(target, key)
			if !known {
				return fmt.Errorf("unknown key %q in architecture config", joinKeyPath(path, key))
			}
			if err := unknownKeysIn(object[key], field.Type, joinKeyPath(path, key)); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		items, ok := value.([]any)
		if !ok {
			return nil
		}
		for index, item := range items {
			if err := unknownKeysIn(item, target.Elem(), fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

// jsonField finds the struct field a JSON key decodes into. The match is
// case-insensitive because that is how encoding/json itself resolves keys.
func jsonField(target reflect.Type, key string) (reflect.StructField, bool) {
	for index := 0; index < target.NumField(); index++ {
		field := target.Field(index)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		if strings.EqualFold(name, key) {
			return field, true
		}
	}
	return reflect.StructField{}, false
}

func joinKeyPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// Validate checks configuration values and patterns.
func (c Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported architecture config version %d", c.Version)
	}
	if len(c.SourceRoots) == 0 {
		return errors.New("architecture config needs at least one source_root")
	}
	for _, root := range c.SourceRoots {
		clean := filepath.Clean(filepath.FromSlash(root))
		if root == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(filepath.ToSlash(clean), "../") {
			return fmt.Errorf("source_root %q must be a project-relative path", root)
		}
	}
	if c.Unclassified != "error" && c.Unclassified != "ignore" {
		return errors.New("unclassified must be \"error\" or \"ignore\"")
	}
	for index, rule := range c.Classifications {
		// A missing layer has no sensible default: silently picking one would
		// reclassify a whole directory tree and describe a layering nobody
		// configured, so it is a configuration error.
		switch {
		case rule.Pattern == "":
			return fmt.Errorf("classifications[%d] requires a pattern", index)
		case rule.Layer == "":
			return fmt.Errorf("classifications[%d] (pattern %q) requires a layer", index, rule.Pattern)
		case rule.Feature == "":
			return fmt.Errorf("classifications[%d] (pattern %q) requires a feature", index, rule.Pattern)
		}
		if _, err := compilePattern(rule.Pattern); err != nil {
			return fmt.Errorf("classification pattern %q: %w", rule.Pattern, err)
		}
		if strings.Contains(rule.Feature, "{feature}") && rule.Feature != "{feature}" {
			return fmt.Errorf("classification feature %q must be either a fixed name or {feature}", rule.Feature)
		}
		if rule.Feature == "{feature}" && strings.Count(rule.Pattern, "{feature}") != 1 {
			return fmt.Errorf("classification pattern %q must capture {feature} exactly once", rule.Pattern)
		}
	}
	patterns := append(append(append([]string{}, c.Exclude...), c.RuntimeBoundaries...), c.TestPatterns...)
	for _, rule := range c.Dependencies {
		if len(rule.FromLayers) == 0 || len(rule.ToLayers) == 0 {
			return errors.New("dependency rules require from_layers and to_layers")
		}
		patterns = append(patterns, rule.FromPaths...)
		patterns = append(patterns, rule.ToPaths...)
		patterns = append(patterns, rule.ExceptToPaths...)
	}
	for _, pattern := range patterns {
		if _, err := compilePattern(pattern); err != nil {
			return fmt.Errorf("pattern %q: %w", pattern, err)
		}
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == "*" || candidate == value {
			return true
		}
	}
	return false
}

func (c Config) classify(name string) (Classification, bool) {
	name = filepath.ToSlash(name)
	for _, rule := range c.Classifications {
		matches, captures := matchPattern(rule.Pattern, name)
		if !matches {
			continue
		}
		feature := rule.Feature
		if feature == "{feature}" {
			feature = captures["feature"]
		}
		return Classification{Layer: rule.Layer, Feature: feature}, true
	}
	return Classification{}, false
}

func (c Config) dependencyAllowed(from File, toPath string, to Classification) bool {
	fromPath := filepath.ToSlash(from.Path)
	for _, rule := range c.Dependencies {
		if !contains(rule.FromLayers, from.Classification.Layer) || !featureMatches(rule.FromFeatures, from.Classification.Feature, from.Classification.Feature) {
			continue
		}
		// An omitted from_paths means any file the layer and feature already
		// allow, so the key can only narrow the rule it appears on.
		if len(rule.FromPaths) > 0 && !matchesAny(rule.FromPaths, fromPath) {
			continue
		}
		if !contains(rule.ToLayers, to.Layer) || !featureMatches(rule.ToFeatures, to.Feature, from.Classification.Feature) {
			continue
		}
		if len(rule.ToPaths) > 0 && !matchesAny(rule.ToPaths, toPath) {
			continue
		}
		if matchesAny(rule.ExceptToPaths, toPath) {
			continue
		}
		return true
	}
	return false
}

func featureMatches(allowed []string, actual, source string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, value := range allowed {
		if value == "*" || value == actual || value == "same" && actual == source {
			return true
		}
	}
	return false
}

// checkMinimumVersion enforces the config's own minimum_gdkit_version against
// the running binary. It reads the key with a non-strict decode of its own so
// the floor is reported ahead of any other configuration problem; malformed
// JSON is left to the decode in LoadConfig, which reports it with an offset.
//
// The development-build bypass is read from the environment here rather than
// passed in by the caller because the check has to run at this point in
// LoadConfig to order correctly, so the bypass has to as well.
func checkMinimumVersion(data []byte, configPath string) error {
	var document struct {
		MinimumGDKitVersion string `json:"minimum_gdkit_version"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil
	}
	if document.MinimumGDKitVersion == "" {
		return nil
	}
	minimum, err := versiongate.ParseRequirement(document.MinimumGDKitVersion)
	if err != nil {
		return fmt.Errorf("minimum_gdkit_version %w", err)
	}
	reported := detectedVersion()
	if versiongate.IsDevelopment(reported) {
		if os.Getenv(versiongate.AllowDevelopmentEnvironmentVariable) != "" {
			return nil
		}
		return fmt.Errorf("%s requires gdkit %s or newer, which is not satisfied by a development build; set %s=1 to bypass",
			configPath, minimum, versiongate.AllowDevelopmentEnvironmentVariable)
	}
	current, err := versiongate.Parse(reported)
	if err != nil {
		return fmt.Errorf("%s requires gdkit %s or newer, but this binary reports an unrecognized version %q", configPath, minimum, reported)
	}
	if current.Less(minimum) {
		return fmt.Errorf("%s requires gdkit %s or newer, but this binary is %s", configPath, minimum, reported)
	}
	return nil
}
