package semantic

import "github.com/cafecito-games/gdparser/ast"

// CollectionLifetime returns the narrowest collection type justified by a
// populated local literal and every modeled use after its declaration. A
// reasoned Unknown means the literal itself was unknown. An untyped Array or
// Dictionary means the literal was known but its complete binding lifetime
// could not be closed precisely.
func (a *Analyzer) CollectionLifetime(declaration *ast.VariableDeclaration) Type {
	generic, collection := genericCollection(declaration)
	if !collection {
		return Unknown("declaration has no direct collection literal")
	}
	if a == nil || a.scopes == nil || a.interfaces == nil || a.interfaces.index == nil || a.engine == nil || declaration == nil {
		return generic
	}
	if a.scopes.nodeShared(declaration) {
		return generic
	}
	scope, indexed := a.scopes.ScopeAt(declaration)
	if !indexed || scope == nil || scope.blocked != "" {
		return generic
	}
	body, owned := a.collectionOwner(scope.context.classID, declaration)
	if !owned {
		return generic
	}
	initial := a.TypeOf(declaration.Value)
	if initial.Kind() == KindUnknown {
		return initial
	}
	if initial.Kind() != generic.Kind() || !fullyKnownCollection(initial) {
		return generic
	}
	walker := collectionLifetimeWalker{
		analyzer:    a,
		declaration: declaration,
		current:     initial,
	}
	walker.visitStatements(body)
	if !walker.started || walker.lost {
		return generic
	}
	return walker.current
}

func genericCollection(declaration *ast.VariableDeclaration) (Type, bool) {
	if declaration == nil {
		return Type{}, false
	}
	switch declaration.Value.(type) {
	case *ast.ArrayLiteral:
		return Array(nil), true
	case *ast.DictionaryLiteral:
		return Dictionary(nil, nil), true
	default:
		return Type{}, false
	}
}

func fullyKnownCollection(typeValue Type) bool {
	switch typeValue.Kind() {
	case KindArray:
		element, typed := typeValue.Element()
		return typed && element.Kind() != KindUnknown
	case KindDictionary:
		key, typedKey := typeValue.Key()
		value, typedValue := typeValue.Value()
		return typedKey && typedValue && key.Kind() != KindUnknown && value.Kind() != KindUnknown
	default:
		return false
	}
}

type collectionOwnerKey struct {
	node ast.Node
	slot uint8
}

type collectionOwner struct {
	key  collectionOwnerKey
	body []ast.Statement
}

func (a *Analyzer) collectionOwner(classID string, target *ast.VariableDeclaration) ([]ast.Statement, bool) {
	class := a.interfaces.index.Classes[classID]
	if class == nil {
		return nil, false
	}
	owners := map[collectionOwnerKey][]ast.Statement{}
	lambdas := map[*ast.LambdaExpression]bool{}
	seenRoots := map[ast.Node]bool{}
	addLambdas := func(root ast.Node) {
		if isNilNode(root) {
			return
		}
		ast.Inspect(root, func(node ast.Node) bool {
			lambda, ok := node.(*ast.LambdaExpression)
			if ok && lambda != nil && !lambdas[lambda] {
				lambdas[lambda] = true
				owners[collectionOwnerKey{node: lambda}] = lambda.Body
			}
			return true
		})
	}
	for _, indexed := range class.Declarations {
		root := indexed.Node
		if isNilNode(root) || seenRoots[root] {
			continue
		}
		seenRoots[root] = true
		switch node := root.(type) {
		case *ast.FunctionDeclaration:
			owners[collectionOwnerKey{node: node}] = node.Body
		case *ast.VariableDeclaration:
			if node.Getter != nil {
				owners[collectionOwnerKey{node: node, slot: 1}] = node.Getter
			}
			if node.Setter != nil {
				owners[collectionOwnerKey{node: node, slot: 2}] = node.Setter.Body
			}
		}
		addLambdas(root)
	}
	var body []ast.Statement
	matches := 0
	for _, owner := range owners {
		if statementsContainDeclaration(owner, target) {
			body = owner
			matches++
		}
	}
	return body, matches == 1
}

