package engineschema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/cafecito-games/gdkit/internal/semantic"
)

// ErrRawParse distinguishes malformed JSON from a well-formed dump whose
// retained schema is invalid. Lint maps these sentinels to stable failure kinds.
var ErrRawParse = errors.New("malformed extension API JSON")

// ErrRawInvalid reports a well-formed input that cannot publish a complete
// engine schema.
var ErrRawInvalid = errors.New("invalid extension API schema")

// MaxRawJSONBytes bounds project-controlled extension_api.json input before
// decoding. The official 4.7.2 dump is 6,965,057 bytes; 64 MiB leaves generous
// headroom for future supported minors without permitting unbounded reads.
const MaxRawJSONBytes = 64 << 20

// ReadRawJSON reads one raw dump with the same ceiling enforced by LoadRaw and
// GenerateArtifact. Callers use this before retaining project or maintainer
// input in memory; the +1 probe distinguishes an exact-boundary file from an
// oversized one without consuming the rest of the stream.
func ReadRawJSON(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, MaxRawJSONBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxRawJSONBytes {
		return nil, fmt.Errorf("%w: extension API JSON exceeds %d bytes", ErrRawInvalid, MaxRawJSONBytes)
	}
	return data, nil
}

type rawDocument struct {
	Header           *rawHeader        `json:"header"`
	BuiltinClasses   []rawBuiltinClass `json:"builtin_classes"`
	Classes          []rawClass        `json:"classes"`
	Singletons       []rawSingleton    `json:"singletons"`
	UtilityFunctions []rawUtility      `json:"utility_functions"`
}

type rawHeader struct {
	VersionMajor    *int    `json:"version_major"`
	VersionMinor    *int    `json:"version_minor"`
	VersionPatch    *int    `json:"version_patch"`
	VersionStatus   *string `json:"version_status"`
	VersionBuild    *string `json:"version_build"`
	VersionFullName *string `json:"version_full_name"`
}

type rawBuiltinClass struct {
	Name      string        `json:"name"`
	Members   []rawProperty `json:"members"`
	Methods   []rawMethod   `json:"methods"`
	Operators []rawOperator `json:"operators"`
}

type rawClass struct {
	Name       string        `json:"name"`
	Inherits   string        `json:"inherits"`
	Methods    []rawMethod   `json:"methods"`
	Properties []rawProperty `json:"properties"`
}

type rawMethod struct {
	Name        string            `json:"name"`
	ReturnValue rawOptionalType   `json:"return_value"`
	ReturnType  rawOptionalString `json:"return_type"`
	Arguments   []rawArgument     `json:"arguments"`
	IsStatic    *bool             `json:"is_static"`
	IsVararg    *bool             `json:"is_vararg"`
}

type rawType struct {
	Type string `json:"type"`
}

type rawArgument struct {
	Type         string            `json:"type"`
	DefaultValue rawOptionalString `json:"default_value"`
}

type rawProperty struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type rawOperator struct {
	Name       string `json:"name"`
	RightType  string `json:"right_type"`
	ReturnType string `json:"return_type"`
}

type rawSingleton struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type rawUtility struct {
	Name       string            `json:"name"`
	ReturnType rawOptionalString `json:"return_type"`
	Arguments  []rawArgument     `json:"arguments"`
	IsVararg   *bool             `json:"is_vararg"`
}

// These wrappers distinguish an omitted optional producer field from an
// explicitly null or wrongly typed one. Presence is semantic for defaults and
// for the two return encodings, so treating malformed as absent would be a
// fail-open coercion.
type rawOptionalString struct {
	Present bool
	Value   string
}

func (v *rawOptionalString) UnmarshalJSON(data []byte) error {
	v.Present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("must be a string, not null")
	}
	return json.Unmarshal(data, &v.Value)
}

type rawOptionalType struct {
	Present bool
	Value   rawType
}

func (v *rawOptionalType) UnmarshalJSON(data []byte) error {
	v.Present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("must be an object, not null")
	}
	return json.Unmarshal(data, &v.Value)
}

