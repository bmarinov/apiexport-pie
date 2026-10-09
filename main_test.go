package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const exportManifest = `apiVersion: apis.kcp.io/v1alpha2
kind: APIExport
metadata:
  name: core.example.io
spec:
  resources:
  - group: core.example.io
    name: widgets
    schema: v1.widgets.core.example.io
    storage:
      crd: {}
`

const schemaManifest = `apiVersion: apis.kcp.io/v1alpha1
kind: APIResourceSchema
metadata:
  name: v1.widgets.core.example.io
spec:
  group: core.example.io
  names:
    kind: Widget
    plural: widgets
  scope: Cluster
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      type: object
`

const configMapManifest = `apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
`

const brokenExportManifest = `apiVersion: apis.kcp.io/v1alpha2
kind: APIExport
metadata:
  name: broken.example.io
spec: not-a-map
`

const usageLine = "usage: apiexport-pie [-v] [-o FILE [--watch]] DIR\n       apiexport-pie [-v] --surface [-o FILE [--watch]] DIR"

func TestRun(t *testing.T) {
	t.Run("prints the diagram of the manifests in DIR and nothing on stderr", func(t *testing.T) {
		dir := writeTree(t, map[string]string{
			"ws/export.yaml":   exportManifest,
			"ws/widgets.yaml":  schemaManifest,
			"ws/settings.yaml": configMapManifest,
		})
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{dir}, &stdout, &stderr)

		want := `vars: {
  d2-config: {
    layout-engine: elk
  }
}
"apiexport:ws/export.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io"
  "Widget": "widgets.core.example.io" {constraint: "v1"}
}
`
		if code != 0 || stdout.String() != want || stderr.Len() != 0 {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0 and stdout:\n%s", code, stdout.String(), stderr.String(), want)
		}
	})

	t.Run("warns on stderr about what it could not draw and still draws the rest", func(t *testing.T) {
		dir := writeTree(t, map[string]string{
			"ws/broken.yaml": brokenExportManifest,
			"ws/export.yaml": exportManifest,
		})
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{dir}, &stdout, &stderr)

		if code != 0 || !strings.Contains(stdout.String(), `"apiexport:ws/export.yaml#1"`) ||
			!strings.HasPrefix(stderr.String(), "warning: ws/broken.yaml#1: ") || strings.Count(stderr.String(), "\n") != 1 {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0, the core export drawn, one warning for ws/broken.yaml#1", code, stdout.String(), stderr.String())
		}
	})

	t.Run("warns on stderr about exports a use case binds that kcp refuses together", func(t *testing.T) {
		dir := writeTree(t, map[string]string{
			"ws/export.yaml":  exportManifest,
			"ws/other.yaml":   strings.Replace(exportManifest, "name: core.example.io", "name: other.example.io", 1),
			"ws/widgets.yaml": schemaManifest,
			"types/account.yaml": `apiVersion: tenancy.kcp.io/v1alpha1
kind: WorkspaceType
metadata:
  name: account
spec:
  defaultAPIBindings:
  - export: core.example.io
    path: root:pms
  - export: other.example.io
    path: root:pms
`,
		})
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{dir}, &stdout, &stderr)

		want := "warning: types/account.yaml#1: WorkspaceType account binds core.example.io and other.example.io, which both serve widgets.core.example.io; kcp binds only one of them\n"
		if code != 0 || stderr.String() != want || !strings.Contains(stdout.String(), `"workspacetype:types/account.yaml#1"`) {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0, the diagram, and stderr:\n%s", code, stdout.String(), stderr.String(), want)
		}
	})

	t.Run("with -v also reports the documents it skipped", func(t *testing.T) {
		dir := writeTree(t, map[string]string{
			"ws/export.yaml":   exportManifest,
			"ws/settings.yaml": configMapManifest,
		})
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"-v", dir}, &stdout, &stderr)

		if code != 0 || stderr.String() != "skipped: ws/settings.yaml#1: v1 ConfigMap is not drawn\n" {
			t.Errorf("run() = %d\nstderr:\n%s\nwant 0 and one skipped line for ws/settings.yaml#1", code, stderr.String())
		}
	})

	t.Run("with --surface prints which use case gets which kinds as a markdown table", func(t *testing.T) {
		dir := writeTree(t, map[string]string{
			"ws/export.yaml":  exportManifest,
			"ws/widgets.yaml": schemaManifest,
			"types/account.yaml": `apiVersion: tenancy.kcp.io/v1alpha1
kind: WorkspaceType
metadata:
  name: account
spec:
  defaultAPIBindings:
  - export: core.example.io
    path: root:pms
`,
		})
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"--surface", dir}, &stdout, &stderr)

		want := `| APIExport | kind | account |
|---|---|:-:|
| **core.example.io** | Widget | ● |
`
		if code != 0 || stdout.String() != want || stderr.Len() != 0 {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0 and stdout:\n%s", code, stdout.String(), stderr.String(), want)
		}
	})

	t.Run("with --surface and -o FILE.d2 writes the table as d2, for d2 to draw", func(t *testing.T) {
		dir := writeTree(t, map[string]string{"ws/export.yaml": exportManifest, "ws/widgets.yaml": schemaManifest})
		file := filepath.Join(t.TempDir(), "surface.d2")
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"--surface", "-o", file, dir}, &stdout, &stderr)

		written, err := os.ReadFile(file)
		if code != 0 || err != nil || stdout.Len() != 0 || !strings.HasPrefix(string(written), "grid-rows: ") ||
			!strings.Contains(string(written), `"Widget"`) {
			t.Errorf("run() = %d, read error %v\nstdout:\n%s\nstderr:\n%s\nFILE:\n%s\nwant 0 and the table as d2 in FILE", code, err, stdout.String(), stderr.String(), written)
		}
	})

	t.Run("with --surface and -o FILE that is not .d2 writes the markdown table", func(t *testing.T) {
		dir := writeTree(t, map[string]string{"ws/export.yaml": exportManifest, "ws/widgets.yaml": schemaManifest})
		file := filepath.Join(t.TempDir(), "surface.md")
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"--surface", "-o", file, dir}, &stdout, &stderr)

		written, err := os.ReadFile(file)
		if code != 0 || err != nil || !strings.HasPrefix(string(written), "| APIExport | kind |") {
			t.Errorf("run() = %d, read error %v\nstderr:\n%s\nFILE:\n%s\nwant 0 and markdown in FILE", code, err, stderr.String(), written)
		}
	})

	t.Run("with --surface, -o FILE.d2 and --watch goes on to show FILE in d2", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		dir := writeTree(t, map[string]string{"ws/export.yaml": exportManifest})
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"--surface", "-o", filepath.Join(t.TempDir(), "surface.d2"), "--watch", dir}, &stdout, &stderr)

		if code != 1 || !strings.Contains(stderr.String(), "--watch needs d2 on PATH") {
			t.Errorf("run() = %d\nstderr:\n%s\nwant 1 and a missing d2, not a usage error", code, stderr.String())
		}
	})

	t.Run("with -h prints usage on stdout and exits 0", func(t *testing.T) {
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"-h"}, &stdout, &stderr)

		if code != 0 || !strings.Contains(stdout.String(), usageLine) || stderr.Len() != 0 {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0 and usage on stdout", code, stdout.String(), stderr.String())
		}
	})

	usageErrors := []struct {
		name   string
		args   []string
		reason string
	}{
		{name: "without DIR", args: nil, reason: "want one DIR, got 0"},
		{name: "with more than one DIR", args: []string{"a", "b"}, reason: "want one DIR, got 2"},
		{name: "with an unknown flag", args: []string{"-x", "a"}, reason: "flag provided but not defined: -x"},
		{name: "with --watch but no -o", args: []string{"--watch", "a"}, reason: "--watch needs -o FILE, the file d2 shows"},
		{name: "with --surface and --watch on a markdown FILE", args: []string{"--surface", "-o", "s.md", "--watch", "a"},
			reason: "--watch shows FILE in d2, so FILE must end in .d2"},
		{name: "with --watch on an image FILE", args: []string{"-o", "pie.svg", "--watch", "a"},
			reason: "--watch shows FILE in d2, so FILE must end in .d2"},
	}
	for _, tt := range usageErrors {
		t.Run("says why, prints usage and exits 2 "+tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(context.Background(), tt.args, &stdout, &stderr)

			want := "apiexport-pie: " + tt.reason + "\n" + usageLine + "\n"
			if code != 2 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), want) {
				t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 2 and stderr starting with:\n%s", code, stdout.String(), stderr.String(), want)
			}
		})
	}

	t.Run("with -o writes the diagram to FILE and nothing to stdout", func(t *testing.T) {
		dir := writeTree(t, map[string]string{"ws/export.yaml": exportManifest})
		file := filepath.Join(t.TempDir(), "pie.d2")
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"-o", file, dir}, &stdout, &stderr)

		written, err := os.ReadFile(file)
		if code != 0 || err != nil || !strings.Contains(string(written), `"apiexport:ws/export.yaml#1"`) || stdout.Len() != 0 {
			t.Errorf("run() = %d, file %q (%v), stdout %q, stderr %q; want 0, the diagram in the file, empty stdout",
				code, written, err, stdout.String(), stderr.String())
		}
	})

	t.Run("exits 1 when -o names a directory that does not exist", func(t *testing.T) {
		dir := writeTree(t, map[string]string{"ws/export.yaml": exportManifest})
		file := filepath.Join(t.TempDir(), "missing", "pie.d2")
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"-o", file, dir}, &stdout, &stderr)

		if code != 1 || !strings.Contains(stderr.String(), file) {
			t.Errorf("run() = %d\nstderr:\n%s\nwant 1 and an error naming %s", code, stderr.String(), file)
		}
	})

	t.Run("with --watch exits 1 explaining that d2 is needed when it is not on PATH", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		dir := writeTree(t, map[string]string{"ws/export.yaml": exportManifest})
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"-o", filepath.Join(t.TempDir(), "pie.d2"), "--watch", dir}, &stdout, &stderr)

		if code != 1 || !strings.Contains(stderr.String(), "--watch needs d2 on PATH") {
			t.Errorf("run() = %d\nstderr:\n%s\nwant 1 and an explanation", code, stderr.String())
		}
	})

	t.Run("with -o FILE.svg hands the diagram to d2, which draws FILE", func(t *testing.T) {
		fakeD2(t, `[ "$1" = "-" ] || exit 9
{ echo "drawn by d2"; cat; } > "$2"`)
		dir := writeTree(t, map[string]string{"ws/export.yaml": exportManifest})
		file := filepath.Join(t.TempDir(), "pie.svg")
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"-o", file, dir}, &stdout, &stderr)

		drawn, err := os.ReadFile(file)
		if code != 0 || err != nil || stdout.Len() != 0 || stderr.Len() != 0 ||
			!strings.HasPrefix(string(drawn), "drawn by d2\nvars: {") || !strings.Contains(string(drawn), `"apiexport:ws/export.yaml#1"`) {
			t.Errorf("run() = %d, read error %v\nstdout:\n%s\nstderr:\n%s\nd2 got:\n%s\nwant 0 and the diagram piped to d2", code, err, stdout.String(), stderr.String(), drawn)
		}
	})

	t.Run("with --surface and -o FILE.png hands the table as d2 to d2", func(t *testing.T) {
		fakeD2(t, `{ echo "drawn by d2"; cat; } > "$2"`)
		dir := writeTree(t, map[string]string{"ws/export.yaml": exportManifest})
		file := filepath.Join(t.TempDir(), "surface.png")
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"--surface", "-o", file, dir}, &stdout, &stderr)

		drawn, err := os.ReadFile(file)
		if code != 0 || err != nil || !strings.HasPrefix(string(drawn), "drawn by d2\ngrid-rows: ") {
			t.Errorf("run() = %d, read error %v\nstderr:\n%s\nd2 got:\n%s\nwant 0 and the table as d2", code, err, stderr.String(), drawn)
		}
	})

	t.Run("exits 1 with d2's own words when d2 cannot draw FILE", func(t *testing.T) {
		fakeD2(t, `echo "err: no browser for png" >&2
exit 1`)
		dir := writeTree(t, map[string]string{"ws/export.yaml": exportManifest})
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"-o", filepath.Join(t.TempDir(), "pie.png"), dir}, &stdout, &stderr)

		if code != 1 || !strings.Contains(stderr.String(), "err: no browser for png") {
			t.Errorf("run() = %d\nstderr:\n%s\nwant 1 and d2's message", code, stderr.String())
		}
	})

	t.Run("with -o FILE.pdf exits 1 explaining that d2 is needed when it is not on PATH", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		dir := writeTree(t, map[string]string{"ws/export.yaml": exportManifest})
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"-o", filepath.Join(t.TempDir(), "pie.pdf"), dir}, &stdout, &stderr)

		if code != 1 || !strings.Contains(stderr.String(), "pie.pdf needs d2 on PATH") {
			t.Errorf("run() = %d\nstderr:\n%s\nwant 1 and an explanation", code, stderr.String())
		}
	})

	t.Run("exits 1 naming DIR when it cannot be read", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{dir}, &stdout, &stderr)

		if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), dir) {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 1 and an error naming %s", code, stdout.String(), stderr.String(), dir)
		}
	})

	t.Run("exits 1 when the diagram cannot be written", func(t *testing.T) {
		dir := writeTree(t, map[string]string{"export.yaml": exportManifest})
		var stderr bytes.Buffer

		code := run(context.Background(), []string{dir}, failingWriter{}, &stderr)

		if code != 1 || !strings.Contains(stderr.String(), "disk full") {
			t.Errorf("run() = %d\nstderr:\n%s\nwant 1 and the write error", code, stderr.String())
		}
	})
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// fakeD2 puts a d2 on PATH that runs script with d2's arguments, ahead of any real one.
func fakeD2(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "d2"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating fixture dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing fixture: %v", err)
		}
	}
	return root
}
