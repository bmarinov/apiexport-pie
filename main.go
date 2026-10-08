// Command apiexport-pie draws the kcp APIExports and APIResourceSchemas found under a directory as d2.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/bmarinov/apiexport-pie/internal/pie"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usage = "usage: apiexport-pie [-v] DIR"

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(_ context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("apiexport-pie", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	verbose := flags.Bool("v", false, "also report documents that are not drawn")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout, flags)
			return exitOK
		}
		report(stderr, "apiexport-pie: %v", err)
		printUsage(stderr, flags)
		return exitUsage
	}
	if flags.NArg() != 1 {
		printUsage(stderr, flags)
		return exitUsage
	}
	dir := flags.Arg(0)

	model, diags, err := pie.Scan(os.DirFS(dir))
	if err != nil {
		report(stderr, "apiexport-pie: %s: %v", dir, err)
		return exitError
	}
	for _, d := range diags {
		if d.Level == pie.Warning || *verbose {
			report(stderr, "%s", d)
		}
	}
	if _, err := stdout.Write(pie.RenderD2(model)); err != nil {
		report(stderr, "apiexport-pie: writing diagram: %v", err)
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