// LoadRaw validates and compiles one unmodified extension_api.json. Unknown
// producer fields are intentionally ignored for forward compatibility; every
// retained field is explicit above and fully validated before publication.
func LoadRaw(data []byte, source SourceKind, sourceCommit string) (*Loaded, error) {
	document, err := distillRaw(data, source, sourceCommit)
	if err != nil {
		return nil, err
	}
	digest, err := schemaDigest(document)
	if err != nil {
		return nil, fmt.Errorf("%w: calculate schema digest: %v", ErrRawInvalid, err)
	}
	engine, err := compileDocument(document)
	if err != nil {
		return nil, fmt.Errorf("%w: compile schema: %v", ErrRawInvalid, err)
	}
	provenance := document.Provenance
	provenance.SchemaSHA256 = digest
	return &Loaded{Engine: engine, Provenance: provenance}, nil
}

func distillRaw(data []byte, source SourceKind, sourceCommit string) (document, error) {
	if !source.valid() {
		return document{}, fmt.Errorf("%w: unknown source %q", ErrRawInvalid, source)
	}
	if source == SourceEmbedded {
		if err := requiredText("embedded source_commit", sourceCommit); err != nil {
			return document{}, err
		}
	}
	if source == SourceOverride && sourceCommit != "" {
		return document{}, fmt.Errorf("%w: override source_commit must be empty", ErrRawInvalid)
	}
	if len(data) > MaxRawJSONBytes {
		return document{}, fmt.Errorf("%w: extension API JSON exceeds %d bytes", ErrRawInvalid, MaxRawJSONBytes)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return document{}, fmt.Errorf("%w", ErrRawParse)
	}
	var raw rawDocument
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		// json.Valid already proved syntax and single-value framing. A typed
		// decode failure therefore means a retained field has the wrong shape.
		return document{}, fmt.Errorf("%w: %v", ErrRawInvalid, err)
	}
	if err := validateRawHeaderAndTables(raw); err != nil {
		return document{}, err
	}

	rawDigest := sha256.Sum256(data)
	result := document{
		Version: schemaVersion,
		Provenance: Provenance{
			Source: source,
			Version: Version{
				Major: *raw.Header.VersionMajor,
				Minor: *raw.Header.VersionMinor,
				Patch: *raw.Header.VersionPatch,
			},
			Status: *raw.Header.VersionStatus, Build: *raw.Header.VersionBuild,
			FullName: *raw.Header.VersionFullName, SourceCommit: sourceCommit,
			RawSHA256: hex.EncodeToString(rawDigest[:]),
		},
		Builtins:   make([]builtinRecord, 0, len(raw.BuiltinClasses)),
		Classes:    make([]classRecord, 0, len(raw.Classes)),
		Methods:    []methodRecord{},
		Properties: []propertyRecord{},
		Operators:  []operatorRecord{},
		Singletons: make([]singletonRecord, 0, len(raw.Singletons)),
		Utilities:  make([]utilityRecord, 0, len(raw.UtilityFunctions)),
	}
	for _, builtin := range raw.BuiltinClasses {
		if err := requiredText("builtin name", builtin.Name); err != nil {
			return document{}, err
		}
		result.Builtins = append(result.Builtins, builtinRecord{Name: builtin.Name})
		for _, method := range builtin.Methods {
			record, err := distillMethod(builtin.Name, method)
			if err != nil {
				return document{}, err
			}
			result.Methods = append(result.Methods, record)
		}
		for _, member := range builtin.Members {
			if err := validateProperty(builtin.Name, member); err != nil {
				return document{}, err
			}
			result.Properties = append(result.Properties, propertyRecord{Owner: builtin.Name, Name: member.Name, Type: member.Type})
		}
		for _, operator := range builtin.Operators {
			if err := requiredText("operator name", operator.Name); err != nil {
				return document{}, err
			}
			if err := optionalText("operator right_type", operator.RightType); err != nil {
				return document{}, err
			}
			if err := requiredText("operator return_type", operator.ReturnType); err != nil {
				return document{}, err
			}
			result.Operators = append(result.Operators, operatorRecord{
				Left: builtin.Name, Operator: operator.Name, Right: operator.RightType, ReturnType: operator.ReturnType,
			})
		}
	}
	for _, class := range raw.Classes {
		if err := requiredText("class name", class.Name); err != nil {
			return document{}, err
		}
		if err := optionalText("class inherits", class.Inherits); err != nil {
			return document{}, err
		}
		result.Classes = append(result.Classes, classRecord{Name: class.Name, Inherits: class.Inherits})
		for _, method := range class.Methods {
			record, err := distillMethod(class.Name, method)
			if err != nil {
				return document{}, err
			}
			result.Methods = append(result.Methods, record)
		}
		for _, property := range class.Properties {
			if err := validateProperty(class.Name, property); err != nil {
				return document{}, err
			}
			result.Properties = append(result.Properties, propertyRecord{Owner: class.Name, Name: property.Name, Type: property.Type})
		}
	}
	for _, singleton := range raw.Singletons {
		if err := requiredText("singleton name", singleton.Name); err != nil {
			return document{}, err
		}
		if err := requiredText("singleton type", singleton.Type); err != nil {
			return document{}, err
		}
		result.Singletons = append(result.Singletons, singletonRecord{Name: singleton.Name, Type: singleton.Type})
	}
	for _, utility := range raw.UtilityFunctions {
		if err := requiredText("utility name", utility.Name); err != nil {
			return document{}, err
		}
		if utility.IsVararg == nil {
			return document{}, fmt.Errorf("%w: utility %q is missing is_vararg", ErrRawInvalid, utility.Name)
		}
		returnType := "void"
		if utility.ReturnType.Present {
			returnType = utility.ReturnType.Value
			if err := requiredText("utility return_type", returnType); err != nil {
				return document{}, err
			}
		}
		arguments, err := distillArguments("utility "+utility.Name, utility.Arguments)
		if err != nil {
			return document{}, err
		}
		result.Utilities = append(result.Utilities, utilityRecord{
			Name: utility.Name, ReturnType: returnType, Arguments: arguments, Vararg: *utility.IsVararg,
		})
	}

	sortDocument(&result)
	if err := validateDocument(result, true); err != nil {
		return document{}, fmt.Errorf("%w: %v", ErrRawInvalid, err)
	}
	return result, nil
}

