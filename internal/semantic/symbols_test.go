package semantic

import (
	"testing"

	"github.com/cafecito-games/gdparser/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func symbolsTestEngine(t *testing.T) *Engine {
	t.Helper()
	builder := NewEngineBuilder()
	for _, builtin := range []string{"bool", "int", "float", "String", "Vector2", "Color", "Array", "Dictionary", "Callable", "Signal"} {
		require.NoError(t, builder.AddBuiltin(builtin))
	}
	require.NoError(t, builder.AddBuiltinConstructor("Vector2"))
	for _, class := range []struct{ name, parent string }{
		{name: "Object"},
		{name: "RefCounted", parent: "Object"},
		{name: "Resource", parent: "RefCounted"},
		{name: "PackedScene", parent: "Resource"},
		{name: "Node", parent: "Object"},
		{name: "HTTPRequest", parent: "Node"},
		{name: "FileAccess", parent: "RefCounted"},
		{name: "OS", parent: "Object"},
	} {
		require.NoError(t, builder.AddClass(class.name, class.parent))
	}
	require.NoError(t, builder.AddProperty("HTTPRequest", "use_threads", "bool"))
	require.NoError(t, builder.AddMethod("HTTPRequest", "request", "int", []EngineArgumentSpec{{Type: "String"}}, false, false))
	require.NoError(t, builder.AddMethod("Node", "queue_free", "void", nil, false, false))
	require.NoError(t, builder.AddMethod("FileAccess", "open", "FileAccess", []EngineArgumentSpec{{Type: "String"}, {Type: "int"}}, true, false))
	require.NoError(t, builder.AddMethod("OS", "alert", "void", []EngineArgumentSpec{{Type: "String"}}, false, false))
	require.NoError(t, builder.AddSingleton("OS", "OS"))
	require.NoError(t, builder.AddMethod("Color", "html", "Color", []EngineArgumentSpec{{Type: "String"}}, true, false))
	require.NoError(t, builder.AddMethod("Vector2", "normalized", "Vector2", nil, false, false))
	require.NoError(t, builder.AddUtility("str_to_var", "Variant", []EngineArgumentSpec{{Type: "String"}}, false))
	engine, err := builder.Build()
	require.NoError(t, err)
	return engine
}

type memoryResourceSources struct {
	*memorySources
	scripts map[string]string
	scenes  map[string]string
}

func (s *memoryResourceSources) ResolvePreloadResource(from, target string) ResourceResolution {
	return s.resolveResource(from, target)
}

func (s *memoryResourceSources) ResolveLoadResource(from, target string) ResourceResolution {
	return s.resolveResource(from, target)
}

func (s *memoryResourceSources) resolveResource(from, target string) ResourceResolution {
	provenance := ResourceLiteralPath
	if script, ok := s.scripts[from+"\x00"+target]; ok {
		return FoundResource(ResourceScript, target, script, provenance)
	}
	if scene, ok := s.scenes[from+"\x00"+target]; ok {
		return FoundResource(ResourceScene, target, scene, provenance)
	}
	return UnresolvedResource(ResourceMissing, ResourceUnknown, target, provenance, "unmapped resource")
}

func memberAt(t *testing.T, file *ast.File, property string, line int) *ast.MemberExpression {
	t.Helper()
	var matches []*ast.MemberExpression
	ast.Inspect(file, func(node ast.Node) bool {
		member, ok := node.(*ast.MemberExpression)
		if ok && member.Property == property && member.Span().Start.Line == line {
			matches = append(matches, member)
		}
		return true
	})
	require.Len(t, matches, 1, "expected one .%s on line %d", property, line)
	return matches[0]
}

func callAt(t *testing.T, file *ast.File, line int) *ast.CallExpression {
	t.Helper()
	var matches []*ast.CallExpression
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpression)
		if ok && call.Span().Start.Line == line {
			matches = append(matches, call)
		}
		return true
	})
	require.NotEmpty(t, matches, "expected call on line %d", line)
	return matches[0]
}

