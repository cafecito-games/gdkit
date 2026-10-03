package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/cafecito-games/gdkit/format"
	"github.com/cafecito-games/gdkit/internal/textdiff"
	"github.com/cafecito-games/gdkit/project"
)

func runFormat(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runFormatCheck(nil, stdout, stderr)
	}
	switch args[0] {
	case "check":
		return runFormatCheck(args[1:], stdout, stderr)
	case "write":
		return runFormatWrite(args[1:], stdout, stderr)
	case "init":
		return runFormatInit(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown format command %q\n\n%s", args[0], usageText)
		return 2
	}
}

func runFormatCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("format check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configName := flags.String("config", "", "configuration path relative to the project root")
	outputFormat := flags.String("format", "text", "output format: text or json")
	showDiff := flags.Bool("diff", false, "print a unified diff for each file that would change")
	minimumVersion := flags.String("minimum-version", "", minimumVersionUsage)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "format check accepts at most one project root")
		return 2
	}
	if !checkMinimumVersion(*minimumVersion, stderr) {
		return 2
	}
	if *outputFormat != "text" && *outputFormat != "json" {
		fmt.Fprintf(stderr, "unknown output format %q (want text or json)\n", *outputFormat)
		return 2
	}
	if *showDiff && *outputFormat == "json" {
		fmt.Fprintln(stderr, "--diff cannot be combined with --format json")
		return 2
	}
	root := "."
	if flags.NArg() == 1 {
		root = flags.Arg(0)
	}
	snapshot, report, err := formatProject(root, *configName)
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	if *outputFormat == "json" {
		if err := writeFormatReport(stdout, report); err != nil {
			fmt.Fprintln(stderr, "gdkit: write report:", err)
			return 2
		}
	} else {
		writer := bufio.NewWriter(stdout)
		changed := report.Changed()
		for _, result := range changed {
			fmt.Fprintln(writer, "would reformat", result.Path)
			if *showDiff {
				fmt.Fprint(writer, textdiff.Unified("a/"+result.Path, "b/"+result.Path, snapshot.Scripts[result.Path].Source, result.Formatted))
			}
		}
		for _, diagnostic := range report.Diagnostics {
			fmt.Fprintln(writer, diagnostic.String())
		}
		if report.HasChanges() || report.HasDiagnostics() {
			fmt.Fprintf(writer, "format check failed (%d to reformat, %d diagnostics)\n", len(changed), len(report.Diagnostics))
		} else {
			fmt.Fprintf(writer, "format check passed (%d files)\n", len(report.Results))
		}
		if err := writer.Flush(); err != nil {
			fmt.Fprintln(stderr, "gdkit: write report:", err)
			return 2
		}
	}
	if report.HasChanges() || report.HasDiagnostics() {
		return 1
	}
	return 0
}

func runFormatWrite(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("format write", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configName := flags.String("config", "", "configuration path relative to the project root")
	outputFormat := flags.String("format", "text", "output format: text or json")
	minimumVersion := flags.String("minimum-version", "", minimumVersionUsage)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "format write accepts at most one project root")
		return 2
	}
	if !checkMinimumVersion(*minimumVersion, stderr) {
		return 2
	}
	if *outputFormat != "text" && *outputFormat != "json" {
		fmt.Fprintf(stderr, "unknown output format %q (want text or json)\n", *outputFormat)
		return 2
	}
	root := "."
	if flags.NArg() == 1 {
		root = flags.Arg(0)
	}
	snapshot, report, err := formatProject(root, *configName)
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	written, applyError := format.Apply(snapshot, report)
	if *outputFormat == "json" {
		if err := writeFormatReport(stdout, writeReport{Report: report, Written: written}); err != nil {
			fmt.Fprintln(stderr, "gdkit: write report:", err)
			return 2
		}
	} else {
		writer := bufio.NewWriter(stdout)
		for _, path := range written {
			fmt.Fprintln(writer, "reformatted", path)
		}
		for _, diagnostic := range report.Diagnostics {
			fmt.Fprintln(writer, diagnostic.String())
		}
		// A run that stopped part-way has no totals worth stating; the files
		// listed above are the ones that changed on disk.
		if applyError == nil {
			fmt.Fprintf(writer, "format write: %d reformatted, %d unchanged", len(written), len(report.Results)-len(written))
			if report.HasDiagnostics() {
				fmt.Fprintf(writer, ", %d skipped", len(report.Diagnostics))
			}
			fmt.Fprintln(writer)
		}
		if err := writer.Flush(); err != nil {
			fmt.Fprintln(stderr, "gdkit: write report:", err)
			return 2
		}
	}
	if applyError != nil {
		fmt.Fprintln(stderr, "gdkit:", applyError)
		return 2
	}
	if report.HasDiagnostics() {
		return 1
	}
	return 0
}

func runFormatInit(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("format init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	force := flags.Bool("force", false, "replace an existing configuration file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "format init accepts at most one project root")
		return 2
	}
	root := "."
	if flags.NArg() == 1 {
		root = flags.Arg(0)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	configData, err := json.MarshalIndent(format.DefaultConfig(), "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	configData = append(configData, '\n')
	name := filepath.Join(root, filepath.FromSlash(format.DefaultConfigPath))
	if err := writeStarter(name, configData, *force); err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	fmt.Fprintln(stdout, "wrote", format.DefaultConfigPath)
	return 0
}

// formatProject loads the project under root and computes its format report.
// It writes nothing; the snapshot is returned so the caller can diff against,
// or write over, the sources the report was computed from.
func formatProject(root, configName string) (*project.Snapshot, format.Report, error) {
	config, err := format.LoadConfig(root, configName)
	if err != nil {
		return nil, format.Report{}, err
	}
	formatter, err := format.New(config)
	if err != nil {
		return nil, format.Report{}, err
	}
	snapshot, err := project.Load(project.Config{Root: root, SourceRoots: config.SourceRoots, Exclude: config.Exclude, HonorIgnoreFile: true})
	if err != nil {
		return nil, format.Report{}, err
	}
	return snapshot, formatter.Format(snapshot), nil
}

// writeReport is the JSON output of format write: the report, plus the paths
// the run actually replaced on disk.
type writeReport struct {
	format.Report
	// Written is project-relative and in path order. It lists the files
	// written before a failure when the run stopped part-way.
	Written []string `json:"written"`
}

// writeFormatReport prints report, a format.Report or a wrapper around one, as
// indented JSON.
func writeFormatReport(stdout io.Writer, report any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