func validateRawHeaderAndTables(raw rawDocument) error {
	if raw.Header == nil {
		return fmt.Errorf("%w: missing header", ErrRawInvalid)
	}
	if raw.BuiltinClasses == nil {
		return fmt.Errorf("%w: missing builtin_classes", ErrRawInvalid)
	}
	if raw.Classes == nil {
		return fmt.Errorf("%w: missing classes", ErrRawInvalid)
	}
	if raw.Singletons == nil {
		return fmt.Errorf("%w: missing singletons", ErrRawInvalid)
	}
	if raw.UtilityFunctions == nil {
		return fmt.Errorf("%w: missing utility_functions", ErrRawInvalid)
	}
	header := raw.Header
	if header.VersionMajor == nil || header.VersionMinor == nil || header.VersionPatch == nil ||
		header.VersionStatus == nil || header.VersionBuild == nil || header.VersionFullName == nil {
		return fmt.Errorf("%w: header is missing a required version field", ErrRawInvalid)
	}
	if *header.VersionMajor <= 0 || *header.VersionMinor < 0 || *header.VersionPatch < 0 {
		return fmt.Errorf("%w: header has invalid numeric version %d.%d.%d", ErrRawInvalid,
			*header.VersionMajor, *header.VersionMinor, *header.VersionPatch)
	}
	for _, field := range []struct{ name, value string }{
		{name: "version_status", value: *header.VersionStatus},
		{name: "version_build", value: *header.VersionBuild},
		{name: "version_full_name", value: *header.VersionFullName},
	} {
		if err := requiredText("header "+field.name, field.value); err != nil {
			return err
		}
	}
	return nil
}

