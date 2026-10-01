package architecture

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser/ast"
	"github.com/cafecito-games/gdparser/token"
)

var typeNamePattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

var engineNames = map[string]struct{}{
	"Node": {}, "Control": {}, "Node2D": {}, "SceneTree": {},
	"Input": {}, "ProjectSettings": {}, "Time": {}, "FileAccess": {}, "ResourceLoader": {},
}

var nodeInspectionCalls = map[string]struct{}{
	"get_node": {}, "get_node_or_null": {}, "find_child": {}, "find_children": {}, "has_node": {},
}

type parsedFile struct {
	file File
	tree *ast.File
}

type shadow struct {
	name       string
	start, end int
}

// Analyzer runs architectural checks for one project root.
type Analyzer struct {
	Root   string
	Config Config
}

// NewAnalyzer constructs an analyzer rooted at root.
func NewAnalyzer(root string, config Config) (*Analyzer, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Analyzer{Root: absolute, Config: config}, nil
}

// Analyze indexes the project and returns all unsuppressed diagnostics.
func (a *Analyzer) Analyze() (Report, error) {
	allowlist, allowDiagnostics, err := loadAllowlist(a.Root, a.Config.Allowlist)
	if err != nil {
		return Report{}, err
	}
	// HonorIgnoreFile stays off: a file hidden by .gdkitignore would take its
	// class_name out of the index and turn references to it into false results.
	snapshot, err := project.Load(project.Config{
		Root:        a.Root,
		SourceRoots: a.Config.SourceRoots,
		Exclude:     a.Config.Exclude,
	})
	if err != nil {
		return Report{}, err
	}
	paths := snapshot.Paths
	uidPaths := snapshot.UIDs
	report := Report{
		Files:       make([]File, 0, len(paths)),
		Edges:       make([]Edge, 0),
		Diagnostics: append(make([]Diagnostic, 0, len(allowDiagnostics)), allowDiagnostics...),
	}
	parsed := make(map[string]*parsedFile, len(paths))
	classes := make(map[string]string)

	for _, name := range paths {
		script := snapshot.Scripts[name]
		classification, classified := a.Config.classify(name)
		indexed := File{Path: name, Classification: classification}
		if !classified && a.Config.Unclassified == "error" {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Rule: "classification.missing", Severity: SeverityError,
				Message: "GDScript file is not assigned to a layer and feature", Location: Location{Path: name, Line: 1, Column: 1},
			})
		}
		if script.ParseError != nil {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Rule: "source.parse", Severity: SeverityError, Message: script.ParseError.Error(), Location: Location{Path: name},
			})
			report.Files = append(report.Files, indexed)
			continue
		}
		if className, location := declaredClass(script.File); className != "" {
			indexed.ClassName = className
			if previous, exists := classes[className]; exists {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{
					Rule: "class_name.duplicate", Severity: SeverityError, Symbol: className, Target: previous,
					Message:  fmt.Sprintf("class_name %s is also declared by %s", className, previous),
					Location: Location{Path: name, Line: location.Start.Line, Column: location.Start.Column},
				})
			} else {
				classes[className] = name
			}
		}
		parsed[name] = &parsedFile{file: indexed, tree: script.File}
		report.Files = append(report.Files, indexed)
	}

	for _, name := range paths {
		file := parsed[name]
		if file == nil {
			continue
		}
		report.Edges = append(report.Edges, a.inspect(file, classes, uidPaths, &report.Diagnostics)...)
	}
	report.Edges = deduplicateEdges(report.Edges)
	a.checkDependencies(parsed, report.Edges, &report.Diagnostics)
	a.checkCycles(report.Edges, &report.Diagnostics)

	unsuppressed := report.Diagnostics[:0]
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Rule != "allowlist.adr" && allowlist.allows(diagnostic) {
			continue
		}
		unsuppressed = append(unsuppressed, diagnostic)
	}
	report.Diagnostics = unsuppressed
	report.sort()
	return report, nil
}

