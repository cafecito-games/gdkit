package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdkit/uid"
)

func runUID(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runUIDCheck(nil, stdout, stderr)
	}
	switch args[0] {
	case "check":
		return runUIDCheck(args[1:], stdout, stderr)
	case "write":
		return runUIDWrite(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown uid command %q\n\n%s", args[0], usageText)
		return 2
	}
}

func runUIDCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("uid check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	outputFormat := flags.String("format", "text", "output format: text or json")
	minimumVersion := flags.String("minimum-version", "", minimumVersionUsage)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	root, code := uidRoot("uid check", flags, *outputFormat, stderr)
	if code != 0 {
		return code
	}
	if !checkMinimumVersion(*minimumVersion, stderr) {
		return 2
	}
	_, report, err := checkUIDs(root)
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	if *outputFormat == "json" {
		if err := writeUIDReport(stdout, report); err != nil {
			fmt.Fprintln(stderr, "gdkit: write report:", err)
			return 2
		}
	} else {
		writer := bufio.NewWriter(stdout)
		for _, diagnostic := range report.Diagnostics {
			fmt.Fprintln(writer, diagnostic.String())
		}
		if report.HasDiagnostics() {
			fmt.Fprintf(writer, "uid check failed (%d missing, %d to repair)\n", len(report.Missing()), len(report.Repairs()))
		} else {
			fmt.Fprintf(writer, "uid check passed (%d files)\n", report.Scripts)
		}
		if err := writer.Flush(); err != nil {
			fmt.Fprintln(stderr, "gdkit: write report:", err)
			return 2
		}
	}
	if report.HasDiagnostics() {
		return 1
	}
	return 0
}

func runUIDWrite(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("uid write", flag.ContinueOnError)
	flags.SetOutput(stderr)
	outputFormat := flags.String("format", "text", "output format: text or json")
	minimumVersion := flags.String("minimum-version", "", minimumVersionUsage)
	repair := flags.Bool("repair", false, "also reissue malformed and duplicated sidecars")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	root, code := uidRoot("uid write", flags, *outputFormat, stderr)
	if code != 0 {
		return code
	}
	if !checkMinimumVersion(*minimumVersion, stderr) {
		return 2
	}
	snapshot, report, err := checkUIDs(root)
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	written, applyError := uid.Apply(snapshot, report, uid.NewGenerator(nil), *repair)
	// Without --repair the malformed and duplicated sidecars are still on
	// disk, so the run reports a failure even though it wrote what it could.
	remaining := 0
	if !*repair {
		remaining = len(report.Repairs())
	}
	if *outputFormat == "json" {
		if err := writeUIDReport(stdout, uidWriteReport{Report: report, Written: written}); err != nil {
			fmt.Fprintln(stderr, "gdkit: write report:", err)
			return 2
		}
	} else {
		writer := bufio.NewWriter(stdout)
		for _, path := range written {
			fmt.Fprintln(writer, "wrote", path)
		}
		if remaining > 0 {
			for _, diagnostic := range report.Repairs() {
				fmt.Fprintln(writer, diagnostic.String())
			}
		}
		// A run that stopped part-way has no totals worth stating; the files
		// listed above are the ones that changed on disk.
		if applyError == nil {
			created := len(report.Missing())
			repaired := len(written) - created
			fmt.Fprintf(writer, "uid write: %d created", created)
			if *repair {
				fmt.Fprintf(writer, ", %d repaired", repaired)
			}
			// Unchanged means nothing was wrong, so the sidecars left for
			// a later --repair are not counted among them. A repaired
			// sidecar can also sit beside a script that no longer exists,
			// which was never one of the files counted here.
			fmt.Fprintf(writer, ", %d unchanged", max(report.Scripts-created-repaired-remaining, 0))
			if remaining > 0 {
				fmt.Fprintf(writer, ", %d left to repair", remaining)
			}
			fmt.Fprintln(writer)
			if remaining > 0 {
				fmt.Fprintln(writer, "re-run with --repair to reissue them, which changes what existing uid:// references resolve to")
			}
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
	if remaining > 0 {
		return 1
	}
	return 0
}

// uidRoot validates the flags both uid commands share and returns the project
// root to work on. A non-zero code is the exit code to return.
func uidRoot(name string, flags *flag.FlagSet, outputFormat string, stderr io.Writer) (string, int) {
	if flags.NArg() > 1 {
		fmt.Fprintf(stderr, "%s accepts at most one project root\n", name)
		return "", 2
	}
	if outputFormat != "text" && outputFormat != "json" {
		fmt.Fprintf(stderr, "unknown output format %q (want text or json)\n", outputFormat)
		return "", 2
	}
	if flags.NArg() == 1 {
		return flags.Arg(0), 0
	}
	return ".", 0
}

// checkUIDs loads the project under root and checks its identities. It writes
// nothing; the snapshot is returned so the caller can write sidecars into the
// project the report was computed from.
//
// There is no configuration file: a uid run has nothing to configure beyond
// the root, and it honors .gdkitignore as lint and format do, so a script
// hidden there is given no identity.
func checkUIDs(root string) (*project.Snapshot, uid.Report, error) {
	snapshot, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true})
	if err != nil {
		return nil, uid.Report{}, err
	}
	return snapshot, uid.Check(snapshot), nil
}

// uidWriteReport is the JSON output of uid write: the report, plus the
// sidecars the run actually wrote.
type uidWriteReport struct {
	uid.Report
	// Written lists sidecar paths, project-relative, in the order they were
	// written. It lists the ones written before a failure when the run
	// stopped part-way.
	Written []string `json:"written"`
}

// writeUIDReport prints report, a uid.Report or a wrapper around one, as
// indented JSON.
func writeUIDReport(stdout io.Writer, report any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
