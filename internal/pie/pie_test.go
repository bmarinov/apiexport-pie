package pie_test

import (
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	apisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	tenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/bmarinov/apiexport-pie/internal/pie"
)

var (
	widgets = resource("core.example.io", "widgets", "v1.widgets.core.example.io")
	gadgets = resource("core.example.io", "gadgets", "v1.gadgets.core.example.io")
)

func coreExportAt(path string) pie.Export {
	return pie.Export{Path: path, Doc: 1, Name: "core.example.io", Resources: []pie.Resource{
		{Group: "core.example.io", Name: "widgets", Schema: "v1.widgets.core.example.io"},
		{Group: "core.example.io", Name: "gadgets", Schema: "v1.gadgets.core.example.io"},
	}}
}

func widgetSchemaAt(path string) pie.Schema {
	return pie.Schema{Path: path, Name: "v1.widgets.core.example.io", Group: "core.example.io", Kind: "Widget", Plural: "widgets"}
}

func gadgetSchemaAt(path string) pie.Schema {
	return pie.Schema{Path: path, Name: "v1.gadgets.core.example.io", Group: "core.example.io", Kind: "Gadget", Plural: "gadgets"}
}

func TestScan(t *testing.T) {
	tests := []struct {
		name      string
		fsys      fstest.MapFS
		want      pie.Model
		wantDiags []string
	}{
		{
			name: "finds an APIExport and the schemas it references",
			fsys: fstest.MapFS{
				"export.yaml":  manifest(t, apiExport("core.example.io", widgets, gadgets)),
				"widgets.yaml": manifest(t, apiResourceSchema("Widget", "widgets", "core.example.io")),
				"gadgets.yaml": manifest(t, apiResourceSchema("Gadget", "gadgets", "core.example.io")),
			},
			want: pie.Model{
				Exports: []pie.Export{coreExportAt("export.yaml")},
				Schemas: []pie.Schema{gadgetSchemaAt("gadgets.yaml"), widgetSchemaAt("widgets.yaml")},
			},
		},
		{
			name: "finds manifests in nested directories with either YAML extension",
			fsys: fstest.MapFS{
				"a/b/export.yml": manifest(t, apiExport("core.example.io", widgets, gadgets)),
				"a/widgets.yaml": manifest(t, apiResourceSchema("Widget", "widgets", "core.example.io")),
			},
			want: pie.Model{
				Exports: []pie.Export{coreExportAt("a/b/export.yml")},
				Schemas: []pie.Schema{widgetSchemaAt("a/widgets.yaml")},
			},
		},
		{
			name: "draws a v1alpha1 APIExport the same as its v1alpha2 equivalent",
			fsys: fstest.MapFS{
				"export.yaml": manifest(t, apiExportV1alpha1("core.example.io", "v1.widgets.core.example.io", "v1.gadgets.core.example.io")),
			},
			want: pie.Model{Exports: []pie.Export{coreExportAt("export.yaml")}},
		},
		{
			name: "puts a v1alpha1 schema of the core group in the empty group, as kcp does",
			fsys: fstest.MapFS{
				"export.yaml": manifest(t, apiExportV1alpha1("legacy.example.io", "v1.configmaps.core")),
			},
			want: pie.Model{Exports: []pie.Export{{Path: "export.yaml", Doc: 1, Name: "legacy.example.io", Resources: []pie.Resource{
				{Group: "", Name: "configmaps", Schema: "v1.configmaps.core"},
			}}}},
		},
		{
			name: "warns about a v1alpha1 APIExport that kcp cannot convert",
			fsys: fstest.MapFS{
				"export.yaml": manifest(t, apiExportV1alpha1("broken.example.io", "not-a-schema-name")),
			},
			want:      pie.Model{},
			wantDiags: []string{"warning export.yaml#1"},
		},
		{
			name: "warns about a v1alpha1 APIExport without a name and does not draw it",
			fsys: fstest.MapFS{
				"export.yaml": manifest(t, apiExportV1alpha1("", "v1.widgets.core.example.io")),
			},
			want:      pie.Model{},
			wantDiags: []string{"warning export.yaml#1"},
		},
		{
			name: "skips kinds other than kcp APIExport and APIResourceSchema",
			fsys: fstest.MapFS{
				"configmap.yaml": {Data: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n")},
				"binding.yaml":   manifest(t, apiBinding("core.example.io")),
				"foreign.yaml":   {Data: []byte("apiVersion: example.io/v1\nkind: APIExport\nmetadata:\n  name: lookalike\n")},
			},
			want:      pie.Model{},
			wantDiags: []string{"skipped binding.yaml#1", "skipped configmap.yaml#1", "skipped foreign.yaml#1"},
		},
		{
			name: "finds WorkspaceTypes with the exports they bind and the types they extend",
			fsys: fstest.MapFS{
				"types.yaml": manifest(t, workspaceType("account",
					[]tenancyv1alpha1.APIExportReference{{Export: "core.example.io", Path: "root:providers"}, {Export: "local.example.io"}},
					[]tenancyv1alpha1.WorkspaceTypeReference{{Name: "security", Path: "root"}})),
			},
			want: pie.Model{WorkspaceTypes: []pie.WorkspaceType{{
				Path: "types.yaml", Doc: 1, Name: "account",
				Binds:   []pie.ExportRef{{Path: "root:providers", Name: "core.example.io"}, {Path: "", Name: "local.example.io"}},
				Extends: []pie.TypeRef{{Path: "root", Name: "security"}},
			}}},
		},
		{
			name: "ignores files without a YAML extension",
			fsys: fstest.MapFS{
				"notes.txt": manifest(t, apiExport("core.example.io", widgets)),
			},
			want: pie.Model{},
		},
		{
			name: "warns about APIExports and APIResourceSchemas that do not decode and keeps scanning",
			fsys: fstest.MapFS{
				"a-broken.yaml":          {Data: []byte("kind: [APIExport\n")},
				"b-mistyped-export.yaml": {Data: []byte("apiVersion: apis.kcp.io/v1alpha2\nkind: APIExport\nmetadata:\n  name: x\nspec: not-a-map\n")},
				"c-mistyped-schema.yaml": {Data: []byte("apiVersion: apis.kcp.io/v1alpha1\nkind: APIResourceSchema\nmetadata:\n  name: y\nspec: not-a-map\n")},
				"d-export.yaml":          manifest(t, apiExport("core.example.io", widgets, gadgets)),
				"e-broken-export.yaml":   {Data: []byte("apiVersion: apis.kcp.io/v1alpha2\nkind: APIExport\nmetadata: [oops\n")},
			},
			want: pie.Model{Exports: []pie.Export{coreExportAt("d-export.yaml")}},
			wantDiags: []string{
				"skipped a-broken.yaml#1",
				"warning b-mistyped-export.yaml#1",
				"warning c-mistyped-schema.yaml#1",
				"warning e-broken-export.yaml#1",
			},
		},
		{
			name: "reads every document of a multi-document file and numbers them in file order",
			fsys: fstest.MapFS{
				"all.yaml": {Data: []byte("---\n" + string(manifest(t, apiExport("core.example.io", widgets, gadgets)).Data) +
					"---\n# a comment-only document\n---\n" +
					string(manifest(t, apiResourceSchema("Widget", "widgets", "core.example.io")).Data) +
					"---\napiVersion: apis.kcp.io/v1alpha2\nkind: APIExport\nmetadata:\n  name: x\nspec: not-a-map\n")},
			},
			want: pie.Model{
				Exports: []pie.Export{coreExportAt("all.yaml")},
				Schemas: []pie.Schema{widgetSchemaAt("all.yaml")},
			},
			wantDiags: []string{"skipped all.yaml#2", "warning all.yaml#4"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diags, err := pie.Scan(tt.fsys)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Scan() =\n%+v\nwant\n%+v", got, tt.want)
			}
			if got := summarize(diags); !reflect.DeepEqual(got, tt.wantDiags) {
				t.Errorf("Scan() diagnostics = %q, want %q", got, tt.wantDiags)
			}
		})
	}
}

