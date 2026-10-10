// Command apiexport-pie draws the kcp APIExports and APIResourceSchemas found under a directory as d2.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/bmarinov/apiexport-pie/internal/pie"
	"github.com/bmarinov/apiexport-pie/internal/watch"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usage = "usage: apiexport-pie [-v] [-o FILE [--watch]] DIR\n       apiexport-pie [-v] --surface [-o FILE [--watch]] DIR\n       apiexport-pie [-v] --diff [--surface [-o FILE]] A B"

// pollInterval is how often --watch draws the diagram again.
const pollInterval = time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("apiexport-pie", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	verbose := flags.Bool("v", false, "also report documents that are not drawn")
	out := flags.String("o", "", "write to `FILE` instead of stdout: d2 draws .svg, .png and .pdf; with --surface, .d2 gets the table as d2, other names markdown")
	watching := flags.Bool("watch", false, "with -o: keep FILE up to date and show it with d2 --watch")
	surface := flags.Bool("surface", false, "print a markdown table instead of the diagram: which workspaces get which kinds, and what they grant")
	diff := flags.Bool("diff", false, "print what changes from tree A to tree B, a line per change; with --surface, both tables as one in d2")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout, flags)
			return exitOK
		}
		report(stderr, "apiexport-pie: %v", err)
		printUsage(stderr, flags)
		return exitUsage
	}
	if reason := misuse(invocation{trees: flags.NArg(), out: *out, watching: *watching, surface: *surface, diff: *diff}); reason != "" {
		report(stderr, "apiexport-pie: %s", reason)
		printUsage(stderr, flags)
		return exitUsage
	}
	if *diff {
		before, after, code := scanTrees(ctx, flags.Arg(0), flags.Arg(1), *verbose, stderr)
		if code != exitOK {
			return code
		}
		if *surface {
			return write(ctx, *out, pie.RenderSurfaceDiffD2(before, after), stdout, stderr)
		}
		return write(ctx, "", pie.RenderDiff(before, after), stdout, stderr)
	}
	dir := flags.Arg(0)

	generate := func() ([]byte, []string, error) {
		model, diags, err := pie.Scan(os.DirFS(dir))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", dir, err)
		}
		diags = append(diags, pie.Check(model)...)
		var notes []string
		for _, d := range diags {
			if d.Level != pie.Skipped || *verbose {
				notes = append(notes, d.String())
			}
		}
		if *surface && (strings.HasSuffix(*out, ".d2") || drawn(*out)) {
			return pie.RenderSurfaceD2(model), notes, nil
		}
		if *surface {
			return pie.RenderSurface(model), notes, nil
		}
		return pie.RenderD2(model), notes, nil
	}

	if *watching {
		return watchDiagram(ctx, *out, generate, stderr)
	}
	diagram, notes, err := generate()
	if err != nil {
		report(stderr, "apiexport-pie: %v", err)
		return exitError
	}
	for _, note := range notes {
		report(stderr, "%s", note)
	}
	return write(ctx, *out, diagram, stdout, stderr)
}

// write puts out what was drawn: through d2 into an image FILE, as is into any other FILE, or on
// stdout without one.
func write(ctx context.Context, out string, content []byte, stdout, stderr io.Writer) int {
	if drawn(out) {
		if err := drawWithD2(ctx, out, content); err != nil {
			report(stderr, "apiexport-pie: %v", err)
			return exitError
		}
		return exitOK
	}
	if out != "" {
		if err := watch.ReplaceFile(out, content); err != nil {
			report(stderr, "apiexport-pie: %v", err)
			return exitError
		}
		return exitOK
	}
	if _, err := stdout.Write(content); err != nil {
		report(stderr, "apiexport-pie: writing to stdout: %v", err)
		return exitError
	}
	return exitOK
}

// invocation is what a command line asks for, as far as misuse can tell.
type invocation struct {
	trees                   int
	out                     string
	watching, surface, diff bool
}

