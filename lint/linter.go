package lint

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/cafecito-games/gdkit/project"
)

// Rule is one check. Rules do no I/O and no sorting; the driver handles both.
// The driver stamps Rule, Path, and Severity onto every diagnostic, so a rule
// sets only Message, Line, Column, and optionally the end position.
type Rule interface {
	// Name is the rule's public identifier. It appears in output, in config,
	// and in inline ignore comments, so it must never change.
	Name() string
	Check(*Context, *project.Script) []Diagnostic
}

// Context gives a rule the project and its resolved configuration.
type Context struct {
	Snapshot *project.Snapshot
	Config   Config

	patterns map[string]*regexp.Regexp
}

// Pattern returns the compiled, anchored pattern for a name rule.
func (c *Context) Pattern(rule string) *regexp.Regexp { return c.patterns[rule] }

var registry = map[string]Rule{}

// register adds a rule. Called from each rule file's init.
func register(rule Rule) {
	if _, exists := registry[rule.Name()]; exists {
		panic("lint: duplicate rule " + rule.Name())
	}
	registry[rule.Name()] = rule
}

// IsRule reports whether name is a known rule. "source-parse" is reported by
// the driver rather than by a registered rule, but is nameable in config.
func IsRule(name string) bool {
	if name == "source-parse" {
		return true
	}
	_, ok := registry[name]
	return ok
}

// RuleNames lists every rule, sorted.
func RuleNames() []string {
	names := make([]string, 0, len(registry)+1)
	names = append(names, "source-parse")
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Linter runs the enabled rules over a snapshot.
type Linter struct {
	context  Context
	enabled  []Rule
	severity map[string]Severity
}

// New validates the configuration and compiles every name pattern, so a bad
// pattern is a configuration error rather than a silently dead rule.
func New(config Config) (*Linter, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	disabled := make(map[string]bool, len(config.Disable))
	for _, name := range config.Disable {
		disabled[name] = true
	}
	patterns := make(map[string]*regexp.Regexp)
	for rule, pattern := range config.namePatterns() {
		compiled, err := regexp.Compile("^(?:" + pattern + ")$")
		if err != nil {
			return nil, fmt.Errorf("pattern for %s: %w", rule, err)
		}
		patterns[rule] = compiled
	}
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	enabled := make([]Rule, 0, len(names))
	for _, name := range names {
		if !disabled[name] {
			enabled = append(enabled, registry[name])
		}
	}
	return &Linter{
		context:  Context{Config: config, patterns: patterns},
		enabled:  enabled,
		severity: config.Severity,
	}, nil
}

// Lint runs every enabled rule over every script in the snapshot.
func (l *Linter) Lint(snapshot *project.Snapshot) Report {
	context := l.context
	context.Snapshot = snapshot
	report := Report{Diagnostics: make([]Diagnostic, 0)}

	for _, path := range snapshot.Paths {
		script := snapshot.Scripts[path]
		if script.ParseError != nil {
			if !l.disabled("source-parse") {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{
					Rule: "source-parse", Severity: l.severityOf("source-parse"),
					Message: script.ParseError.Error(), Path: path, Line: 1, Column: 1,
				})
			}
			continue
		}
		for _, rule := range l.enabled {
			for _, diagnostic := range rule.Check(&context, script) {
				diagnostic.Rule = rule.Name()
				diagnostic.Path = script.Path
				diagnostic.Severity = l.severityOf(rule.Name())
				report.Diagnostics = append(report.Diagnostics, diagnostic)
			}
		}
	}
	report.sort()
	return report
}

func (l *Linter) severityOf(rule string) Severity {
	if severity, ok := l.severity[rule]; ok {
		return severity
	}
	return SeverityError
}

func (l *Linter) disabled(rule string) bool {
	for _, name := range l.context.Config.Disable {
		if name == rule {
			return true
		}
	}
	return false
}
