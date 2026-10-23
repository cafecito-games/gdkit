package generate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckReportsAMissingRegionAsStale(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n",
	})
	assertDiagnostic(t, report, ruleStale)
	assert.True(t, report.HasChanges(), "a missing region is a change")
}

func TestCheckIsCleanAfterWrite(t *testing.T) {
	source := "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string, equals\nvar q: int\nvar r: int\n"
	written := applyOnce(t, DefaultConfig(), map[string]string{"a.gd": source})
	require.Contains(t, written["a.gd"], "func _to_string() -> String:", "nothing was generated")
	report := checkProject(t, DefaultConfig(), map[string]string{"a.gd": written["a.gd"]})
	assert.False(t, report.HasChanges(), "second run was not clean")
	assert.False(t, report.HasDiagnostics(), "second run was not clean")
}

func TestCheckHonoursThePrecedenceOfIgnoreOverConfig(t *testing.T) {
	config := DefaultConfig()
	config.Generate = []Entry{{Paths: []string{"**/*.gd"}, Generators: []string{"to_string"}}}
	report := checkProject(t, config, map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate:ignore\nvar q: int\n",
	})
	assert.False(t, report.HasChanges(), "ignore did not beat config")
	assert.False(t, report.HasDiagnostics(), "ignore did not beat config")
}

func TestConfigGlobsOptAFileIn(t *testing.T) {
	config := DefaultConfig()
	config.Generate = []Entry{{Paths: []string{"domain/**"}, Generators: []string{"to_string"}}}
	report := checkProject(t, config, map[string]string{
		"domain/hex.gd": "class_name Hex\nextends RefCounted\n\nvar q: int\n",
		"ui/panel.gd":   "class_name Panel\nextends Control\n\nvar q: int\n",
	})
	paths := map[string]bool{}
	for _, result := range report.Results {
		paths[result.Path] = true
	}
	assert.True(t, paths["domain/hex.gd"], "the matching file was not opted in")
	assert.False(t, paths["ui/panel.gd"], "a non-matching file was opted in")
}

// addons/** is excluded by default, so a glob written for the project's own
// code cannot start rewriting a third-party addon.
func TestTheDefaultExcludeKeepsAddonsOut(t *testing.T) {
	config := DefaultConfig()
	config.Generate = []Entry{{Paths: []string{"**/*.gd"}, Generators: []string{"to_string"}}}
	report := checkProject(t, config, map[string]string{
		"addons/vendor/thing.gd": "class_name Thing\nextends RefCounted\n\nvar q: int\n",
	})
	assert.False(t, report.HasChanges(), "an addon was opted in")
}

func TestCheckReportsAnIncompatibleSameNameMethodAsAConflict(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n\n" +
			"static func equals(a, b) -> bool:\n\treturn true\n",
	})
	assertDiagnostic(t, report, ruleConflict)
	assert.False(t, report.HasChanges(), "a conflicted class produced a candidate")
}

func TestACompatibleHandwrittenMethodSatisfiesTheRequest(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n\n" +
			"func equals(p_other: Variant) -> bool:\n\treturn true\n",
	})
	assert.False(t, report.HasDiagnostics(), "a compatible method did not satisfy the request")
	assert.False(t, report.HasChanges(), "a compatible method did not satisfy the request")
}

// Excluding a path is the most likely way to orphan a region, so it must not
// also be the way to hide it.
func TestCheckReportsAnOrphanedRegionInAnExcludedFile(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"addons/vendor.gd": "class_name Vendor\nextends RefCounted\n\n" +
			"# gdkit:generated:begin\nfunc _to_string() -> String:\n\treturn \"Vendor()\"\n# gdkit:generated:end\n",
	})
	assertDiagnostic(t, report, ruleOrphaned)
}

// Inheritance is a reverse dependency: an unparseable file may extend a
// requested base and add fields, and nothing in the base names it. So equals
// cannot be trusted while anything is unreadable, while to_string can.
func TestAnUnparseableFileBlocksEqualsButNotToString(t *testing.T) {
	files := map[string]string{
		"a.gd":      "class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
		"broken.gd": "func (((\n",
	}
	report := checkProject(t, DefaultConfig(), files)
	assertDiagnostic(t, report, ruleSourceParse)
	assertDiagnostic(t, report, ruleUnsupported)
	assert.False(t, report.HasChanges(), "equals was generated with an unreadable file in the universe")

	files["a.gd"] = "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n"
	report = checkProject(t, DefaultConfig(), files)
	assert.True(t, report.HasChanges(), "to_string was blocked by an unparseable unrelated file")
}

// The refusal must name the file to fix, not only say the graph is incomplete.
func TestARefusalNamesItsCause(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd":     "class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
		"stray.gd": "class_name Stray\nextends \"res://gone.gd\"\n",
	})
	diagnostic := assertDiagnostic(t, report, ruleUnsupported)
	assert.Contains(t, diagnostic.Message, "stray.gd", "the unresolved class is not named")
}

