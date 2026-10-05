package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cafecito-games/gdkit/architecture"
	"github.com/cafecito-games/gdkit/internal/buildinfo"
	"github.com/cafecito-games/gdkit/internal/failure"
	"github.com/cafecito-games/gdkit/lint"
	"github.com/cafecito-games/gdkit/project"
)

const usageText = `gdkit — static source tooling for GDScript

Usage:
  gdkit arch check   [flags] [project-root]
  gdkit arch init    [flags] [project-root]
  gdkit lint check   [flags] [project-root]
  gdkit lint init    [flags] [project-root]
  gdkit format check [flags] [project-root]
  gdkit format write [flags] [project-root]
  gdkit format init  [flags] [project-root]
  gdkit uid check    [flags] [project-root]
  gdkit uid write    [flags] [project-root]
  gdkit gen check    [flags] [project-root]
  gdkit gen write    [flags] [project-root]
  gdkit gen init     [flags] [project-root]
  gdkit version      [flags]

Commands:
  arch check     enforce architectural dependency rules
  arch init      write starter .gdkit configuration files
  lint check     report style and correctness problems in GDScript
  lint init      write the default .gdkit/lint.json configuration
  format check   report GDScript files that are not formatted
  format write   rewrite GDScript files in the configured style
  format init    write the default .gdkit/format.json configuration
  uid check      report uid:// identities that are missing or unusable, and
                 references that resolve to nothing or to the wrong file
  uid write      create the missing .uid sidecars Godot would have written and
                 repoint the broken references that name a path
  gen check      report classes whose generated methods are missing or stale
  gen write      write the generated methods into the classes that opted in
  gen init       write the default .gdkit/generate.json configuration
  version        print version and source revision information
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-v") {
		fmt.Fprintln(stdout, buildinfo.Current().String())
		return 0
	}
	if len(args) < 1 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usageText)
		return 0
	}
	switch args[0] {
	case "arch":
		return runArch(args[1:], stdout, stderr)
	case "lint":
		return runLint(args[1:], stdout, stderr)
	case "format":
		return runFormat(args[1:], stdout, stderr)
	case "uid":
		return runUID(args[1:], stdout, stderr)
	case "gen":
		return runGen(args[1:], stdout, stderr)
	case "version":
		return runVersion(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usageText)
		return 2
	}
}

func runArch(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runCheck(nil, stdout, stderr)
	}
	switch args[0] {
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown arch command %q\n\n%s", args[0], usageText)
		return 2
	}
}

func runLint(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runLintCheck(nil, stdout, stderr)
	}
	switch args[0] {
	case "check":
		return runLintCheck(args[1:], stdout, stderr)
	case "init":
		return runLintInit(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown lint command %q\n\n%s", args[0], usageText)
		return 2
	}
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(stderr)
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	outputFormat, ok := checkOutputFormat(*format, stderr)
	if !ok {
		return exitUsage
	}
	if flags.NArg() != 0 {
		return reportUsage(stderr, outputFormat, failure.UsageArguments, "version does not accept positional arguments")
	}
	info := buildinfo.Current()
	switch outputFormat {
	case formatText:
		fmt.Fprintln(stdout, info.String())
	case formatJSON:
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(info); err != nil {
			return reportFailure(stderr, outputFormat, failure.OutputWrite, fmt.Errorf("write version: %w", err))
		}
	}
	return 0
}

func runCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("arch check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configName := flags.String("config", "", "configuration path relative to the project root")
	format := flags.String("format", "text", "output format: text or json")
	showEdges := flags.Bool("show-edges", false, "include resolved dependencies in text output")
	minimumVersion := flags.String("minimum-version", "", minimumVersionUsage)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	outputFormat, ok := checkOutputFormat(*format, stderr)
	if !ok {
		return exitUsage
	}
	if flags.NArg() > 1 {
		return reportUsage(stderr, outputFormat, failure.UsageArguments, "arch check accepts at most one project root")
	}
	if !checkMinimumVersion(*minimumVersion, outputFormat, stderr) {
		return exitUsage
	}
	root := "."
	if flags.NArg() == 1 {
		root = flags.Arg(0)
	}
	config, err := architecture.LoadConfig(root, *configName)
	if err != nil {
		return reportFailure(stderr, outputFormat, failure.ConfigInvalid, err)
	}
	analyzer, err := architecture.NewAnalyzer(root, config)
	if err != nil {
		return reportFailure(stderr, outputFormat, failure.ConfigInvalid, err)
	}
	report, err := analyzer.Analyze()
	if err != nil {
		return reportFailure(stderr, outputFormat, failure.AnalysisFailed, err)
	}
	switch outputFormat {
	case formatJSON:
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return reportFailure(stderr, outputFormat, failure.OutputWrite, fmt.Errorf("write report: %w", err))
		}
	case formatText:
		for _, diagnostic := range report.Diagnostics {
			fmt.Fprintln(stdout, diagnostic.String())
		}
		if *showEdges {
			for _, edge := range report.Edges {
				fmt.Fprintf(stdout, "%s:%d:%d: %s -> %s (%s: %s)\n", edge.Location.Path, edge.Location.Line, edge.Location.Column, edge.From, edge.To, edge.Kind, edge.Symbol)
			}
		}
		if len(report.Diagnostics) == 0 {
			fmt.Fprintf(stdout, "architecture check passed (%d files, %d dependencies)\n", len(report.Files), len(report.Edges))
		} else {
			fmt.Fprintf(stdout, "architecture check failed (%d diagnostics, %d files, %d dependencies)\n", len(report.Diagnostics), len(report.Files), len(report.Edges))
		}
	}
	if report.HasErrors() {
		return 1
	}
	return 0
}

func runInit(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("arch init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	force := flags.Bool("force", false, "replace existing starter files")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "arch init accepts at most one project root")
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
	configData, err := json.MarshalIndent(architecture.DefaultConfig(), "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	configData = append(configData, '\n')
	allowlistData := []byte("{\n  \"version\": 1,\n  \"exceptions\": []\n}\n")
	files := []struct {
		name string
		data []byte
	}{
		{architecture.DefaultConfigPath, configData},
		{architecture.DefaultAllowlistPath, allowlistData},
	}
	if !*force {
		for _, file := range files {
			name := filepath.Join(root, filepath.FromSlash(file.name))
			if _, statErr := os.Stat(name); statErr == nil {
				fmt.Fprintf(stderr, "gdkit: %s already exists (use --force to replace it)\n", name)
				return 2
			} else if !errors.Is(statErr, os.ErrNotExist) {
				fmt.Fprintln(stderr, "gdkit:", statErr)
				return 2
			}
		}
	}
	for _, file := range files {
		name := filepath.Join(root, filepath.FromSlash(file.name))
		if err := writeStarter(name, file.data, *force); err != nil {
			fmt.Fprintln(stderr, "gdkit:", err)
			return 2
		}
		fmt.Fprintln(stdout, "wrote", file.name)
	}
	return 0
}

func runLintCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("lint check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configName := flags.String("config", "", "configuration path relative to the project root")
	format := flags.String("format", "text", "output format: text or json")
	disable := flags.String("disable", "", "comma-separated rule names to turn off")
	enable := flags.String("enable", "", "comma-separated rule names to turn on, for rules that ship inert")
	minimumVersion := flags.String("minimum-version", "", minimumVersionUsage)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	outputFormat, ok := checkOutputFormat(*format, stderr)
	if !ok {
		return exitUsage
	}
	if flags.NArg() > 1 {
		return reportUsage(stderr, outputFormat, failure.UsageArguments, "lint check accepts at most one project root")
	}
	if !checkMinimumVersion(*minimumVersion, outputFormat, stderr) {
		return exitUsage
	}
	root := "."
	if flags.NArg() == 1 {
		root = flags.Arg(0)
	}
	config, err := lint.LoadConfig(root, *configName)
	if err != nil {
		return reportFailure(stderr, outputFormat, failure.ConfigInvalid, err)
	}
	for _, name := range strings.Split(*disable, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !lint.IsRule(name) {
			return reportUsage(stderr, outputFormat, failure.UsageArguments,
				fmt.Sprintf("--disable names unknown rule %q", name))
		}
		config.Disable = append(config.Disable, name)
	}
	for _, name := range strings.Split(*enable, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !lint.IsRule(name) {
			return reportUsage(stderr, outputFormat, failure.UsageArguments,
				fmt.Sprintf("--enable names unknown rule %q", name))
		}
		config.Enable = append(config.Enable, name)
	}
	linter, err := lint.New(config)
	if err != nil {
		return reportFailure(stderr, outputFormat, failure.ConfigInvalid, err)
	}
	snapshot, err := project.Load(project.Config{Root: root, SourceRoots: config.SourceRoots, Exclude: config.Exclude, HonorIgnoreFile: true})
	if err != nil {
		return reportFailure(stderr, outputFormat, failure.ProjectLoad, err)
	}
	report := linter.Lint(snapshot)
	if report.Diagnostics == nil {
		report.Diagnostics = []lint.Diagnostic{}
	}
	if outputFormat == formatJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return reportFailure(stderr, outputFormat, failure.OutputWrite, fmt.Errorf("write report: %w", err))
		}
	} else {
		writer := bufio.NewWriter(stdout)
		for _, diagnostic := range report.Diagnostics {
			fmt.Fprintln(writer, diagnostic.String())
		}
		switch {
		case report.HasErrors():
			fmt.Fprintf(writer, "lint check failed (%d diagnostics)\n", len(report.Diagnostics))
		case report.HasFindings():
			// Warnings do not fail the run, so the summary must not claim it failed.
			count := len(report.Diagnostics)
			noun := "warnings"
			if count == 1 {
				noun = "warning"
			}
			fmt.Fprintf(writer, "lint check passed (%d %s)\n", count, noun)
		default:
			fmt.Fprintln(writer, "lint check passed (no diagnostics)")
		}
		if err := writer.Flush(); err != nil {
			return reportFailure(stderr, outputFormat, failure.OutputWrite, fmt.Errorf("write report: %w", err))
		}
	}
	if report.HasErrors() {
		return 1
	}
	return 0
}

func runLintInit(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("lint init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	force := flags.Bool("force", false, "replace an existing configuration file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "lint init accepts at most one project root")
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
	configData, err := json.MarshalIndent(lint.DefaultConfig(), "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	configData = append(configData, '\n')
	name := filepath.Join(root, filepath.FromSlash(lint.DefaultConfigPath))
	if err := writeStarter(name, configData, *force); err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	fmt.Fprintln(stdout, "wrote", lint.DefaultConfigPath)
	return 0
}

func writeStarter(name string, data []byte, force bool) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(name, flags, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s already exists (use --force to replace it)", name)
	}
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
