package generate

import (
	"strings"
	"testing"
)

func TestCheckReportsAMissingRegionAsStale(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n",
	})
	assertDiagnostic(t, report, ruleStale)
	if !report.HasChanges() {
		t.Error("a missing region is a change")
	}
}

func TestCheckIsCleanAfterWrite(t *testing.T) {
	source := "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string, equals\nvar q: int\nvar r: int\n"
	written := applyOnce(t, DefaultConfig(), map[string]string{"a.gd": source})
	if !strings.Contains(written["a.gd"], "func _to_string() -> String:") {
		t.Fatalf("nothing was generated:\n%s", written["a.gd"])
	}
	report := checkProject(t, DefaultConfig(), map[string]string{"a.gd": written["a.gd"]})
	if report.HasChanges() || report.HasDiagnostics() {
		t.Errorf("second run was not clean: %+v %+v", report.Results, report.Diagnostics)
	}
}

func TestCheckHonoursThePrecedenceOfIgnoreOverConfig(t *testing.T) {
	config := DefaultConfig()
	config.Generate = []Entry{{Paths: []string{"**/*.gd"}, Generators: []string{"to_string"}}}
	report := checkProject(t, config, map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate:ignore\nvar q: int\n",
	})
	if report.HasChanges() || report.HasDiagnostics() {
		t.Errorf("ignore did not beat config: %+v %+v", report.Results, report.Diagnostics)
	}
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
	if !paths["domain/hex.gd"] {
		t.Error("the matching file was not opted in")
	}
	if paths["ui/panel.gd"] {
		t.Error("a non-matching file was opted in")
	}
}

// addons/** is excluded by default, so a glob written for the project's own
// code cannot start rewriting a third-party addon.
func TestTheDefaultExcludeKeepsAddonsOut(t *testing.T) {
	config := DefaultConfig()
	config.Generate = []Entry{{Paths: []string{"**/*.gd"}, Generators: []string{"to_string"}}}
	report := checkProject(t, config, map[string]string{
		"addons/vendor/thing.gd": "class_name Thing\nextends RefCounted\n\nvar q: int\n",
	})
	if report.HasChanges() {
		t.Errorf("an addon was opted in: %+v", report.Results)
	}
}

func TestCheckReportsAnIncompatibleSameNameMethodAsAConflict(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n\n" +
			"static func equals(a, b) -> bool:\n\treturn true\n",
	})
	assertDiagnostic(t, report, ruleConflict)
	if report.HasChanges() {
		t.Error("a conflicted class produced a candidate")
	}
}

func TestACompatibleHandwrittenMethodSatisfiesTheRequest(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n\n" +
			"func equals(p_other: Variant) -> bool:\n\treturn true\n",
	})
	if report.HasDiagnostics() || report.HasChanges() {
		t.Errorf("a compatible method did not satisfy the request: %+v %+v", report.Diagnostics, report.Results)
	}
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
	if report.HasChanges() {
		t.Error("equals was generated with an unreadable file in the universe")
	}

	files["a.gd"] = "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q: int\n"
	report = checkProject(t, DefaultConfig(), files)
	if !report.HasChanges() {
		t.Error("to_string was blocked by an unparseable unrelated file")
	}
}

// The refusal must name the file to fix, not only say the graph is incomplete.
func TestARefusalNamesItsCause(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd":     "class_name Hex\nextends RefCounted\n\n# gdkit:generate = equals\nvar q: int\n",
		"stray.gd": "class_name Stray\nextends \"res://gone.gd\"\n",
	})
	diagnostic := assertDiagnostic(t, report, ruleUnsupported)
	if !strings.Contains(diagnostic.Message, "stray.gd") {
		t.Errorf("message = %q, want the unresolved class named", diagnostic.Message)
	}
}

func TestARefusalForAnUnopenedAncestorNamesIt(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"base.gd": "class_name Base\nextends RefCounted\n\nvar q: int\n",
		"sub.gd":  "class_name Sub\nextends Base\n\n# gdkit:generate = equals\nvar r: int\n",
	})
	diagnostic := assertDiagnostic(t, report, ruleUnsupported)
	if !strings.Contains(diagnostic.Message, "Base") {
		t.Errorf("message = %q, want the ancestor named", diagnostic.Message)
	}
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
	if report.HasChanges() || report.HasDiagnostics() {
		t.Errorf("a marker inside a function was treated as a class directive: %+v %+v",
			report.Results, report.Diagnostics)
	}
}

