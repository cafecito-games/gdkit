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

	"github.com/cafecito-games/gdkit/internal/failure"
	"github.com/cafecito-games/gdkit/internal/glob"
	"github.com/cafecito-games/gdkit/internal/versiongate"
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

	// GodotVersion is the engine version the project targets, written as
	// "major.minor" or "major.minor.patch". A rule whose fix needs newer syntax
	// than this reports nothing, so a project is never told to write a type
	// annotation its engine cannot parse.
	//
	// It defaults to the newest Godot gdkit knows, which is safe only because
	// every version-gated rule ships inert: a project that has not opted in
	// cannot be affected by the default, and a project on an older engine
	// lowers this one key instead of hunting for the right rule name.
	GodotVersion string `json:"godot_version"`

	// Disable turns rules off by name.
	Disable []string `json:"disable,omitempty"`
	// Enable turns on rules that ship inert. Naming a rule that is not pending
	// is accepted and does nothing, so a config keeps working unchanged after a
	// rule graduates to running by default. Disable wins over Enable.
	Enable []string `json:"enable,omitempty"`
	// EnableNewRules opts in to every pending rule at once, including ones a
	// later release adds. It trades reproducibility across upgrades for always
	// running the strictest policy gdkit knows.
	EnableNewRules bool `json:"enable_new_rules,omitempty"`
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

	// The six fields below hold function-name glob patterns the matching typing
	// rule skips. An empty list means no exemptions: the rules are inert until
	// a project enables them, which is PendingRule's job and never a list's.
	// MissingDocstring below is the exception, not the pattern — its empty list
	// turns that rule off, because it predates PendingRule.
	//
	// What the pattern matches depends on the rule. For require-return-type and
	// require-argument-type it is the function being declared; for a lambda's
	// parameter it is the enclosing function, not the lambda. For
	// require-variable-type, require-typed-collection, and
	// require-typed-loop-variable it is the enclosing function, so ["_process"]
	// quiets a hot loop's locals without quieting the file; a class-scope
	// declaration has no enclosing function, and a rule passes no name for one, so
	// no list can exempt it — it is suppressed with a # gdkit:ignore comment
	// instead. Code inside a property accessor is named for the property, which
	// is the only name a reader could write a pattern for.
	//
	// For require-signal-argument-type it is the signal's own name, as it is for
	// a bare collection written in a signal's payload: a signal is not inside a
	// function, so its own name is the only name a reader could write a pattern
	// for.
	RequireReturnType         []string `json:"require-return-type,omitempty"`
	RequireArgumentType       []string `json:"require-argument-type,omitempty"`
	RequireVariableType       []string `json:"require-variable-type,omitempty"`
	RequireTypedCollection    []string `json:"require-typed-collection,omitempty"`
	RequireSignalArgumentType []string `json:"require-signal-argument-type,omitempty"`
	RequireTypedLoopVariable  []string `json:"require-typed-loop-variable,omitempty"`

	// MissingDocstring lists the member kinds that require a "##"
	// documentation comment. It is empty by default, which makes the
	// missing-docstring rule inert, so a project opts in one kind at a time.
	MissingDocstring []string `json:"missing-docstring"`

	NoEngineLogging NoEngineLoggingConfig `json:"no-engine-logging"`
}

// NoEngineLoggingConfig configures the no-engine-logging rule.
type NoEngineLoggingConfig struct {
	// Functions names the calls the rule reports. A name may carry one
	// qualifier, as in "OS.alert"; an unqualified name matches a bare call
	// only, so a method of the project's own logger is never reported. An
	// empty list silences the rule.
	Functions []string `json:"functions"`
	// Logger names the abstraction the diagnostic points at. When it is empty
	// the message says "a logger abstraction" instead.
	Logger string `json:"logger,omitempty"`
}

// DefaultConfig is gdlint's default policy, expressed in gdkit's config shape.
func DefaultConfig() Config {
	return Config{
		Version:      1,
		SourceRoots:  []string{"."},
		Exclude:      []string{".git/**", ".godot/**", ".gdkit/**", "addons/**"},
		GodotVersion: "4.7",

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

		// Copied, because decoding a configuration file onto the defaults
		// writes through the slice it finds here.
		NoEngineLogging: NoEngineLoggingConfig{
			Functions: append([]string(nil), defaultEngineLoggingFunctions...),
		},
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

// exemptPatterns maps each typing rule to its configured exempt patterns.
func (c Config) exemptPatterns() map[string][]string {
	return map[string][]string{
		"require-return-type":          c.RequireReturnType,
		"require-argument-type":        c.RequireArgumentType,
		"require-variable-type":        c.RequireVariableType,
		"require-typed-collection":     c.RequireTypedCollection,
		"require-signal-argument-type": c.RequireSignalArgumentType,
		"require-typed-loop-variable":  c.RequireTypedLoopVariable,
	}
}

// compileExemptPatterns compiles every exempt pattern once, so no rule compiles
// one per file.
func (c Config) compileExemptPatterns() (map[string][]glob.Pattern, error) {
	compiled := make(map[string][]glob.Pattern)
	for rule, patterns := range c.exemptPatterns() {
		for _, pattern := range patterns {
			parsed, err := glob.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("%s exempt pattern %q: %w", rule, pattern, err)
			}
			compiled[rule] = append(compiled[rule], parsed)
		}
	}
	return compiled, nil
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
		return Config{}, failure.WrapPath(failure.ConfigRead, name, fmt.Errorf("read lint config: %w", err))
	}
	config := DefaultConfig()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, failure.WrapPath(failure.ConfigParse, name, fmt.Errorf("parse lint config: %w", err))
	}
	if err := config.Validate(); err != nil {
		return Config{}, failure.WrapPath(failure.ConfigInvalid, name, err)
	}
	return config, nil
}

// Validate checks configuration values, compiles every exclude and name
// pattern, and rejects names that no rule uses, so a typo fails loudly.
func (c Config) Validate() error {
	_, err := c.validate()
	return err
}

// compiledConfig holds everything validate compiles once, so no rule compiles
// anything per file. Context carries it, and a rule reads it through Context's
// accessors by convention rather than by enforcement: every rule lives in this
// package, so an unexported field is reachable either way.
type compiledConfig struct {
	patterns     map[string]*regexp.Regexp
	godotVersion versiongate.Version
	exempt       map[string][]glob.Pattern
}

// validate is Validate, also returning the compiled configuration so a caller
// that needs it does not compile twice.
func (c Config) validate() (*compiledConfig, error) {
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
	// Enable is not required to name a *pending* rule. A rule that graduates to
	// running by default would otherwise turn every config that opted in to it
	// into a configuration error on upgrade.
	for _, name := range c.Enable {
		if !IsRule(name) {
			return nil, fmt.Errorf("enable names unknown rule %q", name)
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
	for _, function := range c.NoEngineLogging.Functions {
		if err := validateEngineLoggingFunction(function); err != nil {
			return nil, err
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
	godotVersion, err := versiongate.ParseEngineVersion(c.GodotVersion)
	if err != nil {
		return nil, fmt.Errorf("godot_version: %w", err)
	}
	exempt, err := c.compileExemptPatterns()
	if err != nil {
		return nil, err
	}
	patterns, err := c.compileNamePatterns()
	if err != nil {
		return nil, err
	}
	return &compiledConfig{patterns: patterns, godotVersion: godotVersion, exempt: exempt}, nil
}
