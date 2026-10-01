// Package ignore implements gitignore-style path matching for the
// .gdkitignore file: one pattern per line, "!" to re-include, a trailing "/"
// for directories only, and the last matching pattern deciding.
//
// It differs from git in four ways:
//
//   - A path is tested as itself and through each of its ancestor directories,
//     so a negated pattern can re-include something inside an ignored
//     directory.
//   - Matching is case-sensitive whatever the filesystem, where git follows
//     core.ignoreCase.
//   - "?" and a character class match one character, where git matches one
//     byte, so the two disagree on names outside ASCII.
//   - A malformed pattern is an error that names its line, where git accepts
//     it silently: an unterminated character class, a range that runs
//     backwards, an unknown class name, a class that could only match "/", a
//     lone "!", and a trailing lone backslash.
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
			// Stars that do not fill a whole segment are ordinary stars,
			// however many there are: "a**/b" is "a*/b".
			crossesDirectories := end-index >= 2 && atSegmentStart
			switch {
			case crossesDirectories && end < len(characters) && characters[end] == '/':
				expression.WriteString("(?:.*/)?")
				end++
			case crossesDirectories && end == len(characters):
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
// returns the index just past its closing bracket. A class never matches a
// path separator, as in git, so a range that spans "/" is written as the two
// ranges on either side of it.
func compileClass(characters []rune, start int) (string, int, error) {
	var class strings.Builder
	class.WriteString("[")
	index := start + 1
	negated := index < len(characters) && (characters[index] == '!' || characters[index] == '^')
	if negated {
		class.WriteString("^/")
		index++
	}
	members := 0
	first := true
	for index < len(characters) {
		character := characters[index]
		if character == ']' && !first {
			if members == 0 && !negated {
				return "", 0, fmt.Errorf(`character class %q matches nothing, because a class never matches "/"`, string(characters[start:index+1]))
			}
			class.WriteString("]")
			return class.String(), index + 1, nil
		}
		first = false
		if character == '[' && index+1 < len(characters) && characters[index+1] == ':' {
			end := strings.Index(string(characters[index:]), ":]")
			if end == -1 {
				return "", 0, errors.New("unterminated character class")
			}
			named := string(characters[index:])[:end+2]
			ranges, known := namedClasses[named]
			if !known {
				return "", 0, fmt.Errorf("unknown character class name %q", named)
			}
			for _, bounds := range ranges {
				members += writeClassRange(&class, bounds[0], bounds[1])
			}
			index += len([]rune(named))
			continue
		}
		low, next, err := classMember(characters, index)
		if err != nil {
			return "", 0, err
		}
		high := low
		// A hyphen makes a range unless it is the last member of the class.
		if next+1 < len(characters) && characters[next] == '-' && characters[next+1] != ']' {
			high, next, err = classMember(characters, next+1)
			if err != nil {
				return "", 0, err
			}
			if high < low {
				return "", 0, fmt.Errorf(`character class range "%c-%c" runs backwards`, low, high)
			}
		}
		members += writeClassRange(&class, low, high)
		index = next
	}
	return "", 0, errors.New("unterminated character class")
}

// namedClasses lists the POSIX classes git accepts as the ranges each stands
// for, with the path separator left out of the ones that hold it.
var namedClasses = map[string][][2]rune{
	"[:alnum:]":  {{'0', '9'}, {'A', 'Z'}, {'a', 'z'}},
	"[:alpha:]":  {{'A', 'Z'}, {'a', 'z'}},
	"[:blank:]":  {{' ', ' '}, {'\t', '\t'}},
	"[:cntrl:]":  {{0, 0x1f}, {0x7f, 0x7f}},
	"[:digit:]":  {{'0', '9'}},
	"[:graph:]":  {{'!', '~'}},
	"[:lower:]":  {{'a', 'z'}},
	"[:print:]":  {{' ', '~'}},
	"[:punct:]":  {{'!', '/'}, {':', '@'}, {'[', '`'}, {'{', '~'}},
	"[:space:]":  {{'\t', '\r'}, {' ', ' '}},
	"[:upper:]":  {{'A', 'Z'}},
	"[:xdigit:]": {{'0', '9'}, {'A', 'F'}, {'a', 'f'}},
}

// classMember reads the character at index of a class, taking a backslash as
// escaping the character after it, and returns the index that follows.
func classMember(characters []rune, index int) (rune, int, error) {
	if characters[index] != '\\' {
		return characters[index], index + 1, nil
	}
	if index+1 == len(characters) {
		return 0, 0, errors.New("trailing backslash escapes nothing")
	}
	return characters[index+1], index + 2, nil
}

// writeClassRange writes the characters from low to high into a class, without
// the path separator, and returns how many ranges that took: none when the
// separator was all there was.
func writeClassRange(class *strings.Builder, low, high rune) int {
	if low <= '/' && '/' <= high {
		return writeClassRange(class, low, '/'-1) + writeClassRange(class, '/'+1, high)
	}
	if low > high {
		return 0
	}
	class.WriteString(escapeClassMember(low))
	if high > low {
		class.WriteString("-")
		class.WriteString(escapeClassMember(high))
	}
	return 1
}

func escapeClassMember(character rune) string {
	if strings.ContainsRune(`\]^[-`, character) {
		return `\` + string(character)
	}
	return string(character)
}
