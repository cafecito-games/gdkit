// Package cfg parses Godot ConfigFile documents (such as project.godot,
// export_presets.cfg, override.cfg, and plugin.cfg) into a queryable data model
// and evaluates jq queries against them.
package cfg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/cafecito-games/gdparser/configfile"
	configast "github.com/cafecito-games/gdparser/configfile/ast"
	configformat "github.com/cafecito-games/gdparser/configfile/format"
	"github.com/itchyny/gojq"

	"github.com/cafecito-games/gdkit/internal/failure"
)

// Options controls how a Godot ConfigFile document is mapped into the
// queryable value tree.
type Options struct {
	// Flat disables splitting slash-separated assignment keys into nested
	// objects, preserving literal Godot ConfigFile keys directly under each
	// section.
	Flat bool
}

// ParseFile reads and parses a Godot ConfigFile from disk into the queryable
// document representation.
func ParseFile(path string, options Options) (map[string]any, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, failure.WrapPath(failure.ConfigRead, path, err)
	}
	return Parse(path, source, options)
}

// Parse parses Godot ConfigFile bytes into the queryable document
// representation.
func Parse(name string, source []byte, options Options) (map[string]any, error) {
	parsedFile, err := configfile.ParseFile(name, source)
	if err != nil {
		return nil, failure.WrapPath(failure.ConfigParse, name, err)
	}
	document, err := Build(parsedFile, options)
	if err != nil {
		return nil, failure.WrapPath(failure.ConfigParse, name, err)
	}
	return document, nil
}

// Build converts a parsed Godot ConfigFile AST into a gojq-compatible value
// tree.
//
// Preamble assignments sit directly on the root object. Each [section] is a
// top-level key using its literal section name (merging repeated headers of the
// same name). Unless options.Flat is set, slash-separated assignment keys
// within a section are nested into sub-objects, while preserving colliding
// prefixes without losing either entry.
func Build(file *configast.File, options Options) (map[string]any, error) {
	root := make(map[string]any)
	if file == nil {
		return root, nil
	}

	sectionOrder := make([]string, 0, len(file.Sections))
	sectionStatements := make(map[string][]configast.Statement, len(file.Sections))
	sectionNames := make(map[string]struct{}, len(file.Sections))
	for _, section := range file.Sections {
		if section == nil {
			continue
		}
		if _, exists := sectionNames[section.Name]; !exists {
			sectionNames[section.Name] = struct{}{}
			sectionOrder = append(sectionOrder, section.Name)
		}
		sectionStatements[section.Name] = append(sectionStatements[section.Name], section.Statements...)
	}

	preambleEntries, err := collectEntries(file.Preamble)
	if err != nil {
		return nil, err
	}
	insertEntries(root, preambleEntries, options.Flat, sectionNames)

	for _, sectionName := range sectionOrder {
		entries, err := collectEntries(sectionStatements[sectionName])
		if err != nil {
			return nil, err
		}
		sectionObject := make(map[string]any)
		insertEntries(sectionObject, entries, options.Flat, nil)
		root[sectionName] = sectionObject
	}

	return root, nil
}

type configEntry struct {
	key   string
	value any
}

// collectEntries resolves assignments in source order, keeping the last value
// for duplicate literal keys (matching Godot ConfigFile semantics).
func collectEntries(statements []configast.Statement) ([]configEntry, error) {
	order := make([]string, 0, len(statements))
	values := make(map[string]any, len(statements))
	for _, statement := range statements {
		assignment, ok := statement.(*configast.Assignment)
		if !ok {
			continue
		}
		converted, err := convertExpression(assignment.Value)
		if err != nil {
			return nil, fmt.Errorf("convert key %q: %w", assignment.Key, err)
		}
		if _, exists := values[assignment.Key]; !exists {
			order = append(order, assignment.Key)
		}
		values[assignment.Key] = converted
	}
	entries := make([]configEntry, 0, len(order))
	for _, key := range order {
		entries = append(entries, configEntry{key: key, value: values[key]})
	}
	return entries, nil
}

