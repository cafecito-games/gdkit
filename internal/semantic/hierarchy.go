package semantic

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
