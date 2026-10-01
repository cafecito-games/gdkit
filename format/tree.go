package format

import (
	"reflect"
	"strings"
	"sync"

	"github.com/cafecito-games/gdparser/ast"
	gdformat "github.com/cafecito-games/gdparser/format"
)

// fieldRole says how one struct field takes part in a tree comparison.
type fieldRole int

const (
	// comparedField is compared as it is.
	comparedField fieldRole = iota
	// commentTextField holds comment text, compared by commentKey.
	commentTextField
	// operatorField holds an operator, compared by canonicalOperator.
	operatorField
)

// comparedStructField is one field a tree comparison looks at.
type comparedStructField struct {
	index int
	role  fieldRole
	// omittedWhenEmpty marks a field whose nil and empty forms are distinct
	// syntax, such as a signal written with and without a parameter list.
	omittedWhenEmpty bool
}

var (
	fileType     = reflect.TypeFor[ast.File]()
	literalType  = reflect.TypeFor[ast.Literal]()
	commentType  = reflect.TypeFor[ast.Comment]()
	binaryType   = reflect.TypeFor[ast.BinaryExpression]()
	unaryType    = reflect.TypeFor[ast.UnaryExpression]()
	fieldsByType sync.Map
)

// comparedFields lists the fields of a syntax tree struct that say what the
// source means. It leaves out what only says where or how it was written:
// every span, the blank line counts, and the name of the file.
func comparedFields(structType reflect.Type) []comparedStructField {
	if cached, ok := fieldsByType.Load(structType); ok {
		return cached.([]comparedStructField)
	}
	fields := make([]comparedStructField, 0, structType.NumField())
	for index := range structType.NumField() {
		field := structType.Field(index)
		if !field.IsExported() {
			continue
		}
		name, tagOptions, _ := strings.Cut(field.Tag.Get("json"), ",")
		switch {
		case name == "-", name == "span", strings.HasSuffix(name, "_span"), name == "blank_lines_before":
			continue
		case structType == fileType && field.Name == "Name":
			continue
		}
		compared := comparedStructField{index: index, omittedWhenEmpty: strings.Contains(tagOptions, "omitempty")}
		switch {
		case structType == commentType && field.Name == "Text":
			compared.role = commentTextField
		case (structType == binaryType || structType == unaryType) && field.Name == "Operator":
			compared.role = operatorField
		}
		fields = append(fields, compared)
	}
	fieldsByType.Store(structType, fields)
	return fields
}

// sameTree reports whether two syntax trees say the same thing, allowing only
// the respellings options asks the formatter to make. It reads both trees and
// changes neither.
func sameTree(before, after *ast.File, options gdformat.Options) bool {
	return sameValue(reflect.ValueOf(before), reflect.ValueOf(after), options)
}

func sameValue(before, after reflect.Value, options gdformat.Options) bool {
	switch before.Kind() {
	case reflect.Interface, reflect.Pointer:
		if before.IsNil() || after.IsNil() {
			return before.IsNil() == after.IsNil()
		}
		before, after = before.Elem(), after.Elem()
		if before.Type() != after.Type() {
			return false
		}
		return sameValue(before, after, options)
	case reflect.Struct:
		if before.Type() == literalType {
			return sameLiteral(before.Addr().Interface().(*ast.Literal), after.Addr().Interface().(*ast.Literal), options)
		}
		for _, field := range comparedFields(before.Type()) {
			if !sameField(field, before.Field(field.index), after.Field(field.index), options) {
				return false
			}
		}
		return true
	case reflect.Slice:
		if before.Len() != after.Len() {
			return false
		}
		for index := range before.Len() {
			if !sameValue(before.Index(index), after.Index(index), options) {
				return false
			}
		}
		return true
	case reflect.String:
		return before.String() == after.String()
	case reflect.Bool:
		return before.Bool() == after.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return before.Int() == after.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return before.Uint() == after.Uint()
	default:
		return reflect.DeepEqual(before.Interface(), after.Interface())
	}
}

func sameField(field comparedStructField, before, after reflect.Value, options gdformat.Options) bool {
	switch field.role {
	case commentTextField:
		return commentKey(before.String(), options) == commentKey(after.String(), options)
	case operatorField:
		return canonicalOperator(before.String(), options) == canonicalOperator(after.String(), options)
	}
	if field.omittedWhenEmpty && before.Kind() == reflect.Slice && before.IsNil() != after.IsNil() {
		return false
	}
	return sameValue(before, after, options)
}

// sameLiteral compares two literals by kind and by what their spelling
// stands for. The quote metadata only restates the spelling, so it is not
// compared on its own.
func sameLiteral(before, after *ast.Literal, options gdformat.Options) bool {
	if before.Kind != after.Kind {
		return false
	}
	if before.Raw == after.Raw {
		return true
	}
	return literalKey(before, options) == literalKey(after, options)
}