func statementsContainDeclaration(statements []ast.Statement, target *ast.VariableDeclaration) bool {
	for _, statement := range statements {
		if isNilNode(statement) {
			continue
		}
		if statement == target {
			return true
		}
		switch node := statement.(type) {
		case *ast.IfStatement:
			for _, branch := range node.Branches {
				if statementsContainDeclaration(branch.Body, target) {
					return true
				}
			}
			if statementsContainDeclaration(node.Else, target) {
				return true
			}
		case *ast.WhileStatement:
			if statementsContainDeclaration(node.Body, target) {
				return true
			}
		case *ast.ForStatement:
			if statementsContainDeclaration(node.Body, target) {
				return true
			}
		case *ast.MatchStatement:
			for _, matchCase := range node.Cases {
				if statementsContainDeclaration(matchCase.Body, target) {
					return true
				}
			}
		}
	}
	return false
}

type collectionLifetimeWalker struct {
	analyzer    *Analyzer
	declaration *ast.VariableDeclaration
	target      BindingID
	hasTarget   bool
	started     bool
	lost        bool
	current     Type
}

func (w *collectionLifetimeWalker) visitStatements(statements []ast.Statement) {
	for _, statement := range statements {
		if w.lost {
			return
		}
		w.visitStatement(statement)
	}
}

func (w *collectionLifetimeWalker) visitStatement(statement ast.Statement) {
	if isNilNode(statement) || w.lost {
		return
	}
	if statement == w.declaration {
		if w.started {
			w.lost = true
			return
		}
		w.started = true
		return
	}
	if !w.started {
		w.visitNestedStatements(statement)
		return
	}
	switch node := statement.(type) {
	case *ast.ExpressionStatement:
		w.visitExpression(node.Expression)
	case *ast.VariableDeclaration:
		if w.aliasesTarget(node.Value) {
			w.lost = true
			return
		}
		w.visitExpression(node.Value)
	case *ast.Assignment:
		w.visitAssignment(node)
	case *ast.ReturnStatement:
		if w.aliasesTarget(node.Value) {
			w.lost = true
			return
		}
		w.visitExpression(node.Value)
	case *ast.IfStatement:
		for _, branch := range node.Branches {
			w.visitExpression(branch.Condition)
			w.visitStatements(branch.Body)
		}
		w.visitStatements(node.Else)
	case *ast.WhileStatement:
		w.visitExpression(node.Condition)
		w.visitStatements(node.Body)
	case *ast.ForStatement:
		if !w.directTarget(node.Iterable) && w.containsTarget(node.Iterable) {
			w.lost = true
			return
		}
		w.visitExpression(node.Iterable)
		w.visitStatements(node.Body)
	case *ast.MatchStatement:
		w.visitExpression(node.Value)
		for _, matchCase := range node.Cases {
			for _, pattern := range matchCase.Patterns {
				w.visitExpression(pattern)
			}
			w.visitExpression(matchCase.Guard)
			w.visitStatements(matchCase.Body)
		}
	case *ast.KeywordStatement, *ast.Comment, *ast.Annotation:
		w.visitChildren(statement)
	case *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.SignalDeclaration, *ast.EnumDeclaration:
		w.captureIn(statement)
	default:
		// A future statement form is safe only when it cannot possibly refer to
		// this binding. Unknown syntax that names it loses precision.
		w.captureIn(statement)
	}
}

func (w *collectionLifetimeWalker) visitNestedStatements(statement ast.Statement) {
	switch node := statement.(type) {
	case *ast.IfStatement:
		for _, branch := range node.Branches {
			w.visitStatements(branch.Body)
		}
		w.visitStatements(node.Else)
	case *ast.WhileStatement:
		w.visitStatements(node.Body)
	case *ast.ForStatement:
		w.visitStatements(node.Body)
	case *ast.MatchStatement:
		for _, matchCase := range node.Cases {
			w.visitStatements(matchCase.Body)
		}
	}
}

func (w *collectionLifetimeWalker) visitAssignment(assignment *ast.Assignment) {
	if assignment == nil || w.lost {
		return
	}
	if identifier, ok := assignment.Target.(*ast.Identifier); ok {
		if w.isTarget(identifier) {
			w.lost = true
			return
		}
	}
	if subscript, ok := assignment.Target.(*ast.SubscriptExpression); ok && w.directTarget(subscript.Object) {
		if assignment.Operator != "=" {
			w.lost = true
			return
		}
		if w.aliasesTarget(subscript.Index) {
			w.lost = true
			return
		}
		w.visitExpression(subscript.Index)
		w.visitExpression(assignment.Value)
		if w.aliasesTarget(assignment.Value) {
			w.lost = true
			return
		}
		switch w.current.Kind() {
		case KindArray:
			w.foldArray(assignment.Value)
		case KindDictionary:
			if w.aliasesTarget(subscript.Index) {
				w.lost = true
				return
			}
			w.foldDictionary(subscript.Index, assignment.Value)
		default:
			w.lost = true
		}
		return
	}
	if w.aliasesTarget(assignment.Value) || w.containsTarget(assignment.Target) {
		w.lost = true
		return
	}
	w.visitExpression(assignment.Target)
	w.visitExpression(assignment.Value)
}

