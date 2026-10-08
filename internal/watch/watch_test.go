package watch_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmarinov/apiexport-pie/internal/watch"
)

const fakeViewerEnv = "WATCH_FAKE_VIEWER"

// TestMain lets the test binary stand in for d2: run with fakeViewerEnv set, it plays a viewer
// instead of running tests.
func TestMain(m *testing.M) {
	switch os.Getenv(fakeViewerEnv) {
	case "serve":
		serve()
	case "crash":
		os.Exit(3)
	case "finish":
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// serve behaves like `d2 --watch FILE` (v0.6.9): FILE must exist when it starts, and it runs until
// interrupted, then exits with status 1. It marks FILE.viewing once it has checked FILE.
func serve() {
	file := os.Args[len(os.Args)-1]
	if _, err := os.Stat(file); err != nil {
		os.Exit(7)
	}
	if err := os.WriteFile(file+".viewing", nil, 0o644); err != nil {
		os.Exit(8)
	}
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	<-interrupted
	os.Exit(1)
}

func fakeViewer(mode, file string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], file)
	cmd.Env = append(os.Environ(), fakeViewerEnv+"="+mode)
	return cmd
}

func TestRun(t *testing.T) {
	t.Run("writes the diagram before the viewer starts, and stops the viewer when the context ends", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "pie.d2")
		src := &source{diagram: "a"}
		viewer := fakeViewer("serve", file)
		ctx, cancel := context.WithCancel(t.Context())
		done := runInBackground(ctx, options(file, src, viewer, &reports{}))

		waitFor(t, "the diagram to be written", func() bool { return contentOf(file) == "a" })
		cancel()

		if err := waitResult(t, done); err != nil || viewer.ProcessState == nil {
			t.Errorf("Run() = %v with the viewer exited: %v, want nil once the viewer has exited", err, viewer.ProcessState != nil)
		}
	})

	t.Run("writes an empty diagram too, so the viewer has a file to open", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "pie.d2")
		done := runInBackground(t.Context(), options(file, &source{diagram: ""}, fakeViewer("serve", file), &reports{}))

		waitFor(t, "the empty diagram", func() bool { _, err := os.Stat(file); return err == nil })
		stopAndWait(t, done)
	})

	t.Run("ends without error when the viewer finishes by itself", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "pie.d2")

		if err := waitResult(t, runInBackground(t.Context(), options(file, &source{diagram: "a"}, fakeViewer("finish", file), &reports{}))); err != nil {
			t.Errorf("Run() = %v, want nil", err)
		}
	})

	t.Run("rewrites the file when the diagram changes and leaves it alone while it does not", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "pie.d2")
		src := &source{diagram: "a"}
		done := runInBackground(t.Context(), options(file, src, fakeViewer("serve", file), &reports{}))
		waitFor(t, "the first diagram", func() bool { return contentOf(file) == "a" })
		before := stat(t, file)

		waitFor(t, "a few more generations", func() bool { return src.calls() > 5 })
		if !os.SameFile(before, stat(t, file)) {
			t.Errorf("the file was rewritten although the diagram did not change")
		}

		src.set("b", nil, nil)
		waitFor(t, "the changed diagram", func() bool { return contentOf(file) == "b" })
		stopAndWait(t, done)
	})

	t.Run("reports the notes of each new diagram once", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "pie.d2")
		src := &source{diagram: "a", notes: []string{"warning: a.yaml#1: first"}}
		got := &reports{}
		done := runInBackground(t.Context(), options(file, src, fakeViewer("serve", file), got))
		waitFor(t, "a few generations", func() bool { return src.calls() > 5 })

		src.set("b", []string{"warning: b.yaml#1: second"}, nil)
		waitFor(t, "the changed diagram", func() bool { return contentOf(file) == "b" })
		waitFor(t, "a few more generations", func() bool { return src.calls() > 12 })

		want := []string{"warning: a.yaml#1: first", "warning: b.yaml#1: second"}
		if lines := got.lines(); !slices.Equal(lines, want) {
			t.Errorf("reported %q, want %q", lines, want)
		}
		stopAndWait(t, done)
	})

	t.Run("keeps the last diagram while generating fails, says why, and recovers", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "pie.d2")
		src := &source{diagram: "a"}
		got := &reports{}
		done := runInBackground(t.Context(), options(file, src, fakeViewer("serve", file), got))
		waitFor(t, "the first diagram", func() bool { return contentOf(file) == "a" })

		src.set("", nil, errors.New("half-saved manifest"))
		waitFor(t, "the failure to be reported", func() bool { return got.contains("half-saved manifest") })
		if contentOf(file) != "a" {
			t.Errorf("file = %q while generating fails, want the last diagram %q", contentOf(file), "a")
		}

		src.set("c", nil, nil)
		waitFor(t, "the recovered diagram", func() bool { return contentOf(file) == "c" })
		stopAndWait(t, done)
	})

	t.Run("keeps watching when the file cannot be written, and says why", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "out")
		if err := os.Mkdir(out, 0o755); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(out, "pie.d2")
		src := &source{diagram: "a"}
		got := &reports{}
		done := runInBackground(t.Context(), options(file, src, fakeViewer("serve", file), got))
		waitFor(t, "the viewer to start", func() bool { _, err := os.Stat(file + ".viewing"); return err == nil })

		if err := os.RemoveAll(out); err != nil {
			t.Fatal(err)
		}
		src.set("b", nil, nil)
		waitFor(t, "the write failure to be reported", func() bool { return got.contains("replacing " + file) })

		if err := os.Mkdir(out, 0o755); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the diagram once the directory is back", func() bool { return contentOf(file) == "b" })
		stopAndWait(t, done)
	})

	t.Run("fails without starting the viewer when the first diagram cannot be made", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "pie.d2")
		src := &source{err: errors.New("no such directory")}

		err := waitResult(t, runInBackground(t.Context(), options(file, src, fakeViewer("serve", file), &reports{})))

		if err == nil || !strings.Contains(err.Error(), "no such directory") {
			t.Errorf("Run() = %v, want the generation error", err)
		}
	})

	t.Run("fails when the viewer cannot be started", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "pie.d2")
		missing := exec.Command(filepath.Join(t.TempDir(), "no-such-viewer"), file)

		err := waitResult(t, runInBackground(t.Context(), options(file, &source{diagram: "a"}, missing, &reports{})))

		if err == nil || !strings.Contains(err.Error(), "no-such-viewer") {
			t.Errorf("Run() = %v, want an error naming the viewer", err)
		}
	})

	t.Run("fails when the viewer exits on its own", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "pie.d2")

		err := waitResult(t, runInBackground(t.Context(), options(file, &source{diagram: "a"}, fakeViewer("crash", file), &reports{})))

		if err == nil || !strings.Contains(err.Error(), "exit status 3") {
			t.Errorf("Run() = %v, want the viewer's exit status", err)
		}
	})
}

