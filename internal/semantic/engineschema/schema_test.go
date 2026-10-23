package engineschema

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cafecito-games/gdkit/internal/semantic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fixturePath            = "testdata/extension_api_4_7_2_sample.json"
	officialRawFixturePath = "testdata/extension_api_4_7_2_official.json.gz"
	fixtureRawSHA256       = "106979f23c7131b0fd5997ab1b82d514e3fbbf83bf5709568f4e474deb5f05e5"
	officialRawSHA256      = "d0e4c08c03b165156dabe6bfb6a906baf0069189f62035341230a246c86d6986"
	officialArtifactSHA256 = "49363e14e1f8d5f0c82d0ebf89fcb97911d7acb0ae6f0c583e94311ac75bf6a2"
	officialRawBytes       = 6_965_057
	officialGodotCommit    = "ed1daf0bf"
)

func readRawFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(fixturePath)
	require.NoError(t, err)
	return data
}

func readOfficialRawFixture(t *testing.T) []byte {
	t.Helper()
	compressed, err := os.ReadFile(officialRawFixturePath)
	require.NoError(t, err)
	return decompressArtifactForTest(t, compressed)
}

// This fixture retains rows emitted by the official Godot 4.7.2 producer at
// core/extension/extension_api_dump.cpp:104-127,559-619,623-908,912-1283,
// and 1287-1305 (commit ed1daf0bf). The loader receives the checked-in bytes,
// rather than a hand-built Go value, so field presence and producer extras are
// exercised on the real JSON path.
func TestLoadRawAcceptsGodotProducedFixtureByteForByte(t *testing.T) {
	data := readRawFixture(t)
	loaded, err := LoadRaw(data, SourceOverride, "")
	require.NoError(t, err)
	require.NotNil(t, loaded.Engine)
	provenance := loaded.Provenance
	require.Equal(t, SourceOverride, provenance.Source)
	require.Equal(t, Version{Major: 4, Minor: 7, Patch: 2}, provenance.Version)
	require.Equal(t, "stable", provenance.Status)
	require.Equal(t, "official", provenance.Build)
	require.Equal(t, "Godot Engine v4.7.2.stable.official", provenance.FullName)
	require.Equal(t, fixtureRawSHA256, provenance.RawSHA256)
	require.NotEmpty(t, provenance.SchemaSHA256)
	require.Empty(t, provenance.SourceCommit)
	distilled, err := distillRaw(data, SourceOverride, "")
	require.NoError(t, err)
	constructible := map[string]bool{}
	for _, builtin := range distilled.Builtins {
		require.NotNil(t, builtin.Constructible)
		constructible[builtin.Name] = *builtin.Constructible
	}
	require.True(t, constructible["Vector2"])
	require.False(t, constructible["bool"])

	class := loaded.Engine.Class("Sprite2D")
	for _, want := range []string{"Sprite2D", "Node2D", "CanvasItem", "Node", "Object"} {
		require.Equal(t, semantic.KindClass, class.Kind())
		require.Equal(t, want, class.Name())
		if want == "Object" {
			break
		}
		var ok bool
		class, ok = class.Base()
		require.True(t, ok)
	}
	getNode, ok := loaded.Engine.Method("Node", "get_node")
	require.True(t, ok)
	require.Equal(t, "Node", getNode.ReturnType().Name())
	require.Len(t, getNode.Arguments(), 1)
	require.Equal(t, "NodePath", getNode.Arguments()[0].Type().Name())
	call, ok := loaded.Engine.Method("Object", "call")
	require.True(t, ok)
	require.Equal(t, semantic.KindVariant, call.ReturnType().Kind())
	require.True(t, call.Vararg())
	children, ok := loaded.Engine.Method("Node", "get_children")
	require.True(t, ok)
	require.Len(t, children.Arguments(), 1)
	require.True(t, children.Arguments()[0].HasDefault())
	element, elementOK := children.ReturnType().Element()
	require.Equal(t, semantic.KindArray, children.ReturnType().Kind())
	require.True(t, elementOK)
	require.Equal(t, "Node", element.Name())
	position, ok := loaded.Engine.Property("Node2D", "position")
	require.True(t, ok)
	require.Equal(t, "Vector2", position.Type().Name())
	for _, operator := range []struct{ name, right string }{{"+", "Vector2"}, {"*", "float"}} {
		result, found := loaded.Engine.Operator("Vector2", operator.name, operator.right)
		assert.True(t, found)
		assert.Equal(t, "Vector2", result.Name())
	}
}

