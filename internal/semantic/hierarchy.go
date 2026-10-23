package semantic

import "github.com/cafecito-games/gdparser/ast"

// ClassID returns the stable project class ID of the top-level class declared
// by the script at path. The second result is false when path names no indexed
// top-level class, which happens when the source failed to parse.
func (a *Analyzer) ClassID(path string) (string, bool) {
	if a == nil || a.interfaces == nil || a.interfaces.index == nil {
		return "", false
	}
	class := a.interfaces.index.TopLevel[path]
	if class == nil {
		return "", false
	}
	return class.ID, true
}

// AncestorClassIDs returns the project ancestors of id, nearest first and
// excluding id itself. It is empty when id has no project base, or when the
// ancestry is unresolved or cyclic.
func (a *Analyzer) AncestorClassIDs(id string) []string {
	if a == nil || a.interfaces == nil || a.interfaces.index == nil {
		return nil
	}
	ancestry := a.interfaces.index.Ancestry(id)
	if len(ancestry) <= 1 {
		return nil
	}
	return ancestry[1:]
}

// DescendantClassIDs returns every transitive project descendant of id, sorted
// and excluding id itself.
func (a *Analyzer) DescendantClassIDs(id string) []string {
	if a == nil || a.interfaces == nil || a.interfaces.index == nil {
		return nil
	}
	return a.interfaces.index.Descendants(id)
}

// ClassPath returns the script path that declares class id. The second result
// is false when id names no indexed class.
func (a *Analyzer) ClassPath(id string) (string, bool) {
	if a == nil || a.interfaces == nil || a.interfaces.index == nil {
		return "", false
	}
	class := a.interfaces.index.Classes[id]
	if class == nil {
		return "", false
	}
	return class.Path, true
}

// NativeBase resolves the terminal native engine class of the class identified
// by id, walking its project ancestry. It returns the engine class Type, whose
// recorded base chain covers the full native ancestry, or the zero Type when
// the ancestry is unavailable (no such class, a cycle, or an unresolved project
// base). A terminal base that names no engine class yields an Unknown Type.
//
// Unlike ClassInterface.Type, this resolves the native base directly from the
// index rather than through the class interface, so an unrelated source parse
// failure does not blank the native ancestry of a class whose own chain is
// fully spelled.
func (a *Analyzer) NativeBase(id string) Type {
	if a == nil || a.interfaces == nil || a.interfaces.index == nil || a.engine == nil {
		return Type{}
	}
	index := a.interfaces.index
	seen := map[string]bool{}
	for current := id; current != ""; {
		if seen[current] {
			return Type{}
		}
		seen[current] = true
		class := index.Classes[current]
		if class == nil {
			return Type{}
		}
		if class.UnresolvedBase {
			return Type{}
		}
		if class.ParentID == "" {
			base := class.ExternalBase
			if base == "" {
				base = "RefCounted"
			}
			return a.engine.Class(base)
		}
		current = class.ParentID
	}
	return Type{}
}

// MemberDeclaringClass resolves a member access to the stable project class ID
// that declares the selected member. It returns "" when the receiver type is
// unresolvable, the member is absent or ambiguous, or the selected member is
// declared by an engine class rather than a project class.
func (a *Analyzer) MemberDeclaringClass(expression *ast.MemberExpression) string {
	if a == nil || isNilNode(expression) || a.interfaces == nil {
		return ""
	}
	receiver := a.memberReceiverType(expression.Object)
	if receiver.Kind() != KindClass {
		return ""
	}
	var resolved LookupResult
	if receiver.Meta() {
		resolved = a.interfaces.LookupMetaMember(receiver, expression.Property)
	} else {
		resolved = a.interfaces.LookupMember(receiver, expression.Property)
	}
	if resolved.State() != LookupFound {
		return ""
	}
	member, ok := resolved.Member()
	if !ok {
		return ""
	}
	return member.DeclaringClassID()
}

// memberReceiverType resolves the static type of a member-access receiver. An
// ordinary receiver is its reduced type. A constructor call whose argument
// failed to reduce still resolves to its constructed class, because X.new(...)
// yields an X regardless of its arguments; the whole-call reduction loses that
// result when it fails closed on the argument.
func (a *Analyzer) memberReceiverType(expression ast.Expression) Type {
	receiver := a.TypeOf(expression)
	if receiver.Kind() != KindUnknown {
		return receiver
	}
	call, ok := expression.(*ast.CallExpression)
	if !ok {
		return receiver
	}
	callee, ok := call.Callee.(*ast.MemberExpression)
	if !ok || callee.Property != "new" {
		return receiver
	}
	if a.scopes == nil {
		return receiver
	}
	scope, ok := a.scopes.ScopeAt(callee)
	if !ok {
		return receiver
	}
	token := reductionContextToken{}
	if a.narrow != nil {
		token = a.narrow.tokenAt(callee)
	}
	result := a.typeOfIn(callee, scope, token)
	if !result.hasConstructor {
		return receiver
	}
	return result.constructor
}

// DeclaresMethod reports whether class id directly declares a method named name.
func (a *Analyzer) DeclaresMethod(id, name string) bool {
	if a == nil || a.interfaces == nil || a.interfaces.index == nil {
		return false
	}
	class := a.interfaces.index.Classes[id]
	if class == nil {
		return false
	}
	for _, declaration := range class.Declarations {
		if declaration.Kind == DeclarationMethod && declaration.Name == name {
			return true
		}
	}
	return false
}
