package lint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdkit/internal/failure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func restrictedConfig(restrictions ...Restriction) Config {
	config := DefaultConfig()
	config.Enable = []string{"restricted-api"}
	config.RestrictedAPI = RestrictedAPIConfig{Restrictions: restrictions}
	return config
}

func restrictedDiagnostics(report Report) []Diagnostic {
	var out []Diagnostic
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule == "restricted-api" {
			out = append(out, diagnostic)
		}
	}
	return out
}

func TestRestrictedAPIIsInertUntilEnabled(t *testing.T) {
	config := DefaultConfig()
	config.RestrictedAPI = RestrictedAPIConfig{
		Restrictions: []Restriction{{Symbol: "HTTPRequest.new"}},
	}
	source := "func a() -> void:\n\tvar r := HTTPRequest.new()\n\tprint(r)\n"
	assertRuleWithConfig(t, config, "restricted-api", source)
	assertRuleWithConfig(t, restrictedConfig(Restriction{Symbol: "HTTPRequest.new"}), "restricted-api", source, 2)
}

func TestRestrictedAPISilencedByEmptyRestrictions(t *testing.T) {
	source := "func a() -> void:\n\tvar r := HTTPRequest.new()\n\tr.use_threads = true\n"
	assertRuleWithConfig(t, restrictedConfig(), "restricted-api", source)
}

func TestRestrictedAPIEnforcesHTTPRequestIsolationPolicy(t *testing.T) {
	config := restrictedConfig(
		Restriction{
			Symbol:  "HTTPRequest.new",
			Allow:   []string{"core/client_http.gd"},
			Message: "Use ClientHttp.begin(host[, timeout_seconds]).",
		},
		Restriction{
			Symbol:  "HTTPRequest.use_threads",
			Kind:    "assignment",
			Message: "Cancelling a threaded request joins its worker and can never return.",
		},
	)

	report := lintProject(t, config, map[string]string{
		"core/client_http.gd": `class_name ClientHttp
extends Node

static func begin(parent: Node) -> HTTPRequest:
	var request := HTTPRequest.new()
	parent.add_child(request)
	return request

static func broken(request: HTTPRequest) -> void:
	request.use_threads = true
`,
		"features/auth/login.gd": `extends Node

## Never write use_threads = true here; see ClientHttp.
class Thing:
	var use_threads := false

func _ready() -> void:
	var note := "HTTPRequest.new() and use_threads = true"
	var thing := Thing.new()
	thing.use_threads = true
	var allowed := ClientHttp.begin(self)
	if allowed.use_threads:
		print(note)
	var forbidden := HTTPRequest.new()
	forbidden.use_threads = true
`,
		"tests/client_http_test.gd": `extends Node

func test_completion_handler() -> void:
	# gdkit:ignore = restricted-api
	var stub := HTTPRequest.new()
	var unguarded := HTTPRequest.new()
	print(stub, unguarded)
`,
	})

	found := restrictedDiagnostics(report)
	require.Len(t, found, 4)

	assert.Equal(t, "core/client_http.gd", found[0].Path)
	assert.Equal(t, 10, found[0].Line)
	assert.Equal(t, `"HTTPRequest.use_threads" is restricted: Cancelling a threaded request joins its worker and can never return.`, found[0].Message)

	assert.Equal(t, "features/auth/login.gd", found[1].Path)
	assert.Equal(t, 14, found[1].Line)
	assert.Equal(t, `"HTTPRequest.new" is restricted: Use ClientHttp.begin(host[, timeout_seconds]).`, found[1].Message)

	assert.Equal(t, "features/auth/login.gd", found[2].Path)
	assert.Equal(t, 15, found[2].Line)
	assert.Equal(t, `"HTTPRequest.use_threads" is restricted: Cancelling a threaded request joins its worker and can never return.`, found[2].Message)

	assert.Equal(t, "tests/client_http_test.gd", found[3].Path)
	assert.Equal(t, 6, found[3].Line)
	assert.Equal(t, `"HTTPRequest.new" is restricted: Use ClientHttp.begin(host[, timeout_seconds]).`, found[3].Message)
}