// insertEntries places entries into target. When flat is false, keys with
// non-empty slash-separated segments are expanded into nested maps unless a
// strict prefix is itself an exact key (or a reserved name), in which case the
// remaining suffix is stored literally on the current object so neither entry
// overwrites the other regardless of source order.
func insertEntries(target map[string]any, entries []configEntry, flat bool, reserved map[string]struct{}) {
	if flat {
		for _, entry := range entries {
			target[entry.key] = entry.value
		}
		return
	}

	exactKeys := make(map[string]struct{}, len(entries)+len(reserved))
	for name := range reserved {
		exactKeys[name] = struct{}{}
	}
	for _, entry := range entries {
		exactKeys[entry.key] = struct{}{}
	}

	for _, entry := range entries {
		if !canSplitKey(entry.key) {
			target[entry.key] = entry.value
			continue
		}
		segments := strings.Split(entry.key, "/")
		current := target
		stored := false
		for index := 0; index < len(segments)-1; index++ {
			prefix := strings.Join(segments[:index+1], "/")
			if _, collides := exactKeys[prefix]; collides {
				remaining := strings.Join(segments[index:], "/")
				current[remaining] = entry.value
				stored = true
				break
			}
			segment := segments[index]
			next, exists := current[segment]
			if !exists {
				child := make(map[string]any)
				current[segment] = child
				current = child
				continue
			}
			child, ok := next.(map[string]any)
			if !ok {
				remaining := strings.Join(segments[index:], "/")
				current[remaining] = entry.value
				stored = true
				break
			}
			current = child
		}
		if !stored {
			current[segments[len(segments)-1]] = entry.value
		}
	}
}

func canSplitKey(key string) bool {
	if !strings.Contains(key, "/") {
		return false
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" {
			return false
		}
	}
	return true
}

func convertExpression(expression configast.Expression) (any, error) {
	if expression == nil {
		return nil, nil
	}
	switch node := expression.(type) {
	case *configast.NullLiteral:
		return nil, nil
	case *configast.BoolLiteral:
		return node.Value, nil
	case *configast.IntegerLiteral:
		return parseInteger(node.Raw)
	case *configast.FloatLiteral:
		return parseFloat(node.Raw)
	case *configast.StringLiteral:
		return node.Value, nil
	case *configast.Identifier:
		return convertIdentifier(node.Name), nil
	case *configast.UnaryExpression:
		return convertUnary(node)
	case *configast.ArrayLiteral:
		return convertArray(node)
	case *configast.TypedArrayLiteral:
		return convertArray(node.Value)
	case *configast.DictionaryLiteral:
		return convertDictionary(node)
	case *configast.TypedDictionaryLiteral:
		return convertDictionary(node.Value)
	case *configast.ConstructorCall:
		return convertConstructor(node)
	default:
		return nil, fmt.Errorf("unsupported config expression %T", expression)
	}
}

func parseInteger(raw string) (any, error) {
	cleaned := strings.ReplaceAll(raw, "_", "")
	if cleaned == "" {
		return 0, nil
	}
	base := 10
	digits := cleaned
	lower := strings.ToLower(cleaned)
	switch {
	case strings.HasPrefix(lower, "0x"):
		base = 16
		digits = cleaned[2:]
	case strings.HasPrefix(lower, "0b"):
		base = 2
		digits = cleaned[2:]
	case strings.HasPrefix(lower, "0o"):
		base = 8
		digits = cleaned[2:]
	}
	parsed, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return nil, fmt.Errorf("invalid integer literal %q", raw)
	}
	return normalizeBigInt(parsed), nil
}

func normalizeBigInt(value *big.Int) any {
	if value.IsInt64() {
		number := value.Int64()
		if number >= math.MinInt && number <= math.MaxInt {
			return int(number)
		}
	}
	return value
}

func parseFloat(raw string) (float64, error) {
	cleaned := strings.ReplaceAll(raw, "_", "")
	if cleaned == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid float literal %q: %w", raw, err)
	}
	return parsed, nil
}

