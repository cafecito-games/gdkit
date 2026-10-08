package lint

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cafecito-games/gdkit/internal/semantic"
	"github.com/cafecito-games/gdkit/internal/semantic/engineschema"
	"github.com/cafecito-games/gdkit/internal/semanticsource"
	"github.com/cafecito-games/gdkit/internal/versiongate"
	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"
)

const (
	corpusRevision                 = "e74c06f54e1bdbe79d40641193fb89fcc0c207e2"
	corpusBaselineGDKit            = "2711952538c158df14086c43e872117789c6a70b"
	corpusPreChangeCollectionCount = 861
	corpusIssue80BaseGDKit         = "bab1b00fdba422ecde2aacd1eb0d65336ac988b5"
	corpusIssue80BaseCount         = 983
	corpusIssue80BaseDigest        = "d55e163439108a1d280cfaf71291e2c767a8aa98279c87c2fd33c9c1c77c937b"
	corpusPayloadPath              = "features/auth/auth_client.gd"
	corpusPayloadLine              = 122
	// corpusPreChangeSuppliedDigest was supplied with the issue, but no
	// serializer accompanied it. The receipt records it alongside the pinned
	// canonical digest below rather than claiming an unverifiable match.
	corpusPreChangeSuppliedDigest = "efbcc3c71a48e69428d8e1fdd434ead7a5e10acd4733c5e80fcc784e13165ae3"
	corpusCanonicalTupleFormat    = "compact-json:path,line,column,end_line,end_column,rule,severity,message; sorted by those fields"
	corpusSceneNodeCategory       = "scene-node access (deferred to #53)"
	corpusUnknownReasonLimit      = 10
	corpusTimingSamples           = 3
	// The pinned corpus excludes its one generated protocol declaration from
	// lint findings while a selected codec constructs that class. It is the
	// concrete case this universe/selection split exists for.
	corpusExcludedDependency = "protocol/UzirNetcodeTransportV1EnvelopeJoinWorldRequest.pb.gd"
	corpusExcludedClassName  = "UzirNetcodeTransportV1EnvelopeJoinWorldRequest"
	corpusDependencyConsumer = "features/game/net/codec/envelope_codec.gd"
)

// corpusUnselectedRoots are the dependency roots the pinned corpus excludes
// from lint actions. No diagnostic may name one, however broadly the universe
// is walked.
var corpusUnselectedRoots = []string{"protocol/", "addons/", "scripts/", "tools/", "script_templates/"}

// corpusConfig is the default policy with the tool-managed directories that
// hold duplicate checkouts of a project excluded, so a corpus rooted at a
// working directory is not inflated by agent worktrees.
func corpusConfig() Config {
	config := DefaultConfig()
	config.Exclude = append(config.Exclude, ".worktrees/**", ".claude/**")
	return config
}

// loadCorpus loads the project rooted at root with config.
func loadCorpus(t *testing.T, root string, config Config) *project.Snapshot {
	t.Helper()
	snapshot, err := project.Load(project.Config{Root: root, SourceRoots: config.SourceRoots, Exclude: config.Exclude})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// TestCorpus lints a real project given by GDKIT_CORPUS. It asserts the linter
// does not panic and that two runs over one snapshot produce byte-identical
// reports. It is skipped when the variable is unset so CI stays hermetic.
func TestCorpus(t *testing.T) {
	root := os.Getenv("GDKIT_CORPUS")
	if root == "" {
		t.Skip("GDKIT_CORPUS is not set")
	}
	config := corpusConfig()
	snapshot := loadCorpus(t, root, config)
	linter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}

	report := linter.Lint(snapshot)
	if string(marshalReport(t, report)) != string(marshalReport(t, linter.Lint(snapshot))) {
		t.Fatal("two runs over the same snapshot produced different reports")
	}
	t.Logf("linted %d files, %d diagnostics", len(snapshot.Paths), len(report.Diagnostics))
}

