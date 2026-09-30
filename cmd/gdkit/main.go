package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cafecito-games/gdkit/architecture"
	"github.com/cafecito-games/gdkit/internal/buildinfo"
)

const usageText = `gdkit — static source tooling for GDScript

Usage:
  gdkit arch check [flags] [project-root]
  gdkit arch init  [flags] [project-root]
  gdkit version    [flags]

Commands:
  arch check   enforce architectural dependency rules
  arch init    write starter .gdkit configuration files
  version      print version and source revision information
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
	if args[0] != "arch" {
		if args[0] == "version" {
			return runVersion(args[1:], stdout, stderr)
		}
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usageText)
		return 2
	}
	if len(args) == 1 {
		return runCheck(nil, stdout, stderr)
	}
	switch args[1] {
	case "check":
		return runCheck(args[2:], stdout, stderr)
	case "init":
		return runInit(args[2:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown arch command %q\n\n%s", args[1], usageText)
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
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "version does not accept positional arguments")
		return 2
	}
	info := buildinfo.Current()
	switch *format {
	case "text":
		fmt.Fprintln(stdout, info.String())
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(info); err != nil {
			fmt.Fprintln(stderr, "gdkit: write version:", err)
			return 2
		}
	default:
		fmt.Fprintf(stderr, "unknown output format %q (want text or json)\n", *format)
		return 2
	}
	return 0
}

func runCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("arch check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configName := flags.String("config", "", "configuration path relative to the project root")
	format := flags.String("format", "text", "output format: text or json")
	showEdges := flags.Bool("show-edges", false, "include resolved dependencies in text output")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "arch check accepts at most one project root")
		return 2
	}
	root := "."
	if flags.NArg() == 1 {
		root = flags.Arg(0)
	}
	config, err := architecture.LoadConfig(root, *configName)
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	analyzer, err := architecture.NewAnalyzer(root, config)
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	report, err := analyzer.Analyze()
	if err != nil {
		fmt.Fprintln(stderr, "gdkit:", err)
		return 2
	}
	switch *format {
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintln(stderr, "gdkit: write report:", err)
			return 2
		}
	case "text":
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
	default:
		fmt.Fprintf(stderr, "unknown output format %q (want text or json)\n", *format)
		return 2
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
