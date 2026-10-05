package engineschema

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cafecito-games/gdkit/internal/semantic"
)

const (
	fixturePath            = "testdata/extension_api_4_7_2_sample.json"
	officialRawFixturePath = "testdata/extension_api_4_7_2_official.json.gz"
	fixtureRawSHA256       = "a9bf8cda0343d4ea7440611c3a1f1e18c450f44f2663093e14457262433dfdf3"
	officialRawSHA256      = "d0e4c08c03b165156dabe6bfb6a906baf0069189f62035341230a246c86d6986"
	officialArtifactSHA256 = "bf23992dfff8d700515596254186e13e91df374aa4d13fe4dd74f4ef7792d7f6"
	officialRawBytes       = 6_965_057
	officialGodotCommit    = "ed1daf0bf"
)

func readRawFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readOfficialRawFixture(t *testing.T) []byte {
	t.Helper()
	compressed, err := os.ReadFile(officialRawFixturePath)
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Engine == nil {
		t.Fatal("LoadRaw returned no Engine")
	}
	provenance := loaded.Provenance
	if provenance.Source != SourceOverride || provenance.Version != (Version{Major: 4, Minor: 7, Patch: 2}) ||
		provenance.Status != "stable" || provenance.Build != "official" ||
		provenance.FullName != "Godot Engine v4.7.2.stable.official" || provenance.RawSHA256 != fixtureRawSHA256 ||
		provenance.SchemaSHA256 == "" || provenance.SourceCommit != "" {
		t.Fatalf("provenance = %+v", provenance)
	}

	class := loaded.Engine.Class("Sprite2D")
	for _, want := range []string{"Sprite2D", "Node2D", "CanvasItem", "Node", "Object"} {
		if class.Kind() != semantic.KindClass || class.Name() != want {
			t.Fatalf("class chain at %s = kind %v name %q reason %q", want, class.Kind(), class.Name(), class.Reason())
		}
		if want == "Object" {
			break
		}
		var ok bool
		class, ok = class.Base()
		if !ok {
			t.Fatalf("%s has no base", want)
		}
	}
	getNode, ok := loaded.Engine.Method("Node", "get_node")
	if !ok || getNode.ReturnType().Name() != "Node" || len(getNode.Arguments()) != 1 || getNode.Arguments()[0].Type().Name() != "NodePath" {
		t.Fatalf("Node.get_node = %#v, %t", getNode, ok)
	}
	call, ok := loaded.Engine.Method("Object", "call")
	if !ok || call.ReturnType().Kind() != semantic.KindVariant || !call.Vararg() {
		t.Fatalf("Object.call = %#v, %t", call, ok)
	}
	children, ok := loaded.Engine.Method("Node", "get_children")
	if !ok || len(children.Arguments()) != 1 || !children.Arguments()[0].HasDefault() {
		t.Fatalf("Node.get_children = %#v, %t", children, ok)
	}
	element, elementOK := children.ReturnType().Element()
	if children.ReturnType().Kind() != semantic.KindArray || !elementOK || element.Name() != "Node" {
		t.Fatalf("Node.get_children return = kind %v element %q/%t", children.ReturnType().Kind(), element.Name(), elementOK)
	}
	position, ok := loaded.Engine.Property("Node2D", "position")
	if !ok || position.Type().Name() != "Vector2" {
		t.Fatalf("Node2D.position = %#v, %t", position, ok)
	}
	for _, operator := range []struct{ name, right string }{{"+", "Vector2"}, {"*", "float"}} {
		result, found := loaded.Engine.Operator("Vector2", operator.name, operator.right)
		if !found || result.Name() != "Vector2" {
			t.Errorf("Vector2 %s %s = %q, %t", operator.name, operator.right, result.Name(), found)
		}
	}
}

