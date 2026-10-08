//go:build integration

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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

			if err := cmd.Run(); err != nil || stderr.Len() != 0 {
				t.Fatalf("apiexport-pie %s: %v\nstderr:\n%s", dir, err, stderr.String())
			}
			renderWithD2(t, stdout.Bytes())
		})
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
