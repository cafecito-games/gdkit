package architecture

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

type compiledPattern struct {
	re       *regexp.Regexp
	captures []string
}

var patternCache sync.Map

func compilePattern(pattern string) (compiledPattern, error) {
	pattern = filepath.ToSlash(strings.TrimPrefix(pattern, "./"))
	if cached, ok := patternCache.Load(pattern); ok {
		return cached.(compiledPattern), nil
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
		return compiledPattern{}, fmt.Errorf("invalid glob: %w", err)
	}
	compiled := compiledPattern{re: re, captures: captures}
	patternCache.Store(pattern, compiled)
	return compiled, nil
}

func matchPattern(pattern, name string) (bool, map[string]string) {
	compiled, err := compilePattern(pattern)
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

func matchesAny(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if matches, _ := matchPattern(pattern, name); matches {
			return true
		}
	}
	return false
}
