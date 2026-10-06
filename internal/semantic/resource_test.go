package semantic

import (
	"testing"

	"github.com/cafecito-games/gdparser/ast"
)

func TestDecodeResourceLiteralAcceptsParsedGDScriptStringForms(t *testing.T) {
	source := sources(t, map[string]string{
		"loader.gd": "class_name Loader\nfunc run():\n\tvar regular := \"res://actors/enemy.gd\"\n\tvar escaped := \"res://actors/\\u0065nemy.gd\"\n\tvar raw := r\"res://actors/enemy.gd\"\n\tvar triple := \"\"\"res://actors/enemy.gd\"\"\"\n\tvar raw_triple := r\"\"\"res://actors/enemy.gd\"\"\"\n",
	})
	if failures := source.ParseFailures(); len(failures) != 0 {
		t.Fatalf("real parser fixture failed: %v", failures)
	}
	file := source.File("loader.gd")
	for _, name := range []string{"regular", "escaped", "raw", "triple", "raw_triple"} {
		t.Run(name, func(t *testing.T) {
			got, err := decodeResourceLiteral(reducerVariableValue(t, file, name))
			if err != nil {
				t.Fatalf("decodeResourceLiteral() error = %v", err)
			}
			if want := "res://actors/enemy.gd"; got != want {
				t.Fatalf("decodeResourceLiteral() = %q, want %q", got, want)
			}
		})
	}
}

func TestDecodeResourceLiteralFailsClosedForNonStringAndMalformedShapes(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		expression ast.Expression
	}{
		{name: "identifier", expression: &ast.Identifier{Name: "path"}},
		{name: "string name", expression: &ast.Literal{Kind: ast.StringNameLiteral, Raw: `&"res://enemy.gd"`, Quote: '"'}},
		{name: "mismatched delimiter", expression: &ast.Literal{Kind: ast.StringLiteral, Raw: `"res://enemy.gd`, Quote: '"'}},
		{name: "unsupported escape", expression: &ast.Literal{Kind: ast.StringLiteral, Raw: `"res://enemy\q.gd"`, Quote: '"'}},
		{name: "unpaired surrogate", expression: &ast.Literal{Kind: ast.StringLiteral, Raw: `"res://enemy\ud800.gd"`, Quote: '"'}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got, err := decodeResourceLiteral(testCase.expression); err == nil || got != "" {
				t.Fatalf("decodeResourceLiteral() = %q, %v; want malformed input rejection", got, err)
			}
		})
	}
}

func TestResourceResolutionUsesClosedImmutableVocabulary(t *testing.T) {
	found := FoundResource(ResourceScript, "res://actors/enemy.gd", "actors/enemy.gd", ResourceLiteralPath)
	if found.State() != ResourceFound || found.Kind() != ResourceScript ||
		found.Requested() != "res://actors/enemy.gd" || found.Path() != "actors/enemy.gd" ||
		found.Provenance() != ResourceLiteralPath || found.Reason() != "" {
		t.Fatalf("found resolution = %#v", found)
	}
	failed := UnresolvedResource(ResourceAmbiguousUID, ResourceUnknown, "uid://enemy", ResourceUIDClaim, "uid://enemy has multiple claimants")
	if failed.State() != ResourceAmbiguousUID || failed.Kind() != ResourceUnknown ||
		failed.Requested() != "uid://enemy" || failed.Path() != "" ||
		failed.Provenance() != ResourceUIDClaim || failed.Reason() == "" {
		t.Fatalf("failed resolution = %#v", failed)
	}
	for _, state := range []ResourceState{
		ResourceFound,
		ResourceMissing,
		ResourceInvalid,
		ResourceEscapesProject,
		ResourceAmbiguousUID,
		ResourceUnsupportedKind,
	} {
		if state.String() == "" {
			t.Fatalf("resource state %d is not closed", state)
		}
	}
	for _, kind := range []ResourceKind{ResourceUnknown, ResourceScript, ResourceScene, ResourceText, ResourceImported} {
		if kind.String() == "" {
			t.Fatalf("resource kind %d is not closed", kind)
		}
	}
	for _, provenance := range []ResourceProvenance{ResourceProvenanceUnknown, ResourceLiteralPath, ResourceUIDClaim} {
		if provenance.String() == "" {
			t.Fatalf("resource provenance %d is not closed", provenance)
		}
	}
	for _, malformed := range []ResourceResolution{
		FoundResource(ResourceImported, "res://icon.png", "icon.png", ResourceLiteralPath),
		UnresolvedResource(ResourceUnsupportedKind, ResourceUnknown, "res://icon.png", ResourceLiteralPath, "unsupported"),
		UnresolvedResource(ResourceMissing, ResourceUnknown, "res://missing.gd", ResourceProvenanceUnknown, "missing"),
	} {
		if problem := resourceResolutionProblem(malformed); problem == "" {
			t.Fatalf("malformed resolution %#v was accepted", malformed)
		}
	}
	if problem := resourceResolutionProblem(ResourceResolution{state: ResourceState(99)}); problem == "" {
		t.Fatal("invalid resource-resolution state was accepted")
	}
}
