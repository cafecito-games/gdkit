package lint

import (
	"regexp"
	"sort"

	"github.com/cafecito-games/gdkit/internal/semantic"
	"github.com/cafecito-games/gdkit/internal/semantic/engineschema"
	"github.com/cafecito-games/gdkit/internal/versiongate"
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

// PendingRule is a rule that ships inert: it does not run until a project opts
// in by name through Config.Enable or wholesale through Config.EnableNewRules.
//
// A release adds a rule as pending so that upgrading gdkit cannot change an
// existing project's verdict on unchanged configuration. Widening what an
// existing rule reports is the same event from a project's perspective, so it
// arrives the same way: as a new pending rule name rather than as a quiet
// change to the rule already running.
type PendingRule interface {
	Rule
	// PendingSince names the gdkit release that introduced the rule. It is
	// documentation for the lifecycle policy, not a version comparison.
	PendingSince() string
}

// EngineSchemaRule marks a rule that needs immutable native engine facts.
// Only enabled rules participate in this capability check, so adding a future
// semantic rule cannot make an existing configuration load a schema while the
// rule still ships inert or is disabled.
type EngineSchemaRule interface {
	Rule
	RequiresEngineSchema()
}

// Context gives a rule the project and its resolved configuration.
type Context struct {
	Config Config

	compiled *compiledConfig
	engine   *semantic.Engine
}

// Engine returns the selected immutable engine schema, or nil for a run whose
// enabled rules do not require one and which has no explicit override.
func (c *Context) Engine() *semantic.Engine {
	if c == nil {
		return nil
	}
	return c.engine
}

// Pattern returns the compiled, anchored pattern for a name rule.
func (c *Context) Pattern(rule string) *regexp.Regexp {
	if c.compiled == nil {
		return nil
	}
	return c.compiled.patterns[rule]
}

// supports reports whether the project's configured Godot version is at least
// floor. A typing site names the version that first accepts the annotation it
// wants, so a site is dropped rather than reported when the engine is older.
func (c *Context) supports(floor versiongate.Version) bool {
	if c.compiled == nil {
		return false
	}
	return !c.compiled.godotVersion.Less(floor)
}

// exempt reports whether name matches one of the rule's exempt patterns. A site
// with no such name — a class-scope declaration, which has no enclosing
// function — is never exempt, and is suppressed with a comment instead.
func (c *Context) exempt(rule, name string) bool {
	if c.compiled == nil || name == "" {
		return false
	}
	for _, pattern := range c.compiled.exempt[rule] {
		if matched, _ := pattern.Match(name); matched {
			return true
		}
	}
	return false
}

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

// IsRule reports whether name is a known rule. "source-parse" and
// "unknown-ignore" are reported by the driver rather than by a registered rule,
// but are nameable in config and in suppression comments.
func IsRule(name string) bool {
	if name == "source-parse" || name == "unknown-ignore" {
		return true
	}
	_, ok := registry[name]
	return ok
}

// RuleNames lists every rule, sorted.
func RuleNames() []string {
	names := make([]string, 0, len(registry)+2)
	names = append(names, "source-parse", "unknown-ignore")
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// IsPendingRule reports whether a registered rule ships inert. The driver's own
// "source-parse" and "unknown-ignore" are never pending: they report a file the
// linter could not read as configured, which no project opts in to.
func IsPendingRule(name string) bool {
	rule, ok := registry[name]
	if !ok {
		return false
	}
	_, pending := rule.(PendingRule)
	return pending
}

// PendingRuleNames lists every rule that ships inert, sorted.
func PendingRuleNames() []string {
	names := make([]string, 0, len(registry))
	for name, rule := range registry {
		if _, pending := rule.(PendingRule); pending {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Linter runs the enabled rules over a snapshot.
type Linter struct {
	context      Context
	enabled      []Rule
	disabled     map[string]bool
	severity     map[string]Severity
	engineSchema *engineschema.Provenance
}

// New validates the configuration and compiles every name pattern, so a bad
// pattern is a configuration error rather than a silently dead rule.
func New(config Config) (*Linter, error) {
	return newLinter(config, registeredRules())
}

// NewForProject prepares a linter with project-root-aware external inputs.
// The command path uses this constructor so an explicit extension_api is
// resolved and read once before any rule executes.
func NewForProject(root string, config Config) (*Linter, error) {
	return newLinterForProject(root, config, registeredRules())
}

func registeredRules() []Rule {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	rules := make([]Rule, 0, len(names))
	for _, name := range names {
		rules = append(rules, registry[name])
	}
	return rules
}

// newLinter builds a linter over an explicit rule set, bypassing the global
// registry so the driver can be tested in isolation.
func newLinter(config Config, rules []Rule) (*Linter, error) {
	return buildLinter("", false, config, rules)
}

// newLinterForProject is the explicit-rule equivalent of NewForProject. It
// keeps capability and external-input behavior directly testable without
// mutating the global rule registry.
func newLinterForProject(root string, config Config, rules []Rule) (*Linter, error) {
	return buildLinter(root, true, config, rules)
}

func buildLinter(root string, projectAware bool, config Config, rules []Rule) (*Linter, error) {
	compiled, err := config.validate()
	if err != nil {
		return nil, err
	}
	disabled := make(map[string]bool, len(config.Disable))
	for _, name := range config.Disable {
		disabled[name] = true
	}
	// Disable wins over Enable. An explicit "off" is the stronger statement,
	// and a project that lists a rule in both is most likely turning off
	// something it opted in to earlier.
	opted := make(map[string]bool, len(config.Enable))
	for _, name := range config.Enable {
		opted[name] = true
	}
	enabled := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		name := rule.Name()
		if disabled[name] {
			continue
		}
		if _, pending := rule.(PendingRule); pending && !config.EnableNewRules && !opted[name] {
			continue
		}
		enabled = append(enabled, rule)
	}
	selected, err := prepareEngineSchema(root, projectAware, config, compiled, enabled)
	if err != nil {
		return nil, err
	}
	linter := &Linter{
		context:  Context{Config: config, compiled: compiled},
		enabled:  enabled,
		disabled: disabled,
		severity: config.Severity,
	}
	if selected != nil {
		linter.context.engine = selected.Engine
		provenance := selected.Provenance
		linter.engineSchema = &provenance
	}
	return linter, nil
}

// Lint runs every enabled rule over every script in the snapshot.
func (l *Linter) Lint(snapshot *project.Snapshot) Report {
	report := Report{Diagnostics: make([]Diagnostic, 0)}
	if l.engineSchema != nil {
		provenance := *l.engineSchema
		report.EngineSchema = &provenance
	}

	for _, path := range snapshot.Paths {
		script := snapshot.Scripts[path]
		if script.ParseError != nil {
			if !l.disabled["source-parse"] {
				line, column, message := script.ParseFailure()
				report.Diagnostics = append(report.Diagnostics, Diagnostic{
					Rule: "source-parse", Severity: l.severityOf("source-parse"),
					Message: message, Path: path, Line: line, Column: column,
				})
			}
			continue
		}
		var found []Diagnostic
		for _, rule := range l.enabled {
			for _, diagnostic := range rule.Check(&l.context, script) {
				diagnostic.Rule = rule.Name()
				found = append(found, diagnostic)
			}
		}
		suppressions := parseSuppressions(script)
		if !l.disabled["unknown-ignore"] {
			for _, diagnostic := range suppressions.unknownNames() {
				diagnostic.Rule = "unknown-ignore"
				found = append(found, diagnostic)
			}
		}
		for _, diagnostic := range found {
			if suppressions.silences(diagnostic.Rule, diagnostic.Line) {
				continue
			}
			diagnostic.Path = script.Path
			diagnostic.Severity = l.severityOf(diagnostic.Rule)
			report.Diagnostics = append(report.Diagnostics, diagnostic)
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