func TestReplaceFile(t *testing.T) {
	t.Run("replaces the content and leaves no temporary file behind", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "pie.d2")
		if err := os.WriteFile(file, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}

		if err := watch.ReplaceFile(file, []byte("new")); err != nil {
			t.Fatalf("ReplaceFile() = %v", err)
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if contentOf(file) != "new" || len(entries) != 1 {
			t.Errorf("content = %q, directory holds %d entries, want %q and 1", contentOf(file), len(entries), "new")
		}
	})

	t.Run("leaves a file anyone can read, like one written by a shell", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "pie.d2")

		if err := watch.ReplaceFile(file, []byte("new")); err != nil {
			t.Fatalf("ReplaceFile() = %v", err)
		}

		if mode := stat(t, file).Mode().Perm(); mode != 0o644 {
			t.Errorf("mode = %v, want -rw-r--r--", mode)
		}
	})

	t.Run("fails and cleans up when the file cannot be replaced", func(t *testing.T) {
		dir := t.TempDir()
		occupied := filepath.Join(dir, "pie.d2")
		if err := os.MkdirAll(filepath.Join(occupied, "keep"), 0o755); err != nil {
			t.Fatal(err)
		}

		err := watch.ReplaceFile(occupied, []byte("x"))

		entries, readErr := os.ReadDir(dir)
		if err == nil || readErr != nil || len(entries) != 1 {
			t.Errorf("ReplaceFile() = %v, directory holds %d entries (%v), want an error and no temporary file", err, len(entries), readErr)
		}
	})

	t.Run("fails when the directory does not exist", func(t *testing.T) {
		if err := watch.ReplaceFile(filepath.Join(t.TempDir(), "missing", "pie.d2"), []byte("x")); err == nil {
			t.Errorf("ReplaceFile() = nil, want an error")
		}
	})
}

func options(file string, src *source, viewer *exec.Cmd, got *reports) watch.Options {
	return watch.Options{
		File:     file,
		Interval: 5 * time.Millisecond,
		Generate: src.generate,
		Viewer:   viewer,
		Report:   got.add,
	}
}

// source is the diagram generator, changed by the test while Run polls it.
type source struct {
	mu      sync.Mutex
	diagram string
	notes   []string
	err     error
	n       int
}

func (s *source) generate() ([]byte, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	if s.err != nil {
		return nil, nil, s.err
	}
	return []byte(s.diagram), s.notes, nil
}

func (s *source) set(diagram string, notes []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.diagram, s.notes, s.err = diagram, notes, err
}

func (s *source) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

type reports struct {
	mu   sync.Mutex
	seen []string
}

func (r *reports) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, line)
}

func (r *reports) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.seen)
}

func (r *reports) contains(s string) bool {
	return slices.ContainsFunc(r.lines(), func(line string) bool { return strings.Contains(line, s) })
}

func runInBackground(ctx context.Context, o watch.Options) <-chan error {
	done := make(chan error, 1)
	go func() { done <- watch.Run(ctx, o) }()
	return done
}

func stopAndWait(t *testing.T, done <-chan error) {
	t.Helper()
	// The subtests run on t.Context(), which ends when the subtest returns; Run must stop then.
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("Run did not stop after its context ended")
		}
	})
}

func waitResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("Run did not return")
		return nil
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func contentOf(file string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	return string(data)
}

func stat(t *testing.T, file string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
