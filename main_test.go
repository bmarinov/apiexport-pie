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

const usageLine = "usage: apiexport-pie [-v] [-o FILE [--watch]] DIR"

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

	t.Run("with -h prints usage on stdout and exits 0", func(t *testing.T) {
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"-h"}, &stdout, &stderr)

		if code != 0 || !strings.Contains(stdout.String(), usageLine) || stderr.Len() != 0 {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0 and usage on stdout", code, stdout.String(), stderr.String())
		}
	})

	usageErrors := []struct {
		name string
		args []string
	}{
		{name: "without DIR", args: nil},
		{name: "with more than one DIR", args: []string{"a", "b"}},
		{name: "with an unknown flag", args: []string{"-x", "a"}},
		{name: "with --watch but no -o", args: []string{"--watch", "a"}},
	}
	for _, tt := range usageErrors {
		t.Run("prints usage and exits 2 "+tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(context.Background(), tt.args, &stdout, &stderr)

			if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), usageLine) {
				t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 2 and usage on stderr", code, stdout.String(), stderr.String())
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