func distillMethod(owner string, raw rawMethod) (methodRecord, error) {
	if err := requiredText("method name", raw.Name); err != nil {
		return methodRecord{}, err
	}
	if raw.IsStatic == nil || raw.IsVararg == nil {
		return methodRecord{}, fmt.Errorf("%w: method %s.%s is missing is_static or is_vararg", ErrRawInvalid, owner, raw.Name)
	}
	if raw.ReturnValue.Present && raw.ReturnType.Present {
		return methodRecord{}, fmt.Errorf("%w: method %s.%s has both return_value and return_type", ErrRawInvalid, owner, raw.Name)
	}
	returnType := "void"
	if raw.ReturnValue.Present {
		returnType = raw.ReturnValue.Value.Type
	} else if raw.ReturnType.Present {
		returnType = raw.ReturnType.Value
	}
	if err := requiredText("method return type", returnType); err != nil {
		return methodRecord{}, err
	}
	arguments, err := distillArguments("method "+owner+"."+raw.Name, raw.Arguments)
	if err != nil {
		return methodRecord{}, err
	}
	return methodRecord{
		Owner: owner, Name: raw.Name, ReturnType: returnType, Arguments: arguments,
		Static: *raw.IsStatic, Vararg: *raw.IsVararg,
	}, nil
}

func distillArguments(owner string, raw []rawArgument) ([]argumentRecord, error) {
	arguments := make([]argumentRecord, len(raw))
	for index, argument := range raw {
		if err := requiredText(fmt.Sprintf("%s argument %d type", owner, index), argument.Type); err != nil {
			return nil, err
		}
		arguments[index] = argumentRecord{Type: argument.Type, HasDefault: argument.DefaultValue.Present}
	}
	return arguments, nil
}

func validateProperty(owner string, property rawProperty) error {
	if err := requiredText("property name", property.Name); err != nil {
		return err
	}
	if err := requiredText("property type", property.Type); err != nil {
		return err
	}
	return nil
}

func requiredText(field, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s %q is missing or malformed", ErrRawInvalid, field, value)
	}
	return nil
}

func optionalText(field, value string) error {
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s %q is malformed", ErrRawInvalid, field, value)
	}
	return nil
}

func sortDocument(value *document) {
	sort.Slice(value.Builtins, func(i, j int) bool { return value.Builtins[i].Name < value.Builtins[j].Name })
	sort.Slice(value.Classes, func(i, j int) bool { return value.Classes[i].Name < value.Classes[j].Name })
	sort.Slice(value.Methods, func(i, j int) bool {
		a, b := value.Methods[i], value.Methods[j]
		return a.Owner < b.Owner || (a.Owner == b.Owner && a.Name < b.Name)
	})
	sort.Slice(value.Properties, func(i, j int) bool {
		a, b := value.Properties[i], value.Properties[j]
		return a.Owner < b.Owner || (a.Owner == b.Owner && a.Name < b.Name)
	})
	sort.Slice(value.Operators, func(i, j int) bool {
		a, b := value.Operators[i], value.Operators[j]
		if a.Left != b.Left {
			return a.Left < b.Left
		}
		if a.Operator != b.Operator {
			return a.Operator < b.Operator
		}
		return a.Right < b.Right
	})
	sort.Slice(value.Singletons, func(i, j int) bool { return value.Singletons[i].Name < value.Singletons[j].Name })
	sort.Slice(value.Utilities, func(i, j int) bool { return value.Utilities[i].Name < value.Utilities[j].Name })
}

