package lint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/cafecito-games/gdkit/internal/semantic"
	"github.com/cafecito-games/gdkit/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrailingWhitespaceIsReportedAtTheFirstOffendingColumn(t *testing.T) {
	assertRule(t, "trailing-whitespace", "var a := 1   \nvar b := 2\nvar c := 3\t\n", 1, 3)

	found := lintSource(t, "trailing-whitespace", "var a := 1   \n")
	assert.Equal(t, 11, found[0].Column)
	assert.Equal(t, "a.gd", found[0].Path)
}

func TestDiagnosticStringUsesGdlintShape(t *testing.T) {
	diagnostic := Diagnostic{Rule: "function-name", Severity: SeverityError, Message: `Function name "doThing" is not valid`, Path: "player.gd", Line: 12, Column: 6}
	want := `player.gd:12: Error: Function name "doThing" is not valid (function-name)`
	got := diagnostic.String()
	assert.Equal(t, want, got)
}

func TestDisabledRuleProducesNothing(t *testing.T) {
	config := DefaultConfig()
	config.Disable = []string{"trailing-whitespace"}
	report := lintProject(t, config, map[string]string{"a.gd": "var a := 1   \n"})
	for _, diagnostic := range report.Diagnostics {
		require.NotEqual(t, "trailing-whitespace", diagnostic.Rule)
	}
}

func TestUnparseableFileReportsOnlySourceParse(t *testing.T) {
	report := lintProject(t, DefaultConfig(), map[string]string{"a.gd": "func (   \n"})
	require.Len(t, report.Diagnostics, 1)
	assert.Equal(t, "source-parse", report.Diagnostics[0].Rule)
}

func TestReportSortsByPathLineColumnRule(t *testing.T) {
	report := lintProject(t, DefaultConfig(), map[string]string{
		"b.gd": "var x := 1   \n",
		"a.gd": "var y := 1   \nvar z := 2   \n",
	})
	var order []string
	for _, diagnostic := range report.Diagnostics {
		order = append(order, diagnostic.Path)
	}
	require.Len(t, order, 3)
	assert.Equal(t, "a.gd", order[0])
	assert.Equal(t, "a.gd", order[1])
	assert.Equal(t, "b.gd", order[2])
}

func TestNewRejectsAnUncompilableNamePattern(t *testing.T) {
	config := DefaultConfig()
	config.FunctionName = "([a-z"
	_, err := New(config)
	assert.Error(t, err)
}

// fakeRule reports a fixed diagnostic set, deliberately pre-filled with wrong
// stamped fields so tests can prove the driver overwrites them.
type fakeRule struct {
	name        string
	diagnostics []Diagnostic
}

func (r fakeRule) Name() string                                 { return r.name }
func (r fakeRule) Describe() string                             { return "test" }
func (r fakeRule) Check(*Context, *project.Script) []Diagnostic { return r.diagnostics }

// lintWithRules runs rules over one a.gd file through the driver alone. Rule
// names must be real ones because configuration validates against the registry.
func lintWithRules(t *testing.T, config Config, rules ...Rule) Report {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.gd"), []byte("var a := 1\n"), 0o644))
	linter, err := newLinter(config, rules)
	require.NoError(t, err)
	snapshot, err := project.Load(lintLoadConfig(root, config, linter))
	require.NoError(t, err)
	return linter.Lint(snapshot)
}

func TestDriverStampsRulePathAndSeverity(t *testing.T) {
	rule := fakeRule{name: "max-line-length", diagnostics: []Diagnostic{{
		Rule: "wrong", Path: "wrong.gd", Severity: SeverityWarning, Message: "m", Line: 1, Column: 1,
	}}}
	report := lintWithRules(t, DefaultConfig(), rule)
	require.Len(t, report.Diagnostics, 1)
	got := report.Diagnostics[0]
	assert.Equal(t, "max-line-length", got.Rule)
	assert.Equal(t, "a.gd", got.Path)
	assert.Equal(t, SeverityError, got.Severity)
}