func declaredClass(file *ast.File) (string, token.Span) {
	for _, statement := range file.Statements {
		directive, ok := statement.(*ast.Directive)
		if !ok || directive.Name != "class_name" {
			continue
		}
		identifier, ok := directive.Value.(*ast.Identifier)
		if ok {
			return identifier.Name, identifier.Span()
		}
	}
	return "", token.Span{}
}

func (a *Analyzer) inspect(file *parsedFile, classes, uidPaths map[string]string, diagnostics *[]Diagnostic) []Edge {
	var edges []Edge
	shadows := collectShadows(file.tree)
	pure := file.file.Classification.Layer == "domain" || file.file.Classification.Layer == "application"
	runtimeBoundary := matchesAny(a.Config.RuntimeBoundaries, file.file.Path)
	testFile := matchesAny(a.Config.TestPatterns, file.file.Path)

	addClassEdge := func(name string, span token.Span) {
		target, ok := classes[name]
		if !ok || target == file.file.Path {
			return
		}
		edges = append(edges, Edge{
			From: file.file.Path, To: target, Kind: ClassReference, Symbol: name,
			Location: sourceLocation(file.file.Path, span),
		})
	}
	checkType := func(name string, span token.Span) {
		for _, part := range typeNamePattern.FindAllString(name, -1) {
			addClassEdge(part, span)
			if pure {
				if _, forbidden := engineNames[part]; forbidden {
					*diagnostics = append(*diagnostics, forbiddenEngine(file.file.Path, part, span))
				}
			}
		}
	}

	ast.Inspect(file.tree, func(node ast.Node) bool {
		if node == nil {
			return true
		}
		span := node.Span()
		switch value := node.(type) {
		case *ast.Directive:
			if value.Name == "class_name" {
				if value.Extends != nil {
					inspectExpression(value.Extends, func(name string, span token.Span) {
						addClassEdge(name, span)
						if pure {
							if _, forbidden := engineNames[name]; forbidden {
								*diagnostics = append(*diagnostics, forbiddenEngine(file.file.Path, name, span))
							}
						}
					})
				}
				return false
			}
		case *ast.Identifier:
			if isShadowed(shadows, value.Name, span.Start.Offset) {
				return true
			}
			addClassEdge(value.Name, span)
			if pure {
				if _, forbidden := engineNames[value.Name]; forbidden {
					*diagnostics = append(*diagnostics, forbiddenEngine(file.file.Path, value.Name, span))
				}
			}
		case *ast.TypeExpression:
			checkType(value.Name, span)
		case *ast.VariableDeclaration:
			checkType(value.Type, span)
		case *ast.ForStatement:
			checkType(value.Type, span)
		case *ast.FunctionDeclaration:
			checkType(value.ReturnType, span)
			for _, parameter := range value.Parameters {
				checkType(parameter.Type, span)
			}
		case *ast.LambdaExpression:
			checkType(value.ReturnType, span)
			for _, parameter := range value.Parameters {
				checkType(parameter.Type, span)
			}
		case *ast.SignalDeclaration:
			for _, parameter := range value.Parameters {
				checkType(parameter.Type, span)
			}
			if pure && !runtimeBoundary {
				*diagnostics = append(*diagnostics, Diagnostic{
					Rule: "engine.signal", Severity: SeverityError, Symbol: value.Name,
					Message:  "Godot signals are forbidden in pure domain and application code",
					Location: sourceLocation(file.file.Path, span),
				})
			}
		case *ast.ClassDeclaration:
			checkType(value.Extends, span)
		case *ast.CallExpression:
			callee := callName(value.Callee)
			calleeShadowed := false
			if identifier, ok := value.Callee.(*ast.Identifier); ok {
				calleeShadowed = isShadowed(shadows, identifier.Name, identifier.Span().Start.Offset)
			}
			if pure && callee == "get_tree" && !calleeShadowed {
				*diagnostics = append(*diagnostics, Diagnostic{
					Rule: "engine.tree", Severity: SeverityError, Symbol: callee,
					Message:  "get_tree() is forbidden in pure domain and application code",
					Location: sourceLocation(file.file.Path, span),
				})
			}
			if testFile && pure {
				if _, inspecting := nodeInspectionCalls[callee]; inspecting {
					*diagnostics = append(*diagnostics, Diagnostic{
						Rule: "test.node_inspection", Severity: SeverityError, Symbol: callee,
						Message:  "domain and application tests may not inspect scene nodes",
						Location: sourceLocation(file.file.Path, span),
					})
				}
			}
			if isResourceLoad(value.Callee) && !calleeShadowed && len(value.Arguments) > 0 {
				literal, ok := value.Arguments[0].(*ast.Literal)
				if !ok || literal.Kind != ast.StringLiteral {
					return true
				}
				resource, decodeErr := decodeString(literal.Raw)
				if decodeErr != nil {
					return true
				}
				target, projectResource := a.resolveResource(file.file.Path, resource, uidPaths)
				if !projectResource {
					return true
				}
				location := sourceLocation(file.file.Path, literal.Span())
				if strings.HasPrefix(target, "uid://") || !regularFile(filepath.Join(a.Root, filepath.FromSlash(target))) {
					message := fmt.Sprintf("resource %q does not exist", resource)
					if strings.HasPrefix(target, "uid://") {
						message = fmt.Sprintf("resource UID %q could not be resolved", resource)
					}
					*diagnostics = append(*diagnostics, Diagnostic{
						Rule: "resource.missing", Severity: SeverityError, Symbol: resource, Target: target,
						Message: message, Location: location,
					})
					return true
				}
				edges = append(edges, Edge{From: file.file.Path, To: target, Kind: ResourceLoad, Symbol: resource, Location: location})
				if testFile && pure && isScene(target) {
					*diagnostics = append(*diagnostics, Diagnostic{
						Rule: "test.scene_load", Severity: SeverityError, Symbol: resource, Target: target,
						Message: "domain and application tests may not load scenes", Location: location,
					})
				}
			}
		case *ast.NodePathExpression:
			if testFile && pure {
				*diagnostics = append(*diagnostics, Diagnostic{
					Rule: "test.node_inspection", Severity: SeverityError, Symbol: value.Path,
					Message:  "domain and application tests may not inspect scene nodes",
					Location: sourceLocation(file.file.Path, span),
				})
			}
		}
		return true
	})
	return edges
}

