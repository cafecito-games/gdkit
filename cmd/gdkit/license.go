package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/cafecito-games/gdkit/internal/failure"
	"github.com/cafecito-games/gdkit/internal/textdiff"
	"github.com/cafecito-games/gdkit/licenseheader"
	"github.com/cafecito-games/gdkit/project"
)

func runLicense(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runLicenseCheck(nil, stdout, stderr)
	}
	switch args[0] {
	case "check":
		return runLicenseCheck(args[1:], stdout, stderr)
	case "write":
		return runLicenseWrite(args[1:], stdout, stderr)
	case "init":
		return runLicenseInit(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown license command %q\n\n%s", args[0], usageText)
		return 2
	}
}

func runLicenseCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("license check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configName := flags.String("config", "", "configuration path relative to the project root")
	outputFormat := flags.String("format", "text", "output format: text or json")
	showDiff := flags.Bool("diff", false, "print a unified diff for each file that would change")
	minimumVersion := flags.String("minimum-version", "", minimumVersionUsage)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	resolvedFormat, ok := checkOutputFormat(*outputFormat, stderr)
	if !ok {
		return exitUsage
	}
	if flags.NArg() > 1 {
		return reportUsage(stderr, resolvedFormat, failure.UsageArguments, "license check accepts at most one project root")
	}
	if !checkMinimumVersion(*minimumVersion, resolvedFormat, stderr) {
		return exitUsage
	}
	if *showDiff && resolvedFormat == formatJSON {
		return reportUsage(stderr, resolvedFormat, failure.UsageArguments, "--diff cannot be combined with --format json")
	}
	root := "."
	if flags.NArg() == 1 {
		root = flags.Arg(0)
	}
	snapshot, report, err := licenseProject(root, *configName)
	if err != nil {
		return reportFailure(stderr, resolvedFormat, failure.ConfigInvalid, err)
	}
	if resolvedFormat == formatJSON {
		if err := writeLicenseReport(stdout, report); err != nil {
			return reportFailure(stderr, resolvedFormat, failure.OutputWrite, fmt.Errorf("write report: %w", err))
		}
	} else {
		writer := bufio.NewWriter(stdout)
		changed := report.Changed()
		for _, result := range changed {
			fmt.Fprintln(writer, "would update license header", result.Path)
			if *showDiff {
				fmt.Fprint(writer, textdiff.Unified("a/"+result.Path, "b/"+result.Path, snapshot.Scripts[result.Path].Source, result.Contents))
			}
		}
		for _, diagnostic := range report.Diagnostics {
			fmt.Fprintln(writer, diagnostic.String())
		}
		if report.HasChanges() || report.HasDiagnostics() {
			fmt.Fprintf(writer, "license check failed (%d to update, %d diagnostics)\n", len(changed), len(report.Diagnostics))
		} else {
			fmt.Fprintf(writer, "license check passed (%d files)\n", len(report.Results))
		}
		if err := writer.Flush(); err != nil {
			return reportFailure(stderr, resolvedFormat, failure.OutputWrite, fmt.Errorf("write report: %w", err))
		}
	}
	if report.HasChanges() || report.HasDiagnostics() {
		return 1
	}
	return 0
}

func runLicenseWrite(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("license write", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configName := flags.String("config", "", "configuration path relative to the project root")
	outputFormat := flags.String("format", "text", "output format: text or json")
	minimumVersion := flags.String("minimum-version", "", minimumVersionUsage)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	resolvedFormat, ok := checkOutputFormat(*outputFormat, stderr)
	if !ok {
		return exitUsage
	}
	if flags.NArg() > 1 {
		return reportUsage(stderr, resolvedFormat, failure.UsageArguments, "license write accepts at most one project root")
	}
	if !checkMinimumVersion(*minimumVersion, resolvedFormat, stderr) {
		return exitUsage
	}
	root := "."
	if flags.NArg() == 1 {
		root = flags.Arg(0)
	}
	snapshot, report, err := licenseProject(root, *configName)
	if err != nil {
		return reportFailure(stderr, resolvedFormat, failure.ConfigInvalid, err)
	}
	written, applyError := licenseheader.Apply(snapshot, report)
	if resolvedFormat == formatJSON {
		if err := writeLicenseReport(stdout, licenseWriteReport{Report: report, Written: written}); err != nil {
			return reportFailure(stderr, resolvedFormat, failure.OutputWrite, fmt.Errorf("write report: %w", err))
		}
	} else {
		writer := bufio.NewWriter(stdout)
		for _, path := range written {
			fmt.Fprintln(writer, "updated license header", path)
		}
		for _, diagnostic := range report.Diagnostics {
			fmt.Fprintln(writer, diagnostic.String())
		}
		if applyError == nil {
			fmt.Fprintf(writer, "license write: %d updated, %d unchanged", len(written), len(report.Results)-len(written))
			if report.HasDiagnostics() {
				fmt.Fprintf(writer, ", %d skipped", len(report.Diagnostics))
			}
			fmt.Fprintln(writer)
		}
		if err := writer.Flush(); err != nil {
			return reportFailure(stderr, resolvedFormat, failure.OutputWrite, fmt.Errorf("write report: %w", err))
		}
	}
	if applyError != nil {
		return reportFailure(stderr, resolvedFormat, failure.FileWrite, applyError)
	}
	if report.HasDiagnostics() {
		return 1
	}
	return 0
}

func runLicenseInit(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("license init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	force := flags.Bool("force", false, "replace an existing configuration file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "license init accepts at most one project root")
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
	configData, err := json.MarshalIndent(licenseheader.DefaultConfig(), "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	configData = append(configData, '\n')
	name := filepath.Join(root, filepath.FromSlash(licenseheader.DefaultConfigPath))
	if err := writeStarter(name, configData, *force); err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	fmt.Fprintln(stdout, "wrote", licenseheader.DefaultConfigPath)
	return 0
}

func licenseProject(root, configName string) (*project.Snapshot, licenseheader.Report, error) {
	config, err := licenseheader.LoadConfig(root, configName)
	if err != nil {
		return nil, licenseheader.Report{}, err
	}
	manager, err := licenseheader.New(config)
	if err != nil {
		return nil, licenseheader.Report{}, err
	}
	snapshot, err := project.Load(project.Config{Root: root, SourceRoots: config.SourceRoots, Exclude: config.Exclude, HonorIgnoreFile: true})
	if err != nil {
		return nil, licenseheader.Report{}, failure.Wrap(failure.ProjectLoad, err)
	}
	return snapshot, manager.Check(snapshot), nil
}

type licenseWriteReport struct {
	licenseheader.Report
	Written []string `json:"written"`
}

func writeLicenseReport(stdout io.Writer, report any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
