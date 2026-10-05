package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cafecito-games/gdkit/format"
	"github.com/cafecito-games/gdkit/generate"
	"github.com/cafecito-games/gdkit/internal/failure"
	"github.com/cafecito-games/gdkit/internal/textdiff"
	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdkit/uid"
)

func runGen(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runGenCheck(nil, stdout, stderr)
	}
	switch args[0] {
	case "check":
		return runGenCheck(args[1:], stdout, stderr)
	case "write":
		return runGenWrite(args[1:], stdout, stderr)
	case "init":
		return runGenInit(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	default:
		// "gdkit gen ." and "gdkit gen --format json" both mean check.
		return runGenCheck(args, stdout, stderr)
	}
}

func runGenCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gen check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configName := flags.String("config", "", "configuration path relative to the project root")
	outputFormat := flags.String("format", "text", "output format: text or json")
	showDiff := flags.Bool("diff", false, "print a unified diff for each file that would change")
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
		return reportUsage(stderr, resolvedFormat, failure.UsageArguments, "gen check accepts at most one project root")
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
	snapshot, plan, err := genProject(root, *configName)
	if err != nil {
		return reportFailure(stderr, resolvedFormat, failure.ConfigInvalid, err)
	}
	report := plan.Report()
	if resolvedFormat == formatJSON {
		if err := writeGenReport(stdout, report); err != nil {
			return reportFailure(stderr, resolvedFormat, failure.OutputWrite, fmt.Errorf("write report: %w", err))
		}
	} else {
		writer := bufio.NewWriter(stdout)
		changed := 0
		for _, candidate := range plan.Candidates {
			if !candidate.Changed {
				continue
			}
			changed++
			fmt.Fprintln(writer, "would generate", candidate.Path)
			if *showDiff {
				fmt.Fprint(writer, textdiff.Unified(
					"a/"+candidate.Path, "b/"+candidate.Path,
					snapshot.Scripts[candidate.Path].Source, candidate.Contents))
			}
		}
		for _, diagnostic := range report.Diagnostics {
			if diagnostic.Rule == "generate.stale" {
				continue
			}
			fmt.Fprintln(writer, diagnostic.String())
		}
		if report.HasChanges() || report.HasDiagnostics() {
			fmt.Fprintf(writer, "gen check failed (%d to generate, %d diagnostics)\n",
				changed, len(report.Diagnostics))
		} else {
			fmt.Fprintf(writer, "gen check passed (%d classes)\n", len(report.Results))
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

func runGenWrite(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gen write", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configName := flags.String("config", "", "configuration path relative to the project root")
	outputFormat := flags.String("format", "text", "output format: text or json")
	prune := flags.Bool("prune", false, "remove a generated region whose class no longer opts in")
	minimumVersion := flags.String("minimum-version", "", minimumVersionUsage)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	resolvedFormat, ok := checkOutputFormat(*outputFormat, stderr)
	if !ok {
		return exitUsage
	}
	if flags.NArg() > 1 {
		return reportUsage(stderr, resolvedFormat, failure.UsageArguments, "gen write accepts at most one project root")
	}
	if !checkMinimumVersion(*minimumVersion, resolvedFormat, stderr) {
		return exitUsage
	}
	root := "."
	if flags.NArg() == 1 {
		root = flags.Arg(0)
	}
	snapshot, plan, err := genProject(root, *configName)
	if err != nil {
		return reportFailure(stderr, resolvedFormat, failure.ConfigInvalid, err)
	}
	report := plan.Report()
	written, applyError := generate.Apply(snapshot, plan, *prune)
	// Which paths the run removed a region from, so an orphan diagnostic that
	// --prune has just acted on does not also fail the run.
	pruned := map[string]bool{}
	if *prune {
		for _, path := range written {
			pruned[path] = true
		}
	}
	if resolvedFormat == formatJSON {
		if err := writeGenReport(stdout, genWriteReport{Report: report, Written: written}); err != nil {
			return reportFailure(stderr, resolvedFormat, failure.OutputWrite, fmt.Errorf("write report: %w", err))
		}
	} else {
		writer := bufio.NewWriter(stdout)
		for _, path := range written {
			fmt.Fprintln(writer, "generated", path)
		}
		for _, diagnostic := range report.Diagnostics {
			// Staleness is what write just fixed, so reporting it would be
			// describing the problem the command exists to solve. The same
			// goes for an orphan --prune has just removed.
			if diagnostic.Rule == "generate.stale" {
				continue
			}
			if diagnostic.Rule == "generate.orphaned" && pruned[diagnostic.Path] {
				continue
			}
			fmt.Fprintln(writer, diagnostic.String())
		}
		// A run that stopped part-way has no totals worth stating; the files
		// listed above are the ones that changed on disk.
		if applyError == nil {
			fmt.Fprintf(writer, "gen write: %d generated", len(written))
			if blockers := countBlockers(report, pruned); blockers > 0 {
				fmt.Fprintf(writer, ", %d refused", blockers)
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
	// Staleness is not a failure for write: fixing it is the point. Only a
	// class that was refused is.
	if countBlockers(report, pruned) > 0 {
		return 1
	}
	return 0
}

// countBlockers is the number of diagnostics write did not just resolve.
//
// Staleness never counts: fixing it is what write is for. An orphan counts
// only when it was left in place, because with --prune the diagnostic
// describes something the run has just removed.
func countBlockers(report generate.Report, pruned map[string]bool) int {
	blockers := 0
	for _, diagnostic := range report.Diagnostics {
		switch diagnostic.Rule {
		case "generate.stale":
		case "generate.orphaned":
			if !pruned[diagnostic.Path] {
				blockers++
			}
		default:
			blockers++
		}
	}
	return blockers
}

func runGenInit(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gen init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	force := flags.Bool("force", false, "replace an existing configuration file")
	helpers := flags.Bool("helpers", false, "also write the generated helpers class and its uid sidecar")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "gen init accepts at most one project root")
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
	configData, err := json.MarshalIndent(generate.DefaultConfig(), "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	configData = append(configData, '\n')
	name := filepath.Join(root, filepath.FromSlash(generate.DefaultConfigPath))
	// With --helpers an existing configuration is kept rather than refused:
	// the helpers class is written where that configuration says, and
	// running the command again must be a no-op.
	if _, statErr := os.Stat(name); *helpers && !*force && statErr == nil {
		fmt.Fprintln(stdout, generate.DefaultConfigPath, "already exists")
	} else {
		if err := writeStarter(name, configData, *force); err != nil {
			fmt.Fprintln(stderr, "gdkit:", err)
			return 2
		}
		fmt.Fprintln(stdout, "wrote", generate.DefaultConfigPath)
	}
	if *helpers {
		if code := writeHelpersClass(root, *force, stdout, stderr); code != 0 {
			return code
		}
	}
	return 0
}

// writeHelpersClass writes the generated utility class and its uid sidecar.
//
// Each is written only when absent, and they are reported separately because
// they can be in different states: a project may already hold the script with
// an identity Godot assigned, and overwriting that sidecar would break every
// reference Godot has cached. --force therefore replaces the script and never
// the sidecar.
func writeHelpersClass(root string, force bool, stdout, stderr io.Writer) int {
	config, err := generate.LoadConfig(root, "")
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	relative := config.HelpersPath
	target := filepath.Join(root, filepath.FromSlash(relative))
	script := "class_name GDKitHelpers\nextends RefCounted\n"
	if _, err := os.Stat(target); err == nil && !force {
		fmt.Fprintln(stdout, relative, "already exists")
	} else {
		if err := writeStarter(target, []byte(script), force); err != nil {
			fmt.Fprintln(stderr, "gdkit:", err)
			return 2
		}
		fmt.Fprintln(stdout, "wrote", relative)
	}
	sidecar := target + ".uid"
	if _, err := os.Stat(sidecar); err == nil {
		fmt.Fprintln(stdout, relative+".uid", "already exists")
		return 0
	}
	identifier, err := mintHelpersUID(root)
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	if err := writeStarter(sidecar, []byte(identifier+"\n"), false); err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	fmt.Fprintln(stdout, "wrote", relative+".uid")
	return 0
}

// mintHelpersUID draws an identifier no declaration in the project already
// claims. Every claim is reserved first, including one inside an ignored path:
// a collision would be reported by uid check as a duplicate, and the file
// gdkit told the project to create must not be the cause.
func mintHelpersUID(root string) (string, error) {
	snapshot, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true, Identities: true})
	if err != nil {
		return "", err
	}
	generator := uid.NewGenerator(nil)
	for _, claim := range snapshot.Claims {
		identifier, ok := uid.Decode(claim.UID)
		if !ok {
			// A malformed declaration claims no identifier, and uid check is
			// what reports it; drawing around it is not this command's job.
			continue
		}
		generator.Reserve(identifier)
	}
	identifier, err := generator.Next()
	if err != nil {
		return "", err
	}
	return uid.Encode(identifier), nil
}

// genProject loads the project under root and computes its generation plan.
//
// The universe is loaded unfiltered and the configuration's filters go into the
// selection, because generate indexes more than it writes: equals composes with
// an ancestor and is refused when a descendant would inherit an unsound
// implementation, and a file hidden by .gdkitignore would take its class_name
// and its extends edge out of the graph that decides both.
//
// The project's format configuration is loaded too, so the generated region is
// canonical in the project's own style and gen write introduces no new format
// check finding.
func genProject(root, configName string) (*project.Snapshot, generate.Plan, error) {
	config, err := generate.LoadConfig(root, configName)
	if err != nil {
		return nil, generate.Plan{}, err
	}
	formatting, err := format.LoadConfig(root, "")
	if err != nil {
		return nil, generate.Plan{}, err
	}
	generator, err := generate.New(config, formatting)
	if err != nil {
		return nil, generate.Plan{}, err
	}
	snapshot, err := project.Load(project.Config{
		Root: root,
		Selection: &project.Selection{
			SourceRoots:     config.SourceRoots,
			Exclude:         config.Exclude,
			HonorIgnoreFile: true,
		},
	})
	if err != nil {
		return nil, generate.Plan{}, err
	}
	return snapshot, generator.Check(snapshot), nil
}

// genWriteReport is the JSON output of gen write: the report, plus the paths
// the run actually replaced on disk.
type genWriteReport struct {
	generate.Report
	// Written is project-relative and in path order. It lists the files
	// written before a failure when the run stopped part-way.
	Written []string `json:"written"`
}

// writeGenReport prints report, a generate.Report or a wrapper around one, as
// indented JSON.
func writeGenReport(stdout io.Writer, report any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