func TestScanCleansUpTemplates(t *testing.T) {
	const exportHead = `apiVersion: apis.kcp.io/v1alpha2
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
	widgetsOnly := pie.Export{Path: "export.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{
		{Group: "core.example.io", Name: "widgets", Schema: "v1.widgets.core.example.io"},
	}}

	tests := []struct {
		name      string
		manifest  string
		want      pie.Model
		wantDiags []string
	}{
		{
			name: "a templated identity hash does not stop an export from decoding",
			manifest: exportHead + `  permissionClaims:
  - group: tenancy.kcp.io
    resource: workspaces
    identityHash: {{ .tenancyIdentityHash }}
    verbs: ["*"]
`,
			want: pie.Model{Exports: []pie.Export{widgetsOnly}},
		},
		{
			name: "a templated field that is not a string decodes as its zero value",
			manifest: `apiVersion: apis.kcp.io/v1alpha1
kind: APIResourceSchema
metadata:
  name: v1.widgets.core.example.io
spec:
  group: core.example.io
  names: {kind: Widget, plural: widgets}
  scope: Cluster
  versions:
  - name: v1
    served: {{ .served }}
    storage: true
    schema: {type: object}
`,
			want: pie.Model{Schemas: []pie.Schema{widgetSchemaAt("export.yaml")}},
		},
		{
			name: "content on both sides of a template conditional is kept",
			manifest: exportHead + `{{- if eq .feature "true" }}
  - group: core.example.io
    name: gadgets
    schema: v1.gadgets.core.example.io
    storage:
      crd: {}
{{- else }}
  - group: core.example.io
    name: gizmos
    schema: v1.gizmos.core.example.io
    storage:
      crd: {}
    {{- end }}
`,
			want: pie.Model{Exports: []pie.Export{{Path: "export.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{
				{Group: "core.example.io", Name: "widgets", Schema: "v1.widgets.core.example.io"},
				{Group: "core.example.io", Name: "gadgets", Schema: "v1.gadgets.core.example.io"},
				{Group: "core.example.io", Name: "gizmos", Schema: "v1.gizmos.core.example.io"},
			}}}},
		},
		{
			name: "a template inside a value becomes tmpl",
			manifest: strings.Replace(exportHead, "name: core.example.io",
				"name: {{ .prefix }}-core.example.io", 1),
			want: pie.Model{Exports: []pie.Export{{Path: "export.yaml", Doc: 1, Name: "tmpl-core.example.io", Resources: widgetsOnly.Resources}}},
		},
		{
			name:      "an export whose name is only a template is skipped with a warning",
			manifest:  strings.Replace(exportHead, "name: core.example.io", "name: {{ .exportName }}", 1),
			want:      pie.Model{},
			wantDiags: []string{"warning export.yaml#1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diags, err := pie.Scan(fstest.MapFS{"export.yaml": {Data: []byte(tt.manifest)}})
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Scan() =\n%+v\nwant\n%+v", got, tt.want)
			}
			if got := summarize(diags); !reflect.DeepEqual(got, tt.wantDiags) {
				t.Errorf("Scan() diagnostics = %q, want %q", got, tt.wantDiags)
			}
		})
	}
}

func TestScanSaysWhyADocumentIsNotDrawn(t *testing.T) {
	tests := []struct {
		name       string
		manifest   string
		wantReason string
	}{
		{
			name:       "a kind of no interest",
			manifest:   "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n",
			wantReason: "v1 ConfigMap is not drawn",
		},
		{
			name:       "a document without kind",
			manifest:   "# only a comment\n",
			wantReason: "document without kind is not drawn",
		},
		{
			name:       "a kind of no interest that does not decode",
			manifest:   "apiVersion: v1\nkind: ConfigMap\ndata: [oops\n",
			wantReason: "v1 ConfigMap does not decode: ",
		},
		{
			name:       "an APIExport that does not decode",
			manifest:   "apiVersion: apis.kcp.io/v1alpha2\nkind: APIExport\nmetadata:\n  name: x\nspec: not-a-map\n",
			wantReason: "APIExport does not decode: ",
		},
		{
			name:       "a v1alpha1 APIExport that kcp cannot convert",
			manifest:   "apiVersion: apis.kcp.io/v1alpha1\nkind: APIExport\nmetadata:\n  name: x\nspec:\n  latestResourceSchemas: [not-a-schema-name]\n",
			wantReason: "APIExport does not convert to v1alpha2: ",
		},
		{
			name:       "an APIResourceSchema without a name",
			manifest:   "apiVersion: apis.kcp.io/v1alpha1\nkind: APIResourceSchema\nmetadata: {}\n",
			wantReason: "APIResourceSchema has no metadata.name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags, err := pie.Scan(fstest.MapFS{"doc.yaml": {Data: []byte(tt.manifest)}})
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			if len(diags) != 1 || !strings.HasPrefix(diags[0].Reason, tt.wantReason) {
				t.Errorf("Scan() diagnostics = %v, want one starting %q", diags, tt.wantReason)
			}
		})
	}
}

func TestDiagnosticNamesTheDocumentAndTheReason(t *testing.T) {
	tests := []struct {
		diag pie.Diagnostic
		want string
	}{
		{
			diag: pie.Diagnostic{Level: pie.Warning, Path: "ws/export.yaml", Doc: 2, Reason: "APIExport has no metadata.name"},
			want: "warning: ws/export.yaml#2: APIExport has no metadata.name",
		},
		{
			diag: pie.Diagnostic{Level: pie.Skipped, Path: "ws/settings.yaml", Doc: 1, Reason: "v1 ConfigMap is not drawn"},
			want: "skipped: ws/settings.yaml#1: v1 ConfigMap is not drawn",
		},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.diag.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScanReportsWhatItCannotRead(t *testing.T) {
	tests := []struct {
		name       string
		unreadable string
	}{
		{name: "the tree itself", unreadable: "."},
		{name: "a manifest in the tree", unreadable: "ws/export.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := unreadableFS{
				FS:   fstest.MapFS{"ws/export.yaml": manifest(t, apiExport("core.example.io"))},
				name: tt.unreadable,
			}

			_, _, err := pie.Scan(fsys)

			if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), tt.unreadable) {
				t.Errorf("Scan() error = %v, want a permission error naming %q", err, tt.unreadable)
			}
		})
	}
}

func TestRenderD2(t *testing.T) {
	tests := []struct {
		name  string
		model pie.Model
		want  string
	}{
		{
			name: "draws an export as a table of the kinds it serves",
			model: pie.Model{
				Exports: []pie.Export{coreExportAt("ws/export.yaml")},
				Schemas: []pie.Schema{gadgetSchemaAt("ws/gadgets.yaml"), widgetSchemaAt("ws/widgets.yaml")},
			},
			want: `"apiexport:ws/export.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io"
  "Widget": "widgets.core.example.io" {constraint: "v1"}
  "Gadget": "gadgets.core.example.io" {constraint: "v1"}
}
`,
		},
		{
			name: "shows the plural where the schema is not in the tree",
			model: pie.Model{Exports: []pie.Export{{Path: "ws/export.yaml", Doc: 1, Name: "legacy.example.io", Resources: []pie.Resource{
				{Group: "", Name: "configmaps", Schema: "v1.configmaps.core"},
				{Group: "legacy.example.io", Name: "things", Schema: "v260213-fbdf981.things.legacy.example.io"},
			}}}},
			want: `"apiexport:ws/export.yaml#1": {
  shape: sql_table
  label: "APIExport legacy.example.io"
  "configmaps": "configmaps" {constraint: "v1"}
  "things": "things.legacy.example.io" {constraint: "v260213-fbdf981"}
}
`,
		},
		{
			name: "tells rows of the same kind apart by group",
			model: pie.Model{
				Exports: []pie.Export{{Path: "ws/export.yaml", Doc: 1, Name: "mixed.example.io", Resources: []pie.Resource{
					{Group: "core.example.io", Name: "widgets", Schema: "v1.widgets.core.example.io"},
					{Group: "other.example.io", Name: "widgets", Schema: "v1.widgets.other.example.io"},
				}}},
				Schemas: []pie.Schema{
					widgetSchemaAt("ws/widgets.yaml"),
					{Path: "ws/widgets.yaml", Name: "v1.widgets.other.example.io", Group: "other.example.io", Kind: "Widget", Plural: "widgets"},
				},
			},
			want: `"apiexport:ws/export.yaml#1": {
  shape: sql_table
  label: "APIExport mixed.example.io"
  "Widget (core.example.io)": "widgets.core.example.io" {constraint: "v1"}
  "Widget (other.example.io)": "widgets.other.example.io" {constraint: "v1"}
}
`,
		},
		{
			name: "takes the kind from the schema in the export's own directory first",
			model: pie.Model{
				Exports: []pie.Export{{Path: "b/export.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource()}}},
				Schemas: []pie.Schema{widgetSchemaKindAt("a/widgets.yaml", "WidgetInA"), widgetSchemaKindAt("b/widgets.yaml", "WidgetInB")},
			},
			want: `"apiexport:b/export.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io"
  "WidgetInB": "widgets.core.example.io" {constraint: "v1"}
}
`,
		},
		{
			name: "takes the kind from the first schema of that name elsewhere when its directory has none",
			model: pie.Model{
				Exports: []pie.Export{{Path: "c/export.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource()}}},
				Schemas: []pie.Schema{widgetSchemaKindAt("a/widgets.yaml", "WidgetInA"), widgetSchemaKindAt("b/widgets.yaml", "WidgetInB")},
			},
			want: `"apiexport:c/export.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io"
  "WidgetInA": "widgets.core.example.io" {constraint: "v1"}
}
`,
		},
		{
			name: "keeps exports of the same name apart, wherever they are, and names their document",
			model: pie.Model{Exports: []pie.Export{
				{Path: "config/export.yaml", Doc: 1, Name: "core.example.io"},
				{Path: "test/setup/export.yaml", Doc: 1, Name: "core.example.io"},
				{Path: "test/setup/provider1.yaml", Doc: 1, Name: "provider.example.io"},
				{Path: "test/setup/provider2.yaml", Doc: 1, Name: "provider.example.io"},
				{Path: "test/setup/both.yaml", Doc: 1, Name: "twice.example.io"},
				{Path: "test/setup/both.yaml", Doc: 2, Name: "twice.example.io"},
				{Path: "config/export.yaml", Doc: 2, Name: "other.example.io"},
			}},
			want: `"apiexport:config/export.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · config/export.yaml#1"
}
"apiexport:test/setup/export.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · test/setup/export.yaml#1"
}
"apiexport:test/setup/provider1.yaml#1": {
  shape: sql_table
  label: "APIExport provider.example.io · test/setup/provider1.yaml#1"
}
"apiexport:test/setup/provider2.yaml#1": {
  shape: sql_table
  label: "APIExport provider.example.io · test/setup/provider2.yaml#1"
}
"apiexport:test/setup/both.yaml#1": {
  shape: sql_table
  label: "APIExport twice.example.io · test/setup/both.yaml#1"
}
"apiexport:test/setup/both.yaml#2": {
  shape: sql_table
  label: "APIExport twice.example.io · test/setup/both.yaml#2"
}
"apiexport:config/export.yaml#2": {
  shape: sql_table
  label: "APIExport other.example.io"
}
`,
		},
		{
			name: "lists schemas no export serves by name, with their kind",
			model: pie.Model{
				Exports: []pie.Export{{Path: "ws/export.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource()}}},
				Schemas: []pie.Schema{gadgetSchemaAt("ws/gadgets.yaml"), widgetSchemaAt("ws/widgets.yaml")},
			},
			want: `"apiexport:ws/export.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io"
  "Widget": "widgets.core.example.io" {constraint: "v1"}
}
"unserved": {
  shape: sql_table
  label: "APIResourceSchemas no export serves"
  "v1.gadgets.core.example.io": "Gadget"
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderD2(tt.model)); got != tt.want {
				t.Errorf("RenderD2() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestRenderD2DrawsUseCases(t *testing.T) {
	coreHere := pie.Export{Path: "pms/export.yaml", Doc: 1, Name: "core.example.io"}
	coreTable := `"apiexport:pms/export.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io"
}
`
	tests := []struct {
		name  string
		model pie.Model
		want  string
	}{
		{
			name: "a WorkspaceType lists the exports it binds, where they live, and points at them",
			model: pie.Model{
				Exports: []pie.Export{coreHere},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account",
					Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}}},
			},
			want: `"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account"
  "core.example.io": "root:pms"
}
` + coreTable + `"workspacetype:types/account.yaml#1"."core.example.io" -> "apiexport:pms/export.yaml#1"
`,
		},
		{
			name: "an export outside the tree is drawn once, as a dashed box, and an empty path is the type's own workspace",
			model: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{
						{Path: "root", Name: "tenancy.kcp.io"}, {Path: "", Name: "local.example.io"},
					}},
					{Path: "types/org.yaml", Doc: 1, Name: "org", Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}}},
				},
			},
			want: `"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account"
  "tenancy.kcp.io": "root"
  "local.example.io": "(own workspace)"
}
"workspacetype:types/org.yaml#1": {
  shape: sql_table
  label: "WorkspaceType org"
  "tenancy.kcp.io": "root"
}
"outside": {
  label: "APIExports outside the tree"
  style.stroke-dash: 3
  "tenancy.kcp.io @ root"
  "local.example.io @ (own workspace)"
}
"workspacetype:types/account.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root"
"workspacetype:types/account.yaml#1"."local.example.io" -> "outside"."local.example.io @ (own workspace)"
"workspacetype:types/org.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root"
`,
		},
		{
			name: "bindings inherited through extend are listed once, after the type's own, naming the type that declares them",
			model: pie.Model{
				Exports: []pie.Export{coreHere},
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account",
						Binds:   []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}},
						Extends: []pie.TypeRef{{Path: "root", Name: "security"}, {Path: "root", Name: "universal"}}},
					{Path: "types/security.yaml", Doc: 1, Name: "security",
						Binds:   []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}, {Path: "root:audit", Name: "audit.example.io"}},
						Extends: []pie.TypeRef{{Path: "root", Name: "base"}}},
					{Path: "types/base.yaml", Doc: 1, Name: "base",
						Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}}},
				},
			},
			want: `"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account · extends security, universal (not in tree)"
  "core.example.io": "root:pms"
  "audit.example.io": "root:audit · via security"
  "tenancy.kcp.io": "root · via base"
}
"workspacetype:types/security.yaml#1": {
  shape: sql_table
  label: "WorkspaceType security · extends base"
  "core.example.io": "root:pms"
  "audit.example.io": "root:audit"
  "tenancy.kcp.io": "root · via base"
}
"workspacetype:types/base.yaml#1": {
  shape: sql_table
  label: "WorkspaceType base"
  "tenancy.kcp.io": "root"
}
` + coreTable + `"outside": {
  label: "APIExports outside the tree"
  style.stroke-dash: 3
  "audit.example.io @ root:audit"
  "tenancy.kcp.io @ root"
}
"workspacetype:types/account.yaml#1"."core.example.io" -> "apiexport:pms/export.yaml#1"
"workspacetype:types/account.yaml#1"."audit.example.io" -> "outside"."audit.example.io @ root:audit"
"workspacetype:types/account.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root"
"workspacetype:types/security.yaml#1"."core.example.io" -> "apiexport:pms/export.yaml#1"
"workspacetype:types/security.yaml#1"."audit.example.io" -> "outside"."audit.example.io @ root:audit"
"workspacetype:types/security.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root"
"workspacetype:types/base.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root"
`,
		},
		{
			name: "types that extend each other stop where they started",
			model: pie.Model{WorkspaceTypes: []pie.WorkspaceType{
				{Path: "types/a.yaml", Doc: 1, Name: "a", Binds: []pie.ExportRef{{Path: "root", Name: "a.example.io"}}, Extends: []pie.TypeRef{{Name: "b"}}},
				{Path: "types/b.yaml", Doc: 1, Name: "b", Binds: []pie.ExportRef{{Path: "root", Name: "b.example.io"}}, Extends: []pie.TypeRef{{Name: "a"}}},
			}},
			want: `"workspacetype:types/a.yaml#1": {
  shape: sql_table
  label: "WorkspaceType a · extends b"
  "a.example.io": "root"
  "b.example.io": "root · via b"
}
"workspacetype:types/b.yaml#1": {
  shape: sql_table
  label: "WorkspaceType b · extends a"
  "b.example.io": "root"
  "a.example.io": "root · via a"
}
"outside": {
  label: "APIExports outside the tree"
  style.stroke-dash: 3
  "a.example.io @ root"
  "b.example.io @ root"
}
"workspacetype:types/a.yaml#1"."a.example.io" -> "outside"."a.example.io @ root"
"workspacetype:types/a.yaml#1"."b.example.io" -> "outside"."b.example.io @ root"
"workspacetype:types/b.yaml#1"."b.example.io" -> "outside"."b.example.io @ root"
"workspacetype:types/b.yaml#1"."a.example.io" -> "outside"."a.example.io @ root"
`,
		},
		{
			name: "a binding points at every export of that name in the tree, and repeated type names show their document",
			model: pie.Model{
				Exports: []pie.Export{coreHere, {Path: "test/export.yaml", Doc: 1, Name: "core.example.io"}},
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}},
					{Path: "test/account.yaml", Doc: 1, Name: "account"},
				},
			},
			want: `"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account · types/account.yaml#1"
  "core.example.io": "root:pms"
}
"workspacetype:test/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account · test/account.yaml#1"
}
"apiexport:pms/export.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · pms/export.yaml#1"
}
"apiexport:test/export.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · test/export.yaml#1"
}
"workspacetype:types/account.yaml#1"."core.example.io" -> "apiexport:pms/export.yaml#1"
"workspacetype:types/account.yaml#1"."core.example.io" -> "apiexport:test/export.yaml#1"
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderD2(tt.model)); got != tt.want {
				t.Errorf("RenderD2() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func widgetResource() pie.Resource {
	return pie.Resource{Group: "core.example.io", Name: "widgets", Schema: "v1.widgets.core.example.io"}
}

func widgetSchemaKindAt(path, kind string) pie.Schema {
	s := widgetSchemaAt(path)
	s.Kind = kind
	return s
}

func summarize(diags []pie.Diagnostic) []string {
	var out []string
	for _, d := range diags {
		out = append(out, fmt.Sprintf("%s %s#%d", d.Level, d.Path, d.Doc))
	}
	return out
}

func resource(group, plural, schema string) apisv1alpha2.ResourceSchema {
	return apisv1alpha2.ResourceSchema{
		Group:   group,
		Name:    plural,
		Schema:  schema,
		Storage: apisv1alpha2.ResourceSchemaStorage{CRD: &apisv1alpha2.ResourceSchemaStorageCRD{}},
	}
}

func apiExport(name string, resources ...apisv1alpha2.ResourceSchema) *apisv1alpha2.APIExport {
	return &apisv1alpha2.APIExport{
		TypeMeta:   metav1.TypeMeta{APIVersion: apisv1alpha2.SchemeGroupVersion.String(), Kind: "APIExport"},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       apisv1alpha2.APIExportSpec{Resources: resources},
	}
}

func apiExportV1alpha1(name string, schemas ...string) *apisv1alpha1.APIExport {
	return &apisv1alpha1.APIExport{
		TypeMeta:   metav1.TypeMeta{APIVersion: apisv1alpha1.SchemeGroupVersion.String(), Kind: "APIExport"},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: apisv1alpha1.APIExportSpec{
			LatestResourceSchemas: schemas,
			PermissionClaims: []apisv1alpha1.PermissionClaim{{
				GroupResource: apisv1alpha1.GroupResource{Group: "tenancy.kcp.io", Resource: "workspaces"},
				All:           true,
			}},
		},
	}
}

// apiResourceSchema names the schema v1.<plural>.<group>, the kcp convention with prefix v1.
func apiResourceSchema(kind, plural, group string) *apisv1alpha1.APIResourceSchema {
	return &apisv1alpha1.APIResourceSchema{
		TypeMeta:   metav1.TypeMeta{APIVersion: apisv1alpha1.SchemeGroupVersion.String(), Kind: "APIResourceSchema"},
		ObjectMeta: metav1.ObjectMeta{Name: "v1." + plural + "." + group},
		Spec: apisv1alpha1.APIResourceSchemaSpec{
			Group: group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: plural, Kind: kind},
			Scope: apiextensionsv1.ClusterScoped,
		},
	}
}

func workspaceType(name string, binds []tenancyv1alpha1.APIExportReference, extends []tenancyv1alpha1.WorkspaceTypeReference) *tenancyv1alpha1.WorkspaceType {
	return &tenancyv1alpha1.WorkspaceType{
		TypeMeta:   metav1.TypeMeta{APIVersion: tenancyv1alpha1.SchemeGroupVersion.String(), Kind: "WorkspaceType"},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: tenancyv1alpha1.WorkspaceTypeSpec{
			DefaultAPIBindings: binds,
			Extend:             tenancyv1alpha1.WorkspaceTypeExtension{With: extends},
		},
	}
}

func apiBinding(export string) *apisv1alpha2.APIBinding {
	return &apisv1alpha2.APIBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: apisv1alpha2.SchemeGroupVersion.String(), Kind: "APIBinding"},
		ObjectMeta: metav1.ObjectMeta{Name: export},
		Spec: apisv1alpha2.APIBindingSpec{Reference: apisv1alpha2.BindingReference{
			Export: &apisv1alpha2.ExportBindingReference{Name: export, Path: "root:providers"},
		}},
	}
}

type unreadableFS struct {
	fs.FS
	name string
}

func (u unreadableFS) Open(name string) (fs.File, error) {
	if name == u.name {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return u.FS.Open(name)
}

func manifest(t *testing.T, obj any) *fstest.MapFile {
	t.Helper()
	data, err := yaml.Marshal(obj)
	if err != nil {
		t.Fatalf("marshalling fixture: %v", err)
	}
	return &fstest.MapFile{Data: data}
}