// containsTarget reports any lexical use of the exact binding in a context
// where it is not the receiver of a classified direct mutation. Assignment
// targets are evaluated too: using a mutable collection as another
// collection's key stores its identity just as surely as using it as a value.
func (w *collectionLifetimeWalker) containsTarget(node ast.Node) bool {
	if isNilNode(node) || w.lost {
		return false
	}
	found := false
	ast.Inspect(node, func(candidate ast.Node) bool {
		if w.lost || found {
			return false
		}
		identifier, ok := candidate.(*ast.Identifier)
		if !ok || !w.isTarget(identifier) {
			return true
		}
		found = true
		return false
	})
	return found
}

func (w *collectionLifetimeWalker) visitExpression(expression ast.Expression) {
	if isNilNode(expression) || w.lost {
		return
	}
	switch node := expression.(type) {
	case *ast.Identifier:
		w.isTarget(node)
	case *ast.LambdaExpression:
		w.captureIn(node)
	case *ast.CallExpression:
		w.visitCall(node)
	case *ast.MemberExpression:
		if w.directTarget(node.Object) {
			w.lost = true
			return
		}
		if w.containsTarget(node.Object) {
			w.lost = true
			return
		}
		w.visitExpression(node.Object)
	case *ast.SubscriptExpression:
		if !w.directTarget(node.Object) && w.containsTarget(node.Object) {
			w.lost = true
			return
		}
		w.visitExpression(node.Object)
		w.visitExpression(node.Index)
	default:
		w.visitChildren(expression)
	}
}

func (w *collectionLifetimeWalker) visitCall(call *ast.CallExpression) {
	if call == nil || w.lost {
		return
	}
	if member, ok := call.Callee.(*ast.MemberExpression); ok {
		if w.directTarget(member.Object) {
			for _, argument := range call.Arguments {
				if w.aliasesTarget(argument) {
					w.lost = true
					return
				}
				w.visitExpression(argument)
			}
			w.applyMutation(member.Property, call.Arguments)
			return
		}
		if w.containsTarget(member.Object) {
			w.lost = true
			return
		}
	}
	for _, argument := range call.Arguments {
		if w.aliasesTarget(argument) {
			w.lost = true
			return
		}
	}
	w.visitExpression(call.Callee)
	for _, argument := range call.Arguments {
		w.visitExpression(argument)
	}
}

func (w *collectionLifetimeWalker) visitChildren(node ast.Node) {
	for _, child := range ast.Children(node) {
		switch child := child.(type) {
		case ast.Expression:
			w.visitExpression(child)
		case ast.Statement:
			w.visitStatement(child)
		}
	}
}

func (w *collectionLifetimeWalker) captureIn(node ast.Node) {
	if isNilNode(node) || w.lost {
		return
	}
	ast.Inspect(node, func(candidate ast.Node) bool {
		if w.lost {
			return false
		}
		if identifier, ok := candidate.(*ast.Identifier); ok && w.isTarget(identifier) {
			w.lost = true
			return false
		}
		return true
	})
}

func (w *collectionLifetimeWalker) directTarget(expression ast.Expression) bool {
	identifier, ok := expression.(*ast.Identifier)
	return ok && w.isTarget(identifier)
}

func (w *collectionLifetimeWalker) isTarget(identifier *ast.Identifier) bool {
	if identifier == nil || w.lost || identifier.Name != w.declaration.Name {
		return false
	}
	resolved := w.analyzer.scopes.Resolve(identifier)
	if resolved.State() != LookupFound {
		w.lost = true
		return false
	}
	binding, found := resolved.Binding()
	if !found {
		w.lost = true
		return false
	}
	if w.hasTarget {
		return binding.ID() == w.target
	}
	if binding.Kind() != BindingLocal || binding.Declaration() != w.declaration {
		return false
	}
	w.target = binding.ID()
	w.hasTarget = true
	return true
}

