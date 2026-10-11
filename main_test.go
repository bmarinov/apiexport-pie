package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
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

const usageLine = "usage: apiexport-pie [-v] [--style classic|overview] [-o FILE [--watch]] TREE\n       apiexport-pie [-v] --surface [-o FILE [--watch]] TREE\n       apiexport-pie [-v] --diff [--surface|--style overview [-o FILE]] A B"

func TestDiffReadsTreesFromGit(t *testing.T) {
	other := strings.Replace(exportManifest, "name: core.example.io", "name: other.example.io", 1)
	newRepo := func(t *testing.T) string {
		t.Helper()
		repo := gitRepo(t, map[string]string{"manifests/ws/export.yaml": exportManifest, "README.md": "not a manifest"})
		commitFiles(t, repo, "feature", map[string]string{"manifests/ws/other.yaml": other, "manifests/ws/broken.yaml": brokenExportManifest})
		git(t, repo, "checkout", "-q", "main")
		return repo
	}
	diff := func(t *testing.T, a, b string) (int, string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{"--diff", a, b}, &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
	const added = "added: APIExport other.example.io\nadded: kind widgets.core.example.io in other.example.io\nadded: notice: API group core.example.io is served by 2 APIExports: core.example.io, other.example.io\n"

	t.Run("REF:PATH reads a commit of the repository it runs in, and leaves the checkout and no temporary files", func(t *testing.T) {
		repo := newRepo(t)
		t.Chdir(repo)
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)

		code, stdout, stderr := diff(t, "main:manifests", "feature:manifests")

		if left, err := os.ReadDir(tmp); err != nil || len(left) != 0 {
			t.Errorf("after run() %s holds %v (%v), want nothing left", tmp, left, err)
		}

		if code != 0 || stdout != added || stderr != "warning: feature:manifests/ws/broken.yaml#1: "+strings.SplitN(stderr, ": ", 3)[2] {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0, stdout %q and a warning for feature:manifests/ws/broken.yaml#1", code, stdout, stderr, added)
		}
		if status := git(t, repo, "status", "--porcelain", "--branch"); status != "## main\n" {
			t.Errorf("git status after run() =\n%s\nwant the main checkout, untouched", status)
		}
	})

	t.Run("with REMOTE, a repository by path or URL is fetched from anywhere", func(t *testing.T) {
		repo := newRepo(t)
		t.Chdir(t.TempDir())

		code, stdout, stderr := diff(t, repo+"#main:manifests", "file://"+repo+"#feature:manifests")

		if code != 0 || stdout != added || !strings.HasPrefix(stderr, "warning: file://"+repo+"#feature:manifests/ws/broken.yaml#1: ") {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0, stdout %q and one warning", code, stdout, stderr, added)
		}
	})

	t.Run("REMOTE can be a remote of the repository it runs in", func(t *testing.T) {
		upstream := newRepo(t)
		local := gitRepo(t, map[string]string{"README.md": "a clone"})
		git(t, local, "remote", "add", "upstream", upstream)
		t.Chdir(local)

		code, stdout, stderr := diff(t, "upstream#main:manifests", "upstream#feature:manifests")

		if code != 0 || stdout != added {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0 and stdout %q", code, stdout, stderr, added)
		}
	})

	dir := writeTree(t, map[string]string{"ws/export.yaml": exportManifest})
	for _, arg := range []string{"feature", ":manifests", filepath.Join(dir, "ws", "export.yaml")} {
		t.Run("an argument that is no directory and no REF:PATH says so: "+filepath.Base(arg), func(t *testing.T) {
			code, stdout, stderr := diff(t, dir, arg)

			if want := "apiexport-pie: " + arg + ": not a directory, and not [REMOTE#]REF:PATH\n"; code != 1 || stdout != "" || stderr != want {
				t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 1 and stderr %q", code, stdout, stderr, want)
			}
		})
	}

	t.Run("a ref the remote does not have says what git said", func(t *testing.T) {
		repo := newRepo(t)
		t.Chdir(t.TempDir())

		code, stdout, stderr := diff(t, repo+"#main:manifests", repo+"#nope:manifests")

		said, found := strings.CutPrefix(stderr, "apiexport-pie: "+repo+"#nope:manifests: git fetch: ")
		if code != 1 || stdout != "" || !found || !strings.Contains(said, "nope") {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 1 and git's complaint about nope", code, stdout, stderr)
		}
	})

	t.Run("a ref git does not know says what git said", func(t *testing.T) {
		repo := newRepo(t)
		t.Chdir(repo)

		code, stdout, stderr := diff(t, "main:manifests", "nope:manifests")

		said, found := strings.CutPrefix(stderr, "apiexport-pie: nope:manifests: git archive: ")
		if code != 1 || stdout != "" || !found || !strings.Contains(said, "nope") {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 1 and git's complaint about nope", code, stdout, stderr)
		}
	})
}