func convertIdentifier(name string) any {
	switch strings.ToLower(name) {
	case "inf":
		return math.Inf(1)
	case "nan":
		return math.NaN()
	default:
		return name
	}
}

func convertUnary(unary *configast.UnaryExpression) (any, error) {
	operand, err := convertExpression(unary.Operand)
	if err != nil {
		return nil, err
	}
	switch unary.Operator {
	case "+":
		return operand, nil
	case "-":
		switch number := operand.(type) {
		case int:
			if number == math.MinInt {
				negated := new(big.Int).Neg(big.NewInt(int64(number)))
				return normalizeBigInt(negated), nil
			}
			return -number, nil
		case *big.Int:
			negated := new(big.Int).Neg(number)
			return normalizeBigInt(negated), nil
		case float64:
			return -number, nil
		default:
			return nil, fmt.Errorf("cannot apply unary %q to %T", unary.Operator, operand)
		}
	default:
		return nil, fmt.Errorf("unsupported unary operator %q", unary.Operator)
	}
}

func convertArray(array *configast.ArrayLiteral) ([]any, error) {
	if array == nil {
		return []any{}, nil
	}
	result := make([]any, 0, len(array.Items))
	for _, item := range array.Items {
		element, ok := item.(*configast.ArrayElement)
		if !ok {
			continue
		}
		converted, err := convertExpression(element.Value)
		if err != nil {
			return nil, err
		}
		result = append(result, converted)
	}
	return result, nil
}

