package lint

import "testing"

func TestClassDefinitionsOrderCorrectFileIsClean(t *testing.T) {
	assertNoRule(t, "class-definitions-order", `@tool
class_name Foo
extends Node
"""Docs."""

signal a
enum E { A }
const C = 1
static var s = 1
@export var e = 1
var pub = 1
var _prv = 1
@onready var op = 1
@onready var _oprv = 1

func f():
	pass
`)
}

func TestClassDefinitionsOrderSignalAfterVar(t *testing.T) {
	source := "extends Node\nvar x = 1\nsignal a\n"
	assertRule(t, "class-definitions-order", source, 3)
	found := lintSource(t, "class-definitions-order", source)
	if found[0].Message != "Definition out of order in global scope" || found[0].Column != 1 {
		t.Fatalf("got %d:%d %q", found[0].Line, found[0].Column, found[0].Message)
	}
}

func TestClassDefinitionsOrderOnreadyBeforePlainVar(t *testing.T) {
	assertRule(t, "class-definitions-order", "extends Node\n@onready var a = 1\nvar b = 1\n", 3)
}

func TestClassDefinitionsOrderPrivateBeforePublic(t *testing.T) {
	assertRule(t, "class-definitions-order",
		"extends Node\nvar _p = 1\nvar q = 1\n@onready var _r = 1\n@onready var s = 1\n", 3, 5)
}

func TestClassDefinitionsOrderExportAndStaticVars(t *testing.T) {
	source := "extends Node\nvar a = 1\n@export var b = 1\nstatic var c = 1\n@export static var d = 1\n@export var e = 1\n"
	assertRule(t, "class-definitions-order", source, 3, 4, 5, 6)
	found := lintSource(t, "class-definitions-order", source)
	wantColumns := []int{9, 1, 9, 9}
	for index, diagnostic := range found {
		if diagnostic.Column != wantColumns[index] {
			t.Errorf("line %d column %d, want %d", diagnostic.Line, diagnostic.Column, wantColumns[index])
		}
	}
}

func TestClassDefinitionsOrderStaticVarIgnoresExportAnnotation(t *testing.T) {
	assertNoRule(t, "class-definitions-order", "extends Node\n@export static var a = 1\n@export var b = 1\n")
}

func TestClassDefinitionsOrderDocstrings(t *testing.T) {
	source := "extends Node\n## doc comment\nsignal a\n\"\"\"late docstring\"\"\"\nvar x = 1\n\"\"\"another\"\"\"\n"
	assertRule(t, "class-definitions-order", source, 4, 6)
}

func TestClassDefinitionsOrderInnerClassesAreCheckedSeparately(t *testing.T) {
	source := "extends Node\nclass Inner:\n\tvar x = 1\n\tsignal s\n\tclass Deep:\n\t\tfunc f():\n\t\t\tpass\n\t\tvar y = 1\nfunc f():\n\tpass\nconst C = 1\n"
	assertRule(t, "class-definitions-order", source, 4, 8, 11)
	found := lintSource(t, "class-definitions-order", source)
	wantMessages := []string{
		"Definition out of order in Inner",
		"Definition out of order in Deep",
		"Definition out of order in global scope",
	}
	for index, diagnostic := range found {
		if diagnostic.Message != wantMessages[index] {
			t.Errorf("line %d message %q, want %q", diagnostic.Line, diagnostic.Message, wantMessages[index])
		}
	}
}

func TestClassDefinitionsOrderInnerExtendsClauseIsAMember(t *testing.T) {
	order := []string{"others", "signals", "pubvars"}
	found := lintSourceWithConfig(t, withOrder(order), "class-definitions-order", "class A extends Node:\n\tvar q\n")
	if len(found) != 1 || found[0].Column != 9 ||
		found[0].Message != "Definition order not specified for 'others' or 'extends', please fix/re-generate your gdlintrc file" {
		t.Fatalf("got %+v", found)
	}
}

func TestClassDefinitionsOrderReportsEveryOutOfOrderStatement(t *testing.T) {
	source := "extends Node\nfunc f():\n\tpass\nsignal a\nvar x = 1\nconst C = 1\nenum E { A }\nsignal b\n"
	assertRule(t, "class-definitions-order", source, 4, 5, 6, 7, 8)
}

func TestClassDefinitionsOrderCombinedClassNameExtendsIsExtends(t *testing.T) {
	assertRule(t, "class-definitions-order",
		"class_name Foo extends Node\nsignal a\nextends Node\nclass_name Bar\n", 3, 4)
}

func TestClassDefinitionsOrderToolAnnotationIsASlot(t *testing.T) {
	source := "\"\"\"Docs.\"\"\"\n@tool\nextends Node\nstatic func f():\n\tpass\n@abstract\nfunc g()\nclass I:\n\tpass\nvar t\n"
	assertRule(t, "class-definitions-order", source, 2, 3, 10)
}