func TestRestrictedAPIEnforcesProtoClassIsolationAcrossExcludedGeneratedUniverse(t *testing.T) {
	config := restrictedConfig(Restriction{
		Symbol:  "Uzir*Proto",
		Allow:   []string{"features/game/net/codec/**"},
		Message: "Protocol classes may only be referenced inside the codec layer.",
	})
	config.Exclude = append(config.Exclude, "protocol/**")

	report := lintProject(t, config, map[string]string{
		"protocol/auth.pb.gd": `class_name UzirAuthProto
extends RefCounted

enum Status { OK }

class Payload:
	var token: String
`,
		"features/game/net/codec/auth_codec.gd": `class_name AuthCodec
extends RefCounted

static func encode() -> UzirAuthProto:
	return UzirAuthProto.new()
`,
		"features/ui/login_screen.gd": `extends Node

signal received(packet: UzirAuthProto)

const Preloaded := preload("res://protocol/auth.pb.gd")

var field: UzirAuthProto
var batch: Array[UzirAuthProto]

class SubProto extends UzirAuthProto:
	pass

func decode(payload: UzirAuthProto.Payload) -> UzirAuthProto:
	var created := UzirAuthProto.new()
	for item: UzirAuthProto in batch:
		if item is UzirAuthProto:
			var cast := item as UzirAuthProto
			print(created, payload, cast)
	return field
`,
		"features/ui/direct_extends.gd": `extends UzirAuthProto
`,
		"features/ui/path_extends.gd": `extends "res://protocol/auth.pb.gd"
`,
	})

	found := restrictedDiagnostics(report)
	var locations []struct {
		path string
		line int
	}
	for _, diagnostic := range found {
		locations = append(locations, struct {
			path string
			line int
		}{diagnostic.Path, diagnostic.Line})
		assert.Equal(t, `"UzirAuthProto" is restricted: Protocol classes may only be referenced inside the codec layer.`, diagnostic.Message)
	}

	want := []struct {
		path string
		line int
	}{
		{"features/ui/direct_extends.gd", 1},
		{"features/ui/login_screen.gd", 3},
		{"features/ui/login_screen.gd", 5},
		{"features/ui/login_screen.gd", 7},
		{"features/ui/login_screen.gd", 8},
		{"features/ui/login_screen.gd", 10},
		{"features/ui/login_screen.gd", 13},
		{"features/ui/login_screen.gd", 13},
		{"features/ui/login_screen.gd", 14},
		{"features/ui/login_screen.gd", 15},
		{"features/ui/login_screen.gd", 16},
		{"features/ui/login_screen.gd", 17},
		{"features/ui/path_extends.gd", 1},
	}
	require.Equal(t, want, locations)
}

func TestRestrictedAPIResolvesInheritanceImplicitSelfAndSuper(t *testing.T) {
	config := restrictedConfig(
		Restriction{Symbol: "HTTPRequest.use_threads", Kind: "assignment"},
		Restriction{Symbol: "BaseClient.send", Kind: "call"},
	)

	report := lintProject(t, config, map[string]string{
		"base_client.gd": `class_name BaseClient
extends RefCounted

func send() -> void:
	pass
`,
		"sub_client.gd": `class_name SubClient
extends BaseClient

func send() -> void:
	super()
	super.send()
`,
		"custom_http.gd": `class_name CustomHTTP
extends HTTPRequest

func enable_threads() -> void:
	use_threads = true
	self.use_threads = true
`,
		"caller.gd": `extends Node

func run() -> void:
	var custom := CustomHTTP.new()
	custom.use_threads = true
	var client := SubClient.new()
	client.send()
`,
	})

	found := restrictedDiagnostics(report)
	var got []struct {
		path    string
		line    int
		message string
	}
	for _, diagnostic := range found {
		got = append(got, struct {
			path    string
			line    int
			message string
		}{diagnostic.Path, diagnostic.Line, diagnostic.Message})
	}

	want := []struct {
		path    string
		line    int
		message string
	}{
		{"caller.gd", 5, `"HTTPRequest.use_threads" is restricted`},
		{"caller.gd", 7, `"BaseClient.send" is restricted`},
		{"custom_http.gd", 5, `"HTTPRequest.use_threads" is restricted`},
		{"custom_http.gd", 6, `"HTTPRequest.use_threads" is restricted`},
		{"sub_client.gd", 5, `"BaseClient.send" is restricted`},
		{"sub_client.gd", 6, `"BaseClient.send" is restricted`},
	}
	require.Equal(t, want, got)
}