// scanTrees scans tree a and tree b, each a directory or a commit (see openTree). Warnings name the
// tree in front of the path, since both trees have the same paths; notices are not printed, they are
// what may change.
func scanTrees(ctx context.Context, a, b string, verbose bool, stderr io.Writer) (pie.Model, pie.Model, int) {
	var models []pie.Model
	for _, tree := range []string{a, b} {
		dir, cleanup, err := openTree(ctx, tree)
		if err != nil {
			report(stderr, "apiexport-pie: %s: %v", tree, err)
			return pie.Model{}, pie.Model{}, exitError
		}
		model, diags, err := pie.Scan(os.DirFS(dir))
		if cleaned := cleanup(); cleaned != nil {
			report(stderr, "apiexport-pie: %s: removing what was unpacked: %v", tree, cleaned)
		}
		if err != nil {
			report(stderr, "apiexport-pie: %s: %v", tree, err)
			return pie.Model{}, pie.Model{}, exitError
		}
		for _, d := range append(diags, pie.Check(model)...) {
			if d.Level == pie.Warning || d.Level == pie.Skipped && verbose {
				d.Path = strings.TrimSuffix(tree, "/") + "/" + d.Path
				report(stderr, "%s", d)
			}
		}
		models = append(models, model)
	}
	return models[0], models[1], exitOK
}

// misuse says what is wrong with a command line the flag package accepted, or "" when nothing is.
func misuse(in invocation) string {
	switch {
	case in.diff && in.trees != 2:
		return fmt.Sprintf("--diff wants A and B, got %d", in.trees)
	case in.diff && in.watching:
		return "--watch shows one tree; it does not go with --diff"
	case in.diff && in.out != "" && !in.surface:
		return "--diff writes FILE only with --surface; the list of changes goes to stdout"
	case !in.diff && in.trees != 1:
		return fmt.Sprintf("want one DIR, got %d", in.trees)
	case in.watching && in.out == "":
		return "--watch needs -o FILE, the file d2 shows"
	case in.watching && !strings.HasSuffix(in.out, ".d2"):
		return "--watch shows FILE in d2, so FILE must end in .d2"
	}
	return ""
}

// imageFormats are the -o extensions d2 draws; the tool hands it the d2 source.
var imageFormats = []string{".svg", ".png", ".pdf"}

func drawn(file string) bool { return slices.Contains(imageFormats, filepath.Ext(file)) }

// drawWithD2 has d2 draw source into file, in the format file's extension names.
func drawWithD2(ctx context.Context, file string, source []byte) error {
	d2, err := exec.LookPath("d2")
	if err != nil {
		return fmt.Errorf("-o %s needs d2 on PATH: %w", file, err)
	}
	cmd := exec.CommandContext(ctx, d2, "-", file)
	cmd.Stdin = bytes.NewReader(source)
	// d2 reports success on stderr as well, so its words are passed on only when it fails.
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("d2 could not draw %s: %w\n%s", file, err, bytes.TrimSpace(output))
	}
	return nil
}

func watchDiagram(ctx context.Context, file string, generate func() ([]byte, []string, error), stderr io.Writer) int {
	d2, err := exec.LookPath("d2")
	if err != nil {
		report(stderr, "apiexport-pie: --watch needs d2 on PATH: %v", err)
		return exitError
	}
	viewer := exec.Command(d2, "--watch", file)
	viewer.Stdout, viewer.Stderr = stderr, stderr
	err = watch.Run(ctx, watch.Options{
		File:     file,
		Interval: pollInterval,
		Generate: generate,
		Viewer:   viewer,
		Report:   func(line string) { report(stderr, "%s", line) },
	})
	if err != nil {
		report(stderr, "apiexport-pie: %v", err)
		return exitError
	}
	return exitOK
}

func printUsage(w io.Writer, flags *flag.FlagSet) {
	report(w, usage)
	flags.SetOutput(w)
	flags.PrintDefaults()
}

// report writes one diagnostic line. Its write error is dropped on purpose: stderr is
// the last place left to report to.
func report(stderr io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(stderr, format+"\n", args...)
}
