// Package licenseheader plans and applies managed license headers to GDScript files.
package licenseheader

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

// DefaultConfigPath is where gdkit license looks for its configuration.
const DefaultConfigPath = ".gdkit/license.json"

// Config controls discovery and the exact comment lines in the managed block.
// Header lines do not include GDScript's # comment prefix.
type Config struct {
	Version     int      `json:"version"`
	SourceRoots []string `json:"source_roots"`
	Exclude     []string `json:"exclude"`
	Header      []string `json:"header"`
}

// DefaultConfig is deliberately inert: a project has to name its copyright
// holder and license before gdkit can safely add anything to source files.
func DefaultConfig() Config {
	return Config{
		Version:     1,
		SourceRoots: []string{"."},
		Exclude:     []string{".git/**", ".godot/**", ".gdkit/**", "addons/**"},
		Header:      []string{},
	}
}

// LoadConfig loads a license config. An absent implicit config uses the inert
// default; an explicitly named missing file is an error.
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
		return Config{}, failure.WrapPath(failure.ConfigRead, name, fmt.Errorf("read license config: %w", err))
	}
	config := DefaultConfig()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, failure.WrapPath(failure.ConfigParse, name, fmt.Errorf("parse license config: %w", err))
	}
	if err := config.Validate(); err != nil {
		return Config{}, failure.WrapPath(failure.ConfigInvalid, name, err)
	}
	return config, nil
}

// Validate rejects values that could widen discovery unexpectedly or break the
// line-oriented managed block.
func (c Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported license config version %d", c.Version)
	}
	if len(c.SourceRoots) == 0 {
		return errors.New("license config needs at least one source_root")
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
	for i, line := range c.Header {
		if strings.ContainsAny(line, "\r\n") {
			return fmt.Errorf("header[%d] must be one line", i)
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == beginMarker || trimmed == endMarker {
			return fmt.Errorf("header[%d] uses reserved managed-block marker %q", i, line)
		}
	}
	return nil
}
