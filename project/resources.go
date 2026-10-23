package project

import (
	"os"

	"github.com/cafecito-games/gdparser/textresource"

	"github.com/cafecito-games/gdkit/internal/respath"
)

// parseResourceDocument reads a .tscn, .tres, or .escn file into its syntax
// tree. The header-only path in scanResourceHeader stays a line read because
// it needs one line of a file that can be megabytes; this is for a caller that
// needs what the whole document says.
func parseResourceDocument(name string) (*textresource.Document, bool) {
	source, err := os.ReadFile(name)
	if err != nil {
		return nil, false
	}
	document, parseErr := textresource.ParseFile(name, source)
	if parseErr != nil {
		return nil, false
	}
	return document, true
}

// documentLinks returns every resource reference a document makes, however it
// is written. An [ext_resource] contributes its path, or its uid when it
// carries no path; every other res:// or uid:// string anywhere in the
// document contributes itself.
//
// The blanket scan is deliberate. A resource can name another in a plain
// property — a script's exported path, a theme's font list, a custom
// resource's data table — and the only alternative to reading every string is
// a catalogue of the properties Godot happens to resolve, which cannot be
// complete and rots silently when the engine adds one. Over-reading keeps a
// file alive that nothing truly uses; under-reading recommends deleting a file
// that something does.
func documentLinks(document *textresource.Document) []Link {
	var links []Link
	seen := map[position]struct{}{}
	add := func(text string, line int) {
		if !respath.IsReference(text) {
			return
		}
		at := position{line: line, text: text}
		if _, duplicate := seen[at]; duplicate {
			return
		}
		seen[at] = struct{}{}
		links = append(links, Link{Reference: text, Line: line, Explicit: true})
	}
	inspect := func(node textresource.Node) {
		textresource.Inspect(node, func(child textresource.Node) bool {
			if value, ok := child.(*textresource.StringValue); ok {
				add(value.Value, value.Span().Start.Line)
			}
			return true
		})
	}
	for _, item := range document.Items {
		section, ok := item.(*textresource.Section)
		if !ok {
			inspect(item)
			continue
		}
		switch section.Type {
		case "gd_scene", "gd_resource":
			// The header declares the document's own identity. Reading it as
			// an outgoing reference would make every scene depend on itself,
			// and a self-edge keeps a dead file alive forever.
		case "ext_resource":
			line := section.Span().Start.Line
			if target := attributeString(section, "path"); target != "" {
				add(target, line)
			} else {
				add(attributeString(section, "uid"), line)
			}
		default:
			for _, attribute := range section.Attributes {
				inspect(attribute.Value)
			}
		}
		for _, body := range section.Body {
			inspect(body)
		}
	}
	return links
}

// externalSections returns the [ext_resource] sections of a document in order.
func externalSections(document *textresource.Document) []*textresource.Section {
	var sections []*textresource.Section
	for _, item := range document.Items {
		section, ok := item.(*textresource.Section)
		if ok && section.Type == "ext_resource" {
			sections = append(sections, section)
		}
	}
	return sections
}

// attributeString returns a section header attribute's string value, or ""
// when the header has no such attribute or its value is not a string.
func attributeString(section *textresource.Section, name string) string {
	for _, attribute := range section.Attributes {
		if attribute.Name != name {
			continue
		}
		if value, ok := attribute.Value.(*textresource.StringValue); ok {
			return value.Value
		}
		return ""
	}
	return ""
}

// position keys one reference site within a file, so the ext_resource pass and
// the blanket string pass do not record the same text on the same line twice.
type position struct {
	line int
	text string
}