// This gzip is only a repository-size wrapper. Its decompressed bytes are the
// complete, unmodified output of the official Godot 4.7.2 binary. Godot emits
// the retained header/tables at core/extension/extension_api_dump.cpp:104-127,
// 559-619,623-908,912-1283,1287-1305 (commit ed1daf0bf).
func TestLoadRawAcceptsOfficialGodot472DumpByteForByte(t *testing.T) {
	raw := readOfficialRawFixture(t)
	if len(raw) != officialRawBytes || digestBytes(raw) != officialRawSHA256 {
		t.Fatalf("official raw fixture = %d bytes, SHA-256 %s", len(raw), digestBytes(raw))
	}
	loaded, err := LoadRaw(raw, SourceOverride, "")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Provenance.Version != (Version{Major: 4, Minor: 7, Patch: 2}) ||
		loaded.Provenance.Build != "official" || loaded.Provenance.RawSHA256 != officialRawSHA256 {
		t.Fatalf("official raw provenance = %+v", loaded.Provenance)
	}
	assertCoreEngineFacts(t, loaded.Engine)
}

func TestOfficialGodot472RegeneratesCommittedArtifactByteForByte(t *testing.T) {
	generated, err := GenerateArtifact(readOfficialRawFixture(t), officialGodotCommit)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, godot47Artifact) {
		t.Fatalf("generated artifact SHA-256 = %s, committed = %s", digestBytes(generated), digestBytes(godot47Artifact))
	}
	if got := digestBytes(generated); got != officialArtifactSHA256 {
		t.Fatalf("generated artifact SHA-256 = %s, want %s", got, officialArtifactSHA256)
	}
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
				if err != nil || loaded.Provenance.Source != test.source {
					t.Fatalf("LoadRaw() = %+v, %v", loaded, err)
				}
				return
			}
			if err == nil || !errors.Is(err, ErrRawInvalid) {
				t.Fatalf("LoadRaw() error = %v, want ErrRawInvalid", err)
			}
		})
	}
}

func TestGenerateArtifactIsDeterministicAndIdentityBound(t *testing.T) {
	data := readRawFixture(t)
	first, err := GenerateArtifact(data, officialGodotCommit)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateArtifact(data, officialGodotCommit)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("the same raw input produced different artifact bytes")
	}
	otherIdentity, err := GenerateArtifact(data, "different-commit")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, otherIdentity) {
		t.Fatal("changing source identity did not change the artifact")
	}

	reader, err := gzip.NewReader(bytes.NewReader(first))
	if err != nil {
		t.Fatal(err)
	}
	if !reader.ModTime.IsZero() || reader.Name != "" || reader.Comment != "" || len(reader.Extra) != 0 {
		t.Fatalf("gzip header is not normalized: %+v", reader.Header)
	}
	uncompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(uncompressed, []byte(fixturePath)) || bytes.Contains(uncompressed, []byte("generated_at")) {
		t.Fatal("artifact identity includes a path or timestamp")
	}

	document, digest, err := decodeArtifactJSON(uncompressed)
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" || document.Provenance.SourceCommit != officialGodotCommit {
		t.Fatalf("artifact digest/provenance = %q / %+v", digest, document.Provenance)
	}
	if !sort.SliceIsSorted(document.Classes, func(i, j int) bool { return document.Classes[i].Name < document.Classes[j].Name }) ||
		!sort.SliceIsSorted(document.Methods, func(i, j int) bool {
			return document.Methods[i].Owner < document.Methods[j].Owner ||
				(document.Methods[i].Owner == document.Methods[j].Owner && document.Methods[i].Name < document.Methods[j].Name)
		}) {
		t.Fatal("artifact records are not in stable identity order")
	}
	loaded, err := LoadArtifact(first)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Provenance.SchemaSHA256 != digest || loaded.Provenance.RawSHA256 != fixtureRawSHA256 || loaded.Provenance.Source != SourceEmbedded {
		t.Fatalf("loaded provenance = %+v", loaded.Provenance)
	}
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
			if err == nil || !errors.Is(err, ErrRawInvalid) || loaded != nil || !strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("LoadRaw() = %+v, %v", loaded, err)
			}
		})
	}
}

