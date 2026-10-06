package semantic

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/cafecito-games/gdparser/ast"
)

// ResourceState describes the resolver's complete result vocabulary. A
// resolver must never turn an unsupported condition into a guessed resource
// type: every non-found state carries a reason instead.
type ResourceState uint8

const (
	ResourceFound ResourceState = iota + 1
	ResourceMissing
	ResourceInvalid
	ResourceEscapesProject
	ResourceAmbiguousUID
	ResourceUnsupportedKind
)

func (s ResourceState) String() string {
	switch s {
	case ResourceFound:
		return "found"
	case ResourceMissing:
		return "missing"
	case ResourceInvalid:
		return "invalid"
	case ResourceEscapesProject:
		return "escapes-project"
	case ResourceAmbiguousUID:
		return "ambiguous-uid"
	case ResourceUnsupportedKind:
		return "unsupported-kind"
	default:
		return fmt.Sprintf("ResourceState(%d)", s)
	}
}

// ResourceKind names the immutable inventory category a resolver retained.
// Imported assets deliberately do not get a generic Resource answer: their
// selected-engine type is not evidence the inventory holds.
type ResourceKind uint8

const (
	ResourceUnknown ResourceKind = iota
	ResourceScript
	ResourceScene
	ResourceText
	ResourceImported
)

func (k ResourceKind) String() string {
	switch k {
	case ResourceUnknown:
		return "unknown"
	case ResourceScript:
		return "script"
	case ResourceScene:
		return "scene"
	case ResourceText:
		return "text"
	case ResourceImported:
		return "imported"
	default:
		return fmt.Sprintf("ResourceKind(%d)", k)
	}
}

// ResourceProvenance records whether a target was established by its literal
// project path or by an identity claim. It lets callers retain the distinction
// without exposing the resolver's mutable source collections.
type ResourceProvenance uint8

const (
	ResourceProvenanceUnknown ResourceProvenance = iota
	ResourceLiteralPath
	ResourceUIDClaim
)

func (p ResourceProvenance) String() string {
	switch p {
	case ResourceProvenanceUnknown:
		return "unknown"
	case ResourceLiteralPath:
		return "literal-path"
	case ResourceUIDClaim:
		return "uid-claim"
	default:
		return fmt.Sprintf("ResourceProvenance(%d)", p)
	}
}

// ResourceResolution is one immutable resource lookup answer. A found answer
// has the canonical project-relative slash path; a non-found answer never has
// a path and always has a reason.
type ResourceResolution struct {
	state      ResourceState
	kind       ResourceKind
	requested  string
	path       string
	provenance ResourceProvenance
	reason     string
}

// FoundResource constructs the resolver's successful result value.
func FoundResource(kind ResourceKind, requested, canonicalPath string, provenance ResourceProvenance) ResourceResolution {
	return ResourceResolution{
		state:      ResourceFound,
		kind:       kind,
		requested:  requested,
		path:       canonicalPath,
		provenance: provenance,
	}
}

// UnresolvedResource constructs a reasoned non-found resolver result. The
// reducer validates every returned value again, so a custom resolver cannot
// smuggle a malformed or incomplete result into a type answer.
func UnresolvedResource(state ResourceState, kind ResourceKind, requested string, provenance ResourceProvenance, reason string) ResourceResolution {
	return ResourceResolution{
		state:      state,
		kind:       kind,
		requested:  requested,
		provenance: provenance,
		reason:     reason,
	}
}

// State reports the resolution state.
func (r ResourceResolution) State() ResourceState { return r.state }

// Kind reports the captured resource category.
func (r ResourceResolution) Kind() ResourceKind { return r.kind }

// Requested reports the decoded spelling passed to the resolver.
func (r ResourceResolution) Requested() string { return r.requested }

// Path reports a canonical project-relative slash path only for a found result.
func (r ResourceResolution) Path() string { return r.path }

// Provenance reports whether the answer came from a literal path or UID claim.
func (r ResourceResolution) Provenance() ResourceProvenance { return r.provenance }

// Reason explains a non-found result. Found results have no reason.
func (r ResourceResolution) Reason() string { return r.reason }

func resourceResolutionProblem(resolution ResourceResolution) string {
	switch resolution.state {
	case ResourceFound:
		if resolution.kind != ResourceScript && resolution.kind != ResourceScene && resolution.kind != ResourceText {
			return fmt.Sprintf("found resource has unsupported kind %s", resolution.kind)
		}
		if strings.TrimSpace(resolution.requested) == "" {
			return "found resource has an empty requested spelling"
		}
		if !validResourcePath(resolution.path) {
			return "found resource has no canonical project-relative path"
		}
		if resolution.provenance != ResourceLiteralPath && resolution.provenance != ResourceUIDClaim {
			return fmt.Sprintf("found resource has invalid provenance %s", resolution.provenance)
		}
		if resolution.reason != "" {
			return "found resource carries an unresolved reason"
		}
	case ResourceMissing, ResourceInvalid, ResourceEscapesProject, ResourceAmbiguousUID, ResourceUnsupportedKind:
		if resolution.path != "" {
			return "non-found resource exposes a canonical path"
		}
		if strings.TrimSpace(resolution.reason) == "" {
			return fmt.Sprintf("%s resource has no reason", resolution.state)
		}
		if resolution.provenance != ResourceLiteralPath && resolution.provenance != ResourceUIDClaim {
			return fmt.Sprintf("%s resource has invalid provenance %s", resolution.state, resolution.provenance)
		}
		if resolution.state == ResourceUnsupportedKind && resolution.kind == ResourceUnknown {
			return "unsupported resource has no captured kind"
		}
	default:
		return fmt.Sprintf("resource resolution returned invalid state %d", resolution.state)
	}
	return ""
}