// This gzip is only a repository-size wrapper. Its decompressed bytes are the
// complete, unmodified output of the official Godot 4.7.2 binary. Godot emits
// the retained header/tables at core/extension/extension_api_dump.cpp:104-127,
// 559-619,623-908,912-1283,1287-1305 (commit ed1daf0bf).
func TestLoadRawAcceptsOfficialGodot472DumpByteForByte(t *testing.T) {
	raw := readOfficialRawFixture(t)
	require.Equal(t, officialRawBytes, len(raw))
	require.Equal(t, officialRawSHA256, digestBytes(raw))
	var producer rawDocument
	err := json.Unmarshal(raw, &producer)
	require.NoError(t, err)
	require.False(t, len(producer.Classes) <= semantic.EngineInheritanceDepthLimit)
	constructorRows := 0
	for _, builtin := range producer.BuiltinClasses {
		require.NotNil(t, builtin.Constructors)
		require.NotEmpty(t, builtin.Constructors)
		constructorRows += len(builtin.Constructors)
	}
	require.Len(t, producer.BuiltinClasses, 38)
	require.Equal(t, 156, constructorRows)
	distilled, err := distillRaw(raw, SourceOverride, "")
	require.NoError(t, err)
	for _, builtin := range distilled.Builtins {
		require.NotNil(t, builtin.Constructible)
		require.True(t, *builtin.Constructible)
	}
	loaded, err := LoadRaw(raw, SourceOverride, "")
	require.NoError(t, err)
	require.Equal(t, Version{Major: 4, Minor: 7, Patch: 2}, loaded.Provenance.Version)
	require.Equal(t, "official", loaded.Provenance.Build)
	require.Equal(t, officialRawSHA256, loaded.Provenance.RawSHA256)
	assertCoreEngineFacts(t, loaded.Engine)
}

func TestLoadRawSizeLimitBoundary(t *testing.T) {
	fixture := readRawFixture(t)
	atLimit := make([]byte, MaxRawJSONBytes)
	copy(atLimit, fixture)
	for index := len(fixture); index < len(atLimit); index++ {
		atLimit[index] = ' '
	}
	readAtLimit, err := ReadRawJSON(bytes.NewReader(atLimit))
	require.NoError(t, err)
	require.Len(t, readAtLimit, MaxRawJSONBytes)
	loaded, err := LoadRaw(readAtLimit, SourceOverride, "")
	require.NoError(t, err)
	require.NotNil(t, loaded)

	reader := &countingRawReader{remaining: MaxRawJSONBytes + 1024}
	data, err := ReadRawJSON(reader)
	require.Error(t, err)
	require.Nil(t, data)
	require.ErrorIs(t, err, ErrRawInvalid)
	require.Equal(t, int64(MaxRawJSONBytes+1), reader.read)

	aboveLimit := make([]byte, MaxRawJSONBytes+1)
	loaded, err = LoadRaw(aboveLimit, SourceOverride, "")
	require.Error(t, err)
	require.Nil(t, loaded)
	require.ErrorIs(t, err, ErrRawInvalid)
	require.Contains(t, err.Error(), "exceeds")
}

type countingRawReader struct {
	remaining int64
	read      int64
}

func (r *countingRawReader) Read(buffer []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	count := int64(len(buffer))
	if count > r.remaining {
		count = r.remaining
	}
	for index := range buffer[:count] {
		buffer[index] = ' '
	}
	r.remaining -= count
	r.read += count
	return int(count), nil
}

func TestLoadRawRejectsTypeNestingBeforePublication(t *testing.T) {
	for _, depth := range []int{semantic.EngineTypeNestingLimit, semantic.EngineTypeNestingLimit + 1} {
		t.Run(fmt.Sprintf("depth-%d", depth), func(t *testing.T) {
			data := mutateRawFixture(t, mutateNestedNamedRow("classes", "Node2D", "properties", "position", func(row map[string]any) {
				row["type"] = strings.Repeat("typedarray::", depth) + "Object"
			}))
			loaded, err := LoadRaw(data, SourceOverride, "")
			if depth == semantic.EngineTypeNestingLimit {
				require.NoError(t, err)
				require.NotNil(t, loaded)
				return
			}
			require.Error(t, err)
			require.Nil(t, loaded)
			require.ErrorIs(t, err, ErrRawInvalid)
			require.Contains(t, err.Error(), "nesting exceeds")
		})
	}
}

func TestLoadRawRejectsInheritanceDepthBeforePublication(t *testing.T) {
	for _, count := range []int{semantic.EngineInheritanceDepthLimit, semantic.EngineInheritanceDepthLimit + 1} {
		t.Run(fmt.Sprintf("classes-%d", count), func(t *testing.T) {
			data := mutateRawFixture(t, func(document map[string]any) {
				classes := make([]any, count)
				parent := ""
				for index := range count {
					name := fmt.Sprintf("DepthClass%04d", index)
					classes[index] = map[string]any{"name": name, "inherits": parent}
					parent = name
				}
				document["classes"] = classes
			})
			loaded, err := LoadRaw(data, SourceOverride, "")
			if count == semantic.EngineInheritanceDepthLimit {
				require.NoError(t, err)
				require.NotNil(t, loaded)
				return
			}
			require.Error(t, err)
			require.Nil(t, loaded)
			require.ErrorIs(t, err, ErrRawInvalid)
			require.Contains(t, err.Error(), "inheritance depth exceeds")
		})
	}
}

func TestOfficialGodot472RegeneratesCommittedArtifactByteForByte(t *testing.T) {
	generated, err := GenerateArtifact(readOfficialRawFixture(t), officialGodotCommit)
	require.NoError(t, err)
	require.Equal(t, godot47Artifact, generated)
	require.Equal(t, officialArtifactSHA256, digestBytes(generated))
}