func TestRawValidationDistinguishesMalformedFromInvalid(t *testing.T) {
	for _, malformed := range [][]byte{
		[]byte(`{"header":`),
		append(readRawFixture(t), []byte(` {}`)...),
	} {
		loaded, err := LoadRaw(malformed, SourceOverride, "")
		if err == nil || !errors.Is(err, ErrRawParse) || loaded != nil {
			t.Fatalf("malformed LoadRaw() = %+v, %v", loaded, err)
		}
	}

	for _, key := range []string{"header", "builtin_classes", "classes", "singletons", "utility_functions"} {
		t.Run("missing-"+key, func(t *testing.T) {
			data := mutateRawFixture(t, func(document map[string]any) { delete(document, key) })
			loaded, err := LoadRaw(data, SourceOverride, "")
			if err == nil || !errors.Is(err, ErrRawInvalid) || loaded != nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("LoadRaw() = %+v, %v", loaded, err)
			}
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
			if err == nil || !errors.Is(err, ErrRawInvalid) || loaded != nil {
				t.Fatalf("LoadRaw() = %+v, %v", loaded, err)
			}
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
	if loaded, err := LoadRaw(data, SourceOverride, ""); err != nil || loaded == nil {
		t.Fatalf("LoadRaw() = %+v, %v", loaded, err)
	}
}

func TestEmbeddedRegistryHasOnlyExact47AndOfficialProvenance(t *testing.T) {
	if got := SupportedMinors(); len(got) != 1 || got[0] != (MinorVersion{Major: 4, Minor: 7}) {
		t.Fatalf("SupportedMinors() = %+v", got)
	}
	for _, version := range []MinorVersion{{Major: 4, Minor: 6}, {Major: 4, Minor: 8}, {Major: 5, Minor: 0}} {
		loaded, err := LoadEmbedded(version.Major, version.Minor)
		if err == nil || !errors.Is(err, ErrUnsupportedVersion) || loaded != nil {
			t.Fatalf("LoadEmbedded(%+v) = %+v, %v", version, loaded, err)
		}
	}
	loaded, err := LoadEmbedded(4, 7)
	if err != nil {
		t.Fatal(err)
	}
	wantVersion := Version{Major: 4, Minor: 7, Patch: 2}
	if loaded.Provenance.Source != SourceEmbedded || loaded.Provenance.Version != wantVersion ||
		loaded.Provenance.RawSHA256 != officialRawSHA256 || loaded.Provenance.SourceCommit != officialGodotCommit {
		t.Fatalf("embedded provenance = %+v", loaded.Provenance)
	}
	if loaded.Engine.Class("Animation").Kind() != semantic.KindClass {
		t.Fatal("official table does not contain Animation")
	}
	assertCoreEngineFacts(t, loaded.Engine)
	override, err := LoadRaw(readRawFixture(t), SourceOverride, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := override.Engine.Class("Animation"); got.Kind() != semantic.KindUnknown {
		t.Fatalf("override unexpectedly merged embedded Animation: kind %v name %q", got.Kind(), got.Name())
	}
	loaded.Provenance.Source = SourceOverride
	again, err := LoadEmbedded(4, 7)
	if err != nil {
		t.Fatal(err)
	}
	if again == loaded || again.Engine != loaded.Engine || again.Provenance.Source != SourceEmbedded {
		t.Fatalf("registry publication was mutable: first=%p second=%p provenance=%+v", loaded, again, again.Provenance)
	}
}

func TestArtifactRejectsDigestTamperingAndNonCanonicalRecords(t *testing.T) {
	blob, err := GenerateArtifact(readRawFixture(t), officialGodotCommit)
	if err != nil {
		t.Fatal(err)
	}
	payload := decompressArtifactForTest(t, blob)
	var envelope artifact
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}

	t.Run("digest", func(t *testing.T) {
		tampered := envelope
		tampered.Digest = strings.Repeat("0", sha256.Size*2)
		loaded, err := LoadArtifact(compressArtifactForTest(t, tampered))
		if err == nil || loaded != nil || !strings.Contains(err.Error(), "digest") {
			t.Fatalf("LoadArtifact() = %+v, %v", loaded, err)
		}
	})

	t.Run("record order", func(t *testing.T) {
		tampered := envelope
		tampered.Schema.Classes = append([]classRecord(nil), envelope.Schema.Classes...)
		tampered.Schema.Classes[0], tampered.Schema.Classes[1] = tampered.Schema.Classes[1], tampered.Schema.Classes[0]
		tampered.Digest, err = schemaDigest(tampered.Schema)
		if err != nil {
			t.Fatal(err)
		}
		loaded, loadErr := LoadArtifact(compressArtifactForTest(t, tampered))
		if loadErr == nil || loaded != nil || !strings.Contains(loadErr.Error(), "canonical order") {
			t.Fatalf("LoadArtifact() = %+v, %v", loaded, loadErr)
		}
	})
}

func TestLoadRegistryArtifactRequiresExactIdentity(t *testing.T) {
	blob, err := GenerateArtifact(readRawFixture(t), officialGodotCommit)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadArtifact(blob)
	if err != nil {
		t.Fatal(err)
	}
	matching := registryIdentity{
		version:      baseline.Provenance.Version,
		rawSHA256:    baseline.Provenance.RawSHA256,
		sourceCommit: baseline.Provenance.SourceCommit,
		schemaSHA256: baseline.Provenance.SchemaSHA256,
	}

	var envelope artifact
	if err := json.Unmarshal(decompressArtifactForTest(t, blob), &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Schema.Provenance.Source = SourceOverride
	envelope.Schema.Provenance.SourceCommit = ""
	envelope.Digest, err = schemaDigest(envelope.Schema)
	if err != nil {
		t.Fatal(err)
	}
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
				if err != nil || loaded == nil || loaded.Engine == nil {
					t.Fatalf("loadRegistryArtifact() = %+v, %v", loaded, err)
				}
				return
			}
			if err == nil || loaded != nil || !strings.Contains(err.Error(), "does not match its registry identity") {
				t.Fatalf("loadRegistryArtifact() = %+v, %v", loaded, err)
			}
		})
	}
}