func validateDocument(value document, requireSorted bool) error {
	if value.Version != schemaVersion {
		return fmt.Errorf("unsupported engine schema version %d", value.Version)
	}
	if !value.Provenance.Source.valid() {
		return fmt.Errorf("unknown schema source %q", value.Provenance.Source)
	}
	if value.Provenance.Version.Major <= 0 || value.Provenance.Version.Minor < 0 || value.Provenance.Version.Patch < 0 {
		return fmt.Errorf("invalid engine version %+v", value.Provenance.Version)
	}
	for _, field := range []struct{ name, value string }{
		{name: "status", value: value.Provenance.Status},
		{name: "build", value: value.Provenance.Build},
		{name: "full_name", value: value.Provenance.FullName},
	} {
		if err := schemaText("schema provenance "+field.name, field.value); err != nil {
			return err
		}
	}
	if value.Provenance.Source == SourceEmbedded {
		if err := schemaText("embedded schema provenance source_commit", value.Provenance.SourceCommit); err != nil {
			return err
		}
	}
	if value.Provenance.Source == SourceOverride && value.Provenance.SourceCommit != "" {
		return errors.New("override schema provenance must not carry source_commit")
	}
	if value.Provenance.SchemaSHA256 != "" {
		return errors.New("serialized schema must not contain its own schema_sha256")
	}
	if !validDigest(value.Provenance.RawSHA256) {
		return errors.New("schema provenance has an invalid raw_sha256")
	}
	if value.Builtins == nil || value.Classes == nil || value.Methods == nil || value.Properties == nil ||
		value.Operators == nil || value.Singletons == nil || value.Utilities == nil {
		return errors.New("schema is missing a required table")
	}

	types := make(map[string]bool, len(value.Builtins)+len(value.Classes))
	for _, builtin := range value.Builtins {
		if err := schemaText("builtin name", builtin.Name); err != nil {
			return err
		}
		if types[builtin.Name] {
			return fmt.Errorf("duplicate engine type %q", builtin.Name)
		}
		types[builtin.Name] = true
	}
	for _, class := range value.Classes {
		if err := schemaText("class name", class.Name); err != nil {
			return err
		}
		if strings.TrimSpace(class.Inherits) != class.Inherits {
			return fmt.Errorf("class %q has malformed parent %q", class.Name, class.Inherits)
		}
		if types[class.Name] {
			return fmt.Errorf("duplicate engine type %q", class.Name)
		}
		types[class.Name] = true
	}
	if err := uniqueMethods(value.Methods, types); err != nil {
		return err
	}
	if err := uniqueProperties(value.Properties, types); err != nil {
		return err
	}
	if err := uniqueOperators(value.Operators, types); err != nil {
		return err
	}
	if err := uniqueSingletons(value.Singletons); err != nil {
		return err
	}
	if err := uniqueUtilities(value.Utilities); err != nil {
		return err
	}
	if requireSorted {
		copyValue := value
		copyValue.Builtins = append([]builtinRecord(nil), value.Builtins...)
		copyValue.Classes = append([]classRecord(nil), value.Classes...)
		copyValue.Methods = append([]methodRecord(nil), value.Methods...)
		copyValue.Properties = append([]propertyRecord(nil), value.Properties...)
		copyValue.Operators = append([]operatorRecord(nil), value.Operators...)
		copyValue.Singletons = append([]singletonRecord(nil), value.Singletons...)
		copyValue.Utilities = append([]utilityRecord(nil), value.Utilities...)
		sortDocument(&copyValue)
		canonical, _ := json.Marshal(copyValue)
		original, _ := json.Marshal(value)
		if !bytes.Equal(canonical, original) {
			return errors.New("schema records are not in canonical order")
		}
	}
	return nil
}

func uniqueMethods(rows []methodRecord, types map[string]bool) error {
	seen := map[engineIdentity]bool{}
	for _, row := range rows {
		if !types[row.Owner] {
			return fmt.Errorf("method %s.%s names unknown owner", row.Owner, row.Name)
		}
		if err := schemaText("method name", row.Name); err != nil {
			return err
		}
		if err := schemaText("method return_type", row.ReturnType); err != nil {
			return err
		}
		if row.Arguments == nil {
			return fmt.Errorf("method %s.%s is missing arguments", row.Owner, row.Name)
		}
		for _, argument := range row.Arguments {
			if err := schemaText("method argument type", argument.Type); err != nil {
				return err
			}
		}
		key := engineIdentity{first: row.Owner, second: row.Name}
		if seen[key] {
			return fmt.Errorf("duplicate engine method %s.%s", row.Owner, row.Name)
		}
		seen[key] = true
	}
	return nil
}

func uniqueProperties(rows []propertyRecord, types map[string]bool) error {
	seen := map[engineIdentity]bool{}
	for _, row := range rows {
		if !types[row.Owner] {
			return fmt.Errorf("property %s.%s names unknown owner", row.Owner, row.Name)
		}
		if err := schemaText("property name", row.Name); err != nil {
			return err
		}
		if err := schemaText("property type", row.Type); err != nil {
			return err
		}
		key := engineIdentity{first: row.Owner, second: row.Name}
		if seen[key] {
			return fmt.Errorf("duplicate engine property %s.%s", row.Owner, row.Name)
		}
		seen[key] = true
	}
	return nil
}