func TestAnalyzerSymbolQueriesResolveConstructorsAndMembers(t *testing.T) {
	base := sources(t, map[string]string{
		"core/client_http.gd": "class_name ClientHttp\nextends RefCounted\nenum Mode { IDLE, BUSY }\nclass Options:\n\tvar timeout: float\nstatic func begin() -> int:\n\treturn Mode.IDLE\n",
		"core/base_client.gd": "class_name BaseClient\nextends RefCounted\nfunc send() -> void:\n\tpass\n",
		"core/sub_client.gd":  "class_name SubClient\nextends BaseClient\nfunc send() -> void:\n\tsuper()\n\tsuper.send()\n",
		"net/custom_http.gd":  "class_name CustomHTTP\nextends HTTPRequest\nfunc configure() -> void:\n\tuse_threads = true\n",
		"bus.gd":              "extends Node\nfunc publish() -> void:\n\tpass\n",
		"caller.gd": `extends Node

class Thing:
	var use_threads := false

func make_http(node) -> HTTPRequest:
	return HTTPRequest.new()

func run(untyped) -> void:
	var http := HTTPRequest.new()
	http.use_threads = true
	var custom := CustomHTTP.new()
	custom.use_threads = true
	custom.queue_free()
	var thing := Thing.new()
	thing.use_threads = true
	var options := ClientHttp.Options.new()
	ClientHttp.begin()
	var sub := SubClient.new()
	sub.send()
	OS.alert("warn")
	FileAccess.open("res://data.bin", 1)
	var ok_code := HTTPRequest.RESULT_SUCCESS
	var tint := Color.html("#ffffff")
	var zero := Vector2.ZERO
	var vec := Vector2()
	var unit := vec.normalized()
	var mode := ClientHttp.Mode.IDLE
	make_http($Missing).use_threads = true
	HTTPRequest.new($Missing).use_threads = true
	untyped.use_threads = true
	GameBus.publish()
	str_to_var("1")
	print_debug("trace")
	var print_debug := 1
	var shadowed := print_debug
`,
	})
	base.autoloads["GameBus"] = "bus.gd"
	analyzer := NewAnalyzer(base, symbolsTestEngine(t))

	caller := base.File("caller.gd")
	require.Equal(t, []string{"HTTPRequest.new"}, analyzer.MemberSymbols(memberAt(t, caller, "new", 10)))
	require.Equal(t, []string{"HTTPRequest.use_threads"}, analyzer.MemberSymbols(memberAt(t, caller, "use_threads", 11)))
	require.Equal(t, []string{"CustomHTTP.new"}, analyzer.MemberSymbols(memberAt(t, caller, "new", 12)))
	require.Equal(t, []string{"HTTPRequest.use_threads", "CustomHTTP.use_threads"}, analyzer.MemberSymbols(memberAt(t, caller, "use_threads", 13)))
	require.Equal(t, []string{"Node.queue_free", "CustomHTTP.queue_free", "HTTPRequest.queue_free"}, analyzer.MemberSymbols(memberAt(t, caller, "queue_free", 14)))

	// File-local inner class Thing has no global identity and never matches HTTPRequest or RefCounted.
	require.Empty(t, analyzer.MemberSymbols(memberAt(t, caller, "new", 15)))
	require.Empty(t, analyzer.MemberSymbols(memberAt(t, caller, "use_threads", 16)))

	require.Equal(t, []string{"ClientHttp.Options"}, analyzer.MemberSymbols(memberAt(t, caller, "Options", 17)))
	require.Equal(t, []string{"ClientHttp.Options.new"}, analyzer.MemberSymbols(memberAt(t, caller, "new", 17)))
	require.Equal(t, []string{"ClientHttp.begin"}, analyzer.MemberSymbols(memberAt(t, caller, "begin", 18)))
	require.Equal(t, []string{"SubClient.new"}, analyzer.MemberSymbols(memberAt(t, caller, "new", 19)))
	require.Equal(t, []string{"SubClient.send", "BaseClient.send"}, analyzer.MemberSymbols(memberAt(t, caller, "send", 20)))
	require.Equal(t, []string{"OS.alert"}, analyzer.MemberSymbols(memberAt(t, caller, "alert", 21)))
	require.Equal(t, []string{"FileAccess.open"}, analyzer.MemberSymbols(memberAt(t, caller, "open", 22)))
	require.Equal(t, []string{"HTTPRequest.RESULT_SUCCESS"}, analyzer.MemberSymbols(memberAt(t, caller, "RESULT_SUCCESS", 23)))
	require.Equal(t, []string{"Color.html"}, analyzer.MemberSymbols(memberAt(t, caller, "html", 24)))
	require.Equal(t, []string{"Vector2.ZERO"}, analyzer.MemberSymbols(memberAt(t, caller, "ZERO", 25)))
	require.Equal(t, []string{"Vector2.normalized"}, analyzer.MemberSymbols(memberAt(t, caller, "normalized", 27)))
	require.Equal(t, []string{"ClientHttp.Mode.IDLE"}, analyzer.MemberSymbols(memberAt(t, caller, "IDLE", 28)))
	require.Equal(t, []string{"HTTPRequest.use_threads"}, analyzer.MemberSymbols(memberAt(t, caller, "use_threads", 29)))
	require.Equal(t, []string{"HTTPRequest.use_threads"}, analyzer.MemberSymbols(memberAt(t, caller, "use_threads", 30)))
	require.Empty(t, analyzer.MemberSymbols(memberAt(t, caller, "use_threads", 31)))
	require.Equal(t, []string{"GameBus.publish"}, analyzer.MemberSymbols(memberAt(t, caller, "publish", 32)))

	require.Equal(t, []string{"str_to_var"}, analyzer.IdentifierSymbols(scopeIdentifierAt(t, caller, "str_to_var", 33)))
	require.Equal(t, []string{"print_debug"}, analyzer.IdentifierSymbols(scopeIdentifierAt(t, caller, "print_debug", 34)))
	require.Empty(t, analyzer.IdentifierSymbols(scopeIdentifierAt(t, caller, "print_debug", 36)))

	custom := base.File("net/custom_http.gd")
	require.Equal(t, []string{"HTTPRequest.use_threads", "CustomHTTP.use_threads"}, analyzer.IdentifierSymbols(scopeIdentifierAt(t, custom, "use_threads", 4)))

	sub := base.File("core/sub_client.gd")
	require.Equal(t, []string{"BaseClient.send"}, analyzer.IdentifierSymbols(scopeIdentifierAt(t, sub, "super", 4)))
	require.Equal(t, []string{"BaseClient.send"}, analyzer.MemberSymbols(memberAt(t, sub, "send", 5)))

	clientFile := base.File("core/client_http.gd")
	require.Equal(t, []string{"ClientHttp.Mode.IDLE"}, analyzer.MemberSymbols(memberAt(t, clientFile, "IDLE", 7)))
}