func TestARefusalForAnUnopenedAncestorNamesIt(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n\n# gdkit:generate = equals\nvar r: int\n",
	})
	diagnostic := assertDiagnostic(t, report, ruleUnsupported)
	assert.Contains(t, diagnostic.Message, "Base", "the ancestor is not named")
}

func TestAnUnknownGeneratorNameIsAMarkerDiagnostic(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = hash\nvar q: int\n",
	})
	assertDiagnostic(t, report, ruleMarker)
}

// A marker inside a function body is not a class-level directive: a class's
// body span covers its functions, so this has to be structural.
func TestADirectiveIsOnlyADirectStatementOfAClassSuite(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\nvar q: int\n\n" +
			"func f() -> void:\n\t# gdkit:generate = to_string\n\tpass\n",
	})
	assert.False(t, report.HasChanges(), "a marker inside a function was treated as a class directive")
	assert.False(t, report.HasDiagnostics(), "a marker inside a function was treated as a class directive")
}

func TestAMarkerOnAnInnerClassIsRefused(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Holder\nextends RefCounted\n\n" +
			"class Nested:\n\t# gdkit:generate = to_string\n\tvar q: int\n",
	})
	assertDiagnostic(t, report, ruleUnsupported)
	assert.False(t, report.HasChanges(), "an inner class produced a candidate")
}

// gen write is not a partial format write: code outside the region survives
// verbatim even when it is not canonical.
func TestCheckKeepsEveryByteOutsideTheRegion(t *testing.T) {
	source := "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q:int\n\n\nfunc untouched( ) :\n\tpass\n"
	got := applyOnce(t, DefaultConfig(), map[string]string{"a.gd": source})["a.gd"]
	assert.Contains(t, got, "var q:int", "code outside the region was reformatted")
	assert.Contains(t, got, "func untouched( ) :", "code outside the region was reformatted")
	assert.Contains(t, got, beginSentinel, "nothing was generated")
}

// A verification failure withdraws a capability, so a child that composed with
// it must be refused too. Without the re-resolution loop the child emits a
// super call against a method that was never written.
func TestARefusedParentRefusesTheChildThatComposedWithIt(t *testing.T) {
	config := DefaultConfig()
	snapshot := loadSelected(t, config, map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n\n# gdkit:generate = equals\nvar r: int\n",
	})
	generator, err := New(config, formatDefault())
	require.NoError(t, err)
	// Damage only Base's output, so only Base fails verification.
	generator.emit = func(emitter Emitter, class *Class, index *Index, capabilities *Capabilities) (string, []Diagnostic) {
		if class.Path == "base.gd" {
			return "func equals(p_other: Variant) -> bool\n\treturn true\n", nil
		}
		return emitter.Emit(class, index, capabilities)
	}
	report := generator.Check(snapshot).Report()
	refused := map[string]string{}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule != ruleStale {
			refused[diagnostic.Path] = diagnostic.Rule
		}
	}
	assert.NotEmpty(t, refused["base.gd"], "base.gd was not refused")
	assert.NotEmpty(t, refused["sub.gd"], "sub.gd generated a super call against a method that will not exist")
}

// The oracle turns the formatting invariant from a claim into a precondition of
// writing, so it is tested against verify directly: a candidate that reaches
// canonicalise is canonical by construction, which is the point.
func TestVerifyRefusesACandidateTheFormatterWouldChange(t *testing.T) {
	config := DefaultConfig()
	source := "class_name Hex\nextends RefCounted\n\nvar q: int\n"
	snapshot := loadSelected(t, config, map[string]string{"a.gd": source})
	generator, err := New(config, formatDefault())
	require.NoError(t, err)
	index := BuildIndex(snapshot)
	class := index.TopLevel["a.gd"]
	require.True(t, generator.canonical("a.gd", []byte(source)), "the fixture source is not canonical, so the oracle would not run")
	// Valid GDScript that reparses and leaves every byte outside the region
	// alone, but that the full-file formatter would respace.
	region := beginSentinel + "\nfunc  _to_string( ) -> String:\n\treturn      \"Hex()\"\n" + endSentinel + "\n"
	contents, span, err := Splice([]byte(source), region, generator.gap)
	require.NoError(t, err)
	candidate := Candidate{Path: "a.gd", Class: class.ID, Contents: contents, Region: span, Changed: true}
	err = generator.verify(snapshot.Scripts["a.gd"], class, candidate)
	require.Error(t, err, "a non-canonical candidate passed verification")
	assert.Contains(t, err.Error(), "not formatted", "the oracle's refusal was not recorded")
}