func TestAMarkerOnAnInnerClassIsRefused(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Holder\nextends RefCounted\n\n" +
			"class Nested:\n\t# gdkit:generate = to_string\n\tvar q: int\n",
	})
	assertDiagnostic(t, report, ruleUnsupported)
	if report.HasChanges() {
		t.Error("an inner class produced a candidate")
	}
}

// gen write is not a partial format write: code outside the region survives
// verbatim even when it is not canonical.
func TestCheckKeepsEveryByteOutsideTheRegion(t *testing.T) {
	source := "class_name Hex\nextends RefCounted\n\n# gdkit:generate = to_string\nvar q:int\n\n\nfunc untouched( ) :\n\tpass\n"
	got := applyOnce(t, DefaultConfig(), map[string]string{"a.gd": source})["a.gd"]
	if !strings.Contains(got, "var q:int") || !strings.Contains(got, "func untouched( ) :") {
		t.Errorf("code outside the region was reformatted:\n%s", got)
	}
	if !strings.Contains(got, beginSentinel) {
		t.Errorf("nothing was generated:\n%s", got)
	}
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
	if err != nil {
		t.Fatal(err)
	}
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
	if refused["base.gd"] == "" {
		t.Errorf("base.gd was not refused: %+v", report.Diagnostics)
	}
	if refused["sub.gd"] == "" {
		t.Error("sub.gd generated a super call against a method that will not exist")
	}
}

// The oracle turns the formatting invariant from a claim into a precondition of
// writing, so it is tested against verify directly: a candidate that reaches
// canonicalise is canonical by construction, which is the point.
func TestVerifyRefusesACandidateTheFormatterWouldChange(t *testing.T) {
	config := DefaultConfig()
	source := "class_name Hex\nextends RefCounted\n\nvar q: int\n"
	snapshot := loadSelected(t, config, map[string]string{"a.gd": source})
	generator, err := New(config, formatDefault())
	if err != nil {
		t.Fatal(err)
	}
	index := BuildIndex(snapshot)
	class := index.TopLevel["a.gd"]
	if !generator.canonical("a.gd", []byte(source)) {
		t.Fatal("the fixture source is not canonical, so the oracle would not run")
	}
	// Valid GDScript that reparses and leaves every byte outside the region
	// alone, but that the full-file formatter would respace.
	region := beginSentinel + "\nfunc  _to_string( ) -> String:\n\treturn      \"Hex()\"\n" + endSentinel + "\n"
	contents, span, err := Splice([]byte(source), region, generator.gap)
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{Path: "a.gd", Class: class.ID, Contents: contents, Region: span, Changed: true}
	err = generator.verify(snapshot.Scripts["a.gd"], class, candidate)
	if err == nil {
		t.Fatal("a non-canonical candidate passed verification")
	}
	if !strings.Contains(err.Error(), "not formatted") {
		t.Errorf("err = %v, want the oracle's refusal", err)
	}
}

// Verification must also catch a candidate that changed a byte outside the
// region, which is the invariant replacing format's token-stream check.
func TestVerifyRefusesAChangeOutsideTheRegion(t *testing.T) {
	config := DefaultConfig()
	source := "class_name Hex\nextends RefCounted\n\nvar q: int\n"
	snapshot := loadSelected(t, config, map[string]string{"a.gd": source})
	generator, err := New(config, formatDefault())
	if err != nil {
		t.Fatal(err)
	}
	index := BuildIndex(snapshot)
	class := index.TopLevel["a.gd"]
	region := beginSentinel + "\nfunc _to_string() -> String:\n\treturn \"Hex()\"\n" + endSentinel + "\n"
	contents, span, err := Splice([]byte("class_name Hex\nextends RefCounted\n\nvar tampered: int\n"), region, generator.gap)
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{Path: "a.gd", Class: class.ID, Contents: contents, Region: span, Changed: true}
	if err := generator.verify(snapshot.Scripts["a.gd"], class, candidate); err == nil {
		t.Fatal("a candidate that rewrote code outside the region passed verification")
	}
}

func TestTheHelpersDirectiveIsNotAccepted(t *testing.T) {
	report := checkProject(t, DefaultConfig(), map[string]string{
		"a.gd": "class_name Thing\nextends RefCounted\n\n# gdkit:generate = helpers\nvar x: int\n",
	})
	assertDiagnostic(t, report, ruleMarker)
}
