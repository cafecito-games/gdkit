package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/cafecito-games/gdkit/assets"
	"github.com/cafecito-games/gdkit/internal/failure"
	"github.com/cafecito-games/gdkit/project"
)

func runAssets(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runAssetsCheck(nil, stdout, stderr)
	}
	switch args[0] {
	case "check":
		return runAssetsCheck(args[1:], stdout, stderr)
	case "init":
		return runAssetsInit(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	default:
		// "gdkit assets ." and "gdkit assets --format json" both mean check.
		return runAssetsCheck(args, stdout, stderr)
	}
}

func runAssetsCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("assets check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configName := flags.String("config", "", "configuration path relative to the project root")
	outputFormat := flags.String("format", "text", "output format: text or json")
	disable := flags.String("disable", "", "comma-separated rule names to turn off")
	minimumVersion := flags.String("minimum-version", "", minimumVersionUsage)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	// --format is validated before anything else can fail, because its value
	// decides how every later failure is reported.
	resolvedFormat, ok := checkOutputFormat(*outputFormat, stderr)
	if !ok {
		return exitUsage
	}
	if flags.NArg() > 1 {
		return reportUsage(stderr, resolvedFormat, failure.UsageArguments, "assets check accepts at most one project root")
	}
	if !checkMinimumVersion(*minimumVersion, resolvedFormat, stderr) {
		return exitUsage
	}
	root := "."
	if flags.NArg() == 1 {
		root = flags.Arg(0)
	}
	config, err := assets.LoadConfig(root, *configName)
	if err != nil {
		return reportFailure(stderr, resolvedFormat, failure.ConfigInvalid, err)
	}
	disabled, err := disabledRules(*disable)
	if err != nil {
		return reportUsage(stderr, resolvedFormat, failure.UsageArguments, err.Error())
	}
	checker, err := assets.NewChecker(config)
	if err != nil {
		return reportFailure(stderr, resolvedFormat, failure.ConfigInvalid, err)
	}
	snapshot, err := project.Load(assetsLoad(root, config))
	if err != nil {
		return reportFailure(stderr, resolvedFormat, failure.ProjectLoad, err)
	}
	report := withoutRules(checker.Check(snapshot), disabled)

	if resolvedFormat == formatJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return reportFailure(stderr, resolvedFormat, failure.OutputWrite, fmt.Errorf("write report: %w", err))
		}
	} else {
		writer := bufio.NewWriter(stdout)
		for _, diagnostic := range report.Diagnostics {
			fmt.Fprintln(writer, formatAssetsDiagnostic(diagnostic))
		}
		if len(report.Diagnostics) == 0 {
			fmt.Fprintf(writer, "assets check passed (%d of %d files eligible)\n",
				report.Reportable, report.Inventoried)
		} else {
			fmt.Fprintf(writer, "assets check: %d findings (%d of %d files eligible)\n",
				len(report.Diagnostics), report.Reportable, report.Inventoried)
		}
		if err := writer.Flush(); err != nil {
			return reportFailure(stderr, resolvedFormat, failure.OutputWrite, fmt.Errorf("write report: %w", err))
		}
	}
	if report.Failed() {
		return 1
	}
	return 0
}

// assetsLoad is how this tool loads a project, and the shape is the point.
//
// The configuration's filters go into the selection rather than onto the
// config, because assets reads far more than it reports on. Narrowing the
// walk would delete the outgoing references of everything it hid, and every
// asset those files were the only user of would immediately report as unused
// — the tool would recommend deleting a file because it was told not to look
// at the thing using it.
func assetsLoad(root string, config assets.Config) project.Config {
	return project.Config{
		Root:      root,
		Inventory: true,
		// A project can mount a shared addon with a directory symlink, and
		// Godot loads what is behind it. A walk that skipped the mount would
		// report every file inside it as unused and every reference into it
		// as missing, including the manifest's own reference to its
		// plugin.cfg. This is read-only discovery and the tool never writes.
		FollowDirectorySymlinks: true,
		Selection: &project.Selection{
			SourceRoots:     config.SourceRoots,
			Exclude:         config.Exclude,
			HonorIgnoreFile: true,
		},
	}
}

// formatAssetsDiagnostic renders one finding in the spelling the other tools
// use. A finding about a whole file has no meaningful line and reports at 1,
// while one about a reference reports at the line a reader would go edit.
func formatAssetsDiagnostic(diagnostic assets.Diagnostic) string {
	line := diagnostic.Line
	if line == 0 {
		line = 1
	}
	severity := "Error"
	if diagnostic.Severity == assets.SeverityWarning {
		severity = "Warning"
	}
	return fmt.Sprintf("%s:%d: %s: %s (%s)", diagnostic.Path, line, severity, diagnostic.Message, diagnostic.Rule)
}

// disabledRules parses --disable, rejecting a name no rule has so a typo
// fails loudly rather than silently turning nothing off.
func disabledRules(value string) (map[string]bool, error) {
	disabled := map[string]bool{}
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !assets.IsRule(name) {
			return nil, fmt.Errorf("--disable names unknown rule %q", name)
		}
		disabled[name] = true
	}
	return disabled, nil
}

// withoutRules drops the findings of disabled rules. The counts are left
// alone: they describe what was inventoried, not what was reported.
func withoutRules(report assets.Report, disabled map[string]bool) assets.Report {
	if len(disabled) == 0 {
		return report
	}
	kept := make([]assets.Diagnostic, 0, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		if !disabled[diagnostic.Rule] {
			kept = append(kept, diagnostic)
		}
	}
	report.Diagnostics = kept
	return report
}

func runAssetsInit(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("assets init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	force := flags.Bool("force", false, "replace an existing configuration file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "assets init accepts at most one project root")
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
	configData, err := json.MarshalIndent(assets.DefaultConfig(), "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	configData = append(configData, '\n')
	name := filepath.Join(root, filepath.FromSlash(assets.DefaultConfigPath))
	if err := writeStarter(name, configData, *force); err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	fmt.Fprintln(stdout, "wrote", assets.DefaultConfigPath)
	return 0
}