func TestSemanticAnalyzerConstructionFollowsEnabledCollectionCapability(t *testing.T) {
	tests := []struct {
		name          string
		config        Config
		rules         []Rule
		wantConstruct int
		wantSchema    bool
	}{
		{
			name:   "inert collection rule",
			config: DefaultConfig(),
			rules:  []Rule{typingRule{rule: ruleRequireTypedCollection}},
		},
		{
			name: "disabled collection rule",
			config: func() Config {
				config := DefaultConfig()
				config.Enable = []string{ruleRequireTypedCollection}
				config.Disable = []string{ruleRequireTypedCollection}
				return config
			}(),
			rules: []Rule{typingRule{rule: ruleRequireTypedCollection}},
		},
		{
			name: "only another typing rule enabled",
			config: func() Config {
				config := DefaultConfig()
				config.Enable = []string{ruleRequireReturnType}
				return config
			}(),
			rules: []Rule{
				typingRule{rule: ruleRequireReturnType},
				typingRule{rule: ruleRequireTypedCollection},
			},
		},
		{
			name: "enabled collection rule",
			config: func() Config {
				config := DefaultConfig()
				config.Enable = []string{ruleRequireTypedCollection}
				return config
			}(),
			rules:         []Rule{typingRule{rule: ruleRequireTypedCollection}},
			wantConstruct: 1,
			wantSchema:    true,
		},
		{
			name: "collection through enable new rules",
			config: func() Config {
				config := DefaultConfig()
				config.EnableNewRules = true
				return config
			}(),
			rules:         []Rule{typingRule{rule: ruleRequireTypedCollection}},
			wantConstruct: 1,
			wantSchema:    true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			linter, err := newLinter(test.config, test.rules)
			require.NoError(t, err)
			original := linter.newSemanticAnalyzer
			constructed := 0
			linter.newSemanticAnalyzer = func(snapshot *project.Snapshot, engine *semantic.Engine) *semantic.Analyzer {
				constructed++
				return original(snapshot, engine)
			}
			snapshot := semanticSnapshotFiles(t, map[string]string{
				"first.gd":  "var first := [1]\n",
				"second.gd": "var second := [2]\n",
			})
			report := linter.Lint(snapshot)
			require.Equal(t, test.wantConstruct, constructed)
			require.Equal(t, test.wantSchema, report.EngineSchema != nil)
		})
	}
}

func TestSemanticAnalyzerIsRunLocalAcrossSnapshots(t *testing.T) {
	config := DefaultConfig()
	config.Enable = []string{ruleRequireTypedCollection}
	linter, err := newLinter(config, []Rule{typingRule{rule: ruleRequireTypedCollection}})
	require.NoError(t, err)
	first := semanticSnapshot(t, "func first() -> void:\n\tvar values := [1]\n")
	second := semanticSnapshot(t, "func second() -> void:\n\tvar values := [\"text\"]\n")
	original := linter.newSemanticAnalyzer
	var seen []*project.Snapshot
	linter.newSemanticAnalyzer = func(snapshot *project.Snapshot, engine *semantic.Engine) *semantic.Analyzer {
		seen = append(seen, snapshot)
		return original(snapshot, engine)
	}

	firstReport := linter.Lint(first)
	secondReport := linter.Lint(second)
	require.Len(t, seen, 2)
	require.Equal(t, first, seen[0])
	require.Equal(t, second, seen[1])
	require.Nil(t, linter.context.analyzer)
	got := firstReport.Diagnostics
	require.Len(t, got, 1)
	require.Equal(t, "Array has no element type; write Array[int]", got[0].Message)
	got = secondReport.Diagnostics
	require.Len(t, got, 1)
	require.Equal(t, "Array has no element type; write Array[String]", got[0].Message)
}

func semanticSnapshot(t *testing.T, source string) *project.Snapshot {
	return semanticSnapshotFiles(t, map[string]string{"a.gd": source})
}

func semanticSnapshotFiles(t *testing.T, files map[string]string) *project.Snapshot {
	t.Helper()
	root := t.TempDir()
	for path, source := range files {
		require.NoError(t, os.WriteFile(filepath.Join(root, path), []byte(source), 0o644))
	}
	snapshot, err := project.Load(project.Config{Root: root, SourceRoots: []string{"."}})
	require.NoError(t, err)
	return snapshot
}

func TestDriverAppliesSeverityOverride(t *testing.T) {
	config := DefaultConfig()
	config.Severity = map[string]Severity{"max-line-length": SeverityWarning}
	rule := fakeRule{name: "max-line-length", diagnostics: []Diagnostic{{Message: "m", Line: 1, Column: 1}}}
	report := lintWithRules(t, config, rule)
	require.Len(t, report.Diagnostics, 1)
	assert.Equal(t, SeverityWarning, report.Diagnostics[0].Severity)
}

