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

const usage = "usage: apiexport-pie [-v] [-o FILE [--watch]] DIR\n       apiexport-pie [-v] --surface [-o FILE [--watch]] DIR"

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
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout, flags)
			return exitOK
		}
		report(stderr, "apiexport-pie: %v", err)
		printUsage(stderr, flags)
		return exitUsage
	}
	if reason := misuse(flags.NArg(), *out, *watching); reason != "" {
		report(stderr, "apiexport-pie: %s", reason)
		printUsage(stderr, flags)
		return exitUsage
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
	if drawn(*out) {
		if err := drawWithD2(ctx, *out, diagram); err != nil {
			report(stderr, "apiexport-pie: %v", err)
			return exitError
		}
		return exitOK
	}
	if *out != "" {
		if err := watch.ReplaceFile(*out, diagram); err != nil {
			report(stderr, "apiexport-pie: %v", err)
			return exitError
		}
		return exitOK
	}
	if _, err := stdout.Write(diagram); err != nil {
		report(stderr, "apiexport-pie: writing diagram: %v", err)
		return exitError
	}
	return exitOK
}

// misuse says what is wrong with a command line the flag package accepted, or "" when nothing is.
func misuse(dirs int, out string, watching bool) string {
	switch {
	case dirs != 1:
		return fmt.Sprintf("want one DIR, got %d", dirs)
	case watching && out == "":
		return "--watch needs -o FILE, the file d2 shows"
	case watching && !strings.HasSuffix(out, ".d2"):
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
