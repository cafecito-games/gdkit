package architecture

import "github.com/cafecito-games/gdkit/internal/glob"

func compilePattern(pattern string) (glob.Pattern, error) { return glob.Compile(pattern) }

func matchPattern(pattern, name string) (bool, map[string]string) { return glob.Match(pattern, name) }

func matchesAny(patterns []string, name string) bool { return glob.MatchAny(patterns, name) }
