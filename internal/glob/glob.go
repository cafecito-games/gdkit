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

// Match reports whether name matches pattern, returning any captures.
func Match(pattern, name string) (bool, map[string]string) {
	compiled, err := Compile(pattern)
	if err != nil {
		return false, nil
	}
	match := compiled.re.FindStringSubmatch(filepath.ToSlash(strings.TrimPrefix(name, "./")))
	if match == nil {
		return false, nil
	}
	captures := make(map[string]string, len(compiled.captures))
	for i, capture := range compiled.captures {
		captures[capture] = match[i+1]
	}
	return true, captures
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
