package lint

import (
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
	Config Config

	patterns map[string]*regexp.Regexp
}

// Pattern returns the compiled, anchored pattern for a name rule.
func (c *Context) Pattern(rule string) *regexp.Regexp { return c.patterns[rule] }

var registry = map[string]Rule{}

// register adds a rule. Called from each rule file's init.
func register(rule Rule) {
	switch rule.Name() {
	case "source-parse", "unknown-ignore":
		panic("lint: reserved rule name " + rule.Name())
	}
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
	disabled map[string]bool
	severity map[string]Severity
}

// New validates the configuration and compiles every name pattern, so a bad
// pattern is a configuration error rather than a silently dead rule.
func New(config Config) (*Linter, error) {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	rules := make([]Rule, 0, len(names))
	for _, name := range names {
		rules = append(rules, registry[name])
	}
	return newLinter(config, rules)
}

// newLinter builds a linter over an explicit rule set, bypassing the global
// registry so the driver can be tested in isolation.
func newLinter(config Config, rules []Rule) (*Linter, error) {
	patterns, err := config.validate()
	if err != nil {
		return nil, err
	}
	disabled := make(map[string]bool, len(config.Disable))
	for _, name := range config.Disable {
		disabled[name] = true
	}
	enabled := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		if !disabled[rule.Name()] {
			enabled = append(enabled, rule)
		}
	}
	return &Linter{
		context:  Context{Config: config, patterns: patterns},
		enabled:  enabled,
		disabled: disabled,
		severity: config.Severity,
	}, nil
}

// Lint runs every enabled rule over every script in the snapshot.
func (l *Linter) Lint(snapshot *project.Snapshot) Report {
	report := Report{Diagnostics: make([]Diagnostic, 0)}

	for _, path := range snapshot.Paths {
		script := snapshot.Scripts[path]
		if script.ParseError != nil {
			if !l.disabled["source-parse"] {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{
					Rule: "source-parse", Severity: l.severityOf("source-parse"),
					Message: script.ParseError.Error(), Path: path, Line: 1, Column: 1,
				})
			}
			continue
		}
		for _, rule := range l.enabled {
			for _, diagnostic := range rule.Check(&l.context, script) {
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
