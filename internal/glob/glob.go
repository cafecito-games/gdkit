// Package glob implements gdkit's path-matching syntax: **/ crosses
// directories, * and ? stay within a segment, and {feature} captures one
// segment. It is not path/filepath.Match.
package glob

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Pattern is a compiled glob together with the names it captures.
// A Pattern must be created by Compile; a zero Pattern has a nil regexp and will panic.
type Pattern struct {
	re       *regexp.Regexp
	captures []string
}

var cache sync.Map

// Compile compiles pattern, caching the result.
func Compile(pattern string) (Pattern, error) {
	pattern = filepath.ToSlash(strings.TrimPrefix(pattern, "./"))
	if cached, ok := cache.Load(pattern); ok {
		return cached.(Pattern), nil
	}
	var expression strings.Builder
	expression.WriteString("^")
	var captures []string
	for i := 0; i < len(pattern); {
		switch {
		case strings.HasPrefix(pattern[i:], "**/"):
			expression.WriteString("(?:.*/)?")
			i += 3
		case strings.HasPrefix(pattern[i:], "**"):
			expression.WriteString(".*")
			i += 2
		case strings.HasPrefix(pattern[i:], "{feature}"):
			expression.WriteString("([^/]+)")
			captures = append(captures, "feature")
			i += len("{feature}")
		case pattern[i] == '*':
			expression.WriteString("[^/]*")
			i++
		case pattern[i] == '?':
			expression.WriteString("[^/]")
			i++
		default:
			expression.WriteString(regexp.QuoteMeta(string(pattern[i])))
			i++
		}
	}
	expression.WriteString("$")
	re, err := regexp.Compile(expression.String())
	if err != nil {
		return Pattern{}, fmt.Errorf("invalid glob: %w", err)
	}
	compiled := Pattern{re: re, captures: captures}
	cache.Store(pattern, compiled)
	return compiled, nil
}

// Match reports whether name matches the compiled pattern, returning any
// captures. Use this to match one pattern against many paths.
func (p Pattern) Match(name string) (bool, map[string]string) {
	submatch := p.re.FindStringSubmatch(filepath.ToSlash(strings.TrimPrefix(name, "./")))
	if submatch == nil {
		return false, nil
	}
	captures := make(map[string]string, len(p.captures))
	for index, capture := range p.captures {
		captures[capture] = submatch[index+1]
	}
	return true, captures
}

// Match reports whether name matches pattern, returning any captures. An
// invalid pattern never matches.
func Match(pattern, name string) (bool, map[string]string) {
	compiled, err := Compile(pattern)
	if err != nil {
		return false, nil
	}
	return compiled.Match(name)
}

// MatchAny reports whether name matches at least one pattern.
func MatchAny(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if matches, _ := Match(pattern, name); matches {
			return true
		}
	}
	return false
}
