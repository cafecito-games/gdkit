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
	parsed *gojq.Query
	code   *gojq.Code
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
	compiled, err := compileWithInput(parsed, gojq.NewIter[any]())
	if err != nil {
		return nil, err
	}
	return &Query{parsed: parsed, code: compiled}, nil
}

func compileWithInput(parsed *gojq.Query, inputIterator gojq.Iter) (*gojq.Code, error) {
	compiled, err := gojq.Compile(
		parsed,
		gojq.WithEnvironLoader(os.Environ),
		gojq.WithInputIter(inputIterator),
	)
	if err != nil {
		return nil, failure.Wrap(failure.QueryParse, fmt.Errorf("invalid query: %w", err))
	}
	return compiled, nil
}

type inputCursor struct {
	values []any
	index  int
}

func (cursor *inputCursor) Next() (any, bool) {
	if cursor.index >= len(cursor.values) {
		return nil, false
	}
	value := cursor.values[cursor.index]
	cursor.index++
	return value, true
}

// Iterator streams results produced by evaluating a Query against one or more
// inputs.
type Iterator struct {
	ctx        context.Context
	code       *gojq.Code
	cursor     *inputCursor
	current    gojq.Iter
	compileErr error
	halted     bool
}

// Iterate evaluates the compiled query against a single input and returns a
// streaming iterator over the results.
func (query *Query) Iterate(ctx context.Context, input any) *Iterator {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Iterator{
		ctx:     ctx,
		code:    query.code,
		current: query.code.RunWithContext(ctx, input),
	}
}

// IterateInputs evaluates the compiled query across a sequence of inputs,
// supporting jq's input and inputs functions over the remaining documents.
func (query *Query) IterateInputs(ctx context.Context, inputs []any) *Iterator {
	if ctx == nil {
		ctx = context.Background()
	}
	cursor := &inputCursor{values: inputs}
	compiled, err := compileWithInput(query.parsed, cursor)
	if err != nil {
		return &Iterator{ctx: ctx, compileErr: err}
	}
	return &Iterator{
		ctx:    ctx,
		code:   compiled,
		cursor: cursor,
	}
}

// Next yields the next query result. When ok is false and err is nil, the
// stream finished cleanly (with halted true when stopped by jq's halt).
func (iterator *Iterator) Next() (value any, ok bool, halted bool, err error) {
	if iterator.compileErr != nil {
		compileErr := iterator.compileErr
		iterator.compileErr = nil
		return nil, false, false, compileErr
	}
	if iterator.halted {
		return nil, false, true, nil
	}
	for {
		if iterator.current == nil {
			if iterator.cursor == nil {
				return nil, false, false, nil
			}
			nextInput, hasInput := iterator.cursor.Next()
			if !hasInput {
				return nil, false, false, nil
			}
			iterator.current = iterator.code.RunWithContext(iterator.ctx, nextInput)
		}
		item, hasMore := iterator.current.Next()
		if !hasMore {
			iterator.current = nil
			continue
		}
		if evalErr, isError := item.(error); isError {
			var haltErr *gojq.HaltError
			if errors.As(evalErr, &haltErr) {
				iterator.halted = true
				iterator.current = nil
				if haltErr.ExitCode() == 0 {
					return nil, false, true, nil
				}
				return nil, false, true, failure.Wrap(failure.QueryEval, evalErr)
			}
			return nil, false, false, failure.Wrap(failure.QueryEval, evalErr)
		}
		return item, true, false, nil
	}
}

// Run evaluates the compiled query against a single input and collects all
// results.
func (query *Query) Run(ctx context.Context, input any) ([]any, error) {
	return collectIterator(query.Iterate(ctx, input))
}

// RunInputs evaluates the compiled query across a sequence of inputs (with
// input/inputs support) and collects all results.
func (query *Query) RunInputs(ctx context.Context, inputs []any) ([]any, error) {
	return collectIterator(query.IterateInputs(ctx, inputs))
}

func collectIterator(iterator *Iterator) ([]any, error) {
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