func convertDictionary(dictionary *configast.DictionaryLiteral) (map[string]any, error) {
	result := make(map[string]any)
	if dictionary == nil {
		return result, nil
	}
	for _, item := range dictionary.Items {
		entry, ok := item.(*configast.DictionaryEntry)
		if !ok {
			continue
		}
		key, err := convertKeyExpression(entry.Key)
		if err != nil {
			return nil, err
		}
		value, err := convertExpression(entry.Value)
		if err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, nil
}

func convertKeyExpression(key configast.Expression) (string, error) {
	switch node := key.(type) {
	case *configast.StringLiteral:
		return node.Value, nil
	case *configast.Identifier:
		return node.Name, nil
	default:
		converted, err := convertExpression(key)
		if err != nil {
			return "", err
		}
		if text, ok := converted.(string); ok {
			return text, nil
		}
		return FormatGodot(converted)
	}
}

func convertConstructor(constructor *configast.ConstructorCall) (any, error) {
	positional := make([]any, 0, len(constructor.Items))
	var properties map[string]any
	firstArgumentIsPositional := false
	firstClassName := ""

	argumentIndex := 0
	for _, item := range constructor.Items {
		argument, ok := item.(*configast.ConstructorArgument)
		if !ok {
			continue
		}
		if argument.Key == nil {
			if constructor.Name == "Object" && argumentIndex == 0 {
				firstArgumentIsPositional = true
				switch classNode := argument.Value.(type) {
				case *configast.Identifier:
					firstClassName = classNode.Name
				case *configast.StringLiteral:
					firstClassName = classNode.Value
				default:
					converted, err := convertExpression(argument.Value)
					if err != nil {
						return nil, err
					}
					formatted, err := FormatGodot(converted)
					if err != nil {
						return nil, err
					}
					firstClassName = formatted
				}
				argumentIndex++
				continue
			}
			converted, err := convertExpression(argument.Value)
			if err != nil {
				return nil, err
			}
			positional = append(positional, converted)
		} else {
			key, err := convertKeyExpression(argument.Key)
			if err != nil {
				return nil, err
			}
			converted, err := convertExpression(argument.Value)
			if err != nil {
				return nil, err
			}
			if properties == nil {
				properties = make(map[string]any)
			}
			properties[key] = converted
		}
		argumentIndex++
	}

	if isPackedArrayConstructor(constructor.Name) && len(properties) == 0 {
		return positional, nil
	}

	if constructor.Name == "Object" && firstArgumentIsPositional {
		if properties == nil {
			properties = make(map[string]any)
		}
		result := map[string]any{
			"$type":      "Object",
			"class":      firstClassName,
			"properties": properties,
		}
		if len(positional) > 0 {
			result["args"] = positional
		}
		return result, nil
	}

	result := map[string]any{
		"$type": constructor.Name,
		"args":  positional,
	}
	if len(properties) > 0 {
		result["properties"] = properties
	}
	return result, nil
}

func isPackedArrayConstructor(name string) bool {
	switch name {
	case "PackedByteArray",
		"PackedInt32Array",
		"PackedInt64Array",
		"PackedFloat32Array",
		"PackedFloat64Array",
		"PackedStringArray",
		"PackedVector2Array",
		"PackedVector3Array",
		"PackedColorArray",
		"PackedVector4Array",
		"PoolByteArray",
		"PoolIntArray",
		"PoolRealArray",
		"PoolStringArray",
		"PoolVector2Array",
		"PoolVector3Array",
		"PoolColorArray":
		return true
	default:
		return false
	}
}

// FormatJSON encodes a query result value as deterministic JSON using jq's
// marshaling rules (sorted keys, no HTML escaping, big.Int precision).
func FormatJSON(value any, compact bool) ([]byte, error) {
	if err := validateQueryValue(value); err != nil {
		return nil, err
	}
	encoded, err := gojq.Marshal(value)
	if err != nil {
		return nil, err
	}
	if compact {
		return encoded, nil
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, encoded, "", "  "); err != nil {
		return nil, err
	}
	return indented.Bytes(), nil
}

// FormatRaw writes string values unquoted and falls back to FormatJSON for
// non-string values, matching jq's --raw-output behavior.
func FormatRaw(value any, compact bool) ([]byte, error) {
	if text, ok := value.(string); ok {
		return []byte(text), nil
	}
	return FormatJSON(value, compact)
}

// FormatGodot formats a query result value as canonical Godot ConfigFile
// Variant syntax.
func FormatGodot(value any) (string, error) {
	expression, err := toConfigExpression(value)
	if err != nil {
		return "", err
	}
	return configformat.Expression(expression), nil
}

func validateQueryValue(value any) error {
	switch typed := value.(type) {
	case nil, bool, int, float64, *big.Int, json.Number, string:
		return nil
	case []any:
		for _, element := range typed {
			if err := validateQueryValue(element); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for _, element := range typed {
			if err := validateQueryValue(element); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported value type %T", value)
	}
}

func toConfigExpression(value any) (configast.Expression, error) {
	switch typed := value.(type) {
	case nil:
		return &configast.NullLiteral{}, nil
	case bool:
		return &configast.BoolLiteral{Value: typed}, nil
	case int:
		return &configast.IntegerLiteral{Raw: strconv.Itoa(typed)}, nil
	case *big.Int:
		if typed == nil {
			return &configast.NullLiteral{}, nil
		}
		return &configast.IntegerLiteral{Raw: typed.String()}, nil
	case json.Number:
		text := typed.String()
		if strings.ContainsAny(text, ".eE") {
			return &configast.FloatLiteral{Raw: text}, nil
		}
		return &configast.IntegerLiteral{Raw: text}, nil
	case float64:
		switch {
		case math.IsNaN(typed):
			return &configast.FloatLiteral{Raw: "nan"}, nil
		case math.IsInf(typed, 1):
			return &configast.FloatLiteral{Raw: "inf"}, nil
		case math.IsInf(typed, -1):
			return &configast.FloatLiteral{Raw: "-inf"}, nil
		default:
			formatted := strconv.FormatFloat(typed, 'g', -1, 64)
			if !strings.ContainsAny(formatted, ".eE") {
				formatted += ".0"
			}
			return &configast.FloatLiteral{Raw: formatted}, nil
		}
	case string:
		return &configast.StringLiteral{Value: typed}, nil
	case []any:
		items := make([]configast.ArrayItem, 0, len(typed))
		for _, element := range typed {
			converted, err := toConfigExpression(element)
			if err != nil {
				return nil, err
			}
			items = append(items, &configast.ArrayElement{Value: converted})
		}
		return &configast.ArrayLiteral{Items: items}, nil
	case map[string]any:
		if constructor, ok, err := toConstructorCall(typed); ok || err != nil {
			return constructor, err
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		items := make([]configast.DictionaryItem, 0, len(keys))
		for _, key := range keys {
			converted, err := toConfigExpression(typed[key])
			if err != nil {
				return nil, err
			}
			items = append(items, &configast.DictionaryEntry{
				Key:   &configast.StringLiteral{Value: key},
				Value: converted,
			})
		}
		return &configast.DictionaryLiteral{Items: items}, nil
	default:
		return nil, fmt.Errorf("unsupported value type %T", value)
	}
}

func toConstructorCall(object map[string]any) (*configast.ConstructorCall, bool, error) {
	typeName, ok := object["$type"].(string)
	if !ok || !isIdentifier(typeName) {
		return nil, false, nil
	}

	if typeName == "Object" {
		className, hasClass := object["class"].(string)
		if !hasClass || !isIdentifier(className) {
			return nil, false, nil
		}
		for key := range object {
			if key != "$type" && key != "class" && key != "properties" && key != "args" {
				return nil, false, nil
			}
		}
		items := []configast.ConstructorItem{
			&configast.ConstructorArgument{Value: &configast.Identifier{Name: className}},
		}
		if rawArgs, hasArgs := object["args"]; hasArgs {
			arguments, ok := rawArgs.([]any)
			if !ok {
				return nil, false, nil
			}
			for _, argument := range arguments {
				converted, err := toConfigExpression(argument)
				if err != nil {
					return nil, false, err
				}
				items = append(items, &configast.ConstructorArgument{Value: converted})
			}
		}
		if rawProperties, hasProperties := object["properties"]; hasProperties {
			properties, ok := rawProperties.(map[string]any)
			if !ok {
				return nil, false, nil
			}
			propertyKeys := make([]string, 0, len(properties))
			for key := range properties {
				propertyKeys = append(propertyKeys, key)
			}
			sort.Strings(propertyKeys)
			for _, key := range propertyKeys {
				converted, err := toConfigExpression(properties[key])
				if err != nil {
					return nil, false, err
				}
				items = append(items, &configast.ConstructorArgument{
					Key:   &configast.StringLiteral{Value: key},
					Value: converted,
				})
			}
		}
		return &configast.ConstructorCall{Name: "Object", Items: items}, true, nil
	}

	rawArgs, hasArgs := object["args"]
	if !hasArgs {
		return nil, false, nil
	}
	arguments, ok := rawArgs.([]any)
	if !ok {
		return nil, false, nil
	}
	for key := range object {
		if key != "$type" && key != "args" && key != "properties" {
			return nil, false, nil
		}
	}
	items := make([]configast.ConstructorItem, 0, len(arguments))
	for _, argument := range arguments {
		converted, err := toConfigExpression(argument)
		if err != nil {
			return nil, false, err
		}
		items = append(items, &configast.ConstructorArgument{Value: converted})
	}
	if rawProperties, hasProperties := object["properties"]; hasProperties {
		properties, ok := rawProperties.(map[string]any)
		if !ok {
			return nil, false, nil
		}
		propertyKeys := make([]string, 0, len(properties))
		for key := range properties {
			propertyKeys = append(propertyKeys, key)
		}
		sort.Strings(propertyKeys)
		for _, key := range propertyKeys {
			converted, err := toConfigExpression(properties[key])
			if err != nil {
				return nil, false, err
			}
			items = append(items, &configast.ConstructorArgument{
				Key:   &configast.StringLiteral{Value: key},
				Value: converted,
			})
		}
	}
	return &configast.ConstructorCall{Name: typeName, Items: items}, true, nil
}

func isIdentifier(text string) bool {
	if text == "" {
		return false
	}
	for index, character := range text {
		if index == 0 {
			if character != '_' && !unicode.IsLetter(character) {
				return false
			}
			continue
		}
		if character != '_' && !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			return false
		}
	}
	return true
}