func TestSourceKindsAreClosedAndValidated(t *testing.T) {
	data := readRawFixture(t)
	tests := []struct {
		source SourceKind
		commit string
		valid  bool
	}{
		{source: SourceEmbedded, commit: officialGodotCommit, valid: true},
		{source: SourceOverride, valid: true},
		{source: SourceKind("future"), valid: false},
		{source: "", valid: false},
	}
	for _, test := range tests {
		t.Run(string(test.source), func(t *testing.T) {
			loaded, err := LoadRaw(data, test.source, test.commit)
			if test.valid {
				require.NoError(t, err)
				require.Equal(t, test.source, loaded.Provenance.Source)
				return
			}
			require.Error(t, err)
			require.ErrorIs(t, err, ErrRawInvalid)
		})
	}
}

func TestGenerateArtifactIsDeterministicAndIdentityBound(t *testing.T) {
	data := readRawFixture(t)
	first, err := GenerateArtifact(data, officialGodotCommit)
	require.NoError(t, err)
	second, err := GenerateArtifact(data, officialGodotCommit)
	require.NoError(t, err)
	require.Equal(t, first, second, "the same raw input produced different artifact bytes")
	otherIdentity, err := GenerateArtifact(data, "different-commit")
	require.NoError(t, err)
	require.NotEqual(t, first, otherIdentity, "changing source identity did not change the artifact")

	reader, err := gzip.NewReader(bytes.NewReader(first))
	require.NoError(t, err)
	require.True(t, reader.ModTime.IsZero())
	require.Empty(t, reader.Name)
	require.Empty(t, reader.Comment)
	require.Empty(t, reader.Extra)
	uncompressed, err := io.ReadAll(reader)
	require.NoError(t, err)
	err = reader.Close()
	require.NoError(t, err)
	require.False(t, bytes.Contains(uncompressed, []byte(fixturePath)), "artifact identity includes a path or timestamp")
	require.False(t, bytes.Contains(uncompressed, []byte("generated_at")), "artifact identity includes a path or timestamp")

	document, digest, err := decodeArtifactJSON(uncompressed)
	require.NoError(t, err)
	require.NotEmpty(t, digest)
	require.Equal(t, officialGodotCommit, document.Provenance.SourceCommit)
	require.True(t, sort.SliceIsSorted(document.Classes, func(i, j int) bool { return document.Classes[i].Name < document.Classes[j].Name }))
	require.True(t, sort.SliceIsSorted(document.Methods, func(i, j int) bool {
		return document.Methods[i].Owner < document.Methods[j].Owner ||
			(document.Methods[i].Owner == document.Methods[j].Owner && document.Methods[i].Name < document.Methods[j].Name)
	}))
	loaded, err := LoadArtifact(first)
	require.NoError(t, err)
	require.Equal(t, digest, loaded.Provenance.SchemaSHA256)
	require.Equal(t, fixtureRawSHA256, loaded.Provenance.RawSHA256)
	require.Equal(t, SourceEmbedded, loaded.Provenance.Source)
}

func TestRawValidationRejectsEveryDuplicateIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "builtin", mutate: duplicateTopLevel("builtin_classes", 0)},
		{name: "class", mutate: duplicateTopLevel("classes", 0)},
		{name: "method", mutate: duplicateNested("classes", "Node", "methods", 0)},
		{name: "property", mutate: duplicateNested("classes", "Node2D", "properties", 0)},
		{name: "operator", mutate: duplicateNested("builtin_classes", "Vector2", "operators", 0)},
		{name: "singleton", mutate: duplicateTopLevel("singletons", 0)},
		{name: "utility", mutate: duplicateTopLevel("utility_functions", 0)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := mutateRawFixture(t, test.mutate)
			loaded, err := LoadRaw(data, SourceOverride, "")
			require.Error(t, err)
			require.ErrorIs(t, err, ErrRawInvalid)
			require.Nil(t, loaded)
			require.Contains(t, err.Error(), "duplicate")
		})
	}
}

func TestRawValidationDistinguishesMalformedFromInvalid(t *testing.T) {
	for _, malformed := range [][]byte{
		[]byte(`{"header":`),
		append(readRawFixture(t), []byte(` {}`)...),
	} {
		loaded, err := LoadRaw(malformed, SourceOverride, "")
		require.Error(t, err)
		require.ErrorIs(t, err, ErrRawParse)
		require.Nil(t, loaded)
	}

	for _, key := range []string{"header", "builtin_classes", "classes", "singletons", "utility_functions"} {
		t.Run("missing-"+key, func(t *testing.T) {
			data := mutateRawFixture(t, func(document map[string]any) { delete(document, key) })
			loaded, err := LoadRaw(data, SourceOverride, "")
			require.Error(t, err)
			require.ErrorIs(t, err, ErrRawInvalid)
			require.Nil(t, loaded)
			require.Contains(t, err.Error(), key)
		})
	}
}