func TestOneTreeIsReadFromGitToo(t *testing.T) {
	other := strings.Replace(exportManifest, "name: core.example.io", "name: other.example.io", 1)
	feature := map[string]string{"ws/export.yaml": exportManifest, "ws/other.yaml": other, "ws/broken.yaml": brokenExportManifest}
	newRepo := func(t *testing.T) string {
		t.Helper()
		repo := gitRepo(t, map[string]string{"manifests/ws/export.yaml": exportManifest, "README.md": "not a manifest"})
		commitFiles(t, repo, "feature", map[string]string{"manifests/ws/other.yaml": other, "manifests/ws/broken.yaml": brokenExportManifest})
		git(t, repo, "checkout", "-q", "main")
		return repo
	}
	draw := func(t *testing.T, args ...string) (int, string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), args, &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}

	t.Run("REF:PATH draws what the same files in a directory draw, in every style, and leaves no temporary files", func(t *testing.T) {
		dir := writeTree(t, feature)
		repo := newRepo(t)
		t.Chdir(repo)
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)

		for _, flags := range [][]string{nil, {"--style", "overview"}, {"--surface"}} {
			wantCode, wantOut, wantErr := draw(t, append(flags, dir)...)
			code, stdout, stderr := draw(t, append(flags, "feature:manifests")...)

			if code != wantCode || stdout != wantOut || stderr != wantErr {
				t.Errorf("run(%v feature:manifests) = %d\nstdout:\n%s\nstderr:\n%s\nwant what the directory gives: %d\nstdout:\n%s\nstderr:\n%s", flags, code, stdout, stderr, wantCode, wantOut, wantErr)
			}
		}
		if left, err := os.ReadDir(tmp); err != nil || len(left) != 0 {
			t.Errorf("after run() %s holds %v (%v), want nothing left", tmp, left, err)
		}
	})

	t.Run("with REMOTE, it is fetched from a repository by path or URL", func(t *testing.T) {
		dir := writeTree(t, feature)
		repo := newRepo(t)
		t.Chdir(t.TempDir())

		_, wantOut, _ := draw(t, "--style", "overview", dir)
		code, stdout, stderr := draw(t, "--style", "overview", "file://"+repo+"#feature:manifests")

		if code != 0 || stdout != wantOut {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0 and what the directory gives:\n%s", code, stdout, stderr, wantOut)
		}
	})

	t.Run("a ref that is not there exits 1 naming the tree", func(t *testing.T) {
		repo := newRepo(t)
		t.Chdir(repo)

		code, stdout, stderr := draw(t, "--style", "overview", "nothing:manifests")

		if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "apiexport-pie: nothing:manifests: ") {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 1 and an error naming nothing:manifests", code, stdout, stderr)
		}
	})
}