func marshalReport(t *testing.T, report Report) []byte {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestCorpusSemanticReceipt mirrors the actual lint CLI's project loading and
// rule opt-in. It is intentionally opt-in: Uzir is evidence for #53's owner,
// not a CI fixture or a source of semantic policy, and this test writes no
// corpus files, configuration, or receipt artifact.
func TestCorpusSemanticReceipt(t *testing.T) {
	root := os.Getenv("GDKIT_CORPUS")
	if root == "" {
		t.Skip("GDKIT_CORPUS is not set")
	}
	if revision := gitAt(t, root, "rev-parse", "HEAD"); revision != corpusRevision {
		t.Fatalf("corpus revision = %s, want %s", revision, corpusRevision)
	}
	if status := gitAt(t, root, "status", "--porcelain=v1"); status != "" {
		t.Fatalf("corpus is not clean: %s", status)
	}

	configPath := filepath.Join(root, filepath.FromSlash(DefaultConfigPath))
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	ignorePresent, ignoreDigest := corpusOptionalDigest(t, filepath.Join(root, ".gdkitignore"))
	config, err := LoadConfig(root, "")
	if err != nil {
		t.Fatal(err)
	}
	enabledConfig := corpusConfigWithEnabledRule(config, ruleRequireTypedCollection)
	// The enabled collection rule needs semantic analysis, so this mirrors the
	// CLI's broad load: the whole project is the dependency universe and the
	// lint filters narrow only Selected. The two mirrors below run no semantic
	// analysis, so their pinned counts and digest are the observable proof
	// that Selected reproduces the filtered walk's Paths.
	snapshot, err := project.Load(project.Config{
		Root: root,
		Selection: &project.Selection{
			SourceRoots:     enabledConfig.SourceRoots,
			Exclude:         enabledConfig.Exclude,
			HonorIgnoreFile: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := project.Load(project.Config{
		Root:            root,
		SourceRoots:     enabledConfig.SourceRoots,
		Exclude:         enabledConfig.Exclude,
		HonorIgnoreFile: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(snapshot.Selected, filtered.Paths) {
		t.Fatalf("broad Selected (%d) does not reproduce the filtered walk (%d)",
			len(snapshot.Selected), len(filtered.Paths))
	}
	if len(snapshot.Paths) <= len(snapshot.Selected) {
		t.Fatalf("broad Paths = %d, want more than Selected = %d", len(snapshot.Paths), len(snapshot.Selected))
	}
	if snapshot.Scripts[corpusExcludedDependency] == nil {
		t.Fatalf("universe is missing the excluded dependency %s", corpusExcludedDependency)
	}
	if slices.Contains(snapshot.Selected, corpusExcludedDependency) {
		t.Fatalf("%s is excluded from lint actions but appears in Selected", corpusExcludedDependency)
	}
	if !slices.Contains(snapshot.Selected, corpusDependencyConsumer) {
		t.Fatalf("Selected is missing the consumer %s", corpusDependencyConsumer)
	}
	declared := semantic.BuildIndex(semanticsource.NewSnapshot(snapshot)).ClassByName(corpusExcludedClassName)
	if declared == nil || declared.Path != corpusExcludedDependency {
		t.Fatalf("ClassByName(%q) = %+v, want the excluded declaration", corpusExcludedClassName, declared)
	}

	legacy, err := newLinterForProject(root, enabledConfig, corpusPreChangeRules())
	if err != nil {
		t.Fatal(err)
	}
	before := legacy.Lint(snapshot)
	if before.EngineSchema != nil {
		t.Fatalf("pre-change mirror unexpectedly selected engine provenance: %+v", before.EngineSchema)
	}
	beforeCollection := corpusCollectionDiagnostics(before)
	if len(before.Diagnostics) != len(beforeCollection) {
		t.Fatalf("pre-change mirror diagnostics include non-collection rules: %+v", before.Diagnostics)
	}
	if len(beforeCollection) != corpusPreChangeCollectionCount {
		t.Fatalf("pre-change collection diagnostics = %d, want %d", len(beforeCollection), corpusPreChangeCollectionCount)
	}

	baseLinter, err := newLinterForProject(root, enabledConfig, corpusIssue80BaseRules())
	if err != nil {
		t.Fatal(err)
	}
	baseReport := baseLinter.Lint(snapshot)
	baseCollection := corpusCollectionDiagnostics(baseReport)
	baseTuples := corpusDiagnosticTuples(baseCollection)
	if len(baseTuples) != corpusIssue80BaseCount {
		t.Fatalf("issue #80 base collection diagnostics = %d, want %d", len(baseTuples), corpusIssue80BaseCount)
	}
	if digest := corpusDiagnosticDigest(baseTuples); digest != corpusIssue80BaseDigest {
		t.Fatalf("issue #80 base collection digest = %s, want %s", digest, corpusIssue80BaseDigest)
	}

	enabled, err := NewForProject(root, enabledConfig)
	if err != nil {
		t.Fatal(err)
	}
	if enabled.context.Engine() == nil {
		t.Fatal("enabled collection run selected no engine")
	}
	var lintAnalyzerConstructions int
	originalFactory := enabled.newSemanticAnalyzer
	enabled.newSemanticAnalyzer = func(snapshot *project.Snapshot, engine *semantic.Engine) *semantic.Analyzer {
		lintAnalyzerConstructions++
		return originalFactory(snapshot, engine)
	}
	after, enabledMedian := timedCorpusLint(t, enabled, snapshot)
	if lintAnalyzerConstructions != corpusTimingSamples {
		t.Fatalf("enabled lint analyzer constructions = %d, want %d", lintAnalyzerConstructions, corpusTimingSamples)
	}
	if after.EngineSchema == nil {
		t.Fatal("enabled collection report omitted engine provenance")
	}
	afterCollection := corpusCollectionDiagnostics(after)
	if len(after.Diagnostics) != len(afterCollection) {
		t.Fatalf("enabled collection run changed a non-collection diagnostic: %+v", after.Diagnostics)
	}

	disabledConfig := corpusConfigWithDisabledRule(config, ruleRequireTypedCollection)
	disabled, err := NewForProject(root, disabledConfig)
	if err != nil {
		t.Fatal(err)
	}
	var disabledAnalyzerConstructions int
	disabledFactory := disabled.newSemanticAnalyzer
	disabled.newSemanticAnalyzer = func(snapshot *project.Snapshot, engine *semantic.Engine) *semantic.Analyzer {
		disabledAnalyzerConstructions++
		return disabledFactory(snapshot, engine)
	}
	_, disabledMedian := timedCorpusLint(t, disabled, snapshot)
	if disabledAnalyzerConstructions != 0 {
		t.Fatalf("disabled lint analyzer constructions = %d, want 0", disabledAnalyzerConstructions)
	}

	firstReceipt := corpusAnalyzerReceipt(t, snapshot, enabled.context.Engine())
	secondReceipt := corpusAnalyzerReceipt(t, snapshot, enabled.context.Engine())
	firstReceiptJSON, err := json.Marshal(firstReceipt)
	if err != nil {
		t.Fatal(err)
	}
	secondReceiptJSON, err := json.Marshal(secondReceipt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstReceiptJSON, secondReceiptJSON) {
		t.Fatalf("two fresh analyzers produced different receipts:\nfirst=%s\nsecond=%s", firstReceiptJSON, secondReceiptJSON)
	}

	for name, report := range map[string]Report{"pre-change": before, "issue80 base": baseReport, "enabled": after} {
		for _, diagnostic := range report.Diagnostics {
			for _, prefix := range corpusUnselectedRoots {
				if strings.HasPrefix(diagnostic.Path, prefix) {
					t.Fatalf("%s mirror reported on unselected dependency: %+v", name, diagnostic)
				}
			}
		}
	}

	beforeTuples := corpusDiagnosticTuples(beforeCollection)
	afterTuples := corpusDiagnosticTuples(afterCollection)
	added, removed, changed := corpusDiagnosticChanges(baseTuples, afterTuples)
	baseExact, baseGeneric := corpusCollectionPrecision(baseTuples)
	afterExact, afterGeneric := corpusCollectionPrecision(afterTuples)
	payload := corpusRequireTuple(t, afterTuples, corpusPayloadPath, corpusPayloadLine)
	if payload.Message == "Dictionary has no element type; write Dictionary[String, String]" {
		t.Fatalf("payload tuple retained the unsound initializer-only suggestion: %+v", payload)
	}
	gdkitRoot := gitAt(t, "", "rev-parse", "--show-toplevel")
	receipt := corpusSemanticReceipt{
		CorpusPath:                         root,
		CorpusRevision:                     corpusRevision,
		CorpusClean:                        true,
		GDKitBaseline:                      corpusBaselineGDKit,
		GDKitHead:                          gitAt(t, gdkitRoot, "rev-parse", "HEAD"),
		LintConfigPath:                     filepath.ToSlash(configPath),
		LintConfigSHA256:                   corpusDigest(configBytes),
		IgnoreFilePresent:                  ignorePresent,
		IgnoreFileSHA256:                   ignoreDigest,
		HonorIgnoreFile:                    true,
		SourceRoots:                        append([]string(nil), enabledConfig.SourceRoots...),
		Exclude:                            append([]string(nil), enabledConfig.Exclude...),
		EngineSchema:                       after.EngineSchema,
		Files:                              len(snapshot.Paths),
		SelectedFiles:                      len(snapshot.Selected),
		Expressions:                        firstReceipt.Expressions,
		Resolved:                           firstReceipt.Resolved,
		Variant:                            firstReceipt.Variant,
		Unknown:                            firstReceipt.Unknown,
		UnknownCategoryCount:               firstReceipt.UnknownCategoryCount,
		TopUnknownReasons:                  firstReceipt.TopUnknownReasons,
		SceneNodeCount:                     firstReceipt.SceneNodeCount,
		SceneNodeRank:                      firstReceipt.SceneNodeRank,
		SceneNodeShareNumerator:            firstReceipt.SceneNodeCount,
		SceneNodeShareDenominator:          firstReceipt.Unknown,
		GateRecommendation:                 firstReceipt.GateRecommendation,
		ReceiptAnalyzerConstructions:       2,
		EnabledLintAnalyzerConstructions:   lintAnalyzerConstructions,
		DisabledLintAnalyzerConstructions:  disabledAnalyzerConstructions,
		BeforeCollectionDiagnostics:        len(beforeTuples),
		BeforeCollectionDigestFormat:       corpusCanonicalTupleFormat,
		BeforeCollectionSuppliedDigest:     corpusPreChangeSuppliedDigest,
		BeforeCollectionSuppliedReproduced: false,
		BeforeCollectionObservedDigest:     corpusDiagnosticDigest(beforeTuples),
		Issue80BaseGDKit:                   corpusIssue80BaseGDKit,
		Issue80BaseCollectionDiagnostics:   len(baseTuples),
		Issue80BaseCollectionDigest:        corpusDiagnosticDigest(baseTuples),
		Issue80BaseExactDiagnostics:        baseExact,
		Issue80BaseGenericDiagnostics:      baseGeneric,
		AfterCollectionDiagnostics:         len(afterTuples),
		AfterCollectionDigest:              corpusDiagnosticDigest(afterTuples),
		AfterCollectionExactDiagnostics:    afterExact,
		AfterCollectionGenericDiagnostics:  afterGeneric,
		AddedCollectionDiagnostics:         len(added),
		RemovedCollectionDiagnostics:       len(removed),
		ChangedCollectionDiagnostics:       len(changed),
		AddedCollectionDigest:              corpusDiagnosticDigest(added),
		RemovedCollectionDigest:            corpusDiagnosticDigest(removed),
		ChangedCollectionDigest:            corpusDiagnosticChangeDigest(changed),
		AddedCollectionTuples:              added,
		RemovedCollectionTuples:            removed,
		ChangedCollectionTuples:            changed,
		PayloadTuple:                       payload,
		DisabledLintMedian:                 disabledMedian.String(),
		EnabledLintMedian:                  enabledMedian.String(),
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("semantic lint corpus receipt: %s", data)
	t.Logf("#53_gate_recommendation = %s", receipt.GateRecommendation)
}

type corpusPreChangeCollectionRule struct{}

func (corpusPreChangeCollectionRule) Name() string         { return ruleRequireTypedCollection }
func (corpusPreChangeCollectionRule) PendingSince() string { return "0.5.0" }
func (corpusPreChangeCollectionRule) Check(context *Context, script *project.Script) []Diagnostic {
	legacy := *context
	legacy.analyzer = nil
	return (typingRule{rule: ruleRequireTypedCollection}).Check(&legacy, script)
}

func corpusPreChangeRules() []Rule {
	rules := registeredRules()
	for index, rule := range rules {
		if rule.Name() == ruleRequireTypedCollection {
			rules[index] = corpusPreChangeCollectionRule{}
			return rules
		}
	}
	panic("require-typed-collection is not registered")
}

// corpusIssue80BaseCollectionRule is the immutable #80-base mirror: it renders
// a populated literal from TypeOf(initializer) and deliberately does not inspect
// later writes. Its collector and renderer are test-local snapshots so a future
// production typing-rule refactor cannot silently change the base receipt.
type corpusIssue80BaseCollectionRule struct{}

func (corpusIssue80BaseCollectionRule) Name() string                { return ruleRequireTypedCollection }
func (corpusIssue80BaseCollectionRule) PendingSince() string        { return "0.5.0" }
func (corpusIssue80BaseCollectionRule) NeedsSemanticAnalysis() bool { return true }
func (corpusIssue80BaseCollectionRule) Check(context *Context, script *project.Script) []Diagnostic {
	if context == nil || context.analyzer == nil || context.Engine() == nil || script == nil {
		return nil
	}
	var found []Diagnostic
	for _, site := range corpusIssue80BaseCollectCollectionSites(script) {
		if !context.supports(site.floor) || context.exempt(ruleRequireTypedCollection, site.enclosing) {
			continue
		}
		message := site.message
		if site.literal != nil {
			var reported bool
			message, reported = corpusIssue80BaseCollectionTypeMessage(context.Engine(), context.analyzer.TypeOf(site.literal))
			if !reported {
				continue
			}
		}
		start, end := site.span.Start, site.span.End
		found = append(found, Diagnostic{
			Message: message, Line: start.Line, Column: runeColumn(script, start),
			EndLine: end.Line, EndColumn: runeColumn(script, end),
		})
	}
	return found
}

type corpusIssue80BaseCollectionSite struct {
	message   string
	enclosing string
	floor     versiongate.Version
	span      token.Span
	literal   ast.Expression
}

type corpusIssue80BaseCollectionCollector struct {
	found []corpusIssue80BaseCollectionSite
}

func corpusIssue80BaseCollectCollectionSites(script *project.Script) []corpusIssue80BaseCollectionSite {
	if script == nil || script.File == nil {
		return nil
	}
	collector := &corpusIssue80BaseCollectionCollector{}
	collector.classBody(script.File.Statements)
	return collector.found
}

func (c *corpusIssue80BaseCollectionCollector) add(site corpusIssue80BaseCollectionSite) {
	c.found = append(c.found, site)
}

func (c *corpusIssue80BaseCollectionCollector) classBody(statements []ast.Statement) {
	for _, statement := range statements {
		switch declaration := statement.(type) {
		case *ast.ClassDeclaration:
			c.classBody(declaration.Body)
		case *ast.FunctionDeclaration:
			c.function(declaration)
		case *ast.VariableDeclaration:
			c.classVariable(declaration)
		case *ast.SignalDeclaration:
			c.signal(declaration)
		case *ast.EnumDeclaration:
			for _, member := range declaration.Members {
				c.inspect("", member.Value)
			}
		}
	}
}

func (c *corpusIssue80BaseCollectionCollector) classVariable(declaration *ast.VariableDeclaration) {
	c.variable(declaration, "")
	c.inspect("", declaration.Value)
	c.functionScope(declaration.Name, declaration.Getter)
	if declaration.Setter != nil {
		c.functionScope(declaration.Name, declaration.Setter.Body)
	}
}

func (c *corpusIssue80BaseCollectionCollector) signal(declaration *ast.SignalDeclaration) {
	c.parameters(declaration.Parameters, declaration.Name)
}

func (c *corpusIssue80BaseCollectionCollector) function(declaration *ast.FunctionDeclaration) {
	c.collection(declaration.ReturnType, declaration.ReturnTypeSpan, declaration.Name)
	c.parameters(declaration.Parameters, declaration.Name)
	c.functionScope(declaration.Name, declaration.Body)
}

func (c *corpusIssue80BaseCollectionCollector) parameters(parameters []ast.Parameter, enclosing string) {
	for _, parameter := range parameters {
		c.inspect(enclosing, parameter.Default)
		c.collection(parameter.Type, parameter.TypeSpan, enclosing)
	}
}

func (c *corpusIssue80BaseCollectionCollector) variable(declaration *ast.VariableDeclaration, enclosing string) {
	c.collection(declaration.Type, declaration.TypeSpan, enclosing)
	if declaration.Type == "" {
		c.collectionLiteral(declaration.Value, enclosing)
	}
}

func (c *corpusIssue80BaseCollectionCollector) collectionLiteral(value ast.Expression, enclosing string) {
	switch literal := value.(type) {
	case *ast.ArrayLiteral:
		if len(literal.Elements) == 0 {
			c.collection("Array", literal.Span(), enclosing)
			return
		}
		c.populatedCollection("Array", literal, literal.Span(), enclosing)
	case *ast.DictionaryLiteral:
		if len(literal.Entries) == 0 {
			c.collection("Dictionary", literal.Span(), enclosing)
			return
		}
		c.populatedCollection("Dictionary", literal, literal.Span(), enclosing)
	}
}

func (c *corpusIssue80BaseCollectionCollector) populatedCollection(typeName string, literal ast.Expression, span token.Span, enclosing string) {
	floor, _, ok := corpusIssue80BaseCollectionSuggestion(typeName)
	if !ok {
		return
	}
	c.add(corpusIssue80BaseCollectionSite{floor: floor, literal: literal, span: span, enclosing: enclosing})
}

func (c *corpusIssue80BaseCollectionCollector) collection(typeName string, span token.Span, enclosing string) {
	floor, form, ok := corpusIssue80BaseCollectionSuggestion(typeName)
	if !ok {
		return
	}
	c.add(corpusIssue80BaseCollectionSite{
		message:   fmt.Sprintf("%s has no element type; write %s", typeName, form),
		enclosing: enclosing,
		floor:     floor,
		span:      span,
	})
}

func (c *corpusIssue80BaseCollectionCollector) functionScope(enclosing string, statements []ast.Statement) {
	for _, statement := range statements {
		c.inspect(enclosing, statement)
	}
}

func (c *corpusIssue80BaseCollectionCollector) inspect(enclosing string, node ast.Node) {
	if node == nil {
		return
	}
	ast.Inspect(node, func(node ast.Node) bool {
		switch declaration := node.(type) {
		case *ast.VariableDeclaration:
			c.variable(declaration, enclosing)
		case *ast.ForStatement:
			c.collection(declaration.Type, declaration.TypeSpan, enclosing)
		case *ast.LambdaExpression:
			c.parameters(declaration.Parameters, enclosing)
		}
		return true
	})
}

func corpusIssue80BaseCollectionSuggestion(typeName string) (versiongate.Version, string, bool) {
	switch typeName {
	case "Array":
		return versiongate.Version{Major: 4}, "Array[T]", true
	case "Dictionary":
		return versiongate.Version{Major: 4, Minor: 4}, "Dictionary[K, V]", true
	default:
		return versiongate.Version{}, "", false
	}
}

func corpusIssue80BaseCollectionTypeMessage(engine *semantic.Engine, typeValue semantic.Type) (string, bool) {
	switch typeValue.Kind() {
	case semantic.KindUnknown:
		return "", false
	case semantic.KindArray:
		element, typed := typeValue.Element()
		if !typed {
			return "Array has no element type; write Array[T]", true
		}
		spelling, status := corpusIssue80BaseSourceWritableType(engine, element)
		switch status {
		case corpusIssue80BaseSourceTypeWritable:
			return fmt.Sprintf("Array has no element type; write Array[%s]", spelling), true
		case corpusIssue80BaseSourceTypeUnknown:
			return "", false
		default:
			return "Array has no element type; write Array[T]", true
		}
	case semantic.KindDictionary:
		key, typedKey := typeValue.Key()
		value, typedValue := typeValue.Value()
		if !typedKey || !typedValue {
			return "Dictionary has no element type; write Dictionary[K, V]", true
		}
		keySpelling, keyStatus := corpusIssue80BaseSourceWritableType(engine, key)
		valueSpelling, valueStatus := corpusIssue80BaseSourceWritableType(engine, value)
		if keyStatus == corpusIssue80BaseSourceTypeUnknown || valueStatus == corpusIssue80BaseSourceTypeUnknown {
			return "", false
		}
		if keyStatus != corpusIssue80BaseSourceTypeWritable || valueStatus != corpusIssue80BaseSourceTypeWritable {
			return "Dictionary has no element type; write Dictionary[K, V]", true
		}
		return fmt.Sprintf("Dictionary has no element type; write Dictionary[%s, %s]", keySpelling, valueSpelling), true
	default:
		return "", false
	}
}

type corpusIssue80BaseSourceTypeStatus uint8

const (
	corpusIssue80BaseSourceTypeUnwritable corpusIssue80BaseSourceTypeStatus = iota
	corpusIssue80BaseSourceTypeWritable
	corpusIssue80BaseSourceTypeUnknown
)

func corpusIssue80BaseSourceWritableType(engine *semantic.Engine, typeValue semantic.Type) (string, corpusIssue80BaseSourceTypeStatus) {
	switch typeValue.Kind() {
	case semantic.KindUnknown:
		return "", corpusIssue80BaseSourceTypeUnknown
	case semantic.KindVariant:
		return "Variant", corpusIssue80BaseSourceTypeWritable
	case semantic.KindCallable:
		return "Callable", corpusIssue80BaseSourceTypeWritable
	case semantic.KindSignal:
		return "Signal", corpusIssue80BaseSourceTypeWritable
	case semantic.KindBuiltin:
		resolved := engine.ResolveType(typeValue.Name())
		if resolved.Kind() == semantic.KindBuiltin && resolved.Equal(typeValue) {
			return typeValue.Name(), corpusIssue80BaseSourceTypeWritable
		}
		return "", corpusIssue80BaseSourceTypeUnwritable
	case semantic.KindClass:
		if typeValue.Meta() {
			return "", corpusIssue80BaseSourceTypeUnwritable
		}
		resolved := engine.Class(typeValue.Name())
		if resolved.Kind() == semantic.KindClass && !resolved.Meta() && resolved.Equal(typeValue) {
			return typeValue.Name(), corpusIssue80BaseSourceTypeWritable
		}
		return "", corpusIssue80BaseSourceTypeUnwritable
	case semantic.KindArray, semantic.KindDictionary:
		if corpusIssue80BaseNestedUnknownType(typeValue) {
			return "", corpusIssue80BaseSourceTypeUnknown
		}
		return "", corpusIssue80BaseSourceTypeUnwritable
	default:
		return "", corpusIssue80BaseSourceTypeUnwritable
	}
}

func corpusIssue80BaseNestedUnknownType(typeValue semantic.Type) bool {
	switch typeValue.Kind() {
	case semantic.KindUnknown:
		return true
	case semantic.KindArray:
		element, typed := typeValue.Element()
		return typed && corpusIssue80BaseNestedUnknownType(element)
	case semantic.KindDictionary:
		key, typedKey := typeValue.Key()
		value, typedValue := typeValue.Value()
		return typedKey && typedValue && (corpusIssue80BaseNestedUnknownType(key) || corpusIssue80BaseNestedUnknownType(value))
	default:
		return false
	}
}

func TestCorpusIssue80BaseCollectionRuleFailsClosedWithoutSemanticContext(t *testing.T) {
	rule := corpusIssue80BaseCollectionRule{}
	if diagnostics := rule.Check(nil, nil); diagnostics != nil {
		t.Fatalf("nil context diagnostics = %+v, want nil", diagnostics)
	}
	if diagnostics := rule.Check(&Context{}, nil); diagnostics != nil {
		t.Fatalf("nil script diagnostics = %+v, want nil", diagnostics)
	}
}

func corpusIssue80BaseRules() []Rule {
	rules := registeredRules()
	for index, rule := range rules {
		if rule.Name() == ruleRequireTypedCollection {
			rules[index] = corpusIssue80BaseCollectionRule{}
			return rules
		}
	}
	panic("require-typed-collection is not registered")
}

func corpusConfigWithEnabledRule(config Config, rule string) Config {
	config.Enable = append([]string(nil), config.Enable...)
	for _, enabled := range config.Enable {
		if enabled == rule {
			return config
		}
	}
	config.Enable = append(config.Enable, rule)
	return config
}

func corpusConfigWithDisabledRule(config Config, rule string) Config {
	config.Disable = append([]string(nil), config.Disable...)
	for _, disabled := range config.Disable {
		if disabled == rule {
			return config
		}
	}
	config.Disable = append(config.Disable, rule)
	return config
}

func timedCorpusLint(t *testing.T, linter *Linter, snapshot *project.Snapshot) (Report, time.Duration) {
	t.Helper()
	durations := make([]time.Duration, 0, corpusTimingSamples)
	var first Report
	var firstJSON []byte
	for sample := 0; sample < corpusTimingSamples; sample++ {
		started := time.Now()
		report := linter.Lint(snapshot)
		durations = append(durations, time.Since(started))
		data := marshalReport(t, report)
		if sample == 0 {
			first, firstJSON = report, data
			continue
		}
		if !bytes.Equal(firstJSON, data) {
			t.Fatalf("lint run %d did not serialize identically to the first run", sample+1)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return first, durations[len(durations)/2]
}

type corpusAnalyzerSummary struct {
	Expressions          int                     `json:"expressions"`
	Resolved             int                     `json:"resolved"`
	Variant              int                     `json:"variant"`
	Unknown              int                     `json:"unknown"`
	UnknownCategoryCount int                     `json:"unknown_category_count"`
	TopUnknownReasons    []corpusUnknownCategory `json:"top_unknown_reasons"`
	SceneNodeCount       int                     `json:"scene_node_count"`
	SceneNodeRank        int                     `json:"scene_node_rank"`
	GateRecommendation   string                  `json:"gate_recommendation"`
}

type corpusUnknownCategory struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

func corpusAnalyzerReceipt(t *testing.T, snapshot *project.Snapshot, engine *semantic.Engine) corpusAnalyzerSummary {
	t.Helper()
	analyzer := semantic.NewAnalyzer(semanticsource.NewSnapshot(snapshot), engine)
	paths := append([]string(nil), snapshot.Paths...)
	sort.Strings(paths)
	unknownReasons := map[string]int{}
	summary := corpusAnalyzerSummary{}
	for _, path := range paths {
		script := snapshot.Scripts[path]
		if script == nil || script.File == nil || script.ParseError != nil {
			continue
		}
		ast.Inspect(script.File, func(node ast.Node) bool {
			expression, ok := node.(ast.Expression)
			if !ok {
				return true
			}
			summary.Expressions++
			typeValue := analyzer.TypeOf(expression)
			switch typeValue.Kind() {
			case semantic.KindUnknown:
				summary.Unknown++
				reason := typeValue.Reason()
				if _, sceneNode := expression.(*ast.NodePathExpression); sceneNode {
					reason = corpusSceneNodeCategory
					summary.SceneNodeCount++
				}
				if strings.TrimSpace(reason) == "" {
					t.Fatalf("Unknown expression at %s:%d:%d has no reason", path, expression.Span().Start.Line, expression.Span().Start.Column)
				}
				unknownReasons[reason]++
			case semantic.KindVariant:
				summary.Variant++
			default:
				summary.Resolved++
			}
			return true
		})
	}
	summary.TopUnknownReasons = make([]corpusUnknownCategory, 0, len(unknownReasons))
	for reason, count := range unknownReasons {
		summary.TopUnknownReasons = append(summary.TopUnknownReasons, corpusUnknownCategory{Reason: reason, Count: count})
	}
	sort.Slice(summary.TopUnknownReasons, func(i, j int) bool {
		if summary.TopUnknownReasons[i].Count != summary.TopUnknownReasons[j].Count {
			return summary.TopUnknownReasons[i].Count > summary.TopUnknownReasons[j].Count
		}
		return summary.TopUnknownReasons[i].Reason < summary.TopUnknownReasons[j].Reason
	})
	summary.UnknownCategoryCount = len(summary.TopUnknownReasons)
	for index, category := range summary.TopUnknownReasons {
		if category.Reason == corpusSceneNodeCategory {
			summary.SceneNodeRank = index + 1
			break
		}
	}
	if len(summary.TopUnknownReasons) > corpusUnknownReasonLimit {
		summary.TopUnknownReasons = append([]corpusUnknownCategory(nil), summary.TopUnknownReasons[:corpusUnknownReasonLimit]...)
	}
	summary.GateRecommendation = corpusGateRecommendation(summary.SceneNodeRank, summary.SceneNodeCount, summary.Unknown)
	return summary
}

func corpusGateRecommendation(sceneNodeRank, sceneNodeCount, unknownTotal int) string {
	if sceneNodeRank >= 1 && sceneNodeRank <= 3 && unknownTotal > 0 &&
		100*uint64(sceneNodeCount) >= 5*uint64(unknownTotal) {
		return "proceed"
	}
	return "close-not-planned"
}

func corpusCollectionDiagnostics(report Report) []Diagnostic {
	var diagnostics []Diagnostic
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == ruleRequireTypedCollection {
			diagnostics = append(diagnostics, diagnostic)
		}
	}
	return diagnostics
}

type corpusDiagnosticTuple struct {
	Path      string   `json:"path"`
	Line      int      `json:"line"`
	Column    int      `json:"column"`
	EndLine   int      `json:"end_line"`
	EndColumn int      `json:"end_column"`
	Rule      string   `json:"rule"`
	Severity  Severity `json:"severity"`
	Message   string   `json:"message"`
}

func corpusDiagnosticTuples(diagnostics []Diagnostic) []corpusDiagnosticTuple {
	tuples := make([]corpusDiagnosticTuple, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		tuples = append(tuples, corpusDiagnosticTuple{
			Path: diagnostic.Path, Line: diagnostic.Line, Column: diagnostic.Column,
			EndLine: diagnostic.EndLine, EndColumn: diagnostic.EndColumn,
			Rule: diagnostic.Rule, Severity: diagnostic.Severity, Message: diagnostic.Message,
		})
	}
	sort.Slice(tuples, func(i, j int) bool { return corpusTupleLess(tuples[i], tuples[j]) })
	return tuples
}

func corpusTupleLess(left, right corpusDiagnosticTuple) bool {
	if left.Path != right.Path {
		return left.Path < right.Path
	}
	if left.Line != right.Line {
		return left.Line < right.Line
	}
	if left.Column != right.Column {
		return left.Column < right.Column
	}
	if left.EndLine != right.EndLine {
		return left.EndLine < right.EndLine
	}
	if left.EndColumn != right.EndColumn {
		return left.EndColumn < right.EndColumn
	}
	if left.Rule != right.Rule {
		return left.Rule < right.Rule
	}
	if left.Severity != right.Severity {
		return left.Severity < right.Severity
	}
	return left.Message < right.Message
}

func corpusDiagnosticDelta(before, after []corpusDiagnosticTuple) (added, removed []corpusDiagnosticTuple) {
	beforeSet := make(map[string]corpusDiagnosticTuple, len(before))
	for _, tuple := range before {
		beforeSet[corpusTupleKey(tuple)] = tuple
	}
	afterSet := make(map[string]corpusDiagnosticTuple, len(after))
	for _, tuple := range after {
		afterSet[corpusTupleKey(tuple)] = tuple
	}
	for key, tuple := range afterSet {
		if _, exists := beforeSet[key]; !exists {
			added = append(added, tuple)
		}
	}
	for key, tuple := range beforeSet {
		if _, exists := afterSet[key]; !exists {
			removed = append(removed, tuple)
		}
	}
	sort.Slice(added, func(i, j int) bool { return corpusTupleLess(added[i], added[j]) })
	sort.Slice(removed, func(i, j int) bool { return corpusTupleLess(removed[i], removed[j]) })
	return added, removed
}

type corpusDiagnosticChange struct {
	Before corpusDiagnosticTuple `json:"before"`
	After  corpusDiagnosticTuple `json:"after"`
}

func corpusDiagnosticChanges(before, after []corpusDiagnosticTuple) (added, removed []corpusDiagnosticTuple, changed []corpusDiagnosticChange) {
	beforeByIdentity := make(map[string]corpusDiagnosticTuple, len(before))
	for _, tuple := range before {
		beforeByIdentity[corpusTupleIdentity(tuple)] = tuple
	}
	afterByIdentity := make(map[string]corpusDiagnosticTuple, len(after))
	for _, tuple := range after {
		afterByIdentity[corpusTupleIdentity(tuple)] = tuple
	}
	for identity, tuple := range afterByIdentity {
		prior, exists := beforeByIdentity[identity]
		if !exists {
			added = append(added, tuple)
			continue
		}
		if corpusTupleKey(prior) != corpusTupleKey(tuple) {
			changed = append(changed, corpusDiagnosticChange{Before: prior, After: tuple})
		}
	}
	for identity, tuple := range beforeByIdentity {
		if _, exists := afterByIdentity[identity]; !exists {
			removed = append(removed, tuple)
		}
	}
	sort.Slice(added, func(i, j int) bool { return corpusTupleLess(added[i], added[j]) })
	sort.Slice(removed, func(i, j int) bool { return corpusTupleLess(removed[i], removed[j]) })
	sort.Slice(changed, func(i, j int) bool { return corpusTupleLess(changed[i].After, changed[j].After) })
	return added, removed, changed
}

func corpusTupleIdentity(tuple corpusDiagnosticTuple) string {
	return fmt.Sprintf("%s\x00%d\x00%d\x00%d\x00%d\x00%s\x00%s", tuple.Path, tuple.Line, tuple.Column,
		tuple.EndLine, tuple.EndColumn, tuple.Rule, tuple.Severity)
}

func corpusCollectionPrecision(tuples []corpusDiagnosticTuple) (exact, generic int) {
	for _, tuple := range tuples {
		switch tuple.Message {
		case "Array has no element type; write Array[T]", "Dictionary has no element type; write Dictionary[K, V]":
			generic++
		default:
			exact++
		}
	}
	return exact, generic
}

func corpusRequireTuple(t *testing.T, tuples []corpusDiagnosticTuple, path string, line int) corpusDiagnosticTuple {
	t.Helper()
	var found []corpusDiagnosticTuple
	for _, tuple := range tuples {
		if tuple.Path == path && tuple.Line == line {
			found = append(found, tuple)
		}
	}
	if len(found) != 1 {
		t.Fatalf("tuple at %s:%d = %+v, want exactly one", path, line, found)
	}
	return found[0]
}

func corpusTupleKey(tuple corpusDiagnosticTuple) string {
	data, err := json.Marshal(tuple)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func corpusDiagnosticDigest(tuples []corpusDiagnosticTuple) string {
	return corpusDigest(corpusCanonicalDiagnosticTupleJSON(tuples))
}

func corpusDiagnosticChangeDigest(changes []corpusDiagnosticChange) string {
	canonical := append([]corpusDiagnosticChange(nil), changes...)
	sort.Slice(canonical, func(i, j int) bool { return corpusTupleLess(canonical[i].After, canonical[j].After) })
	data, err := json.Marshal(canonical)
	if err != nil {
		panic(err)
	}
	return corpusDigest(data)
}

// corpusCanonicalDiagnosticTupleJSON is the receipt's digest format: a
// path/position/rule/severity/message tuple array, sorted by tuple fields and
// encoded as compact JSON in the declared struct-field order. It intentionally
// does not reuse the CLI report JSON, whose envelope and optional fields are
// not part of the before/after comparison.
func corpusCanonicalDiagnosticTupleJSON(tuples []corpusDiagnosticTuple) []byte {
	canonical := append([]corpusDiagnosticTuple(nil), tuples...)
	sort.Slice(canonical, func(i, j int) bool { return corpusTupleLess(canonical[i], canonical[j]) })
	data, err := json.Marshal(canonical)
	if err != nil {
		panic(err)
	}
	return data
}

func corpusDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

type corpusSemanticReceipt struct {
	CorpusPath                         string                   `json:"corpus_path"`
	CorpusRevision                     string                   `json:"corpus_revision"`
	CorpusClean                        bool                     `json:"corpus_clean"`
	GDKitBaseline                      string                   `json:"gdkit_baseline"`
	GDKitHead                          string                   `json:"gdkit_head"`
	LintConfigPath                     string                   `json:"lint_config_path"`
	LintConfigSHA256                   string                   `json:"lint_config_sha256"`
	IgnoreFilePresent                  bool                     `json:"ignore_file_present"`
	IgnoreFileSHA256                   string                   `json:"ignore_file_sha256,omitempty"`
	HonorIgnoreFile                    bool                     `json:"honor_ignore_file"`
	SourceRoots                        []string                 `json:"source_roots"`
	Exclude                            []string                 `json:"exclude"`
	EngineSchema                       *engineschema.Provenance `json:"engine_schema"`
	Files                              int                      `json:"files"`
	SelectedFiles                      int                      `json:"selected_files"`
	Expressions                        int                      `json:"expressions"`
	Resolved                           int                      `json:"resolved"`
	Variant                            int                      `json:"variant"`
	Unknown                            int                      `json:"unknown"`
	UnknownCategoryCount               int                      `json:"unknown_category_count"`
	TopUnknownReasons                  []corpusUnknownCategory  `json:"top_unknown_reasons"`
	SceneNodeCount                     int                      `json:"scene_node_count"`
	SceneNodeRank                      int                      `json:"scene_node_rank"`
	SceneNodeShareNumerator            int                      `json:"scene_node_share_numerator"`
	SceneNodeShareDenominator          int                      `json:"scene_node_share_denominator"`
	GateRecommendation                 string                   `json:"gate_recommendation"`
	ReceiptAnalyzerConstructions       int                      `json:"receipt_analyzer_constructions"`
	EnabledLintAnalyzerConstructions   int                      `json:"enabled_lint_analyzer_constructions"`
	DisabledLintAnalyzerConstructions  int                      `json:"disabled_lint_analyzer_constructions"`
	BeforeCollectionDiagnostics        int                      `json:"before_collection_diagnostics"`
	BeforeCollectionDigestFormat       string                   `json:"before_collection_digest_format"`
	BeforeCollectionSuppliedDigest     string                   `json:"before_collection_supplied_digest"`
	BeforeCollectionSuppliedReproduced bool                     `json:"before_collection_supplied_digest_reproduced"`
	BeforeCollectionObservedDigest     string                   `json:"before_collection_observed_digest"`
	Issue80BaseGDKit                   string                   `json:"issue_80_base_gdkit"`
	Issue80BaseCollectionDiagnostics   int                      `json:"issue_80_base_collection_diagnostics"`
	Issue80BaseCollectionDigest        string                   `json:"issue_80_base_collection_digest"`
	Issue80BaseExactDiagnostics        int                      `json:"issue_80_base_exact_diagnostics"`
	Issue80BaseGenericDiagnostics      int                      `json:"issue_80_base_generic_diagnostics"`
	AfterCollectionDiagnostics         int                      `json:"after_collection_diagnostics"`
	AfterCollectionDigest              string                   `json:"after_collection_digest"`
	AfterCollectionExactDiagnostics    int                      `json:"after_collection_exact_diagnostics"`
	AfterCollectionGenericDiagnostics  int                      `json:"after_collection_generic_diagnostics"`
	AddedCollectionDiagnostics         int                      `json:"added_collection_diagnostics"`
	RemovedCollectionDiagnostics       int                      `json:"removed_collection_diagnostics"`
	ChangedCollectionDiagnostics       int                      `json:"changed_collection_diagnostics"`
	AddedCollectionDigest              string                   `json:"added_collection_digest"`
	RemovedCollectionDigest            string                   `json:"removed_collection_digest"`
	ChangedCollectionDigest            string                   `json:"changed_collection_digest"`
	AddedCollectionTuples              []corpusDiagnosticTuple  `json:"added_collection_tuples"`
	RemovedCollectionTuples            []corpusDiagnosticTuple  `json:"removed_collection_tuples"`
	ChangedCollectionTuples            []corpusDiagnosticChange `json:"changed_collection_tuples"`
	PayloadTuple                       corpusDiagnosticTuple    `json:"payload_tuple"`
	DisabledLintMedian                 string                   `json:"disabled_lint_median"`
	EnabledLintMedian                  string                   `json:"enabled_lint_median"`
}

func corpusOptionalDigest(t *testing.T, path string) (bool, string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return true, corpusDigest(data)
}

func gitAt(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := []string{}
	if directory != "" {
		command = append(command, "-C", directory)
	}
	command = append(command, args...)
	output, err := exec.Command("git", command...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v (%s)", strings.Join(command, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestCorpusGateRecommendation(t *testing.T) {
	tests := []struct {
		name         string
		rank, count  int
		unknownTotal int
		want         string
	}{
		{name: "rank one at exact threshold", rank: 1, count: 5, unknownTotal: 100, want: "proceed"},
		{name: "rank three above threshold", rank: 3, count: 1, unknownTotal: 19, want: "proceed"},
		{name: "rank four", rank: 4, count: 100, unknownTotal: 100, want: "close-not-planned"},
		{name: "below threshold", rank: 2, count: 4, unknownTotal: 100, want: "close-not-planned"},
		{name: "zero unknowns", rank: 0, count: 0, unknownTotal: 0, want: "close-not-planned"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := corpusGateRecommendation(test.rank, test.count, test.unknownTotal); got != test.want {
				t.Fatalf("corpusGateRecommendation(%d, %d, %d) = %q, want %q", test.rank, test.count, test.unknownTotal, got, test.want)
			}
		})
	}
}

func TestCorpusDiagnosticDigestCanonical(t *testing.T) {
	tuples := []corpusDiagnosticTuple{
		{Path: "b.gd", Line: 2, Column: 3, EndLine: 2, EndColumn: 4, Rule: "z", Severity: SeverityWarning, Message: "later"},
		{Path: "a.gd", Line: 1, Column: 1, EndLine: 1, EndColumn: 2, Rule: "a", Severity: SeverityError, Message: "first"},
	}
	const wantJSON = `[{"path":"a.gd","line":1,"column":1,"end_line":1,"end_column":2,"rule":"a","severity":"error","message":"first"},{"path":"b.gd","line":2,"column":3,"end_line":2,"end_column":4,"rule":"z","severity":"warning","message":"later"}]`
	if got := string(corpusCanonicalDiagnosticTupleJSON(tuples)); got != wantJSON {
		t.Fatalf("canonical tuple JSON = %s, want %s", got, wantJSON)
	}
	const wantDigest = "1ff607347b379f868a195bad2a461f62707737fa5e53f931322cd55132a8af65"
	if got := corpusDiagnosticDigest(tuples); got != wantDigest {
		t.Fatalf("canonical tuple digest = %s, want %s", got, wantDigest)
	}
}

// BenchmarkCorpusSemanticLoadAndLint measures what the universe/selection
// split costs end to end on the pinned corpus. Each iteration starts from
// configuration and includes the project load, so the broad walk and parse are
// inside the measurement rather than amortised across iterations. It is a
// measurement receipt, not a threshold: a second load or a second analyzer is
// prevented structurally and by the focused tests, not by a time budget. It
// publishes no artifact and writes nothing to the corpus.
func BenchmarkCorpusSemanticLoadAndLint(b *testing.B) {
	root := os.Getenv("GDKIT_CORPUS")
	if root == "" {
		b.Skip("GDKIT_CORPUS is not set")
	}
	baseConfig, err := LoadConfig(root, "")
	if err != nil {
		b.Fatal(err)
	}
	for _, bench := range []struct {
		name   string
		config Config
	}{
		{name: "nonsemantic_filtered", config: corpusConfigWithDisabledRule(baseConfig, ruleRequireTypedCollection)},
		{name: "semantic_universe_selected", config: corpusConfigWithEnabledRule(baseConfig, ruleRequireTypedCollection)},
	} {
		b.Run(bench.name, func(b *testing.B) {
			// One untimed run states the shape being measured, so a reader of
			// the receipt knows which universe and action set it describes.
			linter, err := NewForProject(root, bench.config)
			if err != nil {
				b.Fatal(err)
			}
			snapshot, err := project.Load(benchmarkLoadConfig(root, bench.config, linter))
			if err != nil {
				b.Fatal(err)
			}
			b.Logf("%s: semantic=%t paths=%d selected=%d",
				bench.name, linter.NeedsSemanticAnalysis(), len(snapshot.Paths), len(snapshot.Selected))

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				linter, err := NewForProject(root, bench.config)
				if err != nil {
					b.Fatal(err)
				}
				snapshot, err := project.Load(benchmarkLoadConfig(root, bench.config, linter))
				if err != nil {
					b.Fatal(err)
				}
				if report := linter.Lint(snapshot); report.Diagnostics == nil {
					b.Fatal("lint produced no diagnostic slice")
				}
			}
		})
	}
}

// benchmarkLoadConfig is the CLI's load decision, duplicated here rather than
// reached through a test helper so the benchmark measures exactly the two
// configurations cmd/gdkit chooses between.
func benchmarkLoadConfig(root string, config Config, linter *Linter) project.Config {
	if linter.NeedsSemanticAnalysis() {
		return project.Config{Root: root, Selection: &project.Selection{
			SourceRoots:     config.SourceRoots,
			Exclude:         config.Exclude,
			HonorIgnoreFile: true,
		}}
	}
	return project.Config{
		Root:            root,
		SourceRoots:     config.SourceRoots,
		Exclude:         config.Exclude,
		HonorIgnoreFile: true,
	}
}