func TestRawValidationRejectsMalformedRetainedRows(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "header field", mutate: func(document map[string]any) {
			delete(document["header"].(map[string]any), "version_build")
		}},
		{name: "header numeric type", mutate: func(document map[string]any) {
			document["header"].(map[string]any)["version_patch"] = "2"
		}},
		{name: "builtin name", mutate: mutateNamedRow("builtin_classes", "Vector2", func(row map[string]any) {
			delete(row, "name")
		})},
		{name: "class name", mutate: mutateNamedRow("classes", "Node", func(row map[string]any) {
			row["name"] = " Node"
		})},
		{name: "class inheritance cycle", mutate: mutateNamedRow("classes", "Object", func(row map[string]any) {
			row["inherits"] = "Node"
		})},
		{name: "method name", mutate: mutateNestedNamedRow("classes", "Node", "methods", "get_node", func(row map[string]any) {
			delete(row, "name")
		})},
		{name: "method static flag", mutate: mutateNestedNamedRow("classes", "Node", "methods", "get_node", func(row map[string]any) {
			delete(row, "is_static")
		})},
		{name: "method virtual flag", mutate: mutateNestedNamedRow("classes", "Node", "methods", "get_node", func(row map[string]any) {
			delete(row, "is_virtual")
		})},
		{name: "method vararg flag", mutate: mutateNestedNamedRow("classes", "Object", "methods", "call", func(row map[string]any) {
			row["is_vararg"] = nil
		})},
		{name: "method return", mutate: mutateNestedNamedRow("classes", "Node", "methods", "get_node", func(row map[string]any) {
			row["return_value"] = map[string]any{}
		})},
		{name: "method competing returns", mutate: mutateNestedNamedRow("classes", "Node", "methods", "get_node", func(row map[string]any) {
			row["return_type"] = "Node"
		})},
		{name: "argument type", mutate: mutateNestedNamedRow("classes", "Node", "methods", "get_node", func(row map[string]any) {
			delete(row["arguments"].([]any)[0].(map[string]any), "type")
		})},
		{name: "argument default shape", mutate: mutateNestedNamedRow("classes", "Node", "methods", "get_children", func(row map[string]any) {
			row["arguments"].([]any)[0].(map[string]any)["default_value"] = map[string]any{}
		})},
		{name: "property name", mutate: mutateNestedNamedRow("classes", "Node2D", "properties", "position", func(row map[string]any) {
			row["name"] = ""
		})},
		{name: "property type", mutate: mutateNestedNamedRow("classes", "Node2D", "properties", "position", func(row map[string]any) {
			delete(row, "type")
		})},
		{name: "operator name", mutate: mutateNestedNamedRow("builtin_classes", "Vector2", "operators", "+", func(row map[string]any) {
			delete(row, "name")
		})},
		{name: "operator right type", mutate: mutateNestedNamedRow("builtin_classes", "Vector2", "operators", "+", func(row map[string]any) {
			row["right_type"] = " Vector2"
		})},
		{name: "operator return type", mutate: mutateNestedNamedRow("builtin_classes", "Vector2", "operators", "+", func(row map[string]any) {
			delete(row, "return_type")
		})},
		{name: "singleton type", mutate: mutateNamedRow("singletons", "Engine", func(row map[string]any) {
			delete(row, "type")
		})},
		{name: "utility name", mutate: mutateNamedRow("utility_functions", "typeof", func(row map[string]any) {
			row["name"] = " "
		})},
		{name: "utility vararg flag", mutate: mutateNamedRow("utility_functions", "typeof", func(row map[string]any) {
			delete(row, "is_vararg")
		})},
		{name: "utility return shape", mutate: mutateNamedRow("utility_functions", "typeof", func(row map[string]any) {
			row["return_type"] = nil
		})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loaded, err := LoadRaw(mutateRawFixture(t, test.mutate), SourceOverride, "")
			require.Error(t, err)
			require.ErrorIs(t, err, ErrRawInvalid)
			require.Nil(t, loaded)
		})
	}
}

func TestRawBuiltinConstructorValidationFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing", mutate: mutateNamedRow("builtin_classes", "Vector2", func(row map[string]any) {
			delete(row, "constructors")
		})},
		{name: "null", mutate: mutateNamedRow("builtin_classes", "Vector2", func(row map[string]any) {
			row["constructors"] = nil
		})},
		{name: "wrong shape", mutate: mutateNamedRow("builtin_classes", "Vector2", func(row map[string]any) {
			row["constructors"] = map[string]any{}
		})},
		{name: "non-object row", mutate: mutateNamedRow("builtin_classes", "Vector2", func(row map[string]any) {
			row["constructors"] = []any{1}
		})},
		{name: "missing index", mutate: mutateNamedRow("builtin_classes", "Vector2", func(row map[string]any) {
			delete(row["constructors"].([]any)[0].(map[string]any), "index")
		})},
		{name: "negative index", mutate: mutateNamedRow("builtin_classes", "Vector2", func(row map[string]any) {
			row["constructors"].([]any)[0].(map[string]any)["index"] = -1
		})},
		{name: "duplicate index", mutate: mutateNamedRow("builtin_classes", "Vector2", func(row map[string]any) {
			row["constructors"].([]any)[1].(map[string]any)["index"] = 0
		})},
		{name: "noncanonical index", mutate: mutateNamedRow("builtin_classes", "Vector2", func(row map[string]any) {
			row["constructors"].([]any)[1].(map[string]any)["index"] = 2
		})},
		{name: "fractional index", mutate: mutateNamedRow("builtin_classes", "Vector2", func(row map[string]any) {
			row["constructors"].([]any)[0].(map[string]any)["index"] = 0.5
		})},
		{name: "null arguments", mutate: mutateNamedRow("builtin_classes", "Vector2", func(row map[string]any) {
			row["constructors"].([]any)[1].(map[string]any)["arguments"] = nil
		})},
		{name: "wrong arguments shape", mutate: mutateNamedRow("builtin_classes", "Vector2", func(row map[string]any) {
			row["constructors"].([]any)[1].(map[string]any)["arguments"] = map[string]any{}
		})},
		{name: "non-object argument", mutate: mutateNamedRow("builtin_classes", "Vector2", func(row map[string]any) {
			row["constructors"].([]any)[1].(map[string]any)["arguments"] = []any{1}
		})},
		{name: "missing argument type", mutate: mutateNamedRow("builtin_classes", "Vector2", func(row map[string]any) {
			delete(row["constructors"].([]any)[1].(map[string]any)["arguments"].([]any)[0].(map[string]any), "type")
		})},
		{name: "unretained argument type", mutate: mutateNamedRow("builtin_classes", "Vector2", func(row map[string]any) {
			row["constructors"].([]any)[1].(map[string]any)["arguments"].([]any)[0].(map[string]any)["type"] = "MissingType"
		})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loaded, err := LoadRaw(mutateRawFixture(t, test.mutate), SourceOverride, "")
			require.Error(t, err)
			require.Nil(t, loaded)
			require.ErrorIs(t, err, ErrRawInvalid)
		})
	}
}

func TestRawValidationIgnoresUnknownProducerFields(t *testing.T) {
	data := mutateRawFixture(t, func(document map[string]any) {
		document["future_table"] = []any{map[string]any{"unknown": true}}
		mutateNamedRow("classes", "Node", func(row map[string]any) {
			row["future_class_field"] = map[string]any{"unknown": true}
		})(document)
	})
	loaded, err := LoadRaw(data, SourceOverride, "")
	require.NoError(t, err)
	require.NotNil(t, loaded)
}

func TestEmbeddedRegistryHasOnlyExact47AndOfficialProvenance(t *testing.T) {
	got := SupportedMinors()
	require.Len(t, got, 1)
	require.Equal(t, MinorVersion{Major: 4, Minor: 7}, got[0])
	for _, version := range []MinorVersion{{Major: 4, Minor: 6}, {Major: 4, Minor: 8}, {Major: 5, Minor: 0}} {
		loaded, err := LoadEmbedded(version.Major, version.Minor)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrUnsupportedVersion)
		require.Nil(t, loaded)
	}
	loaded, err := LoadEmbedded(4, 7)
	require.NoError(t, err)
	wantVersion := Version{Major: 4, Minor: 7, Patch: 2}
	require.Equal(t, SourceEmbedded, loaded.Provenance.Source)
	require.Equal(t, wantVersion, loaded.Provenance.Version)
	require.Equal(t, officialRawSHA256, loaded.Provenance.RawSHA256)
	require.Equal(t, officialGodotCommit, loaded.Provenance.SourceCommit)
	require.Equal(t, semantic.KindClass, loaded.Engine.Class("Animation").Kind())
	assertCoreEngineFacts(t, loaded.Engine)
	override, err := LoadRaw(readRawFixture(t), SourceOverride, "")
	require.NoError(t, err)
	require.Equal(t, semantic.KindUnknown, override.Engine.Class("Animation").Kind())
	loaded.Provenance.Source = SourceOverride
	again, err := LoadEmbedded(4, 7)
	require.NoError(t, err)
	require.NotSame(t, loaded, again)
	require.Same(t, loaded.Engine, again.Engine)
	require.Equal(t, SourceEmbedded, again.Provenance.Source)
}

