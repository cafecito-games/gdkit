package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/cafecito-games/gdkit/cfg"
	"github.com/cafecito-games/gdkit/internal/failure"
)

const (
	formatRaw   = "raw"
	formatGodot = "godot"
)

const cfgUsageText = `Usage:
  gdkit cfg query [flags] <filter> [file...]

Query Godot ConfigFile documents (such as project.godot, export_presets.cfg,
override.cfg, or plugin.cfg) using jq filter syntax.

Reads from stdin when no file is given or when a file path is "-".
`

func runCfg(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, cfgUsageText)
		return 0
	}
	switch args[0] {
	case "query":
		return runCfgQuery(args[1:], stdin, stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, cfgUsageText)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown cfg command %q\n\n%s", args[0], cfgUsageText)
		return 2
	}
}

func runCfgQuery(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cfg query", flag.ContinueOnError)
	flags.SetOutput(stderr)

	outputFormat := flags.String("format", formatJSON, "output format: json, raw, or godot")
	var rawOutput bool
	flags.BoolVar(&rawOutput, "r", false, "write string results without JSON quotes (shorthand for --raw-output)")
	flags.BoolVar(&rawOutput, "raw-output", false, "write string results without JSON quotes")
	var compactOutput bool
	flags.BoolVar(&compactOutput, "c", false, "write compact JSON output (shorthand for --compact-output)")
	flags.BoolVar(&compactOutput, "compact-output", false, "write compact JSON output instead of pretty-printed JSON")
	var exitStatus bool
	flags.BoolVar(&exitStatus, "e", false, "exit 1 if no output was produced or the last result is false or null (shorthand for --exit-status)")
	flags.BoolVar(&exitStatus, "exit-status", false, "exit 1 if no output was produced or the last result is false or null")
	var slurp bool
	flags.BoolVar(&slurp, "s", false, "read all input files into an array and evaluate the query once (shorthand for --slurp)")
	flags.BoolVar(&slurp, "slurp", false, "read all input files into an array and evaluate the query once")
	flat := flags.Bool("flat", false, "keep slash-separated keys flat instead of nesting them into objects")
	minimumVersion := flags.String("minimum-version", "", minimumVersionUsage)

	if err := flags.Parse(args); err != nil {
		return exitUsage
	}

	formatExplicit := false
	flags.Visit(func(parsed *flag.Flag) {
		if parsed.Name == "format" {
			formatExplicit = true
		}
	})

	resolvedFormat, ok := checkCfgOutputFormat(*outputFormat, rawOutput, formatExplicit, stderr)
	if !ok {
		return exitUsage
	}
	if compactOutput && resolvedFormat == formatGodot {
		return reportUsage(stderr, resolvedFormat, failure.UsageArguments, "-c/--compact-output cannot be combined with --format godot")
	}
	if !checkMinimumVersion(*minimumVersion, resolvedFormat, stderr) {
		return exitUsage
	}
	if flags.NArg() < 1 {
		return reportUsage(stderr, resolvedFormat, failure.UsageArguments, "cfg query requires a jq filter expression")
	}

	filter := flags.Arg(0)
	compiled, err := cfg.Compile(filter)
	if err != nil {
		return reportFailure(stderr, resolvedFormat, failure.QueryParse, err)
	}

	filePaths := flags.Args()[1:]
	if len(filePaths) == 0 {
		filePaths = []string{"-"}
	}

	options := cfg.Options{Flat: *flat}
	inputs := make([]any, 0, len(filePaths))
	for _, path := range filePaths {
		var document map[string]any
		if path == "-" {
			source, readErr := io.ReadAll(stdin)
			if readErr != nil {
				return reportFailure(stderr, resolvedFormat, failure.ConfigRead, failure.WrapPath(failure.ConfigRead, "<stdin>", readErr))
			}
			document, err = cfg.Parse("<stdin>", source, options)
		} else {
			document, err = cfg.ParseFile(path, options)
		}
		if err != nil {
			return reportFailure(stderr, resolvedFormat, failure.ConfigParse, err)
		}
		inputs = append(inputs, document)
	}

	if slurp {
		inputs = []any{inputs}
	}

	writer := bufio.NewWriter(stdout)
	emitted := false
	var lastValue any

	for _, input := range inputs {
		iterator := compiled.Iterate(context.Background(), input)
		halted := false
		for {
			value, hasValue, didHalt, evalErr := iterator.Next()
			if evalErr != nil {
				_ = writer.Flush()
				return reportFailure(stderr, resolvedFormat, failure.QueryEval, evalErr)
			}
			if !hasValue {
				halted = didHalt
				break
			}
			if err := writeCfgValue(writer, resolvedFormat, compactOutput, value); err != nil {
				_ = writer.Flush()
				return reportFailure(stderr, resolvedFormat, failure.OutputWrite, fmt.Errorf("write query output: %w", err))
			}
			emitted = true
			lastValue = value
		}
		if halted {
			break
		}
	}

	if err := writer.Flush(); err != nil {
		return reportFailure(stderr, resolvedFormat, failure.OutputWrite, fmt.Errorf("write query output: %w", err))
	}

	if exitStatus && (!emitted || !cfg.IsTruthy(lastValue)) {
		return 1
	}
	return 0
}

func checkCfgOutputFormat(value string, rawOutput, formatExplicit bool, stderr io.Writer) (string, bool) {
	switch value {
	case formatJSON, formatRaw, formatGodot:
	default:
		reportUsage(stderr, formatText, failure.UsageFormat,
			fmt.Sprintf("unknown output format %q (want json, raw, or godot)", value))
		return "", false
	}
	if rawOutput {
		if formatExplicit && value != formatRaw {
			reportUsage(stderr, value, failure.UsageArguments,
				fmt.Sprintf("-r/--raw-output cannot be combined with --format %s", value))
			return "", false
		}
		return formatRaw, true
	}
	return value, true
}

func writeCfgValue(writer *bufio.Writer, outputFormat string, compact bool, value any) error {
	switch outputFormat {
	case formatJSON:
		encoded, err := cfg.FormatJSON(value, compact)
		if err != nil {
			return err
		}
		if _, err := writer.Write(encoded); err != nil {
			return err
		}
		return writer.WriteByte('\n')
	case formatRaw:
		encoded, err := cfg.FormatRaw(value, compact)
		if err != nil {
			return err
		}
		if _, err := writer.Write(encoded); err != nil {
			return err
		}
		return writer.WriteByte('\n')
	case formatGodot:
		encoded, err := cfg.FormatGodot(value)
		if err != nil {
			return err
		}
		if _, err := writer.WriteString(encoded); err != nil {
			return err
		}
		return writer.WriteByte('\n')
	default:
		return fmt.Errorf("unknown output format %q", outputFormat)
	}
}