func TestAnalyzerSymbolQueriesResolveAnnotationsExtendsAndResources(t *testing.T) {
	base := sources(t, map[string]string{
		"proto/auth.gd":  "class_name UzirAuthProto\nextends RefCounted\nclass Payload:\n\tpass\nenum Status { OK }\n",
		"consumer.gd":    "extends UzirAuthProto.Payload\nconst Auth := preload(\"res://proto/auth.gd\")\nconst Scene := preload(\"res://ui.tscn\")\n",
		"by_path_sub.gd": "extends \"res://proto/auth.gd\"\n",
		"http_sub.gd":    "extends HTTPRequest\n",
	})
	base.resolved["by_path_sub.gd\x00res://proto/auth.gd"] = "proto/auth.gd"
	resourceSet := &memoryResourceSources{
		memorySources: base,
		scripts:       map[string]string{"consumer.gd\x00res://proto/auth.gd": "proto/auth.gd"},
		scenes:        map[string]string{"consumer.gd\x00res://ui.tscn": "ui.tscn"},
	}
	analyzer := NewAnalyzer(resourceSet, symbolsTestEngine(t))

	assert.Equal(t, []string{"UzirAuthProto.Payload", "UzirAuthProto"}, analyzer.ExtendsSymbols("consumer.gd"))
	assert.Equal(t, []string{"UzirAuthProto"}, analyzer.ExtendsSymbols("by_path_sub.gd"))
	assert.Equal(t, []string{"HTTPRequest"}, analyzer.ExtendsSymbols("http_sub.gd"))

	consumer := base.File("consumer.gd")
	assert.Equal(t, []string{"UzirAuthProto"}, analyzer.ResourceScriptSymbols(callAt(t, consumer, 2)))
	assert.Empty(t, analyzer.ResourceScriptSymbols(callAt(t, consumer, 3)))

	assert.Equal(t, []string{"Array", "UzirAuthProto"}, analyzer.AnnotationSymbols("consumer.gd", "Array[UzirAuthProto]", false))
	assert.Equal(t, []string{"Dictionary", "String", "UzirAuthProto.Payload", "UzirAuthProto"}, analyzer.AnnotationSymbols("consumer.gd", "Dictionary[String, UzirAuthProto.Payload]", false))
	assert.Equal(t, []string{"UzirAuthProto.Status", "UzirAuthProto"}, analyzer.AnnotationSymbols("consumer.gd", "UzirAuthProto.Status", false))
	assert.Equal(t, []string{"HTTPRequest"}, analyzer.AnnotationSymbols("consumer.gd", "HTTPRequest.Result", false))
	assert.Empty(t, analyzer.AnnotationSymbols("consumer.gd", "void", true))
}