func TestUnpackWritesOnlyRegularFilesInsideTheTree(t *testing.T) {
	archiveOf := func(t *testing.T, headers ...tar.Header) *bytes.Buffer {
		t.Helper()
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, h := range headers {
			body := ""
			if h.Typeflag == tar.TypeReg {
				body = "kind: x\n"
				h.Size = int64(len(body))
			}
			if err := tw.WriteHeader(&h); err != nil {
				t.Fatalf("writing header %s: %v", h.Name, err)
			}
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatalf("writing %s: %v", h.Name, err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatalf("closing the archive: %v", err)
		}
		return &buf
	}

	t.Run("files land under the tree, links are left out", func(t *testing.T) {
		dir := t.TempDir()
		err := unpack(archiveOf(t,
			tar.Header{Name: "ws/export.yaml", Typeflag: tar.TypeReg, Mode: 0o644},
			tar.Header{Name: "ws/link.yaml", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		), dir)

		got, readErr := os.ReadFile(filepath.Join(dir, "ws", "export.yaml"))
		_, linkErr := os.Lstat(filepath.Join(dir, "ws", "link.yaml"))
		if err != nil || readErr != nil || string(got) != "kind: x\n" || !errors.Is(linkErr, os.ErrNotExist) {
			t.Errorf("unpack() = %v; export.yaml %q (%v); link.yaml %v; want the file and no link", err, got, readErr, linkErr)
		}
	})

	t.Run("an entry outside the tree stops it", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "tree")

		err := unpack(archiveOf(t, tar.Header{Name: "../escaped.yaml", Typeflag: tar.TypeReg, Mode: 0o644}), dir)

		_, statErr := os.Stat(filepath.Join(root, "escaped.yaml"))
		if err == nil || !strings.Contains(err.Error(), "../escaped.yaml is outside the tree") || !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("unpack() = %v, escaped.yaml %v; want an error and nothing written outside", err, statErr)
		}
	})
}

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

	t.Run("with --style overview prints the overview of the manifests in DIR", func(t *testing.T) {
		dir := writeTree(t, map[string]string{"ws/export.yaml": exportManifest, "ws/widgets.yaml": schemaManifest})
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"--style", "overview", dir}, &stdout, &stderr)

		want := "\"apiexports\": {\n  label: \"APIEXPORTS\"\n"
		if code != 0 || !strings.Contains(stdout.String(), want) || stderr.Len() != 0 {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0 and stdout containing:\n%s", code, stdout.String(), stderr.String(), want)
		}
	})

	t.Run("with --style classic prints what it prints without --style", func(t *testing.T) {
		dir := writeTree(t, map[string]string{"ws/export.yaml": exportManifest, "ws/widgets.yaml": schemaManifest})
		var classic, plain, stderr bytes.Buffer

		styled := run(context.Background(), []string{"--style", "classic", dir}, &classic, &stderr)
		unstyled := run(context.Background(), []string{dir}, &plain, &stderr)

		if styled != 0 || unstyled != 0 || classic.String() != plain.String() || stderr.Len() != 0 {
			t.Errorf("run() = %d and %d\nwith --style classic:\n%s\nwithout:\n%s\nstderr:\n%s\nwant 0, 0 and the same diagram", styled, unstyled, classic.String(), plain.String(), stderr.String())
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

	t.Run("warns on stderr about exports a consumer binds that kcp refuses together", func(t *testing.T) {
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

		want := "warning: types/account.yaml#1: WorkspaceType account binds core.example.io and other.example.io, which both serve widgets.core.example.io; kcp binds only one of them\n" +
			"notice: ws/other.yaml#1: API group core.example.io is served by 2 APIExports: core.example.io, other.example.io\n"
		if code != 0 || stderr.String() != want || !strings.Contains(stdout.String(), `"workspacetype:types/account.yaml#1"`) {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0, the diagram, and stderr:\n%s", code, stdout.String(), stderr.String(), want)
		}
	})

	t.Run("points out on stderr what is worth a look, and still succeeds", func(t *testing.T) {
		dir := writeTree(t, map[string]string{
			"ws/export.yaml": exportManifest,
			"types/security.yaml": `apiVersion: tenancy.kcp.io/v1alpha1
kind: WorkspaceType
metadata:
  name: security
spec: {}
`,
		})
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{dir}, &stdout, &stderr)

		want := "notice: types/security.yaml#1: WorkspaceType security binds no APIExport\n" +
			"notice: ws/export.yaml#1: APIExport core.example.io is bound by no WorkspaceType or APIBinding in the tree\n"
		if code != 0 || stderr.String() != want {
			t.Errorf("run() = %d\nstderr:\n%s\nwant 0 and stderr:\n%s", code, stderr.String(), want)
		}
	})

	t.Run("with --diff prints what changed from A to B, and warnings of each with its tree", func(t *testing.T) {
		a := writeTree(t, map[string]string{"ws/export.yaml": exportManifest})
		b := writeTree(t, map[string]string{
			"ws/other.yaml":  strings.Replace(exportManifest, "name: core.example.io", "name: other.example.io", 1),
			"ws/broken.yaml": brokenExportManifest,
			"types/security.yaml": `apiVersion: tenancy.kcp.io/v1alpha1
kind: WorkspaceType
metadata:
  name: security
spec: {}
`,
		})
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"--diff", a, b + "/"}, &stdout, &stderr)

		want := "removed: APIExport core.example.io\nadded: APIExport other.example.io\nmoved: kind widgets.core.example.io from core.example.io to other.example.io\nadded: WorkspaceType security\nadded: notice: WorkspaceType security binds no APIExport\nadded: notice: APIExport other.example.io is bound by no WorkspaceType or APIBinding in the tree\n"
		if code != 0 || stdout.String() != want || !strings.HasPrefix(stderr.String(), "warning: "+b+"/ws/broken.yaml#1: ") || strings.Count(stderr.String(), "\n") != 1 {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0, stdout:\n%s\nand one warning for %s/ws/broken.yaml#1", code, stdout.String(), stderr.String(), want, b)
		}
	})

	t.Run("with --diff --surface prints both tables as one in d2, or writes it to FILE", func(t *testing.T) {
		a := writeTree(t, map[string]string{"ws/export.yaml": exportManifest})
		b := writeTree(t, map[string]string{"ws/other.yaml": strings.Replace(exportManifest, "name: core.example.io", "name: other.example.io", 1)})
		file := filepath.Join(t.TempDir(), "diff.d2")
		var stdout, toFile, stderr bytes.Buffer

		code := run(context.Background(), []string{"--diff", "--surface", a, b}, &stdout, &stderr)
		fileCode := run(context.Background(), []string{"--diff", "--surface", "-o", file, a, b}, &toFile, &stderr)

		written, err := os.ReadFile(file)
		const moved = "widgets · core.example.io · not in tree · moved from core.example.io"
		if code != 0 || !strings.HasPrefix(stdout.String(), "grid-rows: ") || !strings.Contains(stdout.String(), moved) || stderr.Len() != 0 {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0 and a d2 table with %q", code, stdout.String(), stderr.String(), moved)
		}
		if fileCode != 0 || err != nil || string(written) != stdout.String() || toFile.Len() != 0 {
			t.Errorf("run() with -o = %d, %s holds\n%s\n(%v), stdout %q; want the same table in the file and nothing on stdout", fileCode, file, written, err, toFile.String())
		}
	})

	t.Run("with --diff --style overview prints both trees as one overview in d2, or writes it to FILE", func(t *testing.T) {
		a := writeTree(t, map[string]string{"ws/export.yaml": exportManifest})
		b := writeTree(t, map[string]string{"ws/export.yaml": exportManifest, "ws/other.yaml": strings.Replace(exportManifest, "name: core.example.io", "name: other.example.io", 1)})
		file := filepath.Join(t.TempDir(), "diff.d2")
		var stdout, toFile, stderr bytes.Buffer

		code := run(context.Background(), []string{"--diff", "--style", "overview", a, b}, &stdout, &stderr)
		fileCode := run(context.Background(), []string{"--diff", "--style", "overview", "-o", file, a, b}, &toFile, &stderr)

		written, err := os.ReadFile(file)
		const added = `"apiexport:other.example.io": "other.example.io" {class: export; style.stroke: "#1098ad"; style.fill: "#e3fafc"}`
		if code != 0 || !strings.HasPrefix(stdout.String(), "vars: {") || !strings.Contains(stdout.String(), added) || stderr.Len() != 0 {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0 and an overview with %q", code, stdout.String(), stderr.String(), added)
		}
		if fileCode != 0 || err != nil || string(written) != stdout.String() || toFile.Len() != 0 {
			t.Errorf("run() with -o = %d, %s holds\n%s\n(%v), stdout %q; want the same overview in the file and nothing on stdout", fileCode, file, written, err, toFile.String())
		}
	})

	t.Run("with --diff and -v also reports the documents each tree skipped, still no notices", func(t *testing.T) {
		a := writeTree(t, map[string]string{
			"ws/export.yaml":   exportManifest,
			"ws/settings.yaml": configMapManifest,
			"types/security.yaml": `apiVersion: tenancy.kcp.io/v1alpha1
kind: WorkspaceType
metadata:
  name: security
spec: {}
`,
		})
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"--diff", "-v", a, a}, &stdout, &stderr)

		want := "skipped: " + a + "/ws/settings.yaml#1: v1 ConfigMap is not drawn\n"
		if code != 0 || stdout.Len() != 0 || stderr.String() != want+want {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 0, no changes, and twice:\n%s", code, stdout.String(), stderr.String(), want)
		}
	})

	t.Run("with --diff and a tree that is not there says which and exits 1", func(t *testing.T) {
		a := writeTree(t, map[string]string{"ws/export.yaml": exportManifest})
		missing := filepath.Join(t.TempDir(), "missing")
		var stdout, stderr bytes.Buffer

		code := run(context.Background(), []string{"--diff", a, missing}, &stdout, &stderr)

		if code != 1 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "apiexport-pie: "+missing+": ") {
			t.Errorf("run() = %d\nstdout:\n%s\nstderr:\n%s\nwant 1 and an error naming %s", code, stdout.String(), stderr.String(), missing)
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

	t.Run("with --surface prints which consumer gets which kinds as a markdown table", func(t *testing.T) {
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
		{name: "without TREE", args: nil, reason: "want one TREE, got 0"},
		{name: "with more than one TREE", args: []string{"a", "b"}, reason: "want one TREE, got 2"},
		{name: "with an unknown flag", args: []string{"-x", "a"}, reason: "flag provided but not defined: -x"},
		{name: "with --watch but no -o", args: []string{"--watch", "a"}, reason: "--watch needs -o FILE, the file d2 shows"},
		{name: "with --surface and --watch on a markdown FILE", args: []string{"--surface", "-o", "s.md", "--watch", "a"},
			reason: "--watch shows FILE in d2, so FILE must end in .d2"},
		{name: "with --watch on an image FILE", args: []string{"-o", "pie.svg", "--watch", "a"},
			reason: "--watch shows FILE in d2, so FILE must end in .d2"},
		{name: "with --diff and one tree", args: []string{"--diff", "a"}, reason: "--diff wants A and B, got 1"},
		{name: "with --diff and -o but no --surface", args: []string{"--diff", "-o", "pie.d2", "a", "b"},
			reason: "--diff writes FILE only with --surface or --style overview; the list of changes goes to stdout"},
		{name: "with --watch on a tree that is no directory", args: []string{"-o", "pie.d2", "--watch", "main:manifests"},
			reason: "--watch follows a directory on disk; main:manifests is not one"},
		{name: "with --watch on a file", args: []string{"-o", "pie.d2", "--watch", "go.mod"},
			reason: "--watch follows a directory on disk; go.mod is not one"},
		{name: "with --diff and --watch", args: []string{"--diff", "--surface", "-o", "pie.d2", "--watch", "a", "b"},
			reason: "--watch shows one tree; it does not go with --diff"},
		{name: "with a --style there is none of", args: []string{"--style", "pie", "a"},
			reason: `--style is classic or overview, got "pie"`},
		{name: "with --style and --surface", args: []string{"--style", "overview", "--surface", "a"},
			reason: "--style picks how the diagram is drawn; it does not go with --surface"},
		{name: "with --style classic and --diff", args: []string{"--style", "classic", "--diff", "a", "b"},
			reason: "--diff is drawn as the overview only: --style overview"},
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

	t.Run("exits 1 naming TREE when it cannot be read", func(t *testing.T) {
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
	writeFiles(t, root, files)
	return root
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating fixture dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing fixture: %v", err)
		}
	}
}

// gitRepo makes a repository whose main branch has one commit of files, and returns its directory.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git on PATH")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	commitFiles(t, repo, "main", files)
	return repo
}

// commitFiles commits files on branch, which starts from the current one if it is new.
func commitFiles(t *testing.T, repo, branch string, files map[string]string) {
	t.Helper()
	git(t, repo, "checkout", "-q", "-B", branch)
	writeFiles(t, repo, files)
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "manifests")
}

// git runs git in dir, away from the user's own git configuration.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
