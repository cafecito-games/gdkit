package lint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cafecito-games/gdkit/internal/glob"
)

// DefaultConfigPath is where gdkit lint looks for its configuration.
const DefaultConfigPath = ".gdkit/lint.json"

// The name shapes gdlint uses. "Private" means an optional leading underscore.
const (
	pascalCase            = `([A-Z][a-z0-9]*)+`
	snakeCase             = `[a-z][a-z0-9]*(_[a-z0-9]+)*`
	privateSnakeCase      = `_?[a-z][a-z0-9]*(_[a-z0-9]+)*`
	upperSnakeCase        = `[A-Z][A-Z0-9]*(_[A-Z0-9]+)*`
	privateUpperSnakeCase = `_?[A-Z][A-Z0-9]*(_[A-Z0-9]+)*`
)

// knownOrderSlots is every slot name class-definitions-order accepts.
var knownOrderSlots = map[string]bool{
	"tools": true, "classnames": true, "extends": true, "docstrings": true,
	"signals": true, "enums": true, "consts": true, "staticvars": true,
	"exports": true, "pubvars": true, "prvvars": true,
	"onreadypubvars": true, "onreadyprvvars": true, "others": true,
}

// Config controls discovery and every rule. JSON keys are gdlint's rule names.
type Config struct {
	Version     int      `json:"version"`
	SourceRoots []string `json:"source_roots"`
	Exclude     []string `json:"exclude"`

	// Disable turns rules off by name.
	Disable []string `json:"disable,omitempty"`
	// Severity overrides a rule's severity. Unlisted rules are errors.
	Severity map[string]Severity `json:"severity,omitempty"`

	FunctionName                string `json:"function-name"`
	ClassName                   string `json:"class-name"`
	SubClassName                string `json:"sub-class-name"`
	SignalName                  string `json:"signal-name"`
	ClassVariableName           string `json:"class-variable-name"`
	ClassLoadVariableName       string `json:"class-load-variable-name"`
	FunctionVariableName        string `json:"function-variable-name"`
	FunctionPreloadVariableName string `json:"function-preload-variable-name"`
	FunctionArgumentName        string `json:"function-argument-name"`
	LoopVariableName            string `json:"loop-variable-name"`
	EnumName                    string `json:"enum-name"`
	EnumElementName             string `json:"enum-element-name"`
	ConstantName                string `json:"constant-name"`
	LoadConstantName            string `json:"load-constant-name"`

	MaxReturns              int `json:"max-returns"`
	MaxPublicMethods        int `json:"max-public-methods"`
	FunctionArgumentsNumber int `json:"function-arguments-number"`

	MaxFileLines  int `json:"max-file-lines"`
	MaxLineLength int `json:"max-line-length"`
	TabCharacters int `json:"tab-characters"`

	ClassDefinitionsOrder []string `json:"class-definitions-order"`

	// MissingDocstring lists the member kinds that require a "##"
	// documentation comment. It is empty by default, which makes the
	// missing-docstring rule inert, so a project opts in one kind at a time.
	MissingDocstring []string `json:"missing-docstring"`
}

// DefaultConfig is gdlint's default policy, expressed in gdkit's config shape.
func DefaultConfig() Config {
	return Config{
		Version:     1,
		SourceRoots: []string{"."},
		Exclude:     []string{".git/**", ".godot/**", ".gdkit/**", "addons/**"},

		FunctionName:                fmt.Sprintf(`(_on_%s(_[a-z0-9]+)*|%s)`, pascalCase, privateSnakeCase),
		ClassName:                   pascalCase,
		SubClassName:                fmt.Sprintf(`_?%s`, pascalCase),
		SignalName:                  snakeCase,
		ClassVariableName:           privateSnakeCase,
		ClassLoadVariableName:       fmt.Sprintf(`(%s|%s)`, pascalCase, privateSnakeCase),
		FunctionVariableName:        snakeCase,
		FunctionPreloadVariableName: pascalCase,
		FunctionArgumentName:        privateSnakeCase,
		LoopVariableName:            privateSnakeCase,
		EnumName:                    pascalCase,
		EnumElementName:             upperSnakeCase,
		ConstantName:                privateUpperSnakeCase,
		LoadConstantName:            fmt.Sprintf(`(%s|%s)`, pascalCase, privateUpperSnakeCase),

		MaxReturns:              6,
		MaxPublicMethods:        20,
		FunctionArgumentsNumber: 10,

		MaxFileLines:  1000,
		MaxLineLength: 100,
		TabCharacters: 1,

		ClassDefinitionsOrder: []string{
			"tools", "classnames", "extends", "docstrings", "signals", "enums",
			"consts", "staticvars", "exports", "pubvars", "prvvars",
			"onreadypubvars", "onreadyprvvars", "others",
		},

		MissingDocstring: []string{},
	}
}

