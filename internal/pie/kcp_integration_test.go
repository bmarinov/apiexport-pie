//go:build integration

package pie

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// batteryAnnotation marks what kcp bootstraps only with that battery; without it, every
// installation has the object.
const batteryAnnotation = "bootstrap.kcp.io/battery"

func TestUniversalIsTheOneKcpCreatesInEveryInstallation(t *testing.T) {
	version := kcpVersion(t)
	root := filepath.Join(kcpModuleDir(t, version), "config", "root")
	m, _, err := Scan(os.DirFS(root))
	if err != nil {
		t.Fatalf("scanning kcp %s config/root: %v", version, err)
	}

	scanned, ok := typeNamed(universalRef.Name, m.WorkspaceTypes)
	if !ok {
		t.Fatalf("kcp %s: config/root has no WorkspaceType universal", version)
	}
	if got := batteryOf(t, filepath.Join(root, scanned.Path)); got != "" {
		t.Errorf("kcp %s: universal is created only with battery %q, not in every installation", version, got)
	}
	scanned.Path, scanned.Doc = "", 0
	if want := kcpUniversal(); !reflect.DeepEqual(scanned, want) {
		t.Errorf("kcp %s: universal is\n%+v\nwant\n%+v", version, scanned, want)
	}

	// Without this, a renamed annotation would let the check above pass on any universal.
	organization, ok := typeNamed("organization", m.WorkspaceTypes)
	if !ok {
		t.Fatalf("kcp %s: config/root has no WorkspaceType organization", version)
	}
	if got := batteryOf(t, filepath.Join(root, organization.Path)); got == "" {
		t.Errorf("kcp %s: organization has no %s annotation, so its absence on universal says nothing", version, batteryAnnotation)
	}
}

func TestKcpExportsAreTheOnesKcpCreatesInRootInEveryInstallation(t *testing.T) {
	version := kcpVersion(t)
	phase0 := filepath.Join(kcpModuleDir(t, version), "config", "root-phase0")
	m, _, err := Scan(os.DirFS(phase0))
	if err != nil {
		t.Fatalf("scanning kcp %s config/root-phase0: %v", version, err)
	}

	var names []string
	for _, e := range m.Exports {
		if got := batteryOf(t, filepath.Join(phase0, e.Path)); got != "" {
			t.Errorf("kcp %s: %s is created only with battery %q, not in every installation", version, e.Name, got)
		}
		names = append(names, e.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, kcpExports) {
		t.Errorf("kcp %s: config/root-phase0 has APIExports %v, want %v", version, names, kcpExports)
	}
}

// kcpVersion is the version of kcp the tool follows: that of its SDK in go.mod.
func kcpVersion(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(goRun(t, ".", "list", "-m", "-f", "{{.Version}}", "github.com/kcp-dev/sdk"))
}

// kcpModuleDir downloads kcp's module at version, or finds it in the module cache. It runs outside
// this module so go.mod and go.sum stay as they are: kcp's module cannot be required, its replace
// directives do not carry over.
func kcpModuleDir(t *testing.T, version string) string {
	t.Helper()
	var module struct{ Dir, Error string }
	out := goRun(t, t.TempDir(), "mod", "download", "-json", "github.com/kcp-dev/kcp@"+version)
	if err := json.Unmarshal([]byte(out), &module); err != nil {
		t.Fatalf("reading go mod download: %v\n%s", err, out)
	}
	if module.Error != "" || module.Dir == "" {
		t.Fatalf("go mod download github.com/kcp-dev/kcp@%s: %s", version, module.Error)
	}
	return module.Dir
}

func batteryOf(t *testing.T, file string) string {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading %s: %v", file, err)
	}
	var doc struct {
		Metadata metav1.ObjectMeta `json:"metadata"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("reading %s: %v", file, err)
	}
	return doc.Metadata.Annotations[batteryAnnotation]
}

// goRun runs the go command in dir and returns its stdout. go mod download reports a failure in its
// JSON as well as by its exit code, so stdout is returned either way for the caller to read.
func goRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return string(out)
}
