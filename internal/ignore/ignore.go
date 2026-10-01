// Package ignore implements gitignore-style path matching for the
// .gdkitignore file: one pattern per line, "!" to re-include, a trailing "/"
// for directories only, and the last matching pattern deciding.
//
// It differs from git in one way. A path is tested as itself and through each
// of its ancestor directories, so a negated pattern can re-include something
// inside an ignored directory.
package ignore

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Matcher decides which project-relative paths an ignore file excludes.
type Matcher struct {
	patterns    []pattern
	hasNegation bool
}

type pattern struct {
	expression    *regexp.Regexp
	negated       bool
	directoryOnly bool
}

// Parse compiles the contents of an ignore file. The error for a malformed
// pattern names its one-based line.
func Parse(source []byte) (*Matcher, error) {
	text := strings.TrimPrefix(string(source), "\xef\xbb\xbf")
	matcher := &Matcher{}
	for index, line := range strings.Split(text, "\n") {
		line = trimTrailingSpaces(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		compiled, matchable, err := compile(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", index+1, err)
		}
		if !matchable {
			continue
		}
		matcher.patterns = append(matcher.patterns, compiled)
		if compiled.negated {
			matcher.hasNegation = true
		}
	}
	return matcher, nil
}

// Ignored reports whether the ignore file excludes path, which is
// slash-separated and project-relative. The last pattern that matches the
// path or one of its ancestor directories decides. A nil Matcher ignores
// nothing.
func (m *Matcher) Ignored(path string, isDirectory bool) bool {
	if m == nil {
		return false
	}
	ignored := false
	for _, candidate := range m.patterns {
		// A pattern that would only repeat the current verdict cannot change
		// the outcome, so its regular expression is not run.
		if candidate.negated != ignored {
			continue
		}
		if candidate.matchesPathOrAncestor(path, isDirectory) {
			ignored = !candidate.negated
		}
	}
	return ignored
}

// HasNegation reports whether any pattern re-includes paths. Without one, an
// ignored directory can be skipped whole, because nothing inside it can be
// brought back.
func (m *Matcher) HasNegation() bool {
	return m != nil && m.hasNegation
}

func (p pattern) matchesPathOrAncestor(path string, isDirectory bool) bool {
	for index := 0; index < len(path); index++ {
		if path[index] == '/' && p.expression.MatchString(path[:index]) {
			return true
		}
	}
	if p.directoryOnly && !isDirectory {
		return false
	}
	return p.expression.MatchString(path)
}

// trimTrailingSpaces removes trailing spaces that are not backslash-escaped,
// as git does. Tabs are kept.
func trimTrailingSpaces(line string) string {
	firstTrailingSpace := -1
	for index := 0; index < len(line); index++ {
		switch line[index] {
		case ' ':
			if firstTrailingSpace == -1 {
				firstTrailingSpace = index
			}
		case '\\':
			index++
			firstTrailingSpace = -1
		default:
			firstTrailingSpace = -1
		}
	}
	if firstTrailingSpace == -1 {
		return line
	}
	return line[:firstTrailingSpace]
}

// compile turns one pattern line into a regular expression over whole
// project-relative paths. matchable is false for a pattern such as "/" that
// names nothing and so can never match.
func compile(line string) (compiled pattern, matchable bool, err error) {
	if strings.HasPrefix(line, "!") {
		compiled.negated = true
		line = line[1:]
		if line == "" {
			return pattern{}, false, errors.New(`"!" is not followed by a pattern`)
		}
	}
	if strings.HasSuffix(line, "/") {
		compiled.directoryOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	anchored := strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return pattern{}, false, nil
	}

	var expression strings.Builder
	expression.WriteString("^")
	if !anchored {
		expression.WriteString("(?:.*/)?")
	}
	characters := []rune(line)
	for index := 0; index < len(characters); {
		character := characters[index]
		switch character {
		case '*':
			end := index
			for end < len(characters) && characters[end] == '*' {
				end++
			}
			atSegmentStart := index == 0 || characters[index-1] == '/'
			switch {
			case end-index == 2 && atSegmentStart && end < len(characters) && characters[end] == '/':
				expression.WriteString("(?:.*/)?")
				end++
			case end-index == 2 && atSegmentStart && end == len(characters):
				expression.WriteString(".+")
			default:
				expression.WriteString("[^/]*")
			}
			index = end
		case '?':
			expression.WriteString("[^/]")
			index++
		case '[':
			class, next, classErr := compileClass(characters, index)
			if classErr != nil {
				return pattern{}, false, classErr
			}
			expression.WriteString(class)
			index = next
		case '\\':
			if index+1 == len(characters) {
				return pattern{}, false, errors.New("trailing backslash escapes nothing")
			}
			expression.WriteString(regexp.QuoteMeta(string(characters[index+1])))
			index += 2
		default:
			expression.WriteString(regexp.QuoteMeta(string(character)))
			index++
		}
	}
	expression.WriteString("$")
	compiled.expression, err = regexp.Compile(expression.String())
	if err != nil {
		return pattern{}, false, fmt.Errorf("invalid pattern %q", line)
	}
	return compiled, true, nil
}

// compileClass translates the character class that opens at start and
// returns the index just past its closing bracket.
func compileClass(characters []rune, start int) (string, int, error) {
	var class strings.Builder
	class.WriteString("[")
	index := start + 1
	negated := index < len(characters) && (characters[index] == '!' || characters[index] == '^')
	if negated {
		// A wildcard never matches a path separator, so a negated class must
		// exclude it explicitly.
		class.WriteString("^/")
		index++
	}
	first := true
	for index < len(characters) {
		character := characters[index]
		switch {
		case character == ']' && !first:
			class.WriteString("]")
			return class.String(), index + 1, nil
		case character == '\\':
			if index+1 == len(characters) {
				return "", 0, errors.New("trailing backslash escapes nothing")
			}
			class.WriteString(escapeClassMember(characters[index+1]))
			index += 2
		case character == '[' && index+1 < len(characters) && characters[index+1] == ':':
			end := strings.Index(string(characters[index:]), ":]")
			if end == -1 {
				return "", 0, errors.New("unterminated character class")
			}
			named := string(characters[index:])[:end+2]
			class.WriteString(named)
			index += len([]rune(named))
		case character == '-':
			class.WriteString("-")
			index++
		case character == '/' && !negated:
			index++
		default:
			class.WriteString(escapeClassMember(character))
			index++
		}
		first = false
	}
	return "", 0, errors.New("unterminated character class")
}

func escapeClassMember(character rune) string {
	if strings.ContainsRune(`\]^[-`, character) {
		return `\` + string(character)
	}
	return string(character)
}
