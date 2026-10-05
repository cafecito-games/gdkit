package generate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cafecito-games/gdkit/internal/failure"
	"github.com/cafecito-games/gdkit/internal/glob"
)

// DefaultConfigPath is where gen init writes and gen check reads.
const DefaultConfigPath = ".gdkit/generate.json"

// Entry opts the files matching Paths into Generators.
type Entry struct {
	Paths      []string `json:"paths"`
	Generators []string `json:"generators"`
}

// Config is the generate policy. There is no godot_version key: every semantic
// this tool relies on is constant across Godot 4.
type Config struct {
	Version     int      `json:"version"`
	SourceRoots []string `json:"source_roots"`
	Exclude     []string `json:"exclude"`
	// Generate opts files in by path. A file matching several entries gets the
	// union of their generators.
	Generate []Entry `json:"generate"`
	// HelpersPath names the generated utility class, project-root-relative.
	// It must be somewhere the snapshot walks, because the class is resolved
	// out of the index like any other: a path the walk prunes would make
	// deep_equals refuse every class while the file sat on disk.
	HelpersPath string `json:"helpers_path,omitempty"`
	// MinimumGdkitVersion is the floor this config needs.
	MinimumGdkitVersion string `json:"minimum_gdkit_version,omitempty"`
}

// DefaultConfig is both the zero-config policy and what gen init writes.
//
// Exclude matches format.DefaultConfig() exactly rather than being empty: an
// empty default would make addons/** a generation target, so a glob someone
// wrote for their own code could start rewriting a third-party addon.
func DefaultConfig() Config {
	return Config{
		Version:     1,
		SourceRoots: []string{"."},
		Exclude:     []string{".git/**", ".godot/**", ".gdkit/**", "addons/**"},
		Generate:    []Entry{},
		HelpersPath: "gdkit_helpers.gd",
	}
}

// LoadConfig loads a generate config. When name is empty it uses the default
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
		return Config{}, failure.WrapPath(failure.ConfigRead, name, fmt.Errorf("read generate config: %w", err))
	}
	config := DefaultConfig()
	// Every list field is cleared before decoding and restored afterwards only
	// when the file omits its key: encoding/json decodes an array element onto
	// whatever the slice already holds at that index, so without this a
	// declared entry would inherit fields from a default entry at the same
	// position.
	defaults := config
	config.SourceRoots, config.Exclude, config.Generate = nil, nil, nil
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, failure.WrapPath(failure.ConfigParse, name, fmt.Errorf("parse generate config: %w", err))
	}
	if config.SourceRoots == nil {
		config.SourceRoots = defaults.SourceRoots
	}
	if config.Exclude == nil {
		config.Exclude = defaults.Exclude
	}
	if config.Generate == nil {
		config.Generate = defaults.Generate
	}
	if err := config.Validate(); err != nil {
		return Config{}, failure.WrapPath(failure.ConfigInvalid, name, err)
	}
	return config, nil
}

// Validate compiles every glob and checks every generator name, so a bad
// config fails as a configuration error rather than silently generating
// nothing.
func (c Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported generate config version %d", c.Version)
	}
	if len(c.SourceRoots) == 0 {
		return errors.New("generate config needs at least one source_root")
	}
	for _, root := range c.SourceRoots {
		clean := filepath.Clean(filepath.FromSlash(root))
		if root == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(filepath.ToSlash(clean), "../") {
			return fmt.Errorf("source_root %q must be a project-relative path", root)
		}
	}
	for _, pattern := range c.Exclude {
		if _, err := glob.Compile(pattern); err != nil {
			return fmt.Errorf("exclude pattern %q: %w", pattern, err)
		}
	}
	if err := c.validateHelpersPath(); err != nil {
		return err
	}
	_, err := c.compile()
	return err
}

// validateHelpersPath checks that the helpers file is somewhere the project
// walk will reach. It is a path-only check and performs no I/O, so it reports
// a path the walk prunes whether or not the file exists yet — which is the
// point: Check cannot tell an excluded file from an absent one, and reporting
// "run gen init --helpers" for a file that is already there would be a lie.
func (c Config) validateHelpersPath() error {
	if c.HelpersPath == "" {
		return errors.New("helpers_path must name the generated utility class")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(c.HelpersPath)))
	if filepath.IsAbs(filepath.FromSlash(c.HelpersPath)) || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("helpers_path %q must be a project-relative path", c.HelpersPath)
	}
	if !strings.HasSuffix(clean, ".gd") {
		return fmt.Errorf("helpers_path %q must name a .gd file", c.HelpersPath)
	}
	for _, pattern := range c.Exclude {
		if glob.MatchAny([]string{pattern}, clean) {
			return fmt.Errorf("helpers_path %q is excluded by %q, so the project walk would never reach it", clean, pattern)
		}
	}
	for _, root := range c.SourceRoots {
		cleanRoot := filepath.ToSlash(filepath.Clean(filepath.FromSlash(root)))
		if cleanRoot == "." || clean == cleanRoot || strings.HasPrefix(clean, cleanRoot+"/") {
			return nil
		}
	}
	return fmt.Errorf("helpers_path %q is outside every source_root, so the project walk would never reach it", clean)
}

// compiled holds the config's prepared forms.
type compiled struct {
	entries     []Entry
	helpersPath string
}

func (c Config) compile() (*compiled, error) {
	result := &compiled{}
	for index, entry := range c.Generate {
		if len(entry.Paths) == 0 {
			return nil, fmt.Errorf("generate[%d]: paths is required", index)
		}
		if len(entry.Generators) == 0 {
			return nil, fmt.Errorf("generate[%d]: generators is required", index)
		}
		for _, pattern := range entry.Paths {
			if _, err := glob.Compile(pattern); err != nil {
				return nil, fmt.Errorf("generate[%d].paths %q: %w", index, pattern, err)
			}
		}
		for _, name := range entry.Generators {
			if !isGeneratorName(name) {
				return nil, fmt.Errorf("generate[%d].generators: unknown generator %q, want one of %s",
					index, name, strings.Join(GeneratorNames(), ", "))
			}
		}
		result.entries = append(result.entries, entry)
	}
	result.helpersPath = filepath.ToSlash(filepath.Clean(filepath.FromSlash(c.HelpersPath)))
	return result, nil
}

// generatorsFor returns the union of the generators every matching entry opts
// path into, deduplicated and in registry order.
//
// Union rather than first-match, because that is the grain of the codebase:
// architecture's dependencies are alternative allow rules, where capability is
// added by adding a rule. Union is also order-independent, so reordering the
// list cannot change the output.
func (c *compiled) generatorsFor(path string) []string {
	seen := map[string]bool{}
	for _, entry := range c.entries {
		if !glob.MatchAny(entry.Paths, path) {
			continue
		}
		for _, name := range entry.Generators {
			seen[name] = true
		}
	}
	names := []string{}
	for _, generator := range registry {
		if seen[generator.Name()] {
			names = append(names, generator.Name())
		}
	}
	return names
}
