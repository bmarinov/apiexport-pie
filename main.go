// Command apiexport-pie draws the kcp APIExports and APIResourceSchemas found under a directory as d2.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
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

const usage = "usage: apiexport-pie [-v] [-o FILE [--watch]] DIR"

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
	out := flags.String("o", "", "write the diagram to `FILE` instead of stdout")
	watching := flags.Bool("watch", false, "with -o: keep FILE up to date and show it with d2 --watch")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout, flags)
			return exitOK
		}
		report(stderr, "apiexport-pie: %v", err)
		printUsage(stderr, flags)
		return exitUsage
	}
	if flags.NArg() != 1 || (*watching && *out == "") {
		printUsage(stderr, flags)
		return exitUsage
	}
	dir := flags.Arg(0)

	generate := func() ([]byte, []string, error) {
		model, diags, err := pie.Scan(os.DirFS(dir))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", dir, err)
		}
		var notes []string
		for _, d := range diags {
			if d.Level == pie.Warning || *verbose {
				notes = append(notes, d.String())
			}
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
