package lint

import (
	"strings"
	"testing"
)

// docstringConfig enables missing-docstring for the named member kinds.
func docstringConfig(kinds ...string) Config {
	config := DefaultConfig()
	config.MissingDocstring = kinds
	return config
}

func TestMissingDocstringIsInertUntilConfigured(t *testing.T) {
	assertNoRule(t, "missing-docstring", `class_name Thing
extends Node

signal thing_happened

func do_thing() -> void:
	pass
`)
}

func TestMissingDocstringChecksOnlyTheConfiguredKinds(t *testing.T) {
	source := `class_name Thing
extends Node

signal thing_happened

var count := 0

func do_thing() -> void:
	pass
`
	assertRuleWithConfig(t, docstringConfig(docKindFunc), "missing-docstring", source, 8)
	assertRuleWithConfig(t, docstringConfig(docKindSignal), "missing-docstring", source, 4)
	assertRuleWithConfig(t, docstringConfig(docKindSignal, docKindVar), "missing-docstring", source, 4, 6)
}

// A "#" comment is a note to the reader of the source; Godot's class reference
// shows only "##".
func TestMissingDocstringRequiresADocumentationComment(t *testing.T) {
	assertRuleWithConfig(t, docstringConfig(docKindFunc), "missing-docstring", `# Does the thing.
func do_thing() -> void:
	pass
`, 2)
}

func TestMissingDocstringAcceptsADocumentationComment(t *testing.T) {
	assertRuleWithConfig(t, docstringConfig(docKindFunc), "missing-docstring", `## Does the thing.
func do_thing() -> void:
	pass
`)
}

// Godot attaches a documentation comment to the declaration below it, and a
// blank line breaks that attachment.
func TestMissingDocstringRejectsADocumentationCommentSeparatedByABlankLine(t *testing.T) {
	assertRuleWithConfig(t, docstringConfig(docKindFunc), "missing-docstring", `## Does the thing.

func do_thing() -> void:
	pass
`, 3)
}

// The comments above a declaration are one block, so a plain note written
// between the documentation and the declaration does not hide it.
func TestMissingDocstringLooksThroughPlainCommentsInTheSameBlock(t *testing.T) {
	assertRuleWithConfig(t, docstringConfig(docKindFunc), "missing-docstring", `## Does the thing.
# Rewritten once the store lands.
func do_thing() -> void:
	pass
`)
}

func TestMissingDocstringLooksAboveTheAnnotationsOfADeclaration(t *testing.T) {
	documented := `## How many things there are.
@export
var count := 0
`
	assertRuleWithConfig(t, docstringConfig(docKindVar), "missing-docstring", documented)

	undocumented := `@export
var count := 0
`
	assertRuleWithConfig(t, docstringConfig(docKindVar), "missing-docstring", undocumented, 2)
}

// A member whose name starts with an underscore is private, so every Godot
// lifecycle callback is exempt without naming one.
func TestMissingDocstringSkipsPrivateMembers(t *testing.T) {
	assertRuleWithConfig(t, docstringConfig(docKindFunc, docKindVar, docKindSignal, docKindConst), "missing-docstring", `var _count := 0

const _MAX := 1

signal _thing_happened

func _ready() -> void:
	pass
`)
}

// Godot reads the class description from the top of the file, after "extends"
// and "class_name" but ahead of every member.
func TestMissingDocstringReadsTheClassCommentFromTheTopOfTheFile(t *testing.T) {
	assertRuleWithConfig(t, docstringConfig(docKindClass), "missing-docstring", `class_name Thing
extends Node
## A thing.

var count := 0
`)
	assertRuleWithConfig(t, docstringConfig(docKindClass), "missing-docstring", `## A thing.
class_name Thing
extends Node
`)
}

// A comment that follows the first member documents that member, not the class.
func TestMissingDocstringDoesNotTakeAMemberCommentAsTheClassComment(t *testing.T) {
	assertRuleWithConfig(t, docstringConfig(docKindClass), "missing-docstring", `class_name Thing
extends Node

var count := 0

## Does the thing.
func do_thing() -> void:
	pass
`, 1)
}

// A script with no class_name is not part of the class reference.
func TestMissingDocstringSkipsAScriptWithoutAClassName(t *testing.T) {
	assertRuleWithConfig(t, docstringConfig(docKindClass), "missing-docstring", `extends Node

var count := 0
`)
}

func TestMissingDocstringChecksInnerClassesAndTheirMembers(t *testing.T) {
	assertRuleWithConfig(t, docstringConfig(docKindClass, docKindVar), "missing-docstring", `extends Node

class Inner:
	var value := 0

class _Hidden:
	var other := 0
`, 3, 4)
}

// An anonymous enum names nothing the class reference can show.
func TestMissingDocstringSkipsAnAnonymousEnum(t *testing.T) {
	assertRuleWithConfig(t, docstringConfig(docKindEnum), "missing-docstring", `extends Node

enum { A, B }

enum Kind { C, D }
`, 5)
}

func TestMissingDocstringNamesTheMemberAndItsKind(t *testing.T) {
	found := lintSourceWithConfig(t, docstringConfig(docKindConst), "missing-docstring", `const MAX_THINGS = 3
`)
	if len(found) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(found))
	}
	if !strings.Contains(found[0].Message, `Constant "MAX_THINGS"`) {
		t.Errorf("message %q does not name the constant", found[0].Message)
	}
}

func TestMissingDocstringIsSuppressedByAnIgnoreComment(t *testing.T) {
	assertRuleWithConfig(t, docstringConfig(docKindFunc), "missing-docstring", `func do_thing() -> void: # gdkit:ignore = missing-docstring
	pass
`)
}

func TestUnknownDocstringKindIsAConfigurationError(t *testing.T) {
	if err := docstringConfig("method").Validate(); err == nil {
		t.Fatal("unknown member kind was accepted")
	}
}