func TestLazyArtifactConcurrentLoadsPublishOneCompleteEngine(t *testing.T) {
	blob, err := GenerateArtifact(readRawFixture(t), officialGodotCommit)
	if err != nil {
		t.Fatal(err)
	}
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
	if calls.Load() != 1 {
		t.Fatalf("artifact decoded %d times, want 1", calls.Load())
	}
	for index := range results {
		if errorsFound[index] != nil || results[index] == nil || results[index].Engine == nil {
			t.Fatalf("result %d = %+v, %v", index, results[index], errorsFound[index])
		}
		if results[index] != results[0] || results[index].Engine != results[0].Engine {
			t.Fatalf("result %d did not observe the single published value", index)
		}
	}
}

func TestCorruptArtifactNeverPublishesPartialState(t *testing.T) {
	lazy := newLazyArtifact([]byte("not gzip"), LoadArtifact)
	for attempt := 0; attempt < 2; attempt++ {
		loaded, err := lazy.Load()
		if err == nil || loaded != nil {
			t.Fatalf("attempt %d = %+v, %v", attempt, loaded, err)
		}
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
	if err != nil {
		t.Fatal(err)
	}
	payload := decompressArtifactForTest(t, godot47Artifact)
	document, _, err := decodeArtifactJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
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
	for spelling, reason := range gotUnknown {
		if !wantUnknown[spelling] {
			t.Errorf("official retained type %q unexpectedly became Unknown: %s", spelling, reason)
		}
	}
	for spelling := range wantUnknown {
		if _, found := gotUnknown[spelling]; !found {
			t.Errorf("official retained type %q is no longer Unknown; remove it from the explicit allowlist", spelling)
		}
	}
}

func assertContainerComponentsResolved(t *testing.T, spelling string, resolved semantic.Type) {
	t.Helper()
	if element, ok := resolved.Element(); ok {
		if element.Kind() == semantic.KindUnknown {
			t.Errorf("official retained type %q hid an Unknown array element: %s", spelling, element.Reason())
		} else {
			assertContainerComponentsResolved(t, spelling, element)
		}
	}
	if key, ok := resolved.Key(); ok {
		if key.Kind() == semantic.KindUnknown {
			t.Errorf("official retained type %q hid an Unknown dictionary key: %s", spelling, key.Reason())
		} else {
			assertContainerComponentsResolved(t, spelling, key)
		}
	}
	if value, ok := resolved.Value(); ok {
		if value.Kind() == semantic.KindUnknown {
			t.Errorf("official retained type %q hid an Unknown dictionary value: %s", spelling, value.Reason())
		} else {
			assertContainerComponentsResolved(t, spelling, value)
		}
	}
}

func BenchmarkLoadEmbeddedArtifactCold(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		loaded, err := LoadArtifact(godot47Artifact)
		if err != nil || loaded == nil {
			b.Fatalf("LoadArtifact() = %+v, %v", loaded, err)
		}
	}
}

