package assets

import (
	"github.com/cafecito-games/gdparser/ast"

	"github.com/cafecito-games/gdkit/project"
)

// classEdges connects each script to the scripts whose class_name it names.
//
// Without this almost nothing in a typed Godot project is reachable: a script
// is usually reached through its global class name rather than through a
// res:// path, so a reachability analysis that reads only paths reports the
// whole project as dead.
//
// Shadowing is deliberately not resolved, unlike in architecture. A local
// variable named like a class is read here as a reference to that class, so a
// shadow keeps a file alive that nothing really uses. That is a false
// negative — a file this tool fails to mention — and the alternative error is
// telling someone to delete a file their code is using. architecture resolves
// shadows because it must not invent a boundary violation; this must not
// invent a deletion.
func classEdges(snapshot *project.Snapshot, connect func(from, to string)) {
	classes := classIndex(snapshot)
	// An autoload is a project global too: Godot resolves the identifier
	// against the manifest, so naming one is a reference to its script.
	for name, target := range snapshot.Autoloads {
		if _, declared := classes[name]; !declared {
			classes[name] = target
		}
	}
	if len(classes) == 0 {
		return
	}
	for _, path := range snapshot.Paths {
		script := snapshot.Scripts[path]
		if script == nil || script.File == nil {
			continue
		}
		ast.Inspect(script.File, func(node ast.Node) bool {
			name := ""
			switch value := node.(type) {
			case *ast.Identifier:
				name = value.Name
			case *ast.TypeExpression:
				name = value.Name
			default:
				return true
			}
			if target, declared := classes[name]; declared {
				connect(path, target)
			}
			return true
		})
	}
}

// classIndex maps every declared class_name to the script declaring it. A
// name declared twice is ambiguous and is dropped rather than resolved to the
// first claimant: keeping one of them alive and not the other would be a
// guess, and architecture already reports the duplicate as an error.
func classIndex(snapshot *project.Snapshot) map[string]string {
	classes := make(map[string]string, len(snapshot.Paths))
	ambiguous := map[string]bool{}
	for _, path := range snapshot.Paths {
		script := snapshot.Scripts[path]
		if script == nil || script.File == nil {
			continue
		}
		name := declaredClass(script.File)
		if name == "" {
			continue
		}
		if _, taken := classes[name]; taken {
			ambiguous[name] = true
			continue
		}
		classes[name] = path
	}
	for name := range ambiguous {
		delete(classes, name)
	}
	return classes
}

// declaredClass returns the class_name a file declares, or "".
func declaredClass(file *ast.File) string {
	for _, statement := range file.Statements {
		directive, ok := statement.(*ast.Directive)
		if !ok || directive.Name != "class_name" {
			continue
		}
		if identifier, isIdentifier := directive.Value.(*ast.Identifier); isIdentifier {
			return identifier.Name
		}
	}
	return ""
}