func inspectExpression(expression ast.Expression, visit func(string, token.Span)) {
	ast.Inspect(expression, func(node ast.Node) bool {
		if identifier, ok := node.(*ast.Identifier); ok {
			visit(identifier.Name, identifier.Span())
		}
		return true
	})
}

func collectShadows(file *ast.File) []shadow {
	var result []shadow
	add := func(name string, span token.Span) {
		if name != "" {
			result = append(result, shadow{name: name, start: span.Start.Offset, end: span.End.Offset})
		}
	}
	var walk func(ast.Node, token.Span)
	var walkStatements func([]ast.Statement, token.Span)
	declarationName := func(statement ast.Statement) string {
		switch value := statement.(type) {
		case *ast.VariableDeclaration:
			return value.Name
		case *ast.FunctionDeclaration:
			return value.Name
		case *ast.ClassDeclaration:
			return value.Name
		case *ast.SignalDeclaration:
			return value.Name
		case *ast.EnumDeclaration:
			return value.Name
		default:
			return ""
		}
	}
	walkStatements = func(statements []ast.Statement, boundary token.Span) {
		for _, statement := range statements {
			add(declarationName(statement), boundary)
		}
		for _, statement := range statements {
			walk(statement, boundary)
		}
	}
	walk = func(node ast.Node, boundary token.Span) {
		if node == nil {
			return
		}
		switch value := node.(type) {
		case *ast.File:
			walkStatements(value.Statements, value.Span())
			return
		case *ast.ClassDeclaration:
			walkStatements(value.Body, value.Span())
			return
		case *ast.FunctionDeclaration:
			for _, parameter := range value.Parameters {
				add(parameter.Name, value.Span())
			}
			walkStatements(value.Body, value.Span())
			return
		case *ast.LambdaExpression:
			for _, parameter := range value.Parameters {
				add(parameter.Name, value.Span())
			}
			walkStatements(value.Body, value.Span())
			return
		case *ast.VariableDeclaration:
			add(value.Name, boundary)
		case *ast.ForStatement:
			add(value.Variable, boundary)
		}
		for _, child := range ast.Children(node) {
			walk(child, boundary)
		}
	}
	walk(file, file.Span())
	return result
}