func BenchmarkLoadOfficialRawOverrideCold(b *testing.B) {
	name := os.Getenv("GDKIT_EXTENSION_API")
	if name == "" {
		b.Skip("set GDKIT_EXTENSION_API to an official raw extension_api.json")
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		b.Fatal(err)
	}
	if got := digestBytes(raw); got != officialRawSHA256 {
		b.Fatalf("raw SHA-256 = %s, want %s", got, officialRawSHA256)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		loaded, err := LoadRaw(raw, SourceOverride, "")
		if err != nil || loaded == nil {
			b.Fatalf("LoadRaw() = %+v, %v", loaded, err)
		}
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
	if err := json.Unmarshal(readRawFixture(t), &document); err != nil {
		t.Fatal(err)
	}
	mutate(document)
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func decompressArtifactForTest(t *testing.T, blob []byte) []byte {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	return payload
}

func compressArtifactForTest(t *testing.T, value artifact) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func assertCoreEngineFacts(t *testing.T, engine *semantic.Engine) {
	t.Helper()
	class := engine.Class("Sprite2D")
	for _, want := range []string{"Sprite2D", "Node2D", "CanvasItem", "Node", "Object"} {
		if class.Kind() != semantic.KindClass || class.Name() != want {
			t.Fatalf("embedded class chain at %s = kind %v name %q reason %q", want, class.Kind(), class.Name(), class.Reason())
		}
		if want == "Object" {
			break
		}
		var ok bool
		class, ok = class.Base()
		if !ok {
			t.Fatalf("embedded %s has no base", want)
		}
	}
	getNode, ok := engine.Method("Node", "get_node")
	if !ok || getNode.ReturnType().Name() != "Node" || len(getNode.Arguments()) != 1 || getNode.Arguments()[0].Type().Name() != "NodePath" {
		t.Fatalf("embedded Node.get_node = %#v, %t", getNode, ok)
	}
	call, ok := engine.Method("Object", "call")
	if !ok || call.ReturnType().Kind() != semantic.KindVariant {
		t.Fatalf("embedded Object.call = %#v, %t", call, ok)
	}
	position, ok := engine.Property("Node2D", "position")
	if !ok || position.Type().Name() != "Vector2" {
		t.Fatalf("embedded Node2D.position = %#v, %t", position, ok)
	}
	for _, operand := range []struct{ operator, right string }{{"+", "Vector2"}, {"*", "float"}} {
		result, found := engine.Operator("Vector2", operand.operator, operand.right)
		if !found || result.Name() != "Vector2" {
			t.Fatalf("embedded Vector2 %s %s = %q, %t", operand.operator, operand.right, result.Name(), found)
		}
	}
}
