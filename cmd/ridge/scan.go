package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"

	"github.com/olgasafonova/ridge/tools"
)

// Exit codes for the command-line mode.
const (
	exitOK    = 0
	exitError = 1 // the scan or the write failed
	exitUsage = 2 // unknown subcommand, unknown flag or missing argument
)

const scanUsage = `Usage: code-to-arch scan [flags] [path]

Scans a codebase and writes the full graph as JSON: every node and edge, with
no cap on their number. The JSON goes to stdout, or to a file with -out.

path is the folder to scan. Use -repo instead for a registered alias.

Flags:
`

// runCommand dispatches a subcommand and returns the process exit code.
func runCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if args[0] != "scan" {
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\nCommands:\n  scan    write scan JSON to stdout or a file\n\nRun with no command to start the MCP server on stdio.\n", args[0])
		return exitUsage
	}
	return runScan(ctx, args[1:], stdout, stderr)
}

// stringList collects a flag that may be given several times.
type stringList []string

func (l *stringList) String() string { return fmt.Sprint([]string(*l)) }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// scanFlags holds the parsed command line of the scan subcommand.
type scanFlags struct {
	args tools.ArchScanArgs
	out  string
}

// parseScanFlags reads the scan subcommand's flags. On failure it has already
// written the reason to stderr; ok is false and code is the exit code.
func parseScanFlags(argv []string, stderr io.Writer) (parsed scanFlags, code int, ok bool) {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprint(stderr, scanUsage)
		fs.PrintDefaults()
	}

	var skipDirs, skipGlobs stringList
	a := &parsed.args
	fs.StringVar(&parsed.out, "out", "", "write the JSON to this `file` instead of stdout")
	fs.StringVar(&a.Repo, "repo", "", "registered `alias` to scan instead of a path")
	fs.BoolVar(&a.IncludeTests, "include-tests", false, "analyze test files and test folders too")
	fs.BoolVar(&a.Samples, "samples", false, "include a short source excerpt on each node")
	fs.IntVar(&a.MaxFiles, "max-files", 0, "stop after this many files (0 = no limit)")
	fs.IntVar(&a.MaxNodes, "max-nodes", 0, "stop after this many nodes (0 = no limit)")
	fs.IntVar(&a.TimeoutSecs, "timeout", 0, "abort after this many `seconds` (0 = 120)")
	fs.Var(&skipDirs, "skip-dir", "folder `name` to skip; may be repeated")
	fs.Var(&skipGlobs, "skip-glob", "file `pattern` to skip, such as '**/*.md'; may be repeated")

	if err := fs.Parse(argv); err != nil {
		if err == flag.ErrHelp {
			return parsed, exitOK, false
		}
		return parsed, exitUsage, false
	}
	a.SkipDirs, a.SkipGlobs = skipDirs, skipGlobs

	switch {
	case fs.NArg() > 1:
		_, _ = fmt.Fprintf(stderr, "scan takes one path, got %d: %v\n", fs.NArg(), fs.Args())
		return parsed, exitUsage, false
	case fs.NArg() == 1:
		a.Path = fs.Arg(0)
	case a.Repo == "":
		_, _ = fmt.Fprintln(stderr, "scan needs a path or -repo")
		fs.Usage()
		return parsed, exitUsage, false
	}

	a.Detail = "full"
	a.MaxDetailNodes = math.MaxInt32 // the point of this mode is the whole graph
	return parsed, exitOK, true
}

// runScan implements "code-to-arch scan". It writes only the JSON to stdout;
// with -out it writes the file and prints one line saying what it wrote.
func runScan(ctx context.Context, argv []string, stdout, stderr io.Writer) int {
	parsed, code, ok := parseScanFlags(argv, stderr)
	if !ok {
		return code
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	result, err := tools.NewHandlerRegistry(logger).Scan(ctx, parsed.args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "scan failed: %v\n", err)
		return exitError
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "encoding result: %v\n", err)
		return exitError
	}
	data = append(data, '\n')

	if parsed.out == "" {
		if _, err := stdout.Write(data); err != nil {
			_, _ = fmt.Fprintf(stderr, "writing result: %v\n", err)
			return exitError
		}
		return exitOK
	}
	out := filepath.Clean(parsed.out)
	if err := os.WriteFile(out, data, 0o600); err != nil { // #nosec G304 G703 -- the path is the user's own -out argument
		_, _ = fmt.Fprintf(stderr, "writing %s: %v\n", out, err)
		return exitError
	}
	_, _ = fmt.Fprintf(stdout, "wrote %d nodes and %d edges to %s (%d bytes)%s\n",
		result.NodeCount, result.EdgeCount, out, len(data), truncatedNote(result.Truncated))
	return exitOK
}

func truncatedNote(truncated bool) string {
	if truncated {
		return "; truncated: a scan limit was hit"
	}
	return ""
}