func TestArtifactRejectsDigestTamperingAndNonCanonicalRecords(t *testing.T) {
	blob, err := GenerateArtifact(readRawFixture(t), officialGodotCommit)
	require.NoError(t, err)
	payload := decompressArtifactForTest(t, blob)
	var envelope artifact
	err = json.Unmarshal(payload, &envelope)
	require.NoError(t, err)

	t.Run("digest", func(t *testing.T) {
		tampered := envelope
		tampered.Digest = strings.Repeat("0", sha256.Size*2)
		loaded, err := LoadArtifact(compressArtifactForTest(t, tampered))
		require.Error(t, err)
		require.Nil(t, loaded)
		require.Contains(t, err.Error(), "digest")
	})

	t.Run("record order", func(t *testing.T) {
		tampered := envelope
		tampered.Schema.Classes = append([]classRecord(nil), envelope.Schema.Classes...)
		tampered.Schema.Classes[0], tampered.Schema.Classes[1] = tampered.Schema.Classes[1], tampered.Schema.Classes[0]
		tampered.Digest, err = schemaDigest(tampered.Schema)
		require.NoError(t, err)
		loaded, loadErr := LoadArtifact(compressArtifactForTest(t, tampered))
		require.Error(t, loadErr)
		require.Nil(t, loaded)
		require.Contains(t, loadErr.Error(), "canonical order")
	})

	t.Run("missing constructibility", func(t *testing.T) {
		tampered := envelope
		tampered.Schema.Builtins = append([]builtinRecord(nil), envelope.Schema.Builtins...)
		tampered.Schema.Builtins[0].Constructible = nil
		tampered.Digest, err = schemaDigest(tampered.Schema)
		require.NoError(t, err)
		loaded, loadErr := LoadArtifact(compressArtifactForTest(t, tampered))
		require.Error(t, loadErr)
		require.Nil(t, loaded)
		require.Contains(t, loadErr.Error(), "constructible")
	})

	t.Run("stale schema version", func(t *testing.T) {
		tampered := envelope
		tampered.Schema.Version = 1
		tampered.Digest, err = schemaDigest(tampered.Schema)
		require.NoError(t, err)
		loaded, loadErr := LoadArtifact(compressArtifactForTest(t, tampered))
		require.Error(t, loadErr)
		require.Nil(t, loaded)
		require.Contains(t, loadErr.Error(), "unsupported engine schema version 1")
	})
}

func TestLoadRegistryArtifactRequiresExactIdentity(t *testing.T) {
	blob, err := GenerateArtifact(readRawFixture(t), officialGodotCommit)
	require.NoError(t, err)
	baseline, err := LoadArtifact(blob)
	require.NoError(t, err)
	matching := registryIdentity{
		version:      baseline.Provenance.Version,
		rawSHA256:    baseline.Provenance.RawSHA256,
		sourceCommit: baseline.Provenance.SourceCommit,
		schemaSHA256: baseline.Provenance.SchemaSHA256,
	}

	var envelope artifact
	err = json.Unmarshal(decompressArtifactForTest(t, blob), &envelope)
	require.NoError(t, err)
	envelope.Schema.Provenance.Source = SourceOverride
	envelope.Schema.Provenance.SourceCommit = ""
	envelope.Digest, err = schemaDigest(envelope.Schema)
	require.NoError(t, err)
	overrideBlob := compressArtifactForTest(t, envelope)

	tests := []struct {
		name     string
		blob     []byte
		mutate   func(*registryIdentity)
		accepted bool
	}{
		{name: "matching", blob: blob, accepted: true},
		{name: "source", blob: overrideBlob},
		{name: "version", blob: blob, mutate: func(identity *registryIdentity) { identity.version.Patch++ }},
		{name: "raw digest", blob: blob, mutate: func(identity *registryIdentity) { identity.rawSHA256 = strings.Repeat("0", sha256.Size*2) }},
		{name: "source commit", blob: blob, mutate: func(identity *registryIdentity) { identity.sourceCommit = "different-commit" }},
		{name: "schema digest", blob: blob, mutate: func(identity *registryIdentity) { identity.schemaSHA256 = strings.Repeat("0", sha256.Size*2) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := matching
			if test.mutate != nil {
				test.mutate(&identity)
			}
			loaded, err := loadRegistryArtifact(test.blob, identity)
			if test.accepted {
				require.NoError(t, err)
				require.NotNil(t, loaded)
				require.NotNil(t, loaded.Engine)
				return
			}
			require.Error(t, err)
			require.Nil(t, loaded)
			require.Contains(t, err.Error(), "does not match its registry identity")
		})
	}
}

func TestLazyArtifactConcurrentLoadsPublishOneCompleteEngine(t *testing.T) {
	blob, err := GenerateArtifact(readRawFixture(t), officialGodotCommit)
	require.NoError(t, err)
	var calls atomic.Int32
	lazy := newLazyArtifact(blob, func(data []byte) (*Loaded, error) {
		calls.Add(1)
		return LoadArtifact(data)
	})

	const workers = 32
	results := make([]*Loaded, workers)
	errorsFound := make([]error, workers)
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[index], errorsFound[index] = lazy.Load()
		}()
	}
	wait.Wait()
	require.Equal(t, int32(1), calls.Load())
	for index := range results {
		require.NoError(t, errorsFound[index])
		require.NotNil(t, results[index])
		require.NotNil(t, results[index].Engine)
		require.Same(t, results[0], results[index])
		require.Same(t, results[0].Engine, results[index].Engine)
	}
}

func TestCorruptArtifactNeverPublishesPartialState(t *testing.T) {
	lazy := newLazyArtifact([]byte("not gzip"), LoadArtifact)
	for attempt := 0; attempt < 2; attempt++ {
		loaded, err := lazy.Load()
		require.Error(t, err)
		require.Nil(t, loaded)
	}
}

