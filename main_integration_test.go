//go:build integration

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// smokeDirsEnv lists real manifest trees to check, separated like PATH. Kept out of the repo so the
// tool stays project agnostic.
const smokeDirsEnv = "APIEXPORT_PIE_SMOKE_DIRS"

func TestDiagramRendersWithD2(t *testing.T) {
	bin := buildBinary(t)
	dir := writeTree(t, map[string]string{
		"ws/export.yaml":  exportManifest,
		"ws/widgets.yaml": schemaManifest,
	})

	diagram, err := exec.CommandContext(t.Context(), bin, dir).Output()
	if err != nil {
		t.Fatalf("apiexport-pie %s: %v", dir, err)
	}
	renderWithD2(t, diagram)
}

func TestOutputToSVGIsDrawnByD2(t *testing.T) {
	bin := buildBinary(t)
	dir := writeTree(t, map[string]string{"ws/export.yaml": exportManifest, "ws/widgets.yaml": schemaManifest})
	for _, args := range [][]string{{"-o"}, {"--surface", "-o"}} {
		file := filepath.Join(t.TempDir(), "out.svg")
		if out, err := exec.CommandContext(t.Context(), bin, append(args, file, dir)...).CombinedOutput(); err != nil {
			t.Fatalf("apiexport-pie %v: %v\n%s", args, err, out)
		}
		if svg, err := os.ReadFile(file); err != nil || !bytes.Contains(svg, []byte("<svg")) {
			t.Errorf("apiexport-pie %v %s: want an SVG, read error %v", args, file, err)
		}
	}
}

func TestRealTreesDrawWithoutWarnings(t *testing.T) {
	dirs := os.Getenv(smokeDirsEnv)
	if dirs == "" {
		t.Skipf("set %s to check real manifest trees", smokeDirsEnv)
	}
	bin := buildBinary(t)

	for _, dir := range filepath.SplitList(dirs) {
		t.Run(dir, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			cmd := exec.CommandContext(t.Context(), bin, dir)
			cmd.Stdout, cmd.Stderr = &stdout, &stderr

			if err := cmd.Run(); err != nil || strings.Contains(stderr.String(), "warning: ") {
				t.Fatalf("apiexport-pie %s: %v\nstderr:\n%s", dir, err, stderr.String())
			}
			renderWithD2(t, stdout.Bytes())
		})
	}
}

func TestWatchKeepsTheDiagramInStepWithTheManifests(t *testing.T) {
	t.Setenv("BROWSER", "0")
	dir := writeTree(t, map[string]string{"ws/export.yaml": exportManifest})
	out := t.TempDir()
	file, svg := filepath.Join(out, "pie.d2"), filepath.Join(out, "pie.svg")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stderr := &lockedBuffer{}
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"-o", file, "--watch", dir}, io.Discard, stderr) }()

	waitUntil(t, "d2 to render the first diagram", func() bool { return fileContains(svg, "core.example.io") })
	other := strings.ReplaceAll(exportManifest, "core.example.io", "other.example.io")
	if err := os.WriteFile(filepath.Join(dir, "ws", "other.yaml"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the edit to reach the diagram", func() bool { return fileContains(file, "other.example.io") })
	waitUntil(t, "d2 to render the edit", func() bool { return fileContains(svg, "other.example.io") })

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("run() = %d after interrupt, want 0\nstderr:\n%s", code, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("run did not stop after interrupt\nstderr:\n%s", stderr.String())
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func fileContains(file, s string) bool {
	data, err := os.ReadFile(file)
	return err == nil && bytes.Contains(data, []byte(s))
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "apiexport-pie")
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("building apiexport-pie: %v\n%s", err, out)
	}
	return bin
}

func renderWithD2(t *testing.T, diagram []byte) {
	t.Helper()
	d2, err := exec.LookPath("d2")
	if err != nil {
		t.Fatalf("integration tests need d2 on PATH: %v", err)
	}
	render := exec.CommandContext(t.Context(), d2, "-", filepath.Join(t.TempDir(), "pie.svg"))
	render.Stdin = bytes.NewReader(diagram)
	if out, err := render.CombinedOutput(); err != nil {
		t.Errorf("d2 rejected the diagram: %v\n%s\ndiagram:\n%s", err, out, diagram)
	}
}