func uniqueOperators(rows []operatorRecord, types map[string]bool) error {
	seen := map[engineIdentity]bool{}
	for _, row := range rows {
		if !types[row.Left] {
			return fmt.Errorf("operator %s %s %s names unknown left type", row.Left, row.Operator, row.Right)
		}
		if err := schemaText("operator name", row.Operator); err != nil {
			return err
		}
		if strings.TrimSpace(row.Right) != row.Right {
			return fmt.Errorf("operator right type %q is malformed", row.Right)
		}
		if err := schemaText("operator return_type", row.ReturnType); err != nil {
			return err
		}
		key := engineIdentity{first: row.Left, second: row.Operator, third: row.Right}
		if seen[key] {
			return fmt.Errorf("duplicate engine operator %s %s %s", row.Left, row.Operator, row.Right)
		}
		seen[key] = true
	}
	return nil
}

func uniqueSingletons(rows []singletonRecord) error {
	seen := map[string]bool{}
	for _, row := range rows {
		if err := schemaText("singleton name", row.Name); err != nil {
			return err
		}
		if err := schemaText("singleton type", row.Type); err != nil {
			return err
		}
		if seen[row.Name] {
			return fmt.Errorf("duplicate engine singleton %s", row.Name)
		}
		seen[row.Name] = true
	}
	return nil
}

func uniqueUtilities(rows []utilityRecord) error {
	seen := map[string]bool{}
	for _, row := range rows {
		if err := schemaText("utility name", row.Name); err != nil {
			return err
		}
		if err := schemaText("utility return_type", row.ReturnType); err != nil {
			return err
		}
		if row.Arguments == nil {
			return fmt.Errorf("utility %s is missing arguments", row.Name)
		}
		for _, argument := range row.Arguments {
			if err := schemaText("utility argument type", argument.Type); err != nil {
				return err
			}
		}
		if seen[row.Name] {
			return fmt.Errorf("duplicate engine utility %s", row.Name)
		}
		seen[row.Name] = true
	}
	return nil
}

type engineIdentity struct {
	first  string
	second string
	third  string
}

func schemaText(field, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s %q is missing or malformed", field, value)
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func schemaDigest(value document) (string, error) {
	value.Provenance.SchemaSHA256 = ""
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func compileDocument(value document) (*semantic.Engine, error) {
	builder := semantic.NewEngineBuilder()
	for _, builtin := range value.Builtins {
		if err := builder.AddBuiltin(builtin.Name); err != nil {
			return nil, err
		}
	}
	for _, class := range value.Classes {
		if err := builder.AddClass(class.Name, class.Inherits); err != nil {
			return nil, err
		}
	}
	for _, method := range value.Methods {
		arguments := make([]semantic.EngineArgumentSpec, len(method.Arguments))
		for index, argument := range method.Arguments {
			arguments[index] = semantic.EngineArgumentSpec{Type: argument.Type, HasDefault: argument.HasDefault}
		}
		if err := builder.AddMethod(method.Owner, method.Name, method.ReturnType, arguments, method.Static, method.Vararg); err != nil {
			return nil, err
		}
	}
	for _, property := range value.Properties {
		if err := builder.AddProperty(property.Owner, property.Name, property.Type); err != nil {
			return nil, err
		}
	}
	for _, operator := range value.Operators {
		if err := builder.AddOperator(operator.Left, operator.Operator, operator.Right, operator.ReturnType); err != nil {
			return nil, err
		}
	}
	for _, singleton := range value.Singletons {
		if err := builder.AddSingleton(singleton.Name, singleton.Type); err != nil {
			return nil, err
		}
	}
	for _, utility := range value.Utilities {
		arguments := make([]semantic.EngineArgumentSpec, len(utility.Arguments))
		for index, argument := range utility.Arguments {
			arguments[index] = semantic.EngineArgumentSpec{Type: argument.Type, HasDefault: argument.HasDefault}
		}
		if err := builder.AddUtility(utility.Name, utility.ReturnType, arguments, utility.Vararg); err != nil {
			return nil, err
		}
	}
	return builder.Build()
}