func TestRestrictedAPIDistinguishesReferenceCallAndAssignmentKinds(t *testing.T) {
	source := `func run(request: HTTPRequest) -> void:
	var callable_new := HTTPRequest.new
	var constructed := HTTPRequest.new()
	var callable_request := request.request
	request.request("https://example.com")
	var current := request.use_threads
	request.use_threads = true
	print(callable_new, constructed, callable_request, current)
`

	assertRuleWithConfig(t, restrictedConfig(Restriction{Symbol: "HTTPRequest.new"}), "restricted-api", source, 2, 3)
	assertRuleWithConfig(t, restrictedConfig(Restriction{Symbol: "HTTPRequest.new", Kind: "call"}), "restricted-api", source, 3)
	assertRuleWithConfig(t, restrictedConfig(Restriction{Symbol: "HTTPRequest.request", Kind: "call"}), "restricted-api", source, 5)
	assertRuleWithConfig(t, restrictedConfig(Restriction{Symbol: "HTTPRequest.use_threads", Kind: "assignment"}), "restricted-api", source, 7)
	assertRuleWithConfig(t, restrictedConfig(Restriction{Symbol: "HTTPRequest.use_threads"}), "restricted-api", source, 6, 7)
}

func TestRestrictedAPISpanAndDefaultMessage(t *testing.T) {
	found := lintSourceWithConfig(t,
		restrictedConfig(Restriction{Symbol: "HTTPRequest.new"}),
		"restricted-api",
		"func a() -> void:\n\tvar r := HTTPRequest.new()\n\tprint(r)\n",
	)
	require.Len(t, found, 1)
	require.Equal(t, `"HTTPRequest.new" is restricted`, found[0].Message)
	require.Equal(t, 2, found[0].Line)
	require.Equal(t, 11, found[0].Column)
	require.Equal(t, 2, found[0].EndLine)
	require.Equal(t, 26, found[0].EndColumn)
}

func TestRestrictedAPIIgnoresShadowedBindingsUntypedReceiversAndLuaDictionaryKeys(t *testing.T) {
	config := restrictedConfig(
		Restriction{Symbol: "HTTPRequest"},
		Restriction{Symbol: "HTTPRequest.use_threads"},
		Restriction{Symbol: "str_to_var"},
		Restriction{Symbol: "print_debug"},
	)
	source := `func run(untyped, str_to_var: Callable) -> void:
	untyped.use_threads = true
	str_to_var.call("x")
	var print_debug := 1
	var dictionary := {HTTPRequest = 1, print_debug = print_debug}
	print(dictionary)
`
	assertRuleWithConfig(t, config, "restricted-api", source)
}

func TestRestrictedAPIReportsColonDictionaryKeyAndGlobalUtilities(t *testing.T) {
	config := restrictedConfig(
		Restriction{Symbol: "OS"},
		Restriction{Symbol: "str_to_var"},
		Restriction{Symbol: "print_debug"},
	)
	source := `func run() -> void:
	var table := {OS: 1}
	var parsed := str_to_var("1")
	print_debug(table, parsed)
`
	assertRuleWithConfig(t, config, "restricted-api", source, 2, 3, 4)
}

