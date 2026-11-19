package cfg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/itchyny/gojq"

	"github.com/cafecito-games/gdkit/internal/failure"
)

// Query is a compiled jq filter ready to evaluate against parsed Godot
// ConfigFile documents.
type Query struct {
	code *gojq.Code
}

// Compile parses and compiles a jq query string.
func Compile(source string) (*Query, error) {
	if strings.TrimSpace(source) == "" {
		return nil, failure.Wrap(failure.QueryParse, errors.New("query must not be empty"))
	}
	parsed, err := gojq.Parse(source)
	if err != nil {
		return nil, failure.Wrap(failure.QueryParse, fmt.Errorf("invalid query: %w", err))
	}
	compiled, err := gojq.Compile(parsed, gojq.WithEnvironLoader(os.Environ))
	if err != nil {
		return nil, failure.Wrap(failure.QueryParse, fmt.Errorf("invalid query: %w", err))
	}
	return &Query{code: compiled}, nil
}

// Iterator streams results produced by evaluating a Query against one input.
type Iterator struct {
	inner gojq.Iter
}

// Iterate evaluates the compiled query against input and returns a streaming
// iterator over the results.
func (query *Query) Iterate(ctx context.Context, input any) *Iterator {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Iterator{inner: query.code.RunWithContext(ctx, input)}
}

// Next yields the next query result. When ok is false and err is nil, the
// stream finished cleanly (with halted true when stopped by jq's halt).
func (iterator *Iterator) Next() (value any, ok bool, halted bool, err error) {
	item, hasMore := iterator.inner.Next()
	if !hasMore {
		return nil, false, false, nil
	}
	if evalErr, isError := item.(error); isError {
		var haltErr *gojq.HaltError
		if errors.As(evalErr, &haltErr) {
			if haltErr.ExitCode() == 0 {
				return nil, false, true, nil
			}
			return nil, false, true, failure.Wrap(failure.QueryEval, evalErr)
		}
		return nil, false, false, failure.Wrap(failure.QueryEval, evalErr)
	}
	return item, true, false, nil
}

// Run evaluates the compiled query against input and collects all results.
func (query *Query) Run(ctx context.Context, input any) ([]any, error) {
	iterator := query.Iterate(ctx, input)
	var results []any
	for {
		value, ok, _, err := iterator.Next()
		if err != nil {
			return results, err
		}
		if !ok {
			return results, nil
		}
		results = append(results, value)
	}
}

// IsTruthy reports jq truthiness: every value except false and null is truthy.
func IsTruthy(value any) bool {
	if value == nil {
		return false
	}
	booleanValue, isBool := value.(bool)
	return !isBool || booleanValue
}