func TestDriverSortsSameLineDiagnosticsByRuleName(t *testing.T) {
	diagnostics := []Diagnostic{{Message: "m", Line: 1, Column: 1}}
	report := lintWithRules(t, DefaultConfig(),
		fakeRule{name: "mixed-tabs-and-spaces", diagnostics: diagnostics},
		fakeRule{name: "max-line-length", diagnostics: diagnostics},
	)
	require.Len(t, report.Diagnostics, 2)
	assert.Equal(t, "max-line-length", report.Diagnostics[0].Rule)
	assert.Equal(t, "mixed-tabs-and-spaces", report.Diagnostics[1].Rule)
}

func TestDriverSkipsDisabledRules(t *testing.T) {
	config := DefaultConfig()
	config.Disable = []string{"max-line-length"}
	rule := fakeRule{name: "max-line-length", diagnostics: []Diagnostic{{Message: "m", Line: 1, Column: 1}}}
	report := lintWithRules(t, config, rule)
	assert.Empty(t, report.Diagnostics)
}

func TestEmptyReportMarshalsAsAnEmptyArray(t *testing.T) {
	report := lintWithRules(t, DefaultConfig())
	data, err := json.Marshal(report)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"diagnostics":[]`)
}

func TestRegisterRejectsReservedNames(t *testing.T) {
	for _, name := range []string{"source-parse", "unknown-ignore"} {
		func() {
			defer func() {
				assert.NotNil(t, recover(), "register(%q) must panic", name)
			}()
			register(fakeRule{name: name})
		}()
	}
}

func writeConfigFile(t *testing.T, root, name, contents string) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(absolute), 0o755))
	require.NoError(t, os.WriteFile(absolute, []byte(contents), 0o644))
}

func TestLoadConfigRejectsAnInvalidNamePattern(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, DefaultConfigPath, `{"function-name": "([a-z"}`)
	_, err := LoadConfig(root, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "function-name")
}

func TestValidateRejectsAnEmptyNamePattern(t *testing.T) {
	config := DefaultConfig()
	config.SignalName = ""
	assert.Error(t, config.Validate())
}

func TestValidateRejectsNegativeLimits(t *testing.T) {
	mutations := map[string]func(*Config){
		"max-returns":               func(c *Config) { c.MaxReturns = -1 },
		"max-public-methods":        func(c *Config) { c.MaxPublicMethods = -1 },
		"function-arguments-number": func(c *Config) { c.FunctionArgumentsNumber = -1 },
		"max-file-lines":            func(c *Config) { c.MaxFileLines = -1 },
		"max-line-length":           func(c *Config) { c.MaxLineLength = -1 },
		"tab-characters":            func(c *Config) { c.TabCharacters = -1 },
	}
	for name, mutate := range mutations {
		config := DefaultConfig()
		mutate(&config)
		err := config.Validate()
		require.Error(t, err, name)
		require.Contains(t, err.Error(), name)
	}
}

func TestLoadConfigFallsBackToDefaultsOnlyWhenNoNameIsGiven(t *testing.T) {
	root := t.TempDir()
	config, err := LoadConfig(root, "")
	require.NoError(t, err, "implicit default path must fall back")
	assert.Equal(t, DefaultConfig().MaxLineLength, config.MaxLineLength)
	_, err = LoadConfig(root, DefaultConfigPath)
	assert.Error(t, err)
	_, err = LoadConfig(root, "other.json")
	assert.Error(t, err)
}

func TestLoadConfigRejectsUnknownKeys(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, DefaultConfigPath, `{"max-line-lenght": 80}`)
	_, err := LoadConfig(root, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "max-line-lenght")
}

func TestLoadConfigKeepsTheDefaultGodotVersionWhenTheKeyIsOmitted(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, DefaultConfigPath, `{"max-line-length": 80}`)
	config, err := LoadConfig(root, "")
	require.NoError(t, err)
	assert.Equal(t, "4.7", config.GodotVersion)
}

func TestLoadConfigRejectsAnEmptyGodotVersion(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, DefaultConfigPath, `{"godot_version": ""}`)
	_, err := LoadConfig(root, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "godot_version")
}

func TestLintSourceFailsOnUnparseableFixture(t *testing.T) {
	got := lintSource(t, "source-parse", "func (   \n")
	assert.Len(t, got, 1)
}

func TestSourceParseReportsThePositionOfTheFailure(t *testing.T) {
	cases := map[string]struct {
		source       string
		line, column int
		message      string
	}{
		"parser": {"var a = 1\nvar b = 2\nfunc (:\n", 3, 6, "expected function name"},
		"lexer":  {"var a = 1\nvar s = \"\\x\"\n", 2, 11, `invalid escape "\x" in string`},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			got := lintSource(t, "source-parse", testCase.source)
			require.Len(t, got, 1)
			require.Equal(t, testCase.line, got[0].Line)
			require.Equal(t, testCase.column, got[0].Column)
			require.Equal(t, testCase.message, got[0].Message)
		})
	}
}

func TestNeedsSemanticAnalysisFollowsTheResolvedEnabledRules(t *testing.T) {
	collection := []Rule{typingRule{rule: ruleRequireTypedCollection}}
	tests := []struct {
		name   string
		config Config
		rules  []Rule
		want   bool
	}{
		{name: "default config", config: DefaultConfig(), rules: collection},
		{
			name: "disable wins over enable",
			config: func() Config {
				config := DefaultConfig()
				config.Enable = []string{ruleRequireTypedCollection}
				config.Disable = []string{ruleRequireTypedCollection}
				return config
			}(),
			rules: collection,
		},
		{
			name: "another typing rule only",
			config: func() Config {
				config := DefaultConfig()
				config.Enable = []string{ruleRequireReturnType}
				return config
			}(),
			rules: []Rule{typingRule{rule: ruleRequireReturnType}},
		},
		{
			name: "enabled collection rule",
			config: func() Config {
				config := DefaultConfig()
				config.Enable = []string{ruleRequireTypedCollection}
				return config
			}(),
			rules: collection,
			want:  true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			linter, err := newLinter(test.config, test.rules)
			require.NoError(t, err)
			got := linter.NeedsSemanticAnalysis()
			require.Equal(t, test.want, got)
			// The capability the load boundary reads and the one Lint acts on
			// must be the same decision, not two caches that can drift.
			original := linter.newSemanticAnalyzer
			constructed := 0
			linter.newSemanticAnalyzer = func(snapshot *project.Snapshot, engine *semantic.Engine) *semantic.Analyzer {
				constructed++
				return original(snapshot, engine)
			}
			linter.Lint(semanticSnapshot(t, "var values := [1]\n"))
			want := 0
			if test.want {
				want = 1
			}
			require.Equal(t, want, constructed)
		})
	}
}

func TestLintReportsOnlyOnSelectedScripts(t *testing.T) {
	snapshot, report := lintTree(t, collectionConfig(),
		[]Rule{typingRule{rule: ruleRequireTypedCollection}},
		map[string]string{
			"a.gd":                "var items := [1]\n# gdkit:ignore = not-a-rule\n",
			"generated/gen.gd":    "var items := [1]\n# gdkit:ignore = not-a-rule\n",
			"generated/broken.gd": "func (((\n",
		})

	// The excluded scripts are dependencies: walked, parsed, and indexed.
	for _, path := range []string{"generated/gen.gd", "generated/broken.gd"} {
		require.NotNil(t, snapshot.Scripts[path])
	}
	require.Equal(t, []string{"a.gd"}, snapshot.Selected)
	for _, diagnostic := range report.Diagnostics {
		assert.Equal(t, "a.gd", diagnostic.Path)
	}
	// Only the selected file's own rule finding and unknown-ignore survive,
	// which pins that the loop narrowed without going silent.
	var rules []string
	for _, diagnostic := range report.Diagnostics {
		rules = append(rules, diagnostic.Rule)
	}
	sort.Strings(rules)
	require.Equal(t, []string{ruleRequireTypedCollection, "unknown-ignore"}, rules)
}

// protoDependency declares the class, named enum, method signature, and return
// type a selected consumer resolves through the complete project universe.
const protoDependency = "class_name Proto\n" +
	"enum Kind { ONE }\n" +
	"static func make() -> Proto:\n\treturn null\n" +
	"func label(value: int) -> String:\n\treturn \"x\"\n"

// protoConsumer is selected and resolves every one of those facts. Each local
// is reported only when the declaration is visible: Array[String] needs
// label's signature and return type, and the three Array[T] forms need the
// class and the named enum to resolve to a known-but-unwritable type. A fact
// the analyzer cannot reach is a reasoned Unknown, which degrades to the
// generic Array[T] wording rather than an exact spelling, so an unreachable
// dependency still reports the finding but cannot propose a precise type.
const protoConsumer = "func run() -> void:\n" +
	"\tvar classes := [Proto.new()]\n" +
	"\tvar enums := [Proto.Kind.ONE]\n" +
	"\tvar returns := [Proto.make()]\n" +
	"\tvar calls := [Proto.new().label(1)]\n"

func TestSelectedScriptResolvesDeclarationsOnlyInAnExcludedDependency(t *testing.T) {
	_, report := lintTree(t, collectionConfig(),
		[]Rule{typingRule{rule: ruleRequireTypedCollection}},
		map[string]string{"generated/proto.gd": protoDependency, "a.gd": protoConsumer})
	var got []string
	for _, diagnostic := range report.Diagnostics {
		require.Equal(t, "a.gd", diagnostic.Path)
		got = append(got, fmt.Sprintf("%d: %s", diagnostic.Line, diagnostic.Message))
	}
	want := []string{
		"2: Array has no element type; write Array[T]",
		"3: Array has no element type; write Array[T]",
		"4: Array has no element type; write Array[T]",
		"5: Array has no element type; write Array[String]",
	}
	require.Equal(t, want, got)
}

// TestUnreachableDependencyFallsBackToGenericWording is the control for the
// test above: when the declaration is not in the universe every element type is
// an unreachable Unknown, so each finding degrades to the generic wording
// instead of proposing an exact spelling. The fourth local is the discriminator:
// it reports Array[T] here and Array[String] when the dependency resolves.
func TestUnreachableDependencyFallsBackToGenericWording(t *testing.T) {
	_, report := lintTree(t, collectionConfig(),
		[]Rule{typingRule{rule: ruleRequireTypedCollection}},
		map[string]string{"a.gd": protoConsumer})
	assertGenericCollectionFallback(t, report)
}

func TestDuplicateDeclarationAcrossSelectionStaysAmbiguous(t *testing.T) {
	_, report := lintTree(t, collectionConfig(),
		[]Rule{typingRule{rule: ruleRequireTypedCollection}},
		map[string]string{
			"generated/proto.gd": protoDependency,
			"other.gd":           "class_name Proto\n",
			"a.gd":               protoConsumer,
		})
	// Both claimants are preserved, so the class stays ambiguous and every
	// dependent answer is Unknown; the rule degrades to generic wording rather
	// than guessing. Picking by selection or by walk order would resolve Proto
	// to the generated declaration and report Array[String] on the last line.
	assertGenericCollectionFallback(t, report)
}

func TestParseFailedExcludedDependencyStaysSilentEvidence(t *testing.T) {
	snapshot, report := lintTree(t, collectionConfig(),
		[]Rule{typingRule{rule: ruleRequireTypedCollection}},
		map[string]string{
			"generated/proto.gd": protoDependency + "func (((\n",
			"a.gd":               protoConsumer,
		})
	script := snapshot.Scripts["generated/proto.gd"]
	require.NotNil(t, script)
	require.NotNil(t, script.ParseError)
	// The failure is retained as evidence, so the consumer's answers are a
	// reasoned Unknown and the rule falls back to generic wording. Nothing is
	// reported for the unselected path.
	assertGenericCollectionFallback(t, report)
}

// assertGenericCollectionFallback pins the four populated literals in
// protoConsumer to the generic Array[T] wording: every element type resolves to
// an unreachable Unknown, so no exact spelling can be proposed.
func assertGenericCollectionFallback(t *testing.T, report Report) {
	t.Helper()
	const generic = "Array has no element type; write Array[T]"
	require.Len(t, report.Diagnostics, 4)
	for index, diagnostic := range report.Diagnostics {
		require.Equal(t, "a.gd", diagnostic.Path)
		assert.Equal(t, index+2, diagnostic.Line)
		assert.Equal(t, generic, diagnostic.Message)
	}
}