func TestOfficialArtifactResolvesOrExplainsEveryRetainedTypeSpelling(t *testing.T) {
	// Godot's 4.7 dump retains two vocabularies that are intentionally outside
	// gdkit's semantic type model: native ABI pointers and comma-separated
	// resource constraints. Pin every such spelling so a newly unresolved type
	// fails this test, and so adding support requires removing its exact entry.
	wantUnknown := map[string]bool{}
	for _, spelling := range []string{
		"AudioFrame*",
		"BaseMaterial3D,ShaderMaterial",
		"CameraAttributesPractical,CameraAttributesPhysical",
		"CanvasItemMaterial,ShaderMaterial",
		"CaretInfo*",
		"Cubemap,CompressedCubemap,PlaceholderCubemap,TextureCubemapRD",
		"CurveTexture,CurveXYZTexture",
		"FogMaterial,ShaderMaterial",
		"Mesh,-PlaneMesh,-PointMesh,-QuadMesh,-RibbonTrailMesh",
		"PanoramaSkyMaterial,ProceduralSkyMaterial,PhysicalSkyMaterial,ShaderMaterial",
		"ParticleProcessMaterial,ShaderMaterial",
		"PhysicsServer2DExtensionMotionResult*",
		"PhysicsServer2DExtensionRayResult*",
		"PhysicsServer2DExtensionShapeRestInfo*",
		"PhysicsServer2DExtensionShapeResult*",
		"PhysicsServer3DExtensionMotionResult*",
		"PhysicsServer3DExtensionRayResult*",
		"PhysicsServer3DExtensionShapeRestInfo*",
		"PhysicsServer3DExtensionShapeResult*",
		"ScriptLanguageExtensionProfilingInfo*",
		"Texture2D,-AnimatedTexture,-AtlasTexture,-CameraTexture,-CanvasTexture,-MeshTexture,-Texture2DRD,-ViewportTexture",
		"Texture2D,Texture3D",
		"Texture2DArray,CompressedTexture2DArray,PlaceholderTexture2DArray,Texture2DArrayRD",
		"const GDExtensionInitializationFunction*",
		"const Glyph*",
		"const uint8_t **",
		"const uint8_t*",
		"const void*",
		"float*",
		"int32_t*",
		"uint8_t*",
		"void*",
	} {
		wantUnknown[spelling] = true
	}

	loaded, err := LoadEmbedded(4, 7)
	require.NoError(t, err)
	payload := decompressArtifactForTest(t, godot47Artifact)
	document, _, err := decodeArtifactJSON(payload)
	require.NoError(t, err)
	spellings := map[string]bool{}
	for _, class := range document.Classes {
		if class.Inherits != "" {
			spellings[class.Inherits] = true
		}
	}
	for _, method := range document.Methods {
		spellings[method.ReturnType] = true
		for _, argument := range method.Arguments {
			spellings[argument.Type] = true
		}
	}
	for _, property := range document.Properties {
		spellings[property.Type] = true
	}
	for _, operator := range document.Operators {
		if operator.Right != "" {
			spellings[operator.Right] = true
		}
		spellings[operator.ReturnType] = true
	}
	for _, singleton := range document.Singletons {
		spellings[singleton.Type] = true
	}
	for _, utility := range document.Utilities {
		spellings[utility.ReturnType] = true
		for _, argument := range utility.Arguments {
			spellings[argument.Type] = true
		}
	}
	gotUnknown := map[string]string{}
	for spelling := range spellings {
		resolved := loaded.Engine.ResolveType(spelling)
		if resolved.Kind() == semantic.KindUnknown {
			gotUnknown[spelling] = resolved.Reason()
		}
		assertContainerComponentsResolved(t, spelling, resolved)
	}
	for spelling := range gotUnknown {
		assert.Contains(t, wantUnknown, spelling)
	}
	for spelling := range wantUnknown {
		assert.Contains(t, gotUnknown, spelling)
	}
}

func assertContainerComponentsResolved(t *testing.T, spelling string, resolved semantic.Type) {
	t.Helper()
	if element, ok := resolved.Element(); ok {
		if element.Kind() == semantic.KindUnknown {
			assert.NotEqual(t, semantic.KindUnknown, element.Kind())
		} else {
			assertContainerComponentsResolved(t, spelling, element)
		}
	}
	if key, ok := resolved.Key(); ok {
		if key.Kind() == semantic.KindUnknown {
			assert.NotEqual(t, semantic.KindUnknown, key.Kind())
		} else {
			assertContainerComponentsResolved(t, spelling, key)
		}
	}
	if value, ok := resolved.Value(); ok {
		if value.Kind() == semantic.KindUnknown {
			assert.NotEqual(t, semantic.KindUnknown, value.Kind())
		} else {
			assertContainerComponentsResolved(t, spelling, value)
		}
	}
}

func BenchmarkLoadEmbeddedArtifactCold(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		loaded, err := LoadArtifact(godot47Artifact)
		require.NoError(b, err)
		require.NotNil(b, loaded)
	}
}

