// Package format rewrites GDScript sources into a canonical style. It drives
// gdparser's formatter over a project.Snapshot and refuses any rewrite that
// would change what a file means.
package format

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cafecito-games/gdkit/internal/glob"
	gdformat "github.com/cafecito-games/gdparser/format"
)

// DefaultConfigPath is where gdkit format looks for its configuration.
const DefaultConfigPath = ".gdkit/format.json"

// BlankLines controls blank line placement. TopLevel is the exact number of
// blank lines around top-level function and class declarations; Nested is the
// most consecutive blank lines kept anywhere else.
type BlankLines struct {
	TopLevel int `json:"top_level"`
	Nested   int `json:"nested"`
}

// Config controls discovery and the style the formatter emits.
type Config struct {
	Version     int      `json:"version"`
	SourceRoots []string `json:"source_roots"`
	Exclude     []string `json:"exclude"`

	// LineWidth is the column budget a line is kept within where possible.
	LineWidth int `json:"line_width"`
	// TabWidth is the columns a tab occupies when measuring a line, and the
	// spaces per level when Indent is "spaces".
	TabWidth int `json:"tab_width"`
	// Indent is "tabs" or "spaces".
	Indent string `json:"indent"`
	// QuoteStyle is "double", "single", or "preserve".
	QuoteStyle string `json:"quote_style"`
	// CommentSpacing is "normalize" or "preserve".
	CommentSpacing string `json:"comment_spacing"`
	// Operators is "words" (and, or, not) or "preserve".
	Operators string `json:"operators"`
	// Numbers is "normalize" or "preserve".
	Numbers string `json:"numbers"`
	// TrailingCommas is "when-broken" or "never".
	TrailingCommas string     `json:"trailing_commas"`
	BlankLines     BlankLines `json:"blank_lines"`
}

// DefaultConfig is the Godot GDScript style guide, expressed in gdkit's config
// shape.
func DefaultConfig() Config {
	return Config{
		Version:     1,
		SourceRoots: []string{"."},
		Exclude:     []string{".git/**", ".godot/**", ".gdkit/**", "addons/**"},

		LineWidth:      100,
		TabWidth:       4,
		Indent:         "tabs",
		QuoteStyle:     "double",
		CommentSpacing: "normalize",
		Operators:      "words",
		Numbers:        "normalize",
		TrailingCommas: "when-broken",
		BlankLines:     BlankLines{TopLevel: 2, Nested: 1},
	}
}

// LoadConfig loads a format config. When name is empty it uses the default
// path and returns defaults if that file does not exist; an explicitly named
// file that is missing is an error. Values are unmarshalled onto the defaults,
// so an omitted field keeps the Godot style.
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
		return Config{}, fmt.Errorf("read format config: %w", err)
	}
	config := DefaultConfig()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("parse format config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// Validate checks configuration values and compiles every exclude pattern, so
// a typo fails loudly instead of silently formatting in the default style.
func (c Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported format config version %d", c.Version)
	}
	if len(c.SourceRoots) == 0 {
		return errors.New("format config needs at least one source_root")
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
	minimums := []struct {
		name  string
		value int
	}{
		{"line_width", c.LineWidth},
		{"tab_width", c.TabWidth},
		{"blank_lines.top_level", c.BlankLines.TopLevel},
		{"blank_lines.nested", c.BlankLines.Nested},
	}
	for _, minimum := range minimums {
		if minimum.value < 1 {
			return fmt.Errorf("%s must be at least 1, got %d", minimum.name, minimum.value)
		}
	}
	_, err := c.options()
	return err
}

// options translates the configuration into the formatter's own options. It
// fails on an enumeration value the formatter has no style for.
func (c Config) options() (gdformat.Options, error) {
	options := gdformat.Options{
		LineWidth:  c.LineWidth,
		TabWidth:   c.TabWidth,
		BlankLines: gdformat.BlankLineLimits{TopLevel: c.BlankLines.TopLevel, Nested: c.BlankLines.Nested},
	}
	switch c.Indent {
	case "tabs":
		options.Indent = gdformat.Tabs
	case "spaces":
		options.Indent = gdformat.Spaces
	default:
		return gdformat.Options{}, fmt.Errorf(`indent must be "tabs" or "spaces", got %q`, c.Indent)
	}
	switch c.QuoteStyle {
	case "double":
		options.QuoteStyle = gdformat.DoubleQuotes
	case "single":
		options.QuoteStyle = gdformat.SingleQuotes
	case "preserve":
		options.QuoteStyle = gdformat.PreserveQuotes
	default:
		return gdformat.Options{}, fmt.Errorf(`quote_style must be "double", "single", or "preserve", got %q`, c.QuoteStyle)
	}
	switch c.CommentSpacing {
	case "normalize":
		options.CommentSpacing = gdformat.NormalizeComments
	case "preserve":
		options.CommentSpacing = gdformat.PreserveComments
	default:
		return gdformat.Options{}, fmt.Errorf(`comment_spacing must be "normalize" or "preserve", got %q`, c.CommentSpacing)
	}
	switch c.Operators {
	case "words":
		options.Operators = gdformat.WordOperators
	case "preserve":
		options.Operators = gdformat.PreserveOperators
	default:
		return gdformat.Options{}, fmt.Errorf(`operators must be "words" or "preserve", got %q`, c.Operators)
	}
	switch c.Numbers {
	case "normalize":
		options.Numbers = gdformat.NormalizeNumbers
	case "preserve":
		options.Numbers = gdformat.PreserveNumbers
	default:
		return gdformat.Options{}, fmt.Errorf(`numbers must be "normalize" or "preserve", got %q`, c.Numbers)
	}
	switch c.TrailingCommas {
	case "when-broken":
		options.TrailingCommas = gdformat.TrailingCommasWhenBroken
	case "never":
		options.TrailingCommas = gdformat.NoTrailingCommas
	default:
		return gdformat.Options{}, fmt.Errorf(`trailing_commas must be "when-broken" or "never", got %q`, c.TrailingCommas)
	}
	return options, nil
}
