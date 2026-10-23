package semantic

import (
	"testing"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/stretchr/testify/require"
)

func TestDecodeResourceLiteralAcceptsParsedGDScriptStringForms(t *testing.T) {
	source := sources(t, map[string]string{
		"loader.gd": "class_name Loader\nfunc run():\n\tvar regular := \"res://actors/enemy.gd\"\n\tvar escaped := \"res://actors/\\u0065nemy.gd\"\n\tvar raw := r\"res://actors/enemy.gd\"\n\tvar triple := \"\"\"res://actors/enemy.gd\"\"\"\n\tvar raw_triple := r\"\"\"res://actors/enemy.gd\"\"\"\n",
	})
	require.Empty(t, source.ParseFailures())
	file := source.File("loader.gd")
	for _, name := range []string{"regular", "escaped", "raw", "triple", "raw_triple"} {
		t.Run(name, func(t *testing.T) {
			got, err := decodeResourceLiteral(reducerVariableValue(t, file, name))
			require.NoError(t, err)
			require.Equal(t, "res://actors/enemy.gd", got)
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
			got, err := decodeResourceLiteral(testCase.expression)
			require.Error(t, err)
			require.Empty(t, got)
		})
	}
}

func TestResourceResolutionUsesClosedImmutableVocabulary(t *testing.T) {
	found := FoundResource(ResourceScript, "res://actors/enemy.gd", "actors/enemy.gd", ResourceLiteralPath)
	require.Equal(t, ResourceFound, found.State())
	require.Equal(t, ResourceScript, found.Kind())
	require.Equal(t, "res://actors/enemy.gd", found.Requested())
	require.Equal(t, "actors/enemy.gd", found.Path())
	require.Equal(t, ResourceLiteralPath, found.Provenance())
	require.Empty(t, found.Reason())
	failed := UnresolvedResource(ResourceAmbiguousUID, ResourceUnknown, "uid://enemy", ResourceUIDClaim, "uid://enemy has multiple claimants")
	require.Equal(t, ResourceAmbiguousUID, failed.State())
	require.Equal(t, ResourceUnknown, failed.Kind())
	require.Equal(t, "uid://enemy", failed.Requested())
	require.Empty(t, failed.Path())
	require.Equal(t, ResourceUIDClaim, failed.Provenance())
	require.NotEmpty(t, failed.Reason())
	for _, state := range []ResourceState{
		ResourceFound,
		ResourceMissing,
		ResourceInvalid,
		ResourceEscapesProject,
		ResourceAmbiguousUID,
		ResourceUnsupportedKind,
	} {
		require.NotEmpty(t, state.String())
	}
	for _, kind := range []ResourceKind{ResourceUnknown, ResourceScript, ResourceScene, ResourceText, ResourceImported} {
		require.NotEmpty(t, kind.String())
	}
	for _, provenance := range []ResourceProvenance{ResourceProvenanceUnknown, ResourceLiteralPath, ResourceUIDClaim} {
		require.NotEmpty(t, provenance.String())
	}
	for _, malformed := range []ResourceResolution{
		FoundResource(ResourceImported, "res://icon.png", "icon.png", ResourceLiteralPath),
		UnresolvedResource(ResourceUnsupportedKind, ResourceUnknown, "res://icon.png", ResourceLiteralPath, "unsupported"),
		UnresolvedResource(ResourceMissing, ResourceUnknown, "res://missing.gd", ResourceProvenanceUnknown, "missing"),
	} {
		require.NotEmpty(t, resourceResolutionProblem(malformed))
	}
	require.NotEmpty(t, resourceResolutionProblem(ResourceResolution{state: ResourceState(99)}))
}