func BenchmarkLoadOfficialRawOverrideCold(b *testing.B) {
	name := os.Getenv("GDKIT_EXTENSION_API")
	if name == "" {
		b.Skip("set GDKIT_EXTENSION_API to an official raw extension_api.json")
	}
	raw, err := os.ReadFile(name)
	require.NoError(b, err)
	got := digestBytes(raw)
	require.Equal(b, officialRawSHA256, got)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		loaded, err := LoadRaw(raw, SourceOverride, "")
		require.NoError(b, err)
		require.NotNil(b, loaded)
	}
}

func duplicateTopLevel(key string, index int) func(map[string]any) {
	return func(document map[string]any) {
		rows := document[key].([]any)
		document[key] = append(rows, rows[index])
	}
}

func duplicateNested(topLevel, owner, nested string, index int) func(map[string]any) {
	return func(document map[string]any) {
		for _, value := range document[topLevel].([]any) {
			row := value.(map[string]any)
			if row["name"] != owner {
				continue
			}
			rows := row[nested].([]any)
			row[nested] = append(rows, rows[index])
			return
		}
		panic("owner not found in fixture: " + owner)
	}
}

func mutateNamedRow(table, name string, mutate func(map[string]any)) func(map[string]any) {
	return func(document map[string]any) {
		for _, value := range document[table].([]any) {
			row := value.(map[string]any)
			if row["name"] == name {
				mutate(row)
				return
			}
		}
		panic("row not found in fixture: " + table + "/" + name)
	}
}

func mutateNestedNamedRow(table, owner, nested, name string, mutate func(map[string]any)) func(map[string]any) {
	return mutateNamedRow(table, owner, func(row map[string]any) {
		for _, value := range row[nested].([]any) {
			nestedRow := value.(map[string]any)
			if nestedRow["name"] == name {
				mutate(nestedRow)
				return
			}
		}
		panic("nested row not found in fixture: " + table + "/" + owner + "/" + nested + "/" + name)
	})
}

func mutateRawFixture(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	var document map[string]any
	err := json.Unmarshal(readRawFixture(t), &document)
	require.NoError(t, err)
	mutate(document)
	data, err := json.Marshal(document)
	require.NoError(t, err)
	return data
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func decompressArtifactForTest(t *testing.T, blob []byte) []byte {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(blob))
	require.NoError(t, err)
	payload, err := io.ReadAll(reader)
	require.NoError(t, err)
	err = reader.Close()
	require.NoError(t, err)
	return payload
}

func compressArtifactForTest(t *testing.T, value artifact) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err = writer.Write(payload)
	require.NoError(t, err)
	err = writer.Close()
	require.NoError(t, err)
	return compressed.Bytes()
}

// The is_virtual flag is retained per class method and must survive both the raw
// loader and the embedded artifact, because lint's unused-private-member rule
// resolves a script's base chain against it.
func TestEngineRecordsVirtualMethods(t *testing.T) {
	loaded, err := LoadEmbedded(4, 7)
	require.NoError(t, err)
	engine := loaded.Engine
	for _, virtual := range []struct{ owner, name string }{
		{"Node", "_ready"},
		{"Node", "_process"},
		{"CanvasItem", "_draw"},
		{"Control", "_gui_input"},
		{"Resource", "_setup_local_to_scene"},
		{"BaseButton", "_toggled"},
		{"MainLoop", "_initialize"},
		{"Logger", "_log_error"},
	} {
		assert.True(t, engine.VirtualMethod(virtual.owner, virtual.name))
	}
	for _, ordinary := range []struct{ owner, name string }{
		{"Object", "call"},
		{"Node", "get_node"},
	} {
		assert.False(t, engine.VirtualMethod(ordinary.owner, ordinary.name))
	}
}

func assertCoreEngineFacts(t *testing.T, engine *semantic.Engine) {
	t.Helper()
	class := engine.Class("Sprite2D")
	for _, want := range []string{"Sprite2D", "Node2D", "CanvasItem", "Node", "Object"} {
		require.Equal(t, semantic.KindClass, class.Kind())
		require.Equal(t, want, class.Name())
		if want == "Object" {
			break
		}
		var ok bool
		class, ok = class.Base()
		require.True(t, ok)
	}
	getNode, ok := engine.Method("Node", "get_node")
	require.True(t, ok)
	require.Equal(t, "Node", getNode.ReturnType().Name())
	require.Len(t, getNode.Arguments(), 1)
	require.Equal(t, "NodePath", getNode.Arguments()[0].Type().Name())
	call, ok := engine.Method("Object", "call")
	require.True(t, ok)
	require.Equal(t, semantic.KindVariant, call.ReturnType().Kind())
	position, ok := engine.Property("Node2D", "position")
	require.True(t, ok)
	require.Equal(t, "Vector2", position.Type().Name())
	for _, operand := range []struct{ operator, right string }{{"+", "Vector2"}, {"*", "float"}} {
		result, found := engine.Operator("Vector2", operand.operator, operand.right)
		require.True(t, found)
		require.Equal(t, "Vector2", result.Name())
	}
}
