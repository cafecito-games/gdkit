package architecture

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultConfigPath    = ".gdkit/architecture.json"
	DefaultAllowlistPath = ".gdkit/allowlist.json"
)

// Config controls discovery, classification, and dependency policy.
type Config struct {
	Version           int                  `json:"version"`
	SourceRoots       []string             `json:"source_roots"`
	Exclude           []string             `json:"exclude"`
	Classifications   []ClassificationRule `json:"classifications"`
	Dependencies      []DependencyRule     `json:"dependencies"`
	RuntimeBoundaries []string             `json:"runtime_boundaries"`
	TestPatterns      []string             `json:"test_patterns"`
	Allowlist         string               `json:"allowlist"`
	Unclassified      string               `json:"unclassified"`
}

// ClassificationRule assigns a layer and feature. {feature} captures one path segment.
type ClassificationRule struct {
	Pattern string `json:"pattern"`
	Layer   string `json:"layer"`
	Feature string `json:"feature"`
}

// DependencyRule allows matching source files to depend on matching targets.
// "same" in ToFeatures means the source feature; "*" means any feature.
type DependencyRule struct {
	FromLayers    []string `json:"from_layers"`
	FromFeatures  []string `json:"from_features,omitempty"`
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
	config := DefaultConfig()
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
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
	for _, rule := range c.Classifications {
		if rule.Pattern == "" || rule.Layer == "" || rule.Feature == "" {
			return errors.New("classification rules require pattern, layer, and feature")
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
	for _, rule := range c.Dependencies {
		if !contains(rule.FromLayers, from.Classification.Layer) || !featureMatches(rule.FromFeatures, from.Classification.Feature, from.Classification.Feature) {
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