func TestClassDefinitionsOrderAnnotationPairing(t *testing.T) {
	source := `extends Node
var z
@export_group("g")
@export var a = 1
@export_range(0, 1) var b = 1
@export
var c = 1
@export_group("h")
@onready var d = 1
@export
@export_group("i")
var e = 1
@warning_ignore("unused_variable") @export var f = 1
@warning_ignore("unused_variable")
@export var g = 1
@export_category("x")
var h
`
	assertRule(t, "class-definitions-order", source, 4, 5, 7, 12, 13, 15, 17)
	found := lintSource(t, "class-definitions-order", source)
	wantColumns := []int{9, 21, 1, 1, 44, 9, 1}
	for index, diagnostic := range found {
		if diagnostic.Column != wantColumns[index] {
			t.Errorf("line %d column %d, want %d", diagnostic.Line, diagnostic.Column, wantColumns[index])
		}
	}
}

// gdparser attaches an annotation to its declaration across a standalone
// annotation and lists the standalone one first. gdlint reads them in source
// order, where the standalone annotation discards the one written before it.
func TestClassDefinitionsOrderStandaloneAnnotationDiscardsTheAnnotationBeforeIt(t *testing.T) {
	assertNoRule(t, "class-definitions-order", "extends Node\nvar z\n@export\n@export_group(\"g\")\nvar a = 1\n")
	assertNoRule(t, "class-definitions-order", "extends Node\nvar z\n@export\n@warning_ignore_start(\"unused_signal\")\nvar a = 1\n")
	assertRule(t, "class-definitions-order", "extends Node\nvar z\n@onready\n@export_subgroup(\"g\")\nvar _a = 1\nvar b\n", 6)
	assertNoRule(t, "class-definitions-order", "extends Node\n@onready\n@export_subgroup(\"g\")\nvar a = 1\nvar _b\n")
	assertRule(t, "class-definitions-order", "extends Node\nvar z\n@export_group(\"g\")\n@export\nvar a = 1\n", 5)
}

func TestClassDefinitionsOrderWarningIgnoreAttachesOnlyForDeclarationWarnings(t *testing.T) {
	source := `extends Node
var z
@export
@warning_ignore("unused_parameter")
var a
@export
@warning_ignore('unused_parameter')
var b
@export
@warning_ignore("unused_variable")
var c
`
	assertRule(t, "class-definitions-order", source, 5)
}

func TestClassDefinitionsOrderAnnotationBeforePassIsDiscarded(t *testing.T) {
	assertRule(t, "class-definitions-order", "extends Node\n@export\npass\nvar h\n")
}

func TestClassDefinitionsOrderExportPrefixAndOnreadyPrecedence(t *testing.T) {
	source := "extends Node\nvar a\n@export_enum(\"a\", \"b\") var b\n@export_node_path var c\n@onready @export var d\n@export @onready var e\n@onready @export_range(0, 1) var f\nvar _g\n@export var h\n"
	assertRule(t, "class-definitions-order", source, 3, 4, 5, 6, 7, 9)
}

func TestClassDefinitionsOrderSemicolonSeparatedMembers(t *testing.T) {
	source := "extends Node\nfunc f():\n\tpass\nvar a = 1; var b = 2\nsignal s\n"
	found := lintSource(t, "class-definitions-order", source)
	if len(found) != 3 || found[0].Column != 1 || found[1].Column != 12 || found[2].Line != 5 {
		t.Fatalf("got %+v", found)
	}
}

func withOrder(order []string) Config {
	config := DefaultConfig()
	config.ClassDefinitionsOrder = order
	return config
}

func TestClassDefinitionsOrderCustomOrderReversesWhatFires(t *testing.T) {
	source := "extends Node\nvar x = 1\nsignal a\n"
	assertRule(t, "class-definitions-order", source, 3)
	reversed := withOrder([]string{
		"signals", "pubvars", "others", "tools", "classnames", "extends", "docstrings",
		"enums", "consts", "staticvars", "exports", "prvvars", "onreadypubvars", "onreadyprvvars",
	})
	assertRuleWithConfig(t, reversed, "class-definitions-order", source, 2, 3)
}

func TestClassDefinitionsOrderSlotMissingFromOrder(t *testing.T) {
	config := withOrder([]string{"pubvars", "signals"})
	source := "extends Node\nvar x = 1\nsignal a\nconst C = 1\nvar y = 1\n"
	assertRuleWithConfig(t, config, "class-definitions-order", source, 1, 4, 5)
	found := lintSourceWithConfig(t, config, "class-definitions-order", source)
	want := []string{
		"Definition order not specified for 'pubvars' or 'extends', please fix/re-generate your gdlintrc file",
		"Definition order not specified for 'signals' or 'consts', please fix/re-generate your gdlintrc file",
		"Definition out of order in global scope",
	}
	for index, diagnostic := range found {
		if diagnostic.Message != want[index] {
			t.Errorf("line %d message %q, want %q", diagnostic.Line, diagnostic.Message, want[index])
		}
	}
}