func (w *collectionLifetimeWalker) aliasesTarget(expression ast.Expression) bool {
	if isNilNode(expression) || w.lost {
		return false
	}
	switch node := expression.(type) {
	case *ast.Identifier:
		return w.isTarget(node)
	case *ast.CallExpression:
		// Their result is not the receiver. visitExpression separately checks
		// receiver mutation and arguments that pass the binding itself.
		return false
	case *ast.SubscriptExpression:
		// Indexing the collection itself yields one of its elements, but indexing
		// a composite that contains it can yield the collection identity.
		return !w.directTarget(node.Object) && w.containsTarget(node.Object)
	case *ast.MemberExpression:
		return w.aliasesTarget(node.Object)
	case *ast.LambdaExpression:
		return false
	default:
		for _, child := range ast.Children(expression) {
			if nested, ok := child.(ast.Expression); ok && w.aliasesTarget(nested) {
				return true
			}
		}
		return false
	}
}

type collectionMutationSpec struct {
	owner          string
	argumentTypes  []string
	defaults       []bool
	arrayValues    []int
	dictionaryKey  int
	dictionaryVal  int
	sourcePosition int
	requireAllArgs bool
}

func collectionMutation(owner, name string) (collectionMutationSpec, bool) {
	method := func(owner string, argumentTypes []string, defaults []bool) collectionMutationSpec {
		return collectionMutationSpec{owner: owner, argumentTypes: argumentTypes, defaults: defaults, dictionaryKey: -1, dictionaryVal: -1, sourcePosition: -1}
	}
	arrayValue := func(position int, argumentTypes ...string) collectionMutationSpec {
		spec := method("Array", argumentTypes, make([]bool, len(argumentTypes)))
		spec.arrayValues = []int{position}
		return spec
	}
	noValue := func(owner string, argumentTypes ...string) collectionMutationSpec {
		return method(owner, argumentTypes, make([]bool, len(argumentTypes)))
	}
	if owner == "Array" {
		switch name {
		case "append", "push_back", "push_front", "fill":
			return arrayValue(0, "Variant"), true
		case "insert", "set":
			return arrayValue(1, "int", "Variant"), true
		case "append_array", "assign":
			spec := method(owner, []string{"Array"}, []bool{false})
			spec.sourcePosition = 0
			return spec, true
		case "clear", "pop_back", "pop_front", "reverse", "shuffle", "sort", "make_read_only":
			return noValue(owner), true
		case "erase", "pop_at", "remove_at", "sort_custom":
			typeName := "int"
			if name == "erase" {
				typeName = "Variant"
			}
			if name == "sort_custom" {
				typeName = "Callable"
			}
			return noValue(owner, typeName), true
		}
	}
	if owner == "Dictionary" {
		switch name {
		case "set":
			spec := method(owner, []string{"Variant", "Variant"}, []bool{false, false})
			spec.dictionaryKey, spec.dictionaryVal = 0, 1
			return spec, true
		case "get_or_add":
			spec := method(owner, []string{"Variant", "Variant"}, []bool{false, true})
			spec.dictionaryKey, spec.dictionaryVal, spec.requireAllArgs = 0, 1, true
			return spec, true
		case "merge":
			spec := method(owner, []string{"Dictionary", "bool"}, []bool{false, true})
			spec.sourcePosition = 0
			return spec, true
		case "assign":
			spec := method(owner, []string{"Dictionary"}, []bool{false})
			spec.sourcePosition = 0
			return spec, true
		case "clear", "sort", "make_read_only":
			return noValue(owner), true
		case "erase":
			return noValue(owner, "Variant"), true
		}
	}
	return collectionMutationSpec{}, false
}