// Verification must also catch a candidate that changed a byte outside the
// region, which is the invariant replacing format's token-stream check.
func TestVerifyRefusesAChangeOutsideTheRegion(t *testing.T) {
	config := DefaultConfig()
	source := "class_name Hex\nextends RefCounted\n\nvar q: int\n"
	snapshot := loadSelected(t, config, map[string]string{"a.gd": source})
	generator, err := New(config, formatDefault())
	require.NoError(t, err)
	index := BuildIndex(snapshot)
	class := index.TopLevel["a.gd"]
	region := beginSentinel + "\nfunc _to_string() -> String:\n\treturn \"Hex()\"\n" + endSentinel + "\n"
	contents, span, err := Splice([]byte("class_name Hex\nextends RefCounted\n\nvar tampered: int\n"), region, generator.gap)
	require.NoError(t, err)
	candidate := Candidate{Path: "a.gd", Class: class.ID, Contents: contents, Region: span, Changed: true}
	err = generator.verify(snapshot.Scripts["a.gd"], class, candidate)
	require.Error(t, err, "a candidate that rewrote code outside the region passed verification")
}

func TestTheHelpersDirectiveIsNotAccepted(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Thing\nextends RefCounted\n\n# gdkit:generate = helpers\nvar x: int\n",
	})
	assertDiagnostic(t, report, ruleMarker)
}

func TestDeepEqualsRefusesWithoutTheCompanionAddon(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Thing\nextends RefCounted\n\n# gdkit:generate = deep_equals\nvar x: int\n",
	})
	diagnostic := assertDiagnostic(t, report, ruleUnsupported)
	assert.False(t, report.HasChanges(), "a candidate was produced without the companion addon")
	for _, want := range []string{"GDKitEquality", "gpm add --name gdkit --source github-release --repo cafecito-games/gdkit"} {
		assert.Contains(t, diagnostic.Message, want)
	}
}

// The command the refusal prints has to be runnable as printed: gpm uses
// --version verbatim as the release tag, so an argument without the leading "v"
// fails against a tag that exists.
func TestTheAddonInstallCommandNamesAReleaseTag(t *testing.T) {
	old := detectedVersion
	t.Cleanup(func() { detectedVersion = old })

	for _, testCase := range []struct {
		version string
		want    string
	}{
		{version: "0.6.0", want: "--version v0.6.0"},
		{version: "1.0.10", want: "--version v1.0.10"},
		{version: "dev", want: `--version <tag>, where <tag> is the gdkit release matching this build, including its leading "v"`},
		{version: "0.7.0-SNAPSHOT-abc1234", want: "--version <tag>"},
		// What a local build of a dirty tree at a tag reports about itself.
		{version: "0.6.0+dirty", want: "--version <tag>"},
		{version: "", want: "--version <tag>"},
	} {
		detectedVersion = func() string { return testCase.version }
		report := checkProject(t, DefaultConfig(), map[string]string{
			"a.gd": "class_name Thing\nextends RefCounted\n\n# gdkit:generate = deep_equals\nvar x: int\n",
		})
		diagnostic := assertDiagnostic(t, report, ruleUnsupported)
		assert.Contains(t, diagnostic.Message, testCase.want, "version "+testCase.version)
		assert.NotContains(t, diagnostic.Message, "--version vdev", "version "+testCase.version)
	}
}

func TestDeepEqualsRefusesAnAddonWithoutTheStaticComparison(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"addons/gdkit/equality_helpers.gd": "class_name GDKitEquality\nextends RefCounted\n\n\n" +
			"static func deep_equals(p_only: Variant) -> bool:\n\treturn true\n",
		"a.gd": "class_name Thing\nextends RefCounted\n\n# gdkit:generate = deep_equals\nvar x: int\n",
	})
	diagnostic := assertDiagnostic(t, report, ruleUnsupported)
	assert.False(t, report.HasChanges(), "a candidate was produced against an addon without the static comparison")
	assert.Contains(t, diagnostic.Message, "too old", "the addon was not called too old")
}

func TestTheCompanionAddonIsNeverGeneratedFor(t *testing.T) {
	report := checkProject(t, DefaultConfig(), withCompanionAddon(map[string]string{
		"a.gd": "class_name Thing\nextends RefCounted\n\n# gdkit:generate = deep_equals\nvar x: int\n",
	}))
	require.Len(t, report.Results, 1, "want a changed candidate for a.gd only")
	assert.Equal(t, "a.gd", report.Results[0].Path)
	assert.True(t, report.Results[0].Changed)
	for _, diagnostic := range report.Diagnostics {
		assert.NotEqual(t, "addons/gdkit/equality_helpers.gd", diagnostic.Path, "the addon was reported")
	}
}

func TestEqualsIsUnaffectedByAMissingHelpersClass(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Thing\nextends RefCounted\n\n# gdkit:generate = equals\nvar x: int\n",
	})
	for _, diagnostic := range report.Diagnostics {
		assert.Equal(t, ruleStale, diagnostic.Rule, "equals was affected by the missing helpers class")
	}
	assert.True(t, report.HasChanges(), "equals produced no candidate")
}