func validResourcePath(value string) bool {
	if value == "" || value == "." || path.IsAbs(value) || strings.Contains(value, "\\") || strings.Contains(value, "://") {
		return false
	}
	if path.Clean(value) != value {
		return false
	}
	return value != ".." && !strings.HasPrefix(value, "../")
}

// decodeResourceLiteral accepts only a parsed GDScript String literal and
// returns its decoded value. Resource loading is deliberately narrower than
// ordinary expression reduction: StringName, interpolation, concatenation,
// and hand-built malformed literals never become resource paths.
func decodeResourceLiteral(expression ast.Expression) (string, error) {
	literal, ok := expression.(*ast.Literal)
	if !ok || literal == nil {
		return "", fmt.Errorf("argument is not a string literal")
	}
	if literal.Kind != ast.StringLiteral {
		return "", fmt.Errorf("literal kind %q is not String", literal.Kind)
	}
	return decodeResourceString(literal)
}

func decodeResourceString(literal *ast.Literal) (string, error) {
	if literal == nil {
		return "", fmt.Errorf("string literal is unavailable")
	}
	spelling := literal.Raw
	if literal.RawPrefix {
		if !strings.HasPrefix(spelling, "r") {
			return "", fmt.Errorf("raw string literal has no raw prefix")
		}
		spelling = spelling[1:]
	}
	if literal.Quote != '\'' && literal.Quote != '"' {
		return "", fmt.Errorf("string literal has invalid quote")
	}
	if len(spelling) < 2 || spelling[0] != literal.Quote || spelling[len(spelling)-1] != literal.Quote {
		return "", fmt.Errorf("string literal delimiters do not match its parsed shape")
	}
	var body string
	if literal.Triple {
		if len(spelling) < 6 || spelling[0] != literal.Quote || spelling[1] != literal.Quote || spelling[2] != literal.Quote ||
			spelling[len(spelling)-3] != literal.Quote || spelling[len(spelling)-2] != literal.Quote || spelling[len(spelling)-1] != literal.Quote {
			return "", fmt.Errorf("triple string literal delimiters do not match its parsed shape")
		}
		body = spelling[3 : len(spelling)-3]
	} else {
		if len(spelling) >= 6 && spelling[0] == literal.Quote && spelling[1] == literal.Quote && spelling[2] == literal.Quote &&
			spelling[len(spelling)-3] == literal.Quote && spelling[len(spelling)-2] == literal.Quote && spelling[len(spelling)-1] == literal.Quote {
			return "", fmt.Errorf("triple string literal disagrees with its parsed shape")
		}
		body = spelling[1 : len(spelling)-1]
	}
	if literal.RawPrefix {
		if !utf8.ValidString(body) {
			return "", fmt.Errorf("raw string literal contains invalid UTF-8")
		}
		return body, nil
	}
	return decodeResourceEscapes(body)
}

func decodeResourceEscapes(body string) (string, error) {
	var decoded strings.Builder
	decoded.Grow(len(body))
	for len(body) > 0 {
		if body[0] != '\\' {
			runeValue, size := utf8.DecodeRuneInString(body)
			if runeValue == utf8.RuneError && size == 1 {
				return "", fmt.Errorf("string literal contains invalid UTF-8")
			}
			decoded.WriteRune(runeValue)
			body = body[size:]
			continue
		}
		body = body[1:]
		if body == "" {
			return "", fmt.Errorf("string literal ends with an escape")
		}
		escape := body[0]
		body = body[1:]
		switch escape {
		case 'a':
			decoded.WriteByte('\a')
		case 'b':
			decoded.WriteByte('\b')
		case 'f':
			decoded.WriteByte('\f')
		case 'n':
			decoded.WriteByte('\n')
		case 'r':
			decoded.WriteByte('\r')
		case 't':
			decoded.WriteByte('\t')
		case 'v':
			decoded.WriteByte('\v')
		case '\\', '\'', '"':
			decoded.WriteByte(escape)
		case '\n':
			// A physical newline escaped by a backslash contributes no byte.
		case '\r':
			// Preserve the lexer's CRLF treatment as one escaped physical line.
			if strings.HasPrefix(body, "\n") {
				body = body[1:]
			}
		case 'u', 'U':
			digits := 4
			if escape == 'U' {
				digits = 6
			}
			if len(body) < digits {
				return "", fmt.Errorf("Unicode escape is incomplete")
			}
			value, err := strconv.ParseUint(body[:digits], 16, 32)
			if err != nil {
				return "", fmt.Errorf("Unicode escape is malformed")
			}
			body = body[digits:]
			runeValue := rune(value)
			if escape == 'u' && runeValue >= 0xd800 && runeValue <= 0xdbff {
				if len(body) < 6 || body[0] != '\\' || body[1] != 'u' {
					return "", fmt.Errorf("Unicode surrogate is unpaired")
				}
				low, lowErr := strconv.ParseUint(body[2:6], 16, 16)
				if lowErr != nil || low < 0xdc00 || low > 0xdfff {
					return "", fmt.Errorf("Unicode surrogate is unpaired")
				}
				body = body[6:]
				runeValue = utf16.DecodeRune(runeValue, rune(low))
			} else if utf16.IsSurrogate(runeValue) || !utf8.ValidRune(runeValue) {
				return "", fmt.Errorf("Unicode escape is not a scalar value")
			}
			decoded.WriteRune(runeValue)
		default:
			return "", fmt.Errorf("string literal has unsupported escape \\%c", escape)
		}
	}
	return decoded.String(), nil
}