func (w *collectionLifetimeWalker) applyMutation(name string, arguments []ast.Expression) {
	owner := ""
	switch w.current.Kind() {
	case KindArray:
		owner = "Array"
	case KindDictionary:
		owner = "Dictionary"
	default:
		w.lost = true
		return
	}
	spec, classified := collectionMutation(owner, name)
	if !classified || w.analyzer.engine == nil {
		w.lost = true
		return
	}
	method, found := w.analyzer.engine.Method(owner, name)
	if !found || method.Owner() != spec.owner || method.Name() != name || method.Static() || method.Vararg() {
		w.lost = true
		return
	}
	engineArguments := method.Arguments()
	if len(engineArguments) != len(spec.argumentTypes) || len(engineArguments) != len(spec.defaults) {
		w.lost = true
		return
	}
	required := 0
	for index, argument := range engineArguments {
		expected := w.analyzer.engine.ResolveType(spec.argumentTypes[index])
		if expected.Kind() == KindUnknown || !argument.Type().Equal(expected) || argument.HasDefault() != spec.defaults[index] {
			w.lost = true
			return
		}
		if !argument.HasDefault() {
			required++
		}
	}
	if len(arguments) < required || len(arguments) > len(engineArguments) || (spec.requireAllArgs && len(arguments) != len(engineArguments)) {
		w.lost = true
		return
	}
	for _, position := range spec.arrayValues {
		if position >= len(arguments) {
			w.lost = true
			return
		}
		w.foldArray(arguments[position])
	}
	if spec.dictionaryKey >= 0 {
		if spec.dictionaryKey >= len(arguments) || spec.dictionaryVal >= len(arguments) {
			w.lost = true
			return
		}
		w.foldDictionary(arguments[spec.dictionaryKey], arguments[spec.dictionaryVal])
	}
	if spec.sourcePosition >= 0 {
		if spec.sourcePosition >= len(arguments) {
			w.lost = true
			return
		}
		w.foldSource(arguments[spec.sourcePosition])
	}
}

func (w *collectionLifetimeWalker) foldArray(expression ast.Expression) {
	if w.lost {
		return
	}
	element, typed := w.current.Element()
	value := w.analyzer.TypeOf(expression)
	if !typed || value.Kind() == KindUnknown {
		w.lost = true
		return
	}
	joined := w.analyzer.commonType(element, value)
	if joined.Kind() == KindUnknown {
		w.lost = true
		return
	}
	w.current = Array(&joined)
}

func (w *collectionLifetimeWalker) foldDictionary(keyExpression, valueExpression ast.Expression) {
	if w.lost {
		return
	}
	key, typedKey := w.current.Key()
	value, typedValue := w.current.Value()
	writtenKey := w.analyzer.TypeOf(keyExpression)
	writtenValue := w.analyzer.TypeOf(valueExpression)
	if !typedKey || !typedValue || writtenKey.Kind() == KindUnknown || writtenValue.Kind() == KindUnknown {
		w.lost = true
		return
	}
	key = w.analyzer.commonType(key, writtenKey)
	value = w.analyzer.commonType(value, writtenValue)
	if key.Kind() == KindUnknown || value.Kind() == KindUnknown {
		w.lost = true
		return
	}
	w.current = Dictionary(&key, &value)
}

func (w *collectionLifetimeWalker) foldSource(expression ast.Expression) {
	if w.lost {
		return
	}
	source := w.analyzer.TypeOf(expression)
	switch w.current.Kind() {
	case KindArray:
		element, typed := source.Element()
		if source.Kind() != KindArray || !typed || element.Kind() == KindUnknown {
			w.lost = true
			return
		}
		w.foldArrayType(element)
	case KindDictionary:
		key, typedKey := source.Key()
		value, typedValue := source.Value()
		if source.Kind() != KindDictionary || !typedKey || !typedValue || key.Kind() == KindUnknown || value.Kind() == KindUnknown {
			w.lost = true
			return
		}
		w.foldDictionaryTypes(key, value)
	default:
		w.lost = true
	}
}

func (w *collectionLifetimeWalker) foldArrayType(value Type) {
	element, typed := w.current.Element()
	if !typed {
		w.lost = true
		return
	}
	joined := w.analyzer.commonType(element, value)
	if joined.Kind() == KindUnknown {
		w.lost = true
		return
	}
	w.current = Array(&joined)
}

func (w *collectionLifetimeWalker) foldDictionaryTypes(writtenKey, writtenValue Type) {
	key, typedKey := w.current.Key()
	value, typedValue := w.current.Value()
	if !typedKey || !typedValue {
		w.lost = true
		return
	}
	key = w.analyzer.commonType(key, writtenKey)
	value = w.analyzer.commonType(value, writtenValue)
	if key.Kind() == KindUnknown || value.Kind() == KindUnknown {
		w.lost = true
		return
	}
	w.current = Dictionary(&key, &value)
}