func isShadowed(shadows []shadow, name string, offset int) bool {
	for _, candidate := range shadows {
		if candidate.name == name && offset >= candidate.start && offset < candidate.end {
			return true
		}
	}
	return false
}

func sourceLocation(name string, span token.Span) Location {
	return Location{Path: name, Line: span.Start.Line, Column: span.Start.Column}
}

func forbiddenEngine(name, symbol string, span token.Span) Diagnostic {
	return Diagnostic{
		Rule: "engine.reference", Severity: SeverityError, Symbol: symbol,
		Message:  fmt.Sprintf("Godot engine type or global %s is forbidden in pure domain and application code", symbol),
		Location: sourceLocation(name, span),
	}
}

func callName(expression ast.Expression) string {
	switch value := expression.(type) {
	case *ast.Identifier:
		return value.Name
	case *ast.MemberExpression:
		return value.Property
	default:
		return ""
	}
}

func isResourceLoad(expression ast.Expression) bool {
	switch value := expression.(type) {
	case *ast.Identifier:
		return value.Name == "load" || value.Name == "preload"
	case *ast.MemberExpression:
		object, ok := value.Object.(*ast.Identifier)
		return ok && object.Name == "ResourceLoader" && value.Property == "load"
	default:
		return false
	}
}

func decodeString(raw string) (string, error) {
	if len(raw) >= 6 && (strings.HasPrefix(raw, `"""`) && strings.HasSuffix(raw, `"""`) || strings.HasPrefix(raw, `'''`) && strings.HasSuffix(raw, `'''`)) {
		return raw[3 : len(raw)-3], nil
	}
	if len(raw) < 2 {
		return "", fmt.Errorf("invalid string literal")
	}
	if raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		content := strings.ReplaceAll(raw[1:len(raw)-1], `\"`, `"`)
		content = strings.ReplaceAll(content, `"`, `\"`)
		content = strings.ReplaceAll(content, `\'`, `'`)
		raw = `"` + content + `"`
	}
	return strconv.Unquote(raw)
}

func (a *Analyzer) resolveResource(from, resource string, uidPaths map[string]string) (string, bool) {
	if strings.HasPrefix(resource, "uid://") {
		target, ok := uidPaths[resource]
		if !ok {
			return resource, true
		}
		return target, true
	}
	if strings.HasPrefix(resource, "user://") || strings.Contains(resource, "://") && !strings.HasPrefix(resource, "res://") {
		return "", false
	}
	var target string
	if strings.HasPrefix(resource, "res://") {
		target = strings.TrimPrefix(resource, "res://")
	} else {
		target = path.Join(path.Dir(from), resource)
	}
	target = path.Clean(strings.TrimPrefix(target, "/"))
	if target == ".." || strings.HasPrefix(target, "../") {
		return target, false
	}
	return target, true
}

func isScene(name string) bool {
	extension := strings.ToLower(path.Ext(name))
	return extension == ".tscn" || extension == ".scn"
}

func deduplicateEdges(edges []Edge) []Edge {
	seen := make(map[string]struct{}, len(edges))
	result := edges[:0]
	for _, edge := range edges {
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d", edge.From, edge.To, edge.Kind, edge.Location.Line, edge.Location.Column)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, edge)
	}
	return result
}