func TestRestrictedAPIDecodesAndValidatesConfiguration(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, DefaultConfigPath, `{
  "enable": ["restricted-api"],
  "restricted-api": {
    "restrictions": [
      {
        "symbol": "HTTPRequest.new",
        "allow": ["core/client_http.gd"],
        "message": "Use ClientHttp.begin."
      },
      {
        "symbol": "HTTPRequest.use_threads",
        "kind": "assignment"
      }
    ]
  }
}`)
	loaded, err := LoadConfig(root, "")
	require.NoError(t, err)
	require.Len(t, loaded.RestrictedAPI.Restrictions, 2)
	assert.Equal(t, "HTTPRequest.new", loaded.RestrictedAPI.Restrictions[0].Symbol)
	assert.Equal(t, []string{"core/client_http.gd"}, loaded.RestrictedAPI.Restrictions[0].Allow)
	assert.Equal(t, "assignment", loaded.RestrictedAPI.Restrictions[1].Kind)

	for _, symbol := range []string{"", "print()", "OS .alert", "OS.", ".alert", "OS..alert", "9print", "OS.9alert", "core/http.gd"} {
		invalid := DefaultConfig()
		invalid.RestrictedAPI.Restrictions = []Restriction{{Symbol: symbol}}
		err := invalid.Validate()
		assert.Error(t, err, "accepted malformed symbol %q", symbol)
		assert.ErrorContains(t, err, "restricted-api")
	}

	invalidKind := DefaultConfig()
	invalidKind.RestrictedAPI.Restrictions = []Restriction{{Symbol: "HTTPRequest.new", Kind: "write"}}
	err = invalidKind.Validate()
	require.Error(t, err)
	require.ErrorContains(t, err, "restricted-api")

	for _, badAllow := range []string{"", "res://core/client_http.gd", "uid://abc", "/core/client_http.gd", "../outside.gd", "core/../../outside.gd", `core\client_http.gd`, "bad\x00path", "bad\npath.gd", "C:/core/client_http.gd"} {
		invalidAllow := DefaultConfig()
		invalidAllow.RestrictedAPI.Restrictions = []Restriction{{Symbol: "HTTPRequest.new", Allow: []string{badAllow}}}
		err = invalidAllow.Validate()
		require.Error(t, err, "accepted invalid allow pattern %q", badAllow)
		require.ErrorContains(t, err, "restricted-api")
	}

	for _, badMessage := range []string{
		"Use ClientHttp.\nSee the networking ADR.",
		"Use ClientHttp.\r\nSee the networking ADR.",
		"Use ClientHttp.\rSee the networking ADR.",
		"Use ClientHttp.\n",
		"Use\tClientHttp.",
		"Use\x00ClientHttp.",
		"Use\u2028ClientHttp.",
		"Use\u2029ClientHttp.",
	} {
		invalidMessage := DefaultConfig()
		invalidMessage.RestrictedAPI.Restrictions = []Restriction{{Symbol: "HTTPRequest.new", Message: badMessage}}
		err = invalidMessage.Validate()
		require.Error(t, err, "accepted multiline or control-character message %q", badMessage)
		require.ErrorContains(t, err, "restricted-api")
	}

	unknownFieldDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(unknownFieldDir, ".gdkit"), 0o755))
	configFile := filepath.Join(unknownFieldDir, filepath.FromSlash(DefaultConfigPath))
	require.NoError(t, os.WriteFile(configFile, []byte(`{"restricted-api":{"restrictions":[{"symbol":"OS.alert","files":["a.gd"]}]}}`), 0o600))
	_, err = LoadConfig(unknownFieldDir, "")
	require.Error(t, err)
	labelled, ok := failure.Of(err)
	require.True(t, ok)
	assert.Equal(t, failure.ConfigParse, labelled.Kind)
}

func TestRestrictedAPIDeduplicatesRepeatedTypeArgumentWhileReportingDistinctOnes(t *testing.T) {
	config := restrictedConfig(Restriction{Symbol: "Uzir*Proto"})
	report := lintProject(t, config, map[string]string{
		"key.gd":   "class_name UzirKeyProto\nextends RefCounted\n",
		"value.gd": "class_name UzirValProto\nextends RefCounted\n",
		"caller.gd": `extends Node

var repeated: Dictionary[UzirKeyProto, UzirKeyProto]
var distinct: Dictionary[UzirKeyProto, UzirValProto]
`,
	})

	found := restrictedDiagnostics(report)
	require.Len(t, found, 3)
	assert.Equal(t, 3, found[0].Line)
	assert.Equal(t, `"UzirKeyProto" is restricted`, found[0].Message)
	assert.Equal(t, 4, found[1].Line)
	assert.Equal(t, `"UzirKeyProto" is restricted`, found[1].Message)
	assert.Equal(t, 4, found[2].Line)
	assert.Equal(t, `"UzirValProto" is restricted`, found[2].Message)
}

func TestRestrictedAPIDefaultsAreNotSharedBetweenConfigs(t *testing.T) {
	first := DefaultConfig()
	first.RestrictedAPI.Restrictions = append(first.RestrictedAPI.Restrictions, Restriction{Symbol: "OS.alert"})
	second := DefaultConfig()
	require.Empty(t, second.RestrictedAPI.Restrictions)
}