// namePatterns maps each name rule to its configured pattern.
func (c Config) namePatterns() map[string]string {
	return map[string]string{
		"function-name":                  c.FunctionName,
		"class-name":                     c.ClassName,
		"sub-class-name":                 c.SubClassName,
		"signal-name":                    c.SignalName,
		"class-variable-name":            c.ClassVariableName,
		"class-load-variable-name":       c.ClassLoadVariableName,
		"function-variable-name":         c.FunctionVariableName,
		"function-preload-variable-name": c.FunctionPreloadVariableName,
		"function-argument-name":         c.FunctionArgumentName,
		"loop-variable-name":             c.LoopVariableName,
		"enum-name":                      c.EnumName,
		"enum-element-name":              c.EnumElementName,
		"constant-name":                  c.ConstantName,
		"load-constant-name":             c.LoadConstantName,
	}
}

// compileNamePatterns compiles every name rule's pattern, anchored to the whole
// identifier. An empty or invalid pattern is a configuration error.
func (c Config) compileNamePatterns() (map[string]*regexp.Regexp, error) {
	patterns := make(map[string]*regexp.Regexp)
	for rule, pattern := range c.namePatterns() {
		if pattern == "" {
			return nil, fmt.Errorf("pattern for %s must not be empty", rule)
		}
		compiled, err := regexp.Compile("^(?:" + pattern + ")$")
		if err != nil {
			return nil, fmt.Errorf("pattern for %s: %w", rule, err)
		}
		patterns[rule] = compiled
	}
	return patterns, nil
}

// LoadConfig loads a lint config. When name is empty it uses the default path
// and returns defaults if that file does not exist; an explicitly named file
// that is missing is an error. Values are unmarshalled onto the defaults, so an omitted
// field keeps gdlint's built-in policy.
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
		return Config{}, fmt.Errorf("read lint config: %w", err)
	}
	config := DefaultConfig()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("parse lint config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// Validate checks configuration values, compiles every exclude and name
// pattern, and rejects names that no rule uses, so a typo fails loudly.
func (c Config) Validate() error {
	_, err := c.validate()
	return err
}

// validate is Validate, also returning the compiled name patterns so a caller
// that needs them does not compile twice.
func (c Config) validate() (map[string]*regexp.Regexp, error) {
	if c.Version != 1 {
		return nil, fmt.Errorf("unsupported lint config version %d", c.Version)
	}
	if len(c.SourceRoots) == 0 {
		return nil, errors.New("lint config needs at least one source_root")
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
	for _, name := range c.Disable {
		if !IsRule(name) {
			return nil, fmt.Errorf("disable names unknown rule %q", name)
		}
	}
	for name, severity := range c.Severity {
		if !IsRule(name) {
			return nil, fmt.Errorf("severity names unknown rule %q", name)
		}
		if severity != SeverityError && severity != SeverityWarning {
			return nil, fmt.Errorf("severity for %q must be \"error\" or \"warning\"", name)
		}
	}
	for _, slot := range c.ClassDefinitionsOrder {
		if !knownOrderSlots[slot] {
			return nil, fmt.Errorf("class-definitions-order names unknown slot %q", slot)
		}
	}
	for _, kind := range c.MissingDocstring {
		if !knownDocKinds[kind] {
			return nil, fmt.Errorf("missing-docstring names unknown member kind %q", kind)
		}
	}
	limits := []struct {
		name  string
		value int
	}{
		{"max-returns", c.MaxReturns},
		{"max-public-methods", c.MaxPublicMethods},
		{"function-arguments-number", c.FunctionArgumentsNumber},
		{"max-file-lines", c.MaxFileLines},
		{"max-line-length", c.MaxLineLength},
		{"tab-characters", c.TabCharacters},
	}
	for _, limit := range limits {
		if limit.value < 0 {
			return nil, fmt.Errorf("%s must not be negative, got %d", limit.name, limit.value)
		}
	}
	return c.compileNamePatterns()
}
