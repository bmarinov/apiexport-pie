package pie_test

import (
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"slices"
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
			name: "keeps the names kcp tells the resources of a group apart by",
			fsys: fstest.MapFS{
				"widgets.yaml": manifest(t, withNames(apiResourceSchema("Widget", "widgets", "core.example.io"), "widget", "WidgetList", "wd", "wdg")),
			},
			want: pie.Model{Schemas: []pie.Schema{{
				Path: "widgets.yaml", Name: "v1.widgets.core.example.io", Group: "core.example.io",
				Kind: "Widget", Plural: "widgets", Singular: "widget", ListKind: "WidgetList", ShortNames: []string{"wd", "wdg"},
			}}},
		},
		{
			name: "finds Workspaces and the type each is of",
			fsys: fstest.MapFS{
				"orgs.yaml":    manifest(t, workspace("orgs", &tenancyv1alpha1.WorkspaceTypeReference{Name: "orgs", Path: "root"})),
				"untyped.yaml": manifest(t, workspace("plain", nil)),
			},
			want: pie.Model{Workspaces: []pie.Workspace{
				{Path: "orgs.yaml", Doc: 1, Name: "orgs", Type: pie.TypeRef{Path: "root", Name: "orgs"}},
				{Path: "untyped.yaml", Doc: 1, Name: "plain"},
			}},
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
			want: pie.Model{Exports: []pie.Export{withClaims(coreExportAt("export.yaml"), pie.Claim{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}})}},
		},
		{
			name: "puts a v1alpha1 schema of the core group in the empty group, as kcp does",
			fsys: fstest.MapFS{
				"export.yaml": manifest(t, apiExportV1alpha1("legacy.example.io", "v1.configmaps.core")),
			},
			want: pie.Model{Exports: []pie.Export{withClaims(pie.Export{Path: "export.yaml", Doc: 1, Name: "legacy.example.io", Resources: []pie.Resource{
				{Group: "", Name: "configmaps", Schema: "v1.configmaps.core"},
			}}, pie.Claim{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}})}},
		},
		{
			name: "records what an export claims, which claims a default selector narrows, and which name an export's identity",
			fsys: fstest.MapFS{"export.yaml": manifest(t, func() *apisv1alpha2.APIExport {
				e := apiExport("terminal.example.io")
				e.Spec.PermissionClaims = []apisv1alpha2.PermissionClaim{
					{GroupResource: apisv1alpha2.GroupResource{Group: "core.example.io", Resource: "accountinfos"}, IdentityHash: "7748f340", Verbs: []string{"get", "list", "watch"}},
					{GroupResource: apisv1alpha2.GroupResource{Resource: "secrets"}, Verbs: []string{"*"},
						DefaultSelector: &apisv1alpha2.PermissionClaimSelector{LabelSelector: metav1.LabelSelector{MatchLabels: map[string]string{"owner": "terminal"}}}},
					{GroupResource: apisv1alpha2.GroupResource{Resource: "configmaps"}, Verbs: []string{"get"},
						DefaultSelector: &apisv1alpha2.PermissionClaimSelector{MatchAll: true}},
				}
				return e
			}())},
			want: pie.Model{Exports: []pie.Export{{Path: "export.yaml", Doc: 1, Name: "terminal.example.io", Claims: []pie.Claim{
				{Group: "core.example.io", Resource: "accountinfos", Verbs: []string{"get", "list", "watch"}, FromExport: true},
				{Group: "", Resource: "secrets", Verbs: []string{"*"}, Selected: true},
				{Group: "", Resource: "configmaps", Verbs: []string{"get"}},
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
				"slice.yaml":     {Data: []byte("apiVersion: apis.kcp.io/v1alpha1\nkind: APIExportEndpointSlice\nmetadata:\n  name: core.example.io\n")},
				"foreign.yaml":   {Data: []byte("apiVersion: example.io/v1\nkind: APIExport\nmetadata:\n  name: lookalike\n")},
			},
			want:      pie.Model{},
			wantDiags: []string{"skipped configmap.yaml#1", "skipped foreign.yaml#1", "skipped slice.yaml#1"},
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
			name: "finds APIBindings and the export each binds, reading v1alpha1 through kcp's conversion",
			fsys: fstest.MapFS{
				"a.yaml": manifest(t, apiBinding("core.example.io")),
				"b.yaml": manifest(t, apiBindingV1alpha1("legacy.example.io")),
			},
			want: pie.Model{APIBindings: []pie.APIBinding{
				{Path: "a.yaml", Doc: 1, Name: "core.example.io", Export: pie.ExportRef{Path: "root:providers", Name: "core.example.io"}},
				{Path: "b.yaml", Doc: 1, Name: "legacy.example.io", Export: pie.ExportRef{Path: "root:providers", Name: "legacy.example.io"},
					Accepts: []pie.GroupResource{{Group: "tenancy.kcp.io", Resource: "workspaces"}}},
			}},
		},
		{
			name: "keeps the claims an APIBinding accepts, not the ones it rejects",
			fsys: fstest.MapFS{"binding.yaml": manifest(t, func() *apisv1alpha2.APIBinding {
				b := apiBinding("core.example.io")
				claim := func(group, resource string, state apisv1alpha2.AcceptablePermissionClaimState) apisv1alpha2.AcceptablePermissionClaim {
					return apisv1alpha2.AcceptablePermissionClaim{
						ScopedPermissionClaim: apisv1alpha2.ScopedPermissionClaim{PermissionClaim: apisv1alpha2.PermissionClaim{
							GroupResource: apisv1alpha2.GroupResource{Group: group, Resource: resource}, Verbs: []string{"*"},
						}},
						State: state,
					}
				}
				b.Spec.PermissionClaims = []apisv1alpha2.AcceptablePermissionClaim{
					claim("", "secrets", apisv1alpha2.ClaimAccepted),
					claim("", "namespaces", apisv1alpha2.ClaimRejected),
				}
				return b
			}())},
			want: pie.Model{APIBindings: []pie.APIBinding{{Path: "binding.yaml", Doc: 1, Name: "core.example.io",
				Export: pie.ExportRef{Path: "root:providers", Name: "core.example.io"}, Accepts: []pie.GroupResource{{Resource: "secrets"}}}}},
		},
		{
			name:      "warns about a v1alpha1 APIBinding without a name and does not draw it",
			fsys:      fstest.MapFS{"binding.yaml": manifest(t, apiBindingV1alpha1(""))},
			want:      pie.Model{},
			wantDiags: []string{"warning binding.yaml#1"},
		},
		{
			name: "warns about a v1alpha1 APIBinding that kcp cannot convert",
			fsys: fstest.MapFS{"binding.yaml": manifest(t, func() *apisv1alpha1.APIBinding {
				b := apiBindingV1alpha1("legacy.example.io")
				b.Annotations = map[string]string{apisv1alpha2.AcceptablePermissionClaimsAnnotation: "not json"}
				return b
			}())},
			want:      pie.Model{},
			wantDiags: []string{"warning binding.yaml#1"},
		},
		{
			name: "warns about an APIBinding that names no export",
			fsys: fstest.MapFS{
				"binding.yaml": {Data: []byte("apiVersion: apis.kcp.io/v1alpha2\nkind: APIBinding\nmetadata:\n  name: nothing\nspec:\n  reference: {}\n")},
			},
			want:      pie.Model{},
			wantDiags: []string{"warning binding.yaml#1"},
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
			name: "a templated identity hash does not stop an export from decoding, and still names an export's identity",
			manifest: exportHead + `  permissionClaims:
  - group: tenancy.kcp.io
    resource: workspaces
    identityHash: {{ .tenancyIdentityHash }}
    verbs: ["*"]
`,
			want: pie.Model{Exports: []pie.Export{withClaims(widgetsOnly, pie.Claim{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}, FromExport: true})}},
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
			name: "an empty list after an action is the other branch's nothing and goes with the action",
			manifest: exportHead + `  permissionClaims:
  {{- if .claims }}
  - group: tenancy.kcp.io
    resource: workspaces
    verbs: ["*"]
  {{- else }} []
  {{- end }}
`,
			want: pie.Model{Exports: []pie.Export{withClaims(widgetsOnly, pie.Claim{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}})}},
		},
		{
			name:     "so does an empty map",
			manifest: strings.Replace(exportHead, "      crd: {}\n", "      {{- if .crd }}\n      crd: {}\n      {{- else }} {}\n      {{- end }}\n", 1),
			want:     pie.Model{Exports: []pie.Export{widgetsOnly}},
		},
		{
			name: "and a comment",
			manifest: exportHead + `  permissionClaims:
  - group: tenancy.kcp.io
    resource: workspaces
    verbs: ["*"]
  {{- end }} # claims
`,
			want: pie.Model{Exports: []pie.Export{withClaims(widgetsOnly, pie.Claim{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}})}},
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
			name:       "an APIBinding that names no export",
			manifest:   "apiVersion: apis.kcp.io/v1alpha2\nkind: APIBinding\nmetadata:\n  name: nothing\nspec:\n  reference: {}\n",
			wantReason: "APIBinding names no export",
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
			name: "starts with the ELK layout, which d2 follows unless D2_LAYOUT or --layout say otherwise",
		},
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
			name: "marks a resource whose schema is not in the tree, naming it by its plural",
			model: pie.Model{Exports: []pie.Export{{Path: "ws/export.yaml", Doc: 1, Name: "legacy.example.io", Resources: []pie.Resource{
				{Group: "", Name: "configmaps", Schema: "v1.configmaps.core"},
				{Group: "legacy.example.io", Name: "things", Schema: "v260213-fbdf981.things.legacy.example.io"},
			}}}},
			want: `"apiexport:ws/export.yaml#1": {
  shape: sql_table
  label: "APIExport legacy.example.io"
  "configmaps": "configmaps" {constraint: "v1 · not in tree"}
  "things": "things.legacy.example.io" {constraint: "v260213-fbdf981 · not in tree"}
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
			name: "an export serving a resource another export serves is orange and says where else",
			model: pie.Model{
				Exports: []pie.Export{
					{Path: "ws/a.yaml", Doc: 1, Name: "a.example.io", Resources: []pie.Resource{widgetResource(), gadgetResource()}},
					{Path: "ws/b.yaml", Doc: 1, Name: "b.example.io", Resources: []pie.Resource{widgetResource()}},
					{Path: "ws/c.yaml", Doc: 1, Name: "c.example.io", Resources: []pie.Resource{widgetResource()}},
				},
				Schemas: []pie.Schema{gadgetSchemaAt("ws/gadgets.yaml"), widgetSchemaAt("ws/widgets.yaml")},
			},
			want: `"apiexport:ws/a.yaml#1": {
  shape: sql_table
  label: "APIExport a.example.io"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in b.example.io, c.example.io"}
  "Gadget": "gadgets.core.example.io" {constraint: "v1"}
}
"apiexport:ws/b.yaml#1": {
  shape: sql_table
  label: "APIExport b.example.io"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in a.example.io, c.example.io"}
}
"apiexport:ws/c.yaml#1": {
  shape: sql_table
  label: "APIExport c.example.io"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in a.example.io, b.example.io"}
}
` + legend(legendShared),
		},
		{
			name: "copies of an export count as one, and never against themselves",
			model: pie.Model{
				Exports: []pie.Export{
					{Path: "config/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource()}},
					{Path: "test/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource()}},
					{Path: "ws/other.yaml", Doc: 1, Name: "other.example.io", Resources: []pie.Resource{widgetResource()}},
				},
				Schemas: []pie.Schema{widgetSchemaAt("ws/widgets.yaml")},
			},
			want: `"apiexport:config/core.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · config/core.yaml#1"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in other.example.io"}
}
"apiexport:test/core.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · test/core.yaml#1"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in other.example.io"}
}
"apiexport:ws/other.yaml#1": {
  shape: sql_table
  label: "APIExport other.example.io"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in core.example.io"}
}
` + legend(legendShared),
		},
		{
			name: "a resource of the same name in another group is another resource",
			model: pie.Model{
				Exports: []pie.Export{
					{Path: "ws/a.yaml", Doc: 1, Name: "a.example.io", Resources: []pie.Resource{widgetResource()}},
					{Path: "ws/b.yaml", Doc: 1, Name: "b.example.io", Resources: []pie.Resource{
						{Group: "other.example.io", Name: "widgets", Schema: "v1.widgets.other.example.io"},
					}},
				},
				Schemas: []pie.Schema{
					widgetSchemaAt("ws/widgets.yaml"),
					{Path: "ws/widgets.yaml", Name: "v1.widgets.other.example.io", Group: "other.example.io", Kind: "Widget", Plural: "widgets"},
				},
			},
			want: `"apiexport:ws/a.yaml#1": {
  shape: sql_table
  label: "APIExport a.example.io"
  "Widget": "widgets.core.example.io" {constraint: "v1"}
}
"apiexport:ws/b.yaml#1": {
  shape: sql_table
  label: "APIExport b.example.io"
  "Widget": "widgets.other.example.io" {constraint: "v1"}
}
`,
		},
		{
			name: "lists schemas no export serves by name, with their kind, in a muted table",
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
  style.fill: "#868e96"
  style.stroke-dash: 3
  "v1.gadgets.core.example.io": "Gadget"
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderD2(tt.model)); got != elkLayout+tt.want {
				t.Errorf("RenderD2() =\n%s\nwant\n%s", got, elkLayout+tt.want)
			}
		})
	}
}

func TestRenderD2DrawsUseCases(t *testing.T) {
	coreHere := pie.Export{Path: "pms/export.yaml", Doc: 1, Name: "core.example.io"}
	coreTable := `"workspace:root:pms": {
  label: "Workspace root:pms"
}
"workspace:root:pms"."apiexport:pms/export.yaml#1": {
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
` + coreTable + `"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#1971c2"}
` + legend(legendBindsByDefault),
		},
		{
			name: "a WorkspaceType names the workspaces in the tree that are of its type",
			model: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/orgs.yaml", Doc: 1, Name: "orgs", Extends: []pie.TypeRef{{Path: "root", Name: "universal"}}},
					{Path: "types/account.yaml", Doc: 1, Name: "account"},
				},
				Workspaces: []pie.Workspace{
					{Path: "ws/orgs.yaml", Doc: 1, Name: "orgs", Type: pie.TypeRef{Path: "root", Name: "orgs"}},
					{Path: "ws/a.yaml", Doc: 1, Name: "a", Type: pie.TypeRef{Path: "root", Name: "account"}},
					{Path: "ws/pms.yaml", Doc: 1, Name: "platform-mesh-system", Type: pie.TypeRef{Path: "root", Name: "universal"}},
					{Path: "ws/b.yaml", Doc: 1, Name: "b", Type: pie.TypeRef{Path: "root", Name: "account"}},
				},
			},
			want: `"workspacetype:types/orgs.yaml#1": {
  shape: sql_table
  label: "WorkspaceType orgs · extends universal (not in tree) · workspace orgs"
}
"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account · workspaces a, b"
}
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
"workspacetype:types/account.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root" {style.stroke: "#1971c2"}
"workspacetype:types/account.yaml#1"."local.example.io" -> "outside"."local.example.io @ (own workspace)" {style.stroke: "#1971c2"}
"workspacetype:types/org.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root" {style.stroke: "#1971c2"}
` + legend(legendBindsByDefault),
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
"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#1971c2"}
"workspacetype:types/account.yaml#1"."audit.example.io" -> "outside"."audit.example.io @ root:audit" {style.stroke: "#1971c2"; style.stroke-dash: 2}
"workspacetype:types/account.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root" {style.stroke: "#1971c2"; style.stroke-dash: 2}
"workspacetype:types/security.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#1971c2"}
"workspacetype:types/security.yaml#1"."audit.example.io" -> "outside"."audit.example.io @ root:audit" {style.stroke: "#1971c2"}
"workspacetype:types/security.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root" {style.stroke: "#1971c2"; style.stroke-dash: 2}
"workspacetype:types/base.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root" {style.stroke: "#1971c2"}
` + legend(legendBindsByDefault, legendBindsThroughExtend),
		},
		{
			name: "APIBinding files are grouped by directory, each listing the exports bound there once",
			model: pie.Model{
				Exports: []pie.Export{coreHere},
				APIBindings: []pie.APIBinding{
					{Path: "pms/a.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}},
					{Path: "features/x/c.yaml", Doc: 1, Name: "providers", Export: pie.ExportRef{Path: "root:pms", Name: "providers.example.io"}},
					{Path: "pms/b.yaml", Doc: 1, Name: "system", Export: pie.ExportRef{Name: "system.example.io"}},
					{Path: "pms/d.yaml", Doc: 1, Name: "core-again", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}},
				},
			},
			want: `"apibindings:pms": {
  shape: sql_table
  label: "APIBindings in pms"
  "core.example.io": "root:pms"
  "system.example.io": "(own workspace)"
}
"apibindings:features/x": {
  shape: sql_table
  label: "APIBindings in features/x"
  "providers.example.io": "root:pms"
}
` + coreTable + `"outside": {
  label: "APIExports outside the tree"
  style.stroke-dash: 3
  "system.example.io @ (own workspace)"
  "providers.example.io @ root:pms"
}
"apibindings:pms"."core.example.io" -> "workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#2f9e44"}
"apibindings:pms"."system.example.io" -> "outside"."system.example.io @ (own workspace)" {style.stroke: "#2f9e44"}
"apibindings:features/x"."providers.example.io" -> "outside"."providers.example.io @ root:pms" {style.stroke: "#2f9e44"}
` + legend(legendBindsByAPIBinding),
		},
		{
			name: "WorkspaceTypes and APIBinding files share the box of an export outside the tree",
			model: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/org.yaml", Doc: 1, Name: "org", Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}}}},
				APIBindings:    []pie.APIBinding{{Path: "pms/t.yaml", Doc: 1, Name: "tenancy", Export: pie.ExportRef{Path: "root", Name: "tenancy.kcp.io"}}},
			},
			want: `"workspacetype:types/org.yaml#1": {
  shape: sql_table
  label: "WorkspaceType org"
  "tenancy.kcp.io": "root"
}
"apibindings:pms": {
  shape: sql_table
  label: "APIBindings in pms"
  "tenancy.kcp.io": "root"
}
"outside": {
  label: "APIExports outside the tree"
  style.stroke-dash: 3
  "tenancy.kcp.io @ root"
}
"workspacetype:types/org.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root" {style.stroke: "#1971c2"}
"apibindings:pms"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root" {style.stroke: "#2f9e44"}
` + legend(legendBindsByDefault, legendBindsByAPIBinding),
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
"workspacetype:types/a.yaml#1"."a.example.io" -> "outside"."a.example.io @ root" {style.stroke: "#1971c2"}
"workspacetype:types/a.yaml#1"."b.example.io" -> "outside"."b.example.io @ root" {style.stroke: "#1971c2"; style.stroke-dash: 2}
"workspacetype:types/b.yaml#1"."b.example.io" -> "outside"."b.example.io @ root" {style.stroke: "#1971c2"}
"workspacetype:types/b.yaml#1"."a.example.io" -> "outside"."a.example.io @ root" {style.stroke: "#1971c2"; style.stroke-dash: 2}
` + legend(legendBindsByDefault, legendBindsThroughExtend),
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
"workspace:root:pms": {
  label: "Workspace root:pms"
}
"workspace:root:pms"."apiexport:pms/export.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · pms/export.yaml#1"
}
"workspace:root:pms"."apiexport:test/export.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · test/export.yaml#1"
}
"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#1971c2"}
"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:test/export.yaml#1" {style.stroke: "#1971c2"}
` + legend(legendBindsByDefault),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderD2(tt.model)); got != elkLayout+tt.want {
				t.Errorf("RenderD2() =\n%s\nwant\n%s", got, elkLayout+tt.want)
			}
		})
	}
}

func TestRenderD2DrawsClaims(t *testing.T) {
	infos := pie.Resource{Group: "core.example.io", Name: "accountinfos", Schema: "v1.accountinfos.core.example.io"}
	infosSchema := pie.Schema{Path: "pms/infos.yaml", Name: "v1.accountinfos.core.example.io", Group: "core.example.io", Kind: "AccountInfo", Plural: "accountinfos"}
	readInfos := pie.Claim{Group: "core.example.io", Resource: "accountinfos", Verbs: []string{"get", "list", "watch"}}

	tests := []struct {
		name  string
		model pie.Model
		want  string
	}{
		{
			name: "an export's header counts its permission claims",
			model: pie.Model{Exports: []pie.Export{{Path: "ws/export.yaml", Doc: 1, Name: "core.example.io", Claims: []pie.Claim{
				{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}},
				{Group: "", Resource: "secrets", Verbs: []string{"get"}},
			}}}},
			want: `"apiexport:ws/export.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · 2 claims"
}
`,
		},
		{
			name: "a claim on a resource another export serves points at that kind, with the verbs",
			model: pie.Model{
				Exports: []pie.Export{
					{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{infos}},
					{Path: "pms/terminal.yaml", Doc: 1, Name: "terminal.example.io", Claims: []pie.Claim{readInfos}},
				},
				Schemas: []pie.Schema{infosSchema},
			},
			want: `"apiexport:pms/core.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io"
  "AccountInfo": "accountinfos.core.example.io" {constraint: "v1"}
}
"apiexport:pms/terminal.yaml#1": {
  shape: sql_table
  label: "APIExport terminal.example.io · 1 claim"
}
"apiexport:pms/terminal.yaml#1" -> "apiexport:pms/core.yaml#1"."AccountInfo": "claims get, list, watch" {style.stroke: "#7048e8"; style.stroke-dash: 4}
` + legend(legendClaims),
		},
		{
			name: "a claim points at every export in the tree that serves the resource",
			model: pie.Model{
				Exports: []pie.Export{
					{Path: "pms/terminal.yaml", Doc: 1, Name: "terminal.example.io", Claims: []pie.Claim{readInfos}},
					{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{infos}},
					{Path: "test/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{infos}},
				},
			},
			want: `"apiexport:pms/terminal.yaml#1": {
  shape: sql_table
  label: "APIExport terminal.example.io · 1 claim"
}
"apiexport:pms/core.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · pms/core.yaml#1"
  "accountinfos": "accountinfos.core.example.io" {constraint: "v1 · not in tree"}
}
"apiexport:test/core.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · test/core.yaml#1"
  "accountinfos": "accountinfos.core.example.io" {constraint: "v1 · not in tree"}
}
"apiexport:pms/terminal.yaml#1" -> "apiexport:pms/core.yaml#1"."accountinfos": "claims get, list, watch" {style.stroke: "#7048e8"; style.stroke-dash: 4}
"apiexport:pms/terminal.yaml#1" -> "apiexport:test/core.yaml#1"."accountinfos": "claims get, list, watch" {style.stroke: "#7048e8"; style.stroke-dash: 4}
` + legend(legendClaims),
		},
		{
			name: "a claim points at the row its kind ends up with when kinds repeat",
			model: pie.Model{
				Exports: []pie.Export{
					{Path: "ws/mixed.yaml", Doc: 1, Name: "mixed.example.io", Resources: []pie.Resource{
						{Group: "core.example.io", Name: "widgets", Schema: "v1.widgets.core.example.io"},
						{Group: "other.example.io", Name: "widgets", Schema: "v1.widgets.other.example.io"},
					}},
					{Path: "ws/user.yaml", Doc: 1, Name: "user.example.io", Claims: []pie.Claim{
						{Group: "other.example.io", Resource: "widgets", Verbs: []string{"*"}},
					}},
				},
				Schemas: []pie.Schema{
					widgetSchemaAt("ws/widgets.yaml"),
					{Path: "ws/widgets.yaml", Name: "v1.widgets.other.example.io", Group: "other.example.io", Kind: "Widget", Plural: "widgets"},
				},
			},
			want: `"apiexport:ws/mixed.yaml#1": {
  shape: sql_table
  label: "APIExport mixed.example.io"
  "Widget (core.example.io)": "widgets.core.example.io" {constraint: "v1"}
  "Widget (other.example.io)": "widgets.other.example.io" {constraint: "v1"}
}
"apiexport:ws/user.yaml#1": {
  shape: sql_table
  label: "APIExport user.example.io · 1 claim"
}
"apiexport:ws/user.yaml#1" -> "apiexport:ws/mixed.yaml#1"."Widget (other.example.io)": "claims *" {style.stroke: "#7048e8"; style.stroke-dash: 4}
` + legend(legendClaims),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderD2(tt.model)); got != elkLayout+tt.want {
				t.Errorf("RenderD2() =\n%s\nwant\n%s", got, elkLayout+tt.want)
			}
		})
	}
}

func TestRenderD2FlagsBindingsKcpRefuses(t *testing.T) {
	exportOf := func(path, name string, resources ...pie.Resource) pie.Export {
		return pie.Export{Path: path, Doc: 1, Name: name, Resources: resources}
	}
	widgetsInAAndB := `"workspace:root:pms": {
  label: "Workspace root:pms"
}
"workspace:root:pms"."apiexport:ws/a.yaml#1": {
  shape: sql_table
  label: "APIExport a.example.io"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in b.example.io"}
}
"workspace:root:pms"."apiexport:ws/b.yaml#1": {
  shape: sql_table
  label: "APIExport b.example.io"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in a.example.io"}
}
`
	tests := []struct {
		name  string
		model pie.Model
		want  string
	}{
		{
			name: "a WorkspaceType binding two exports that serve one resource is red, and its rows say what conflicts",
			model: pie.Model{
				Exports: []pie.Export{
					exportOf("ws/a.yaml", "a.example.io", widgetResource(), gadgetResource()),
					exportOf("ws/b.yaml", "b.example.io", widgetResource()),
					exportOf("ws/c.yaml", "c.example.io", gadgetResource()),
				},
				Schemas: []pie.Schema{gadgetSchemaAt("ws/gadgets.yaml"), widgetSchemaAt("ws/widgets.yaml")},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{
					{Path: "root:pms", Name: "a.example.io"}, {Path: "root:pms", Name: "b.example.io"}, {Path: "root:pms", Name: "c.example.io"},
				}}},
			},
			want: `"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account"
  style.fill: "#e03131"
  "a.example.io": "root:pms · conflicts with b.example.io on widgets.core.example.io, c.example.io on gadgets.core.example.io"
  "b.example.io": "root:pms · conflicts with a.example.io on widgets.core.example.io"
  "c.example.io": "root:pms · conflicts with a.example.io on gadgets.core.example.io"
}
"workspace:root:pms": {
  label: "Workspace root:pms"
}
"workspace:root:pms"."apiexport:ws/a.yaml#1": {
  shape: sql_table
  label: "APIExport a.example.io"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in b.example.io"}
  "Gadget": "gadgets.core.example.io" {constraint: "v1 · also in c.example.io"}
}
"workspace:root:pms"."apiexport:ws/b.yaml#1": {
  shape: sql_table
  label: "APIExport b.example.io"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in a.example.io"}
}
"workspace:root:pms"."apiexport:ws/c.yaml#1": {
  shape: sql_table
  label: "APIExport c.example.io"
  style.fill: "#f08c00"
  "Gadget": "gadgets.core.example.io" {constraint: "v1 · also in a.example.io"}
}
"workspacetype:types/account.yaml#1"."a.example.io" -> "workspace:root:pms"."apiexport:ws/a.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}
"workspacetype:types/account.yaml#1"."b.example.io" -> "workspace:root:pms"."apiexport:ws/b.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}
"workspacetype:types/account.yaml#1"."c.example.io" -> "workspace:root:pms"."apiexport:ws/c.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}
` + legend(legendBindsRefused, legendShared),
		},
		{
			name: "a binding inherited through extend conflicts in the type that inherits it",
			model: pie.Model{
				Exports: []pie.Export{exportOf("ws/a.yaml", "a.example.io", widgetResource()), exportOf("ws/b.yaml", "b.example.io", widgetResource())},
				Schemas: []pie.Schema{widgetSchemaAt("ws/widgets.yaml")},
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root:pms", Name: "a.example.io"}}, Extends: []pie.TypeRef{{Name: "base"}}},
					{Path: "types/base.yaml", Doc: 1, Name: "base", Binds: []pie.ExportRef{{Path: "root:pms", Name: "b.example.io"}}},
				},
			},
			want: `"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account · extends base"
  style.fill: "#e03131"
  "a.example.io": "root:pms · conflicts with b.example.io on widgets.core.example.io"
  "b.example.io": "root:pms · via base · conflicts with a.example.io on widgets.core.example.io"
}
"workspacetype:types/base.yaml#1": {
  shape: sql_table
  label: "WorkspaceType base"
  "b.example.io": "root:pms"
}
` + widgetsInAAndB + `"workspacetype:types/account.yaml#1"."a.example.io" -> "workspace:root:pms"."apiexport:ws/a.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}
"workspacetype:types/account.yaml#1"."b.example.io" -> "workspace:root:pms"."apiexport:ws/b.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}
"workspacetype:types/base.yaml#1"."b.example.io" -> "workspace:root:pms"."apiexport:ws/b.yaml#1" {style.stroke: "#1971c2"}
` + legend(legendBindsByDefault, legendBindsRefused, legendShared),
		},
		{
			name: "a directory of APIBinding files binding two exports that serve one resource is red",
			model: pie.Model{
				Exports: []pie.Export{exportOf("ws/a.yaml", "a.example.io", widgetResource()), exportOf("ws/b.yaml", "b.example.io", widgetResource())},
				Schemas: []pie.Schema{widgetSchemaAt("ws/widgets.yaml")},
				APIBindings: []pie.APIBinding{
					{Path: "pms/a.yaml", Doc: 1, Name: "a", Export: pie.ExportRef{Path: "root:pms", Name: "a.example.io"}},
					{Path: "pms/b.yaml", Doc: 1, Name: "b", Export: pie.ExportRef{Path: "root:pms", Name: "b.example.io"}},
				},
			},
			want: `"apibindings:pms": {
  shape: sql_table
  label: "APIBindings in pms"
  style.fill: "#e03131"
  "a.example.io": "root:pms · conflicts with b.example.io on widgets.core.example.io"
  "b.example.io": "root:pms · conflicts with a.example.io on widgets.core.example.io"
}
` + widgetsInAAndB + `"apibindings:pms"."a.example.io" -> "workspace:root:pms"."apiexport:ws/a.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}
"apibindings:pms"."b.example.io" -> "workspace:root:pms"."apiexport:ws/b.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}
` + legend(legendBindsRefused, legendShared),
		},
		{
			name: "copies of an export conflict once, and every copy's edge turns red",
			model: pie.Model{
				Exports: []pie.Export{
					exportOf("pms/core.yaml", "core.example.io", widgetResource()),
					exportOf("test/core.yaml", "core.example.io", widgetResource()),
					exportOf("ws/other.yaml", "other.example.io", widgetResource()),
				},
				Schemas: []pie.Schema{widgetSchemaAt("ws/widgets.yaml")},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{
					{Path: "root:pms", Name: "core.example.io"}, {Path: "root:pms", Name: "other.example.io"},
				}}},
			},
			want: `"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account"
  style.fill: "#e03131"
  "core.example.io": "root:pms · conflicts with other.example.io on widgets.core.example.io"
  "other.example.io": "root:pms · conflicts with core.example.io on widgets.core.example.io"
}
"workspace:root:pms": {
  label: "Workspace root:pms"
}
"workspace:root:pms"."apiexport:pms/core.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · pms/core.yaml#1"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in other.example.io"}
}
"workspace:root:pms"."apiexport:test/core.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · test/core.yaml#1"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in other.example.io"}
}
"workspace:root:pms"."apiexport:ws/other.yaml#1": {
  shape: sql_table
  label: "APIExport other.example.io"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in core.example.io"}
}
"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}
"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:test/core.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}
"workspacetype:types/account.yaml#1"."other.example.io" -> "workspace:root:pms"."apiexport:ws/other.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}
` + legend(legendBindsRefused, legendShared),
		},
		{
			name: "copies of one export and exports outside the tree never conflict",
			model: pie.Model{
				Exports: []pie.Export{exportOf("pms/core.yaml", "core.example.io", widgetResource()), exportOf("test/core.yaml", "core.example.io", widgetResource())},
				Schemas: []pie.Schema{widgetSchemaAt("ws/widgets.yaml")},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{
					{Path: "root:pms", Name: "core.example.io"}, {Path: "root", Name: "tenancy.kcp.io"},
				}}},
			},
			want: `"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account"
  "core.example.io": "root:pms"
  "tenancy.kcp.io": "root"
}
"workspace:root:pms": {
  label: "Workspace root:pms"
}
"workspace:root:pms"."apiexport:pms/core.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · pms/core.yaml#1"
  "Widget": "widgets.core.example.io" {constraint: "v1"}
}
"workspace:root:pms"."apiexport:test/core.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · test/core.yaml#1"
  "Widget": "widgets.core.example.io" {constraint: "v1"}
}
"outside": {
  label: "APIExports outside the tree"
  style.stroke-dash: 3
  "tenancy.kcp.io @ root"
}
"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#1971c2"}
"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:test/core.yaml#1" {style.stroke: "#1971c2"}
"workspacetype:types/account.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root" {style.stroke: "#1971c2"}
` + legend(legendBindsByDefault),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderD2(tt.model)); got != elkLayout+tt.want {
				t.Errorf("RenderD2() =\n%s\nwant\n%s", got, elkLayout+tt.want)
			}
		})
	}
}

func TestRenderD2ComparesNamesAsKcpDoes(t *testing.T) {
	widgets := func(s *pie.Schema) {}
	gizmos := func(change func(s *pie.Schema)) func(s *pie.Schema) {
		return func(s *pie.Schema) {
			s.Name, s.Kind, s.Plural, s.Singular, s.ListKind, s.ShortNames = "v1.gizmos.core.example.io", "Gizmo", "gizmos", "gizmo", "GizmoList", []string{"gz"}
			change(s)
		}
	}
	tests := []struct {
		name    string
		a, b    func(s *pie.Schema)
		bAbsent bool
		wantRow string
	}{
		{name: "resources that share no name do not conflict", a: widgets, b: gizmos(func(s *pie.Schema) {}),
			wantRow: `"a.example.io": "root:pms"`},
		{name: "a shared singular conflicts", a: widgets, b: gizmos(func(s *pie.Schema) { s.Singular = "widget" }),
			wantRow: `"a.example.io": "root:pms · conflicts with b.example.io on widgets.core.example.io (both use widget)"`},
		{name: "a shared short name conflicts", a: widgets, b: gizmos(func(s *pie.Schema) { s.ShortNames = []string{"gz", "wd"} }),
			wantRow: `"a.example.io": "root:pms · conflicts with b.example.io on widgets.core.example.io (both use wd)"`},
		{name: "a short name that is the other's plural conflicts", a: widgets, b: gizmos(func(s *pie.Schema) { s.ShortNames = []string{"widgets"} }),
			wantRow: `"a.example.io": "root:pms · conflicts with b.example.io on widgets.core.example.io (both use widgets)"`},
		{name: "a shared kind conflicts", a: widgets, b: gizmos(func(s *pie.Schema) { s.Kind = "Widget" }),
			wantRow: `"a.example.io": "root:pms · conflicts with b.example.io on widgets.core.example.io (both use Widget)"`},
		{name: "a shared list kind conflicts", a: widgets, b: gizmos(func(s *pie.Schema) { s.ListKind = "WidgetList" }),
			wantRow: `"a.example.io": "root:pms · conflicts with b.example.io on widgets.core.example.io (both use WidgetList)"`},
		{name: "names are never compared with kinds", a: widgets, b: gizmos(func(s *pie.Schema) { s.Singular, s.ShortNames = "Widget", []string{"WidgetList"} }),
			wantRow: `"a.example.io": "root:pms"`},
		{name: "a singular that is missing never matches", a: func(s *pie.Schema) { s.Singular = "" },
			b:       gizmos(func(s *pie.Schema) { s.Singular = "" }),
			wantRow: `"a.example.io": "root:pms"`},
		{name: "a list kind that is missing never matches", a: func(s *pie.Schema) { s.ListKind = "" },
			b:       gizmos(func(s *pie.Schema) { s.ListKind = "" }),
			wantRow: `"a.example.io": "root:pms"`},
		{name: "the same names in another group do not conflict", a: widgets, b: func(s *pie.Schema) { s.Name, s.Group = "v1.widgets.other.example.io", "other.example.io" },
			wantRow: `"a.example.io": "root:pms"`},
		{name: "a resource whose schema is not in the tree is compared by its plural", a: widgets, b: func(s *pie.Schema) { s.Name = "v2.widgets.core.example.io" }, bAbsent: true,
			wantRow: `"a.example.io": "root:pms · conflicts with b.example.io on widgets.core.example.io"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := pie.Schema{Path: "ws/a/schema.yaml", Name: "v1.widgets.core.example.io", Group: "core.example.io",
				Kind: "Widget", Plural: "widgets", Singular: "widget", ListKind: "WidgetList", ShortNames: []string{"wd"}}
			b := a
			b.Path, b.ShortNames = "ws/b/schema.yaml", slices.Clone(a.ShortNames)
			tt.a(&a)
			tt.b(&b)
			schemas := []pie.Schema{a, b}
			if tt.bAbsent {
				schemas = schemas[:1]
			}
			model := pie.Model{
				Exports: []pie.Export{
					{Path: "ws/a/export.yaml", Doc: 1, Name: "a.example.io", Resources: []pie.Resource{{Group: a.Group, Name: a.Plural, Schema: a.Name}}},
					{Path: "ws/b/export.yaml", Doc: 1, Name: "b.example.io", Resources: []pie.Resource{{Group: b.Group, Name: b.Plural, Schema: b.Name}}},
				},
				Schemas: schemas,
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{
					{Path: "root:pms", Name: "a.example.io"}, {Path: "root:pms", Name: "b.example.io"},
				}}},
			}
			if got := string(pie.RenderD2(model)); !strings.Contains(got, "  "+tt.wantRow+"\n") {
				t.Errorf("RenderD2() has no row\n%s\nin\n%s", tt.wantRow, got)
			}
		})
	}
}

func TestRenderD2GroupsExportsByWorkspace(t *testing.T) {
	core := pie.Export{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io"}
	account := func(binds ...pie.ExportRef) []pie.WorkspaceType {
		return []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: binds}}
	}
	bindingInPMS := func(ref pie.ExportRef) []pie.APIBinding {
		return []pie.APIBinding{{Path: "pms/b.yaml", Doc: 1, Name: "b", Export: ref}}
	}
	tests := []struct {
		name  string
		model pie.Model
		want  string
	}{
		{
			name: "exports go in the box of the workspace their references agree on, and edges follow them",
			model: pie.Model{
				Exports: []pie.Export{
					{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{
						{Group: "core.example.io", Name: "accountinfos", Schema: "v1.accountinfos.core.example.io"},
					}},
					{Path: "pms/system.yaml", Doc: 1, Name: "system.example.io"},
					{Path: "x/term.yaml", Doc: 1, Name: "term.example.io", Claims: []pie.Claim{{Group: "core.example.io", Resource: "accountinfos", Verbs: []string{"get"}}}},
				},
				Schemas:        []pie.Schema{{Path: "pms/infos.yaml", Name: "v1.accountinfos.core.example.io", Group: "core.example.io", Kind: "AccountInfo", Plural: "accountinfos"}},
				WorkspaceTypes: account(pie.ExportRef{Path: "root:pms", Name: "core.example.io"}),
				APIBindings:    bindingInPMS(pie.ExportRef{Path: "root:pms", Name: "system.example.io"}),
			},
			want: `"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account"
  "core.example.io": "root:pms"
}
"apibindings:pms": {
  shape: sql_table
  label: "APIBindings in pms"
  "system.example.io": "root:pms"
}
"workspace:root:pms": {
  label: "Workspace root:pms"
}
"workspace:root:pms"."apiexport:pms/core.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io"
  "AccountInfo": "accountinfos.core.example.io" {constraint: "v1"}
}
"workspace:root:pms"."apiexport:pms/system.yaml#1": {
  shape: sql_table
  label: "APIExport system.example.io"
}
"apiexport:x/term.yaml#1": {
  shape: sql_table
  label: "APIExport term.example.io · 1 claim"
}
"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#1971c2"}
"apibindings:pms"."system.example.io" -> "workspace:root:pms"."apiexport:pms/system.yaml#1" {style.stroke: "#2f9e44"}
"apiexport:x/term.yaml#1" -> "workspace:root:pms"."apiexport:pms/core.yaml#1"."AccountInfo": "claims get" {style.stroke: "#7048e8"; style.stroke-dash: 4}
` + legend(legendBindsByDefault, legendBindsByAPIBinding, legendClaims),
		},
		{
			name: "an empty path names no workspace, so it neither places an export nor contradicts a path",
			model: pie.Model{
				Exports:        []pie.Export{core},
				WorkspaceTypes: account(pie.ExportRef{Name: "core.example.io"}),
				APIBindings:    bindingInPMS(pie.ExportRef{Path: "root:pms", Name: "core.example.io"}),
			},
			want: `"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account"
  "core.example.io": "(own workspace)"
}
"apibindings:pms": {
  shape: sql_table
  label: "APIBindings in pms"
  "core.example.io": "root:pms"
}
"workspace:root:pms": {
  label: "Workspace root:pms"
}
"workspace:root:pms"."apiexport:pms/core.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io"
}
"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#1971c2"}
"apibindings:pms"."core.example.io" -> "workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#2f9e44"}
` + legend(legendBindsByDefault, legendBindsByAPIBinding),
		},
		{
			name: "references that disagree on the workspace leave the export ungrouped, and say so",
			model: pie.Model{
				Exports:        []pie.Export{core},
				WorkspaceTypes: account(pie.ExportRef{Path: "root:a", Name: "core.example.io"}),
				APIBindings:    bindingInPMS(pie.ExportRef{Path: "root:b", Name: "core.example.io"}),
			},
			want: `"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account"
  "core.example.io": "root:a"
}
"apibindings:pms": {
  shape: sql_table
  label: "APIBindings in pms"
  "core.example.io": "root:b"
}
"apiexport:pms/core.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · references disagree: root:a, root:b"
}
"workspacetype:types/account.yaml#1"."core.example.io" -> "apiexport:pms/core.yaml#1" {style.stroke: "#1971c2"}
"apibindings:pms"."core.example.io" -> "apiexport:pms/core.yaml#1" {style.stroke: "#2f9e44"}
` + legend(legendBindsByDefault, legendBindsByAPIBinding),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderD2(tt.model)); got != elkLayout+tt.want {
				t.Errorf("RenderD2() =\n%s\nwant\n%s", got, elkLayout+tt.want)
			}
		})
	}
}

func TestCheckWarnsAboutBindingsKcpRefuses(t *testing.T) {
	exportOf := func(path, name string, resources ...pie.Resource) pie.Export {
		return pie.Export{Path: path, Doc: 1, Name: name, Resources: resources}
	}
	inPMS := func(names ...string) []pie.ExportRef {
		var refs []pie.ExportRef
		for _, name := range names {
			refs = append(refs, pie.ExportRef{Path: "root:pms", Name: name})
		}
		return refs
	}
	gizmos := pie.Resource{Group: "core.example.io", Name: "gizmos", Schema: "v1.gizmos.core.example.io"}
	gizmoKindWidget := pie.Schema{Path: "ws/gizmos.yaml", Name: "v1.gizmos.core.example.io", Group: "core.example.io", Kind: "Widget", Plural: "gizmos"}
	tests := []struct {
		name  string
		model pie.Model
		want  []string
	}{
		{
			name: "each pair of exports a WorkspaceType binds that kcp refuses together is one warning, at the type",
			model: pie.Model{
				Exports: []pie.Export{
					exportOf("ws/a.yaml", "a.example.io", widgetResource()),
					exportOf("ws/b.yaml", "b.example.io", widgetResource()),
					exportOf("ws/c.yaml", "c.example.io", widgetResource()),
				},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: inPMS("a.example.io", "b.example.io", "c.example.io")}},
			},
			want: []string{
				"warning: types/account.yaml#1: WorkspaceType account binds a.example.io and b.example.io, which both serve widgets.core.example.io; kcp binds only one of them",
				"warning: types/account.yaml#1: WorkspaceType account binds a.example.io and c.example.io, which both serve widgets.core.example.io; kcp binds only one of them",
				"warning: types/account.yaml#1: WorkspaceType account binds b.example.io and c.example.io, which both serve widgets.core.example.io; kcp binds only one of them",
			},
		},
		{
			name: "resources that clash on a name are both named, with the name",
			model: pie.Model{
				Exports:        []pie.Export{exportOf("ws/a.yaml", "a.example.io", widgetResource()), exportOf("ws/b.yaml", "b.example.io", gizmos)},
				Schemas:        []pie.Schema{widgetSchemaAt("ws/widgets.yaml"), gizmoKindWidget},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: inPMS("a.example.io", "b.example.io")}},
			},
			want: []string{
				"warning: types/account.yaml#1: WorkspaceType account binds a.example.io and b.example.io, whose widgets.core.example.io and gizmos.core.example.io both use Widget; kcp binds only one of them",
			},
		},
		{
			name: "a binding inherited through extend warns at the type that inherits it",
			model: pie.Model{
				Exports: []pie.Export{exportOf("ws/a.yaml", "a.example.io", widgetResource()), exportOf("ws/b.yaml", "b.example.io", widgetResource())},
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: inPMS("a.example.io"), Extends: []pie.TypeRef{{Name: "base"}}},
					{Path: "types/base.yaml", Doc: 1, Name: "base", Binds: inPMS("b.example.io")},
				},
			},
			want: []string{
				"warning: types/account.yaml#1: WorkspaceType account binds a.example.io and b.example.io, which both serve widgets.core.example.io; kcp binds only one of them",
			},
		},
		{
			name: "a directory of APIBinding files warns at its own file of the later binding",
			model: pie.Model{
				Exports: []pie.Export{exportOf("ws/a.yaml", "a.example.io", widgetResource()), exportOf("ws/b.yaml", "b.example.io", widgetResource())},
				APIBindings: []pie.APIBinding{
					{Path: "features/x/b.yaml", Doc: 1, Name: "b", Export: pie.ExportRef{Path: "root:pms", Name: "b.example.io"}},
					{Path: "pms/a.yaml", Doc: 1, Name: "a", Export: pie.ExportRef{Path: "root:pms", Name: "a.example.io"}},
					{Path: "pms/b.yaml", Doc: 2, Name: "b", Export: pie.ExportRef{Path: "root:pms", Name: "b.example.io"}},
				},
			},
			want: []string{
				"warning: pms/b.yaml#2: APIBindings in pms bind a.example.io and b.example.io, which both serve widgets.core.example.io; kcp binds only one of them",
			},
		},
		{
			name: "bindings kcp accepts leave nothing to say",
			model: pie.Model{
				Exports:        []pie.Export{exportOf("ws/a.yaml", "a.example.io", widgetResource()), exportOf("ws/b.yaml", "b.example.io", gadgetResource())},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: inPMS("a.example.io", "b.example.io")}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, d := range pie.Check(tt.model) {
				if d.Level == pie.Warning {
					got = append(got, d.String())
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Check() =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

func TestCheckPointsOutWhatIsWorthALook(t *testing.T) {
	panels := pie.Resource{Group: "ui.example.io", Name: "panels", Schema: "v1.panels.ui.example.io"}
	core := pie.Export{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource()}}
	typeBinding := func(name string, refs ...pie.ExportRef) pie.WorkspaceType {
		return pie.WorkspaceType{Path: "types/" + name + ".yaml", Doc: 1, Name: name, Binds: refs}
	}
	inPMS := func(name string) pie.ExportRef { return pie.ExportRef{Path: "root:pms", Name: name} }
	inRoot := func(name string) pie.ExportRef { return pie.ExportRef{Path: "root", Name: name} }
	tests := []struct {
		name  string
		model pie.Model
		want  []string
	}{
		{
			name: "an export that serves more than one API group, its copies once",
			model: pie.Model{
				Exports: []pie.Export{
					{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource(), panels, gadgetResource()}},
					{Path: "test/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource(), panels}},
				},
			},
			want: []string{"notice: pms/core.yaml#1: APIExport core.example.io serves 2 API groups: core.example.io, ui.example.io"},
		},
		{
			name: "an API group that more than one export serves, at the second",
			model: pie.Model{Exports: []pie.Export{
				core,
				{Path: "pms/system.yaml", Doc: 1, Name: "system.example.io", Resources: []pie.Resource{gadgetResource()}},
				{Path: "pms/spare.yaml", Doc: 1, Name: "spare.example.io", Resources: []pie.Resource{gadgetResource()}},
			}},
			want: []string{"notice: pms/system.yaml#1: API group core.example.io is served by 3 APIExports: core.example.io, system.example.io, spare.example.io"},
		},
		{
			name: "a WorkspaceType that binds nothing, not even through a type it extends",
			model: pie.Model{
				Exports: []pie.Export{core},
				WorkspaceTypes: []pie.WorkspaceType{
					typeBinding("security"),
					{Path: "types/account.yaml", Doc: 1, Name: "account", Extends: []pie.TypeRef{{Name: "base"}}},
					typeBinding("base", inPMS("core.example.io")),
				},
			},
			want: []string{"notice: types/security.yaml#1: WorkspaceType security binds no APIExport"},
		},
		{
			name: "a WorkspaceType that binds only exports from outside the tree",
			model: pie.Model{
				Exports: []pie.Export{core},
				WorkspaceTypes: []pie.WorkspaceType{
					typeBinding("providers", inRoot("tenancy.kcp.io"), inRoot("topology.kcp.io"), pie.ExportRef{Name: "tenancy.kcp.io"}),
					typeBinding("account", inPMS("core.example.io"), inRoot("tenancy.kcp.io")),
				},
			},
			want: []string{"notice: types/providers.yaml#1: WorkspaceType providers binds only APIExports from outside the tree: tenancy.kcp.io, topology.kcp.io"},
		},
		{
			name: "an export nothing in the tree binds, when the tree binds anything at all",
			model: pie.Model{
				Exports: []pie.Export{
					core,
					{Path: "pms/lonely.yaml", Doc: 1, Name: "lonely.example.io"},
					{Path: "pms/other.yaml", Doc: 1, Name: "other.example.io"},
				},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", inPMS("core.example.io"))},
				APIBindings:    []pie.APIBinding{{Path: "pms/other.yaml", Doc: 2, Name: "other", Export: inPMS("other.example.io")}},
			},
			want: []string{"notice: pms/lonely.yaml#1: APIExport lonely.example.io is bound by no WorkspaceType or APIBinding in the tree"},
		},
		{
			name: "APIBinding files alone are enough for a tree that binds",
			model: pie.Model{
				Exports:     []pie.Export{core, {Path: "pms/lonely.yaml", Doc: 1, Name: "lonely.example.io"}},
				APIBindings: []pie.APIBinding{{Path: "pms/core-binding.yaml", Doc: 1, Name: "core", Export: inPMS("core.example.io")}},
			},
			want: []string{"notice: pms/lonely.yaml#1: APIExport lonely.example.io is bound by no WorkspaceType or APIBinding in the tree"},
		},
		{
			name:  "exports in a tree that binds nothing are not called unbound",
			model: pie.Model{Exports: []pie.Export{core, {Path: "pms/lonely.yaml", Doc: 1, Name: "lonely.example.io"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, d := range pie.Check(tt.model) {
				got = append(got, d.String())
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Check() =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

func TestCheckPointsOutWhereClaimsReach(t *testing.T) {
	export := func(name string, resources []pie.Resource, claims ...pie.Claim) pie.Export {
		return pie.Export{Path: "pms/" + name + ".yaml", Doc: 1, Name: name, Resources: resources, Claims: claims}
	}
	claim := func(group, resource string, verbs ...string) pie.Claim {
		return pie.Claim{Group: group, Resource: resource, Verbs: verbs}
	}
	typeBinding := func(name string, exports ...string) pie.WorkspaceType {
		var refs []pie.ExportRef
		for _, e := range exports {
			refs = append(refs, pie.ExportRef{Path: "root:pms", Name: e})
		}
		return pie.WorkspaceType{Path: "types/" + name + ".yaml", Doc: 1, Name: name, Binds: refs}
	}
	core := export("core.example.io", []pie.Resource{widgetResource(), gadgetResource()})
	terminal := export("terminal.example.io", nil,
		claim("core.example.io", "widgets", "get"), claim("", "secrets", "get"), claim("core.example.io", "gadgets", "get"))
	tests := []struct {
		name  string
		model pie.Model
		want  []string
	}{
		{
			name: "two exports a use case binds together that claim the same resources, at the second",
			model: pie.Model{
				Exports: []pie.Export{
					export("a.example.io", nil, claim("tenancy.kcp.io", "workspaces", "get"), claim("", "secrets", "get"), claim("", "configmaps", "get")),
					export("b.example.io", nil, claim("tenancy.kcp.io", "workspaces", "get"), claim("", "secrets", "list")),
				},
				WorkspaceTypes: []pie.WorkspaceType{
					typeBinding("account", "a.example.io", "b.example.io"),
					typeBinding("org", "a.example.io", "b.example.io"),
					typeBinding("solo", "a.example.io"),
					typeBinding("team", "b.example.io", "a.example.io"),
				},
			},
			want: []string{"notice: pms/b.example.io.yaml#1: APIExports a.example.io and b.example.io both claim workspaces, secrets where account, org and team bind both"},
		},
		{
			name: "resources of one name in different API groups are different resources",
			model: pie.Model{
				Exports: []pie.Export{
					export("a.example.io", nil, claim("a.example.io", "things", "get")),
					export("b.example.io", nil, claim("b.example.io", "things", "get")),
				},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "a.example.io", "b.example.io")},
			},
		},
		{
			name: "claims that overlap only where nothing binds both say nothing",
			model: pie.Model{
				Exports: []pie.Export{
					export("a.example.io", nil, claim("", "secrets", "get")),
					export("b.example.io", nil, claim("", "secrets", "get")),
				},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "a.example.io"), typeBinding("org", "b.example.io")},
			},
		},
		{
			name: "an export that may write kcp's own resources says where, leaving out binding files that refuse",
			model: pie.Model{
				Exports: []pie.Export{export("a.example.io", nil,
					claim("tenancy.kcp.io", "workspaces", "*"),
					claim("apis.kcp.io", "apibindings", "get", "list"),
					claim("", "secrets", "create"),
					claim("core.kcp.io", "logicalclusters", "update"),
				)},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "a.example.io")},
				APIBindings: []pie.APIBinding{
					{Path: "pms/a-binding.yaml", Doc: 1, Name: "a", Export: pie.ExportRef{Path: "root:pms", Name: "a.example.io"},
						Accepts: []pie.GroupResource{{Group: "tenancy.kcp.io", Resource: "workspaces"}}},
					{Path: "refusing/a.yaml", Doc: 1, Name: "a", Export: pie.ExportRef{Path: "root:pms", Name: "a.example.io"},
						Accepts: []pie.GroupResource{{Group: "apis.kcp.io", Resource: "apibindings"}, {Resource: "secrets"}}},
				},
			},
			want: []string{"notice: pms/a.example.io.yaml#1: APIExport a.example.io may write kcp's own resources where account and APIBindings in pms bind it: * on workspaces; update on logicalclusters"},
		},
		{
			name:  "an export nothing binds reaches nowhere",
			model: pie.Model{Exports: []pie.Export{export("a.example.io", nil, claim("tenancy.kcp.io", "workspaces", "*"))}},
		},
		{
			name: "an export that claims what another serves names it, and that its consumers bind both",
			model: pie.Model{
				Exports: []pie.Export{core, terminal},
				WorkspaceTypes: []pie.WorkspaceType{
					typeBinding("account", "terminal.example.io", "core.example.io"), typeBinding("org", "core.example.io", "terminal.example.io"), typeBinding("solo", "core.example.io"),
				},
			},
			want: []string{"notice: pms/terminal.example.io.yaml#1: APIExport terminal.example.io claims widgets, gadgets from core.example.io; account and org bind both"},
		},
		{
			name: "and which of its consumers bind it without the other",
			model: pie.Model{
				Exports: []pie.Export{core, terminal},
				WorkspaceTypes: []pie.WorkspaceType{
					typeBinding("account", "terminal.example.io"), typeBinding("org", "terminal.example.io", "core.example.io"), typeBinding("solo", "terminal.example.io"),
				},
			},
			want: []string{"notice: pms/terminal.example.io.yaml#1: APIExport terminal.example.io claims widgets, gadgets from core.example.io; account and solo bind it without core.example.io"},
		},
		{
			name: "one consumer without the other",
			model: pie.Model{
				Exports:        []pie.Export{core, terminal},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "terminal.example.io"), typeBinding("org", "core.example.io")},
			},
			want: []string{"notice: pms/terminal.example.io.yaml#1: APIExport terminal.example.io claims widgets, gadgets from core.example.io; account binds it without core.example.io"},
		},
		{
			name: "a claim on the export's own resources is no dependency, and without consumers there is nothing more to say",
			model: pie.Model{Exports: []pie.Export{
				export("core.example.io", []pie.Resource{widgetResource()}, claim("core.example.io", "widgets", "get")),
				export("terminal.example.io", nil, claim("core.example.io", "widgets", "get")),
			}},
			want: []string{"notice: pms/terminal.example.io.yaml#1: APIExport terminal.example.io claims widgets from core.example.io"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, d := range pie.Check(tt.model) {
				got = append(got, d.String())
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Check() =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

func TestRenderSurfaceListsWhatIsWorthALookUnderTheTable(t *testing.T) {
	model := pie.Model{
		Exports: []pie.Export{{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource()}}},
		Schemas: []pie.Schema{widgetSchemaAt("pms/widgets.yaml")},
		WorkspaceTypes: []pie.WorkspaceType{
			{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}},
			{Path: "types/security.yaml", Doc: 1, Name: "security"},
		},
	}
	want := `| APIExport | kind | account | security |
|---|---|:-:|:-:|
| **core.example.io** | Widget | ● |  |

**Worth a look**

- WorkspaceType security binds no APIExport
`
	if got := string(pie.RenderSurface(model)); got != want {
		t.Errorf("RenderSurface() =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderSurface(t *testing.T) {
	core := pie.Export{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource(), gadgetResource()}}
	bindsInPMS := func(names ...string) []pie.ExportRef {
		var refs []pie.ExportRef
		for _, name := range names {
			refs = append(refs, pie.ExportRef{Path: "root:pms", Name: name})
		}
		return refs
	}
	tests := []struct {
		name  string
		model pie.Model
		want  string
	}{
		{
			name: "each export is a block of its kinds, marked for every use case that binds it, and a kind without its schema says so",
			model: pie.Model{
				Exports: []pie.Export{core, {Path: "pms/other.yaml", Doc: 1, Name: "other.example.io", Resources: []pie.Resource{
					{Group: "other.example.io", Name: "things", Schema: "v1.things.other.example.io"},
				}}},
				Schemas: []pie.Schema{widgetSchemaAt("pms/widgets.yaml"), gadgetSchemaAt("pms/gadgets.yaml")},
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: bindsInPMS("core.example.io")},
					{Path: "types/org.yaml", Doc: 1, Name: "org", Binds: bindsInPMS("core.example.io", "other.example.io")},
				},
				APIBindings: []pie.APIBinding{{Path: "pms/x.yaml", Doc: 1, Name: "x", Export: pie.ExportRef{Path: "root:pms", Name: "other.example.io"}}},
			},
			want: `| APIExport | kind | account | org | APIBindings in<br/>pms |
|---|---|:-:|:-:|:-:|
| **core.example.io** | Widget | ● | ● |  |
|  | Gadget | ● | ● |  |
| **other.example.io** | things · not in tree |  | ● | ● |
`,
		},
		{
			name: "inherited bindings count, and an export outside the tree gets one row",
			model: pie.Model{
				Exports: []pie.Export{core},
				Schemas: []pie.Schema{widgetSchemaAt("pms/widgets.yaml"), gadgetSchemaAt("pms/gadgets.yaml")},
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: bindsInPMS("core.example.io"), Extends: []pie.TypeRef{{Name: "base"}}},
					{Path: "types/base.yaml", Doc: 1, Name: "base", Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}}},
				},
			},
			want: `| APIExport | kind | account | base |
|---|---|:-:|:-:|
| **core.example.io** | Widget | ● |  |
|  | Gadget | ● |  |
| **tenancy.kcp.io** | (outside the tree) | ● | ● |

**Worth a look**

- WorkspaceType base binds only APIExports from outside the tree: tenancy.kcp.io
`,
		},
		{
			name: "copies of an export are one block, and an export that serves nothing still has its row",
			model: pie.Model{
				Exports: []pie.Export{core, {Path: "test/core.yaml", Doc: 1, Name: "core.example.io", Resources: core.Resources}, {Path: "pms/empty.yaml", Doc: 1, Name: "empty.example.io"}},
				Schemas: []pie.Schema{widgetSchemaAt("pms/widgets.yaml"), gadgetSchemaAt("pms/gadgets.yaml")},
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: bindsInPMS("core.example.io", "empty.example.io")},
				},
			},
			want: `| APIExport | kind | account |
|---|---|:-:|
| **core.example.io** | Widget | ● |
|  | Gadget | ● |
| **empty.example.io** | (no kinds) | ● |
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderSurface(tt.model)); got != tt.want {
				t.Errorf("RenderSurface() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestRenderSurfaceNamesTheGroupOfAKindThatIsNotTheExportsOwn(t *testing.T) {
	model := pie.Model{
		Exports: []pie.Export{
			{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{
				widgetResource(), {Group: "ui.example.io", Name: "panels", Schema: "v1.panels.ui.example.io"},
			}},
			{Path: "pms/system.yaml", Doc: 1, Name: "system.example.io", Resources: []pie.Resource{
				gadgetResource(), {Group: "billing.example.io", Name: "ledgers", Schema: "v1.ledgers.billing.example.io"},
			}},
		},
		Schemas: []pie.Schema{
			widgetSchemaAt("pms/widgets.yaml"), gadgetSchemaAt("pms/gadgets.yaml"),
			{Path: "pms/panels.yaml", Name: "v1.panels.ui.example.io", Group: "ui.example.io", Kind: "Panel", Plural: "panels"},
		},
		WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{
			{Path: "root:pms", Name: "core.example.io"}, {Path: "root:pms", Name: "system.example.io"},
		}}},
	}
	want := `| APIExport | kind | account |
|---|---|:-:|
| **core.example.io** | Widget | ● |
|  | Panel · ui.example.io | ● |
| **system.example.io** | Gadget · core.example.io | ● |
|  | ledgers · billing.example.io · not in tree | ● |

**Worth a look**

- APIExport core.example.io serves 2 API groups: core.example.io, ui.example.io
- APIExport system.example.io serves 2 API groups: core.example.io, billing.example.io
- API group core.example.io is served by 2 APIExports: core.example.io, system.example.io
`
	if got := string(pie.RenderSurface(model)); got != want {
		t.Errorf("RenderSurface() =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderSurfaceShowsWhatABindingGrants(t *testing.T) {
	coreClaiming := func(claims ...pie.Claim) pie.Export {
		return pie.Export{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource(), gadgetResource()}, Claims: claims}
	}
	schemas := []pie.Schema{widgetSchemaAt("pms/widgets.yaml"), gadgetSchemaAt("pms/gadgets.yaml")}
	account := []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}}}
	bindingIn := func(path string, accepts ...pie.GroupResource) pie.APIBinding {
		return pie.APIBinding{Path: path, Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}, Accepts: accepts}
	}
	tests := []struct {
		name  string
		model pie.Model
		want  string
	}{
		{
			name: "an export's claims follow its kinds, a row per set of verbs, narrowed ones selected",
			model: pie.Model{
				Exports: []pie.Export{coreClaiming(
					pie.Claim{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}},
					pie.Claim{Group: "", Resource: "secrets", Verbs: []string{"get", "create"}, Selected: true},
					pie.Claim{Group: "apis.kcp.io", Resource: "apibindings", Verbs: []string{"*"}},
				)},
				Schemas:        schemas,
				WorkspaceTypes: account,
			},
			want: `| APIExport | kind | account |
|---|---|:-:|
| **core.example.io** | Widget | ● |
|  | Gadget | ● |
|  | grants: * on workspaces, apibindings | ● |
|  | grants: get, create on selected secrets | ● |

**Worth a look**

- APIExport core.example.io may write kcp's own resources where account binds it: * on workspaces, apibindings
`,
		},
		{
			name: "a claimed resource that exports in the tree serve says which",
			model: pie.Model{
				Exports: []pie.Export{
					coreClaiming(),
					{Path: "pms/spare.yaml", Doc: 1, Name: "spare.example.io", Resources: []pie.Resource{gadgetResource()}},
					{Path: "pms/terminal.yaml", Doc: 1, Name: "terminal.example.io", Claims: []pie.Claim{
						{Group: "core.example.io", Resource: "widgets", Verbs: []string{"get"}},
						{Resource: "secrets", Verbs: []string{"get"}},
						{Group: "core.example.io", Resource: "gadgets", Verbs: []string{"get"}},
					}},
				},
				Schemas: schemas,
			},
			want: `| APIExport | kind |
|---|---|
| **core.example.io** | Widget |
|  | Gadget |
| **spare.example.io** | Gadget · core.example.io |
| **terminal.example.io** | (no kinds) |
|  | grants: get on widgets from core.example.io, secrets,<br/>gadgets from core.example.io or spare.example.io |

**Worth a look**

- API group core.example.io is served by 2 APIExports: core.example.io, spare.example.io
- APIExport terminal.example.io claims widgets, gadgets from core.example.io
- APIExport terminal.example.io claims gadgets from spare.example.io
`,
		},
		{
			name: "a binding file that accepts less says so on the claims it refuses; WorkspaceTypes grant everything",
			model: pie.Model{
				Exports: []pie.Export{coreClaiming(
					pie.Claim{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}},
					pie.Claim{Group: "", Resource: "secrets", Verbs: []string{"get"}},
					pie.Claim{Group: "", Resource: "configmaps", Verbs: []string{"get"}},
				)},
				Schemas:        schemas,
				WorkspaceTypes: account,
				APIBindings: []pie.APIBinding{
					bindingIn("pms/core.yaml", pie.GroupResource{Group: "tenancy.kcp.io", Resource: "workspaces"}, pie.GroupResource{Resource: "secrets"}),
					bindingIn("features/x/core.yaml"),
				},
			},
			want: `| APIExport | kind | account | APIBindings in<br/>pms | APIBindings in<br/>features/<br/>x |
|---|---|:-:|:-:|:-:|
| **core.example.io** | Widget | ● | ● | ● |
|  | Gadget | ● | ● | ● |
|  | grants: * on workspaces | ● | ● | ●<br/>not workspaces |
|  | grants: get on secrets, configmaps | ● | ●<br/>not configmaps | ●<br/>not secrets, configmaps |

**Worth a look**

- APIExport core.example.io may write kcp's own resources where account and APIBindings in pms bind it: * on workspaces
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderSurface(tt.model)); got != tt.want {
				t.Errorf("RenderSurface() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestRenderSurfaceKeepsGrantsLinesToSixtyCharacters(t *testing.T) {
	c, d := strings.Repeat("c", 22), strings.Repeat("d", 22)
	tests := []struct {
		name   string
		claims []pie.Claim
		want   string
	}{
		{
			name:   "sixty characters fit on one line",
			claims: []pie.Claim{{Resource: strings.Repeat("a", 47), Verbs: []string{"*"}}},
			want:   "grants: * on " + strings.Repeat("a", 47),
		},
		{
			name:   "a longer row breaks after the word that reaches sixty",
			claims: []pie.Claim{{Resource: c, Verbs: []string{"*"}}, {Resource: d, Verbs: []string{"*"}}, {Resource: "e", Verbs: []string{"*"}}},
			want:   "grants: * on " + c + ", " + d + ",<br/>e",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := pie.Model{
				Exports:        []pie.Export{{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io", Claims: tt.claims}},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}}},
			}
			if got := string(pie.RenderSurface(model)); !strings.Contains(got, "|  | "+tt.want+" | ● |") {
				t.Errorf("RenderSurface() =\n%s\nwant a row\n%s", got, tt.want)
			}
		})
	}
}

func TestRenderSurfaceD2DrawsTheTableAsAGridOfCells(t *testing.T) {
	model := pie.Model{
		Exports: []pie.Export{{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource(), gadgetResource()},
			Claims: []pie.Claim{{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}}}}},
		Schemas: []pie.Schema{widgetSchemaAt("pms/widgets.yaml"), gadgetSchemaAt("pms/gadgets.yaml")},
		WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account",
			Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}, {Path: "root", Name: "tenancy.kcp.io"}}}},
		APIBindings: []pie.APIBinding{{Path: "pms/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}}},
	}
	want := `grid-rows: 5
grid-columns: 4
grid-gap: 0
*.style.font-size: 14
*.style.bold: false
*.style.fill: "#ffffff"
"r0c0": "APIExport" {style.bold: true; style.fill: "#e7ebf7"; label.near: center-left; height: 34}
"r0c1": "kind" {style.bold: true; style.fill: "#e7ebf7"; label.near: center-left; height: 34}
"r0c2": "account" {style.bold: true; style.fill: "#e7ebf7"; height: 34}
"r0c3": "APIBindings in\npms" {style.bold: true; style.fill: "#e7ebf7"}
"r1c0": "core.example.io" {style.bold: true; height: 34}
"r1c1": "Widget" {label.near: center-left; height: 34}
"r1c2": "●" {height: 34}
"r1c3": "●" {height: 34}
"r2c0": "" {height: 34}
"r2c1": "Gadget" {label.near: center-left; height: 34}
"r2c2": "●" {height: 34}
"r2c3": "●" {height: 34}
"r3c0": "" {height: 34}
"r3c1": "grants: * on workspaces" {style.fill: "#e5dbff"; label.near: center-left; height: 34}
"r3c2": "●" {height: 34}
"r3c3": "●\nnot workspaces"
"r4c0": "tenancy.kcp.io" {style.fill: "#f4f6fb"; style.bold: true; height: 34}
"r4c1": "(outside the tree)" {style.fill: "#f4f6fb"; label.near: center-left; height: 34}
"r4c2": "●" {style.fill: "#f4f6fb"; height: 34}
"r4c3": "" {style.fill: "#f4f6fb"; height: 34}
"legend": "grants that can write: a verb other than get, list, watch" {near: bottom-center; style.fill: "#e5dbff"; height: 34}
`
	if got := string(pie.RenderSurfaceD2(model)); got != want {
		t.Errorf("RenderSurfaceD2() =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderSurfaceD2ShadesEveryOtherExportWhole(t *testing.T) {
	export := func(name string, claims ...pie.Claim) pie.Export {
		return pie.Export{Path: "pms/" + name + ".yaml", Doc: 1, Name: name, Claims: claims}
	}
	model := pie.Model{Exports: []pie.Export{
		export("a.example.io"),
		export("b.example.io", pie.Claim{Resource: "secrets", Verbs: []string{"get"}}),
		export("c.example.io"),
	}}
	want := `"r1c0": "a.example.io" {style.bold: true; height: 34}
"r1c1": "(no kinds)" {label.near: center-left; height: 34}
"r2c0": "b.example.io" {style.fill: "#f4f6fb"; style.bold: true; height: 34}
"r2c1": "(no kinds)" {style.fill: "#f4f6fb"; label.near: center-left; height: 34}
"r3c0": "" {style.fill: "#f4f6fb"; height: 34}
"r3c1": "grants: get on secrets" {style.fill: "#f4f6fb"; label.near: center-left; height: 34}
"r4c0": "c.example.io" {style.bold: true; height: 34}
"r4c1": "(no kinds)" {label.near: center-left; height: 34}
`
	if got := string(pie.RenderSurfaceD2(model)); !strings.HasSuffix(got, want) {
		t.Errorf("RenderSurfaceD2() =\n%s\nwant it to end in\n%s", got, want)
	}
}

// d2 centres every line of a label, so a claim that wraps is a cell of one-line children, each on the left.
func TestRenderSurfaceD2KeepsTheLinesOfAWrappedClaimOnTheLeft(t *testing.T) {
	c, d := strings.Repeat("c", 22), strings.Repeat("d", 22)
	model := pie.Model{Exports: []pie.Export{
		{Path: "pms/a.yaml", Doc: 1, Name: "a.example.io"},
		{Path: "pms/b.yaml", Doc: 1, Name: "b.example.io", Claims: []pie.Claim{
			{Resource: c, Verbs: []string{"*"}}, {Resource: d, Verbs: []string{"*"}}, {Resource: "e", Verbs: []string{"*"}},
		}},
	}}
	line := `{style.stroke: transparent; style.fill: transparent; style.font-size: 14; style.bold: false; label.near: center-left; height: 20}`
	want := `"r3c0": "" {style.fill: "#f4f6fb"; height: 34}
"r3c1": "" {
  style.fill: "#e5dbff"
  grid-columns: 1
  grid-gap: 0
  "l0": "grants: * on ` + c + `, ` + d + `," ` + line + `
  "l1": "e" ` + line + `
}
"legend": "grants that can write: a verb other than get, list, watch" {near: bottom-center; style.fill: "#e5dbff"; height: 34}
`
	if got := string(pie.RenderSurfaceD2(model)); !strings.HasSuffix(got, want) {
		t.Errorf("RenderSurfaceD2() =\n%s\nwant it to end in\n%s", got, want)
	}
}

func TestRenderSurfaceD2TintsTheClaimsThatCanWrite(t *testing.T) {
	c, d := strings.Repeat("c", 22), strings.Repeat("d", 22)
	readOnly := pie.Export{Path: "pms/a.yaml", Doc: 1, Name: "a.example.io", Claims: []pie.Claim{{Resource: "configmaps", Verbs: []string{"get", "list", "watch"}}}}
	line := `{style.stroke: transparent; style.fill: transparent; style.font-size: 14; style.bold: false; label.near: center-left; height: 20}`
	tests := []struct {
		name    string
		exports []pie.Export
		want    string
	}{
		{
			name: "a claim with any verb beyond get, list and watch is tinted in its own cell, over the shading, and the legend says why",
			exports: []pie.Export{
				{Path: "pms/a.yaml", Doc: 1, Name: "a.example.io", Claims: []pie.Claim{
					{Resource: "configmaps", Verbs: []string{"get", "list", "watch"}},
					{Resource: "secrets", Verbs: []string{"get", "create"}},
				}},
				{Path: "pms/b.yaml", Doc: 1, Name: "b.example.io", Claims: []pie.Claim{
					{Resource: c, Verbs: []string{"*"}}, {Resource: d, Verbs: []string{"*"}}, {Resource: "e", Verbs: []string{"*"}},
				}},
			},
			want: `"r2c1": "grants: get, list, watch on configmaps" {label.near: center-left; height: 34}
"r3c0": "" {height: 34}
"r3c1": "grants: get, create on secrets" {style.fill: "#e5dbff"; label.near: center-left; height: 34}
"r4c0": "b.example.io" {style.fill: "#f4f6fb"; style.bold: true; height: 34}
"r4c1": "(no kinds)" {style.fill: "#f4f6fb"; label.near: center-left; height: 34}
"r5c0": "" {style.fill: "#f4f6fb"; height: 34}
"r5c1": "" {
  style.fill: "#e5dbff"
  grid-columns: 1
  grid-gap: 0
  "l0": "grants: * on ` + c + `, ` + d + `," ` + line + `
  "l1": "e" ` + line + `
}
"legend": "grants that can write: a verb other than get, list, watch" {near: bottom-center; style.fill: "#e5dbff"; height: 34}
`,
		},
		{
			name: "read-only claims keep the shading, wrapped or not, and need no legend",
			exports: []pie.Export{readOnly, {Path: "pms/b.yaml", Doc: 1, Name: "b.example.io", Claims: []pie.Claim{
				{Resource: c, Verbs: []string{"get", "list", "watch"}}, {Resource: d, Verbs: []string{"get", "list", "watch"}},
			}}},
			want: `"r3c0": "b.example.io" {style.fill: "#f4f6fb"; style.bold: true; height: 34}
"r3c1": "(no kinds)" {style.fill: "#f4f6fb"; label.near: center-left; height: 34}
"r4c0": "" {style.fill: "#f4f6fb"; height: 34}
"r4c1": "" {
  style.fill: "#f4f6fb"
  grid-columns: 1
  grid-gap: 0
  "l0": "grants: get, list, watch on ` + c + `," ` + line + `
  "l1": "` + d + `" ` + line + `
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderSurfaceD2(pie.Model{Exports: tt.exports})); !strings.HasSuffix(got, tt.want) {
				t.Errorf("RenderSurfaceD2() =\n%s\nwant it to end in\n%s", got, tt.want)
			}
		})
	}
}

func TestRenderDiff(t *testing.T) {
	export := func(path, name string) pie.Export {
		return pie.Export{Path: path, Doc: 1, Name: name}
	}
	tests := []struct {
		name string
		a, b pie.Model
		want string
	}{
		{
			name: "the same exports, wherever their files are, are no change",
			a:    pie.Model{Exports: []pie.Export{export("pms/core.yaml", "core.example.io")}},
			b:    pie.Model{Exports: []pie.Export{export("moved/core.yaml", "core.example.io")}},
		},
		{
			name: "exports only in A are removed, those only in B added, each once",
			a: pie.Model{Exports: []pie.Export{
				export("pms/core.yaml", "core.example.io"), export("pms/old.yaml", "old.example.io"),
				export("test/old.yaml", "old.example.io"), export("pms/legacy.yaml", "legacy.example.io"),
			}},
			b: pie.Model{Exports: []pie.Export{
				export("pms/new.yaml", "new.example.io"), export("pms/core.yaml", "core.example.io"), export("test/new.yaml", "new.example.io"),
			}},
			want: `removed: APIExport old.example.io
removed: APIExport legacy.example.io
added: APIExport new.example.io
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderDiff(tt.a, tt.b)); got != tt.want {
				t.Errorf("RenderDiff() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestRenderDiffFollowsKindsAcrossExports(t *testing.T) {
	things := pie.Resource{Group: "old.example.io", Name: "things", Schema: "v1.things.old.example.io"}
	gizmos := pie.Resource{Group: "core.example.io", Name: "gizmos", Schema: "v1.gizmos.core.example.io"}
	gizmoSchema := pie.Schema{Path: "pms/gizmos.yaml", Name: "v1.gizmos.core.example.io", Group: "core.example.io", Kind: "Gizmo", Plural: "gizmos"}
	schemas := []pie.Schema{widgetSchemaAt("pms/widgets.yaml"), gadgetSchemaAt("pms/gadgets.yaml"), gizmoSchema}
	export := func(name string, resources ...pie.Resource) pie.Export {
		return pie.Export{Path: "pms/" + name + ".yaml", Doc: 1, Name: name, Resources: resources}
	}
	tests := []struct {
		name string
		a, b pie.Model
		want string
	}{
		{
			name: "a kind that now sits in another export has moved",
			a:    pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", widgetResource(), gadgetResource())}},
			b:    pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", widgetResource()), export("fga.example.io", gadgetResource())}},
			want: `added: APIExport fga.example.io
moved: kind Gadget.core.example.io from core.example.io to fga.example.io
`,
		},
		{
			name: "a kind new to the tree is added in its export, one gone from it removed from its own",
			a:    pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", widgetResource()), export("old.example.io", things, gadgetResource())}},
			b:    pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", widgetResource(), gizmos)}},
			want: `removed: APIExport old.example.io
removed: kind things.old.example.io from old.example.io
removed: kind Gadget.core.example.io from old.example.io
added: kind Gizmo.core.example.io in core.example.io
`,
		},
		{
			name: "a kind one more export serves is added there, one fewer removed there",
			a: pie.Model{Schemas: schemas, Exports: []pie.Export{
				export("core.example.io", widgetResource()), export("spare.example.io", widgetResource(), gadgetResource()),
			}},
			b: pie.Model{Schemas: schemas, Exports: []pie.Export{
				export("core.example.io", widgetResource()), export("spare.example.io", gadgetResource()), export("extra.example.io", gadgetResource()),
			}},
			want: `added: APIExport extra.example.io
removed: kind Widget.core.example.io from spare.example.io
added: kind Gadget.core.example.io in extra.example.io
`,
		},
		{
			name: "a kind that several exports served moves from all of them, copies once",
			a: pie.Model{Schemas: schemas, Exports: []pie.Export{
				export("a.example.io", widgetResource()), export("b.example.io", widgetResource()),
				{Path: "test/a.yaml", Doc: 1, Name: "a.example.io", Resources: []pie.Resource{widgetResource()}},
			}},
			b: pie.Model{Schemas: schemas, Exports: []pie.Export{export("a.example.io"), export("b.example.io"), export("c.example.io", widgetResource())}},
			want: `added: APIExport c.example.io
moved: kind Widget.core.example.io from a.example.io and b.example.io to c.example.io
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderDiff(tt.a, tt.b)); got != tt.want {
				t.Errorf("RenderDiff() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestRenderSurfaceDiffD2(t *testing.T) {
	schemas := []pie.Schema{widgetSchemaAt("pms/widgets.yaml"), gadgetSchemaAt("pms/gadgets.yaml")}
	export := func(name string, resources []pie.Resource, claims ...pie.Claim) pie.Export {
		return pie.Export{Path: "pms/" + name + ".yaml", Doc: 1, Name: name, Resources: resources, Claims: claims}
	}
	typeBinding := func(name string, exports ...string) pie.WorkspaceType {
		var refs []pie.ExportRef
		for _, e := range exports {
			refs = append(refs, pie.ExportRef{Path: "root:pms", Name: e})
		}
		return pie.WorkspaceType{Path: "types/" + name + ".yaml", Doc: 1, Name: name, Binds: refs}
	}

	t.Run("a kind that moved shows in both exports, and each use case says whether it still gets it", func(t *testing.T) {
		a := pie.Model{Schemas: schemas,
			Exports:        []pie.Export{export("core.example.io", []pie.Resource{widgetResource(), gadgetResource()})},
			WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "core.example.io"), typeBinding("org", "core.example.io"), typeBinding("legacy", "core.example.io")},
		}
		b := pie.Model{Schemas: schemas,
			Exports: []pie.Export{export("core.example.io", []pie.Resource{widgetResource()}), export("fga.example.io", []pie.Resource{gadgetResource()})},
			WorkspaceTypes: []pie.WorkspaceType{
				typeBinding("account", "core.example.io", "fga.example.io"), typeBinding("org", "core.example.io"), typeBinding("team", "fga.example.io"),
			},
		}
		want := `grid-rows: 4
grid-columns: 6
grid-gap: 0
*.style.font-size: 14
*.style.bold: false
*.style.fill: "#ffffff"
"r0c0": "APIExport" {style.bold: true; style.fill: "#e7ebf7"; label.near: center-left; height: 34}
"r0c1": "kind" {style.bold: true; style.fill: "#e7ebf7"; label.near: center-left; height: 34}
"r0c2": "account" {style.bold: true; style.fill: "#e7ebf7"; height: 34}
"r0c3": "org" {style.bold: true; style.fill: "#e7ebf7"; height: 34}
"r0c4": "team" {style.bold: true; style.fill: "#c5f6fa"; height: 34}
"r0c5": "legacy" {style.bold: true; style.fill: "#eebefa"; height: 34}
"r1c0": "core.example.io" {style.bold: true; height: 34}
"r1c1": "Widget" {label.near: center-left; height: 34}
"r1c2": "●" {height: 34}
"r1c3": "●" {height: 34}
"r1c4": "" {height: 34}
"r1c5": "○" {style.fill: "#eebefa"; height: 34}
"r2c0": "" {height: 34}
"r2c1": "Gadget · moved to fga.example.io" {style.fill: "#eebefa"; label.near: center-left; height: 34}
"r2c2": "" {height: 34}
"r2c3": "" {height: 34}
"r2c4": "" {height: 34}
"r2c5": "" {height: 34}
"r3c0": "fga.example.io" {style.fill: "#c5f6fa"; style.bold: true; height: 34}
"r3c1": "Gadget · core.example.io · moved from core.example.io" {style.fill: "#c5f6fa"; label.near: center-left; height: 34}
"r3c2": "●" {style.fill: "#f4f6fb"; height: 34}
"r3c3": "○" {style.fill: "#eebefa"; height: 34}
"r3c4": "+●" {style.fill: "#c5f6fa"; height: 34}
"r3c5": "○" {style.fill: "#eebefa"; height: 34}
"legend": "" {
  near: bottom-center
  grid-rows: 1
  grid-gap: 12
  style.stroke: transparent
  style.fill: transparent
  "added": "only in B, +● gained" {style.fill: "#c5f6fa"; style.font-size: 14; style.bold: false; height: 34}
  "removed": "only in A, ○ lost" {style.fill: "#eebefa"; style.font-size: 14; style.bold: false; height: 34}
}
`
		if got := string(pie.RenderSurfaceDiffD2(a, b)); got != want {
			t.Errorf("RenderSurfaceDiffD2() =\n%s\nwant\n%s", got, want)
		}
	})

	t.Run("an export gone with its kind and claims, claims that are new, and what a binding file refuses", func(t *testing.T) {
		things := pie.Resource{Group: "old.example.io", Name: "things", Schema: "v1.things.old.example.io"}
		a := pie.Model{Schemas: schemas,
			Exports: []pie.Export{
				export("core.example.io", []pie.Resource{widgetResource()}),
				export("old.example.io", []pie.Resource{things}, pie.Claim{Resource: "secrets", Verbs: []string{"get"}}),
			},
			WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "core.example.io", "old.example.io")},
		}
		b := pie.Model{Schemas: schemas,
			Exports:        []pie.Export{export("core.example.io", []pie.Resource{widgetResource()}, pie.Claim{Resource: "configmaps", Verbs: []string{"get"}})},
			WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "core.example.io")},
			APIBindings:    []pie.APIBinding{{Path: "pms/core-binding.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}}},
		}
		got := string(pie.RenderSurfaceDiffD2(a, b))
		for _, line := range []string{
			`"r0c3": "APIBindings in\npms" {style.bold: true; style.fill: "#c5f6fa"}`,
			`"r1c3": "+●" {style.fill: "#c5f6fa"; height: 34}`,
			`"r2c1": "grants: get on configmaps" {style.fill: "#c5f6fa"; label.near: center-left; height: 34}`,
			`"r2c2": "+●" {style.fill: "#c5f6fa"; height: 34}`,
			`"r2c3": "+●\nnot configmaps" {style.fill: "#c5f6fa"}`,
			`"r3c0": "old.example.io" {style.fill: "#eebefa"; style.bold: true; height: 34}`,
			`"r3c1": "things · not in tree" {style.fill: "#eebefa"; label.near: center-left; height: 34}`,
			`"r3c2": "○" {style.fill: "#eebefa"; height: 34}`,
			`"r4c1": "grants: get on secrets" {style.fill: "#eebefa"; label.near: center-left; height: 34}`,
			`"r4c2": "○" {style.fill: "#eebefa"; height: 34}`,
			`"r4c3": "" {style.fill: "#f4f6fb"; height: 34}`,
		} {
			if !strings.Contains(got, "\n"+line+"\n") {
				t.Errorf("RenderSurfaceDiffD2() =\n%s\nwant a line\n%s", got, line)
			}
		}
	})

	t.Run("a kind one export more or fewer serves is no move, and a dot stays with the export bound", func(t *testing.T) {
		writes := pie.Claim{Resource: "secrets", Verbs: []string{"*"}}
		a := pie.Model{Schemas: schemas,
			Exports: []pie.Export{
				export("core.example.io", []pie.Resource{gadgetResource()}, writes),
				export("spare.example.io", []pie.Resource{gadgetResource(), widgetResource()}),
			},
			WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "core.example.io", "spare.example.io")},
		}
		b := pie.Model{Schemas: schemas,
			Exports: []pie.Export{
				export("core.example.io", []pie.Resource{gadgetResource(), widgetResource()}, writes),
				export("spare.example.io", []pie.Resource{widgetResource()}),
			},
			WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "core.example.io", "spare.example.io"), typeBinding("team", "core.example.io")},
		}
		got := string(pie.RenderSurfaceDiffD2(a, b))
		for _, line := range []string{
			`"r1c3": "+●" {style.fill: "#c5f6fa"; height: 34}`,
			`"r2c1": "Widget" {style.fill: "#c5f6fa"; label.near: center-left; height: 34}`,
			`"r2c2": "●" {height: 34}`,
			`"r2c3": "+●" {style.fill: "#c5f6fa"; height: 34}`,
			`"r3c1": "grants: * on secrets" {label.near: center-left; height: 34}`,
			`"r4c1": "Widget · core.example.io" {style.fill: "#f4f6fb"; label.near: center-left; height: 34}`,
			`"r4c3": "" {style.fill: "#f4f6fb"; height: 34}`,
			`"r5c1": "Gadget · core.example.io" {style.fill: "#eebefa"; label.near: center-left; height: 34}`,
			`"r5c2": "" {style.fill: "#f4f6fb"; height: 34}`,
			`"r5c3": "" {style.fill: "#f4f6fb"; height: 34}`,
		} {
			if !strings.Contains(got, "\n"+line+"\n") {
				t.Errorf("RenderSurfaceDiffD2() =\n%s\nwant a line\n%s", got, line)
			}
		}
		if strings.Contains(got, "#e5dbff") {
			t.Errorf("RenderSurfaceDiffD2() =\n%s\nwant no write tint in a diff", got)
		}
	})

	t.Run("a kind gone for good is lost under the export each use case had bound", func(t *testing.T) {
		gizmos := pie.Resource{Group: "core.example.io", Name: "gizmos", Schema: "v1.gizmos.core.example.io"}
		types := []pie.WorkspaceType{typeBinding("account", "core.example.io"), typeBinding("team", "spare.example.io")}
		a := pie.Model{Schemas: schemas, WorkspaceTypes: types, Exports: []pie.Export{
			export("core.example.io", []pie.Resource{widgetResource(), gizmos}), export("spare.example.io", []pie.Resource{gadgetResource(), gizmos}),
		}}
		b := pie.Model{Schemas: schemas, WorkspaceTypes: types, Exports: []pie.Export{
			export("core.example.io", []pie.Resource{widgetResource()}), export("spare.example.io", []pie.Resource{gadgetResource()}),
		}}
		got := string(pie.RenderSurfaceDiffD2(a, b))
		for _, line := range []string{
			`"r2c1": "gizmos · not in tree" {style.fill: "#eebefa"; label.near: center-left; height: 34}`,
			`"r2c2": "○" {style.fill: "#eebefa"; height: 34}`,
			`"r2c3": "" {height: 34}`,
			`"r4c2": "" {style.fill: "#f4f6fb"; height: 34}`,
			`"r4c3": "○" {style.fill: "#eebefa"; height: 34}`,
		} {
			if !strings.Contains(got, "\n"+line+"\n") {
				t.Errorf("RenderSurfaceDiffD2() =\n%s\nwant a line\n%s", got, line)
			}
		}
	})

	t.Run("a use case that is new but binds nothing is a change too", func(t *testing.T) {
		a := pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", []pie.Resource{widgetResource()})},
			WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "core.example.io")}}
		b := a
		b.WorkspaceTypes = append(slices.Clone(a.WorkspaceTypes), typeBinding("security"))

		if got := string(pie.RenderSurfaceDiffD2(a, b)); !strings.Contains(got, `"r0c3": "security" {style.bold: true; style.fill: "#c5f6fa"; height: 34}`) || !strings.Contains(got, "\n\"legend\": ") {
			t.Errorf("RenderSurfaceDiffD2() =\n%s\nwant security in cyan and the legend", got)
		}
	})

	t.Run("a tree against itself is the plain table", func(t *testing.T) {
		m := pie.Model{Schemas: schemas,
			Exports:        []pie.Export{export("core.example.io", []pie.Resource{widgetResource(), gadgetResource()}, pie.Claim{Resource: "secrets", Verbs: []string{"get"}})},
			WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "core.example.io"), typeBinding("security")},
		}
		if got, want := string(pie.RenderSurfaceDiffD2(m, m)), string(pie.RenderSurfaceD2(m)); got != want {
			t.Errorf("RenderSurfaceDiffD2(m, m) =\n%s\nwant RenderSurfaceD2(m)\n%s", got, want)
		}
	})
}

func TestRenderD2ExplainsTheRelationsItDraws(t *testing.T) {
	tests := []struct {
		name  string
		model pie.Model
		want  string
	}{
		{
			name: "the legend lists relations in a fixed order, whichever is drawn first",
			model: pie.Model{WorkspaceTypes: []pie.WorkspaceType{
				{Path: "types/account.yaml", Doc: 1, Name: "account", Extends: []pie.TypeRef{{Name: "base"}}},
				{Path: "types/base.yaml", Doc: 1, Name: "base", Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}}},
			}},
			want: `"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account · extends base"
  "tenancy.kcp.io": "root · via base"
}
"workspacetype:types/base.yaml#1": {
  shape: sql_table
  label: "WorkspaceType base"
  "tenancy.kcp.io": "root"
}
"outside": {
  label: "APIExports outside the tree"
  style.stroke-dash: 3
  "tenancy.kcp.io @ root"
}
"workspacetype:types/account.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root" {style.stroke: "#1971c2"; style.stroke-dash: 2}
"workspacetype:types/base.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root" {style.stroke: "#1971c2"}
` + legend(legendBindsByDefault, legendBindsThroughExtend),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderD2(tt.model)); got != elkLayout+tt.want {
				t.Errorf("RenderD2() =\n%s\nwant\n%s", got, elkLayout+tt.want)
			}
		})
	}
}

// elkLayout opens every diagram: ELK routes the edges between the use cases and the exports
// at right angles, which reads better on a page than d2's default layout.
const elkLayout = `vars: {
  d2-config: {
    layout-engine: elk
  }
}
`

// legend is the box a diagram ends with, drawing one example edge per relation it shows.
func legend(entries ...string) string {
	return `"legend": {
  label: "Legend"
  near: bottom-left
  style.stroke-dash: 2
  style.fill: transparent
  *.style.font-size: 12
` + strings.Join(entries, "") + "}\n"
}

const (
	legendBindsByDefault = `  "default:from": "WorkspaceType"
  "default:to": "APIExport"
  "default:from" -> "default:to": "binds by default" {style.stroke: "#1971c2"}
`
	legendBindsThroughExtend = `  "extend:from": "WorkspaceType"
  "extend:to": "APIExport"
  "extend:from" -> "extend:to": "binds through extend" {style.stroke: "#1971c2"; style.stroke-dash: 2}
`
	legendBindsByAPIBinding = `  "apibinding:from": "APIBinding file"
  "apibinding:to": "APIExport"
  "apibinding:from" -> "apibinding:to": "binds" {style.stroke: "#2f9e44"}
`
	legendBindsRefused = `  "refused:from": "use case"
  "refused:to": "APIExport"
  "refused:from" -> "refused:to": "conflicts" {style.stroke: "#e03131"; style.stroke-width: 3}
`
	legendShared = `  "shared": "APIExport with a resource another export also serves" {style.fill: "#f08c00"}
`
	legendClaims = `  "claims:from": "APIExport"
  "claims:to": "claimed kind"
  "claims:from" -> "claims:to": "claims" {style.stroke: "#7048e8"; style.stroke-dash: 4}
`
)

func withClaims(e pie.Export, claims ...pie.Claim) pie.Export {
	e.Claims = claims
	return e
}

func widgetResource() pie.Resource {
	return pie.Resource{Group: "core.example.io", Name: "widgets", Schema: "v1.widgets.core.example.io"}
}

func gadgetResource() pie.Resource {
	return pie.Resource{Group: "core.example.io", Name: "gadgets", Schema: "v1.gadgets.core.example.io"}
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

func withNames(ars *apisv1alpha1.APIResourceSchema, singular, listKind string, shortNames ...string) *apisv1alpha1.APIResourceSchema {
	ars.Spec.Names.Singular, ars.Spec.Names.ListKind, ars.Spec.Names.ShortNames = singular, listKind, shortNames
	return ars
}

func workspace(name string, of *tenancyv1alpha1.WorkspaceTypeReference) *tenancyv1alpha1.Workspace {
	return &tenancyv1alpha1.Workspace{
		TypeMeta:   metav1.TypeMeta{APIVersion: tenancyv1alpha1.SchemeGroupVersion.String(), Kind: "Workspace"},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       tenancyv1alpha1.WorkspaceSpec{Type: of},
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

func apiBindingV1alpha1(export string) *apisv1alpha1.APIBinding {
	return &apisv1alpha1.APIBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: apisv1alpha1.SchemeGroupVersion.String(), Kind: "APIBinding"},
		ObjectMeta: metav1.ObjectMeta{Name: export},
		Spec: apisv1alpha1.APIBindingSpec{
			Reference: apisv1alpha1.BindingReference{
				Export: &apisv1alpha1.ExportBindingReference{Name: export, Path: "root:providers"},
			},
			PermissionClaims: []apisv1alpha1.AcceptablePermissionClaim{{
				PermissionClaim: apisv1alpha1.PermissionClaim{
					GroupResource: apisv1alpha1.GroupResource{Group: "tenancy.kcp.io", Resource: "workspaces"},
					All:           true,
				},
				State: apisv1alpha1.ClaimAccepted,
			}},
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

// overviewHead is how every overview starts: dagre's layout, columns to the left, and the look of
// each kind of box.
const overviewHead = `vars: {
  d2-config: {
    layout-engine: dagre
  }
}
direction: left
classes: {
  column: {style: {fill: transparent; stroke: transparent; font-size: 22; bold: true; font-color: "#868e96"}}
  group: {style: {fill: "#f1f3f5"; stroke: "#ced4da"; font-size: 15; border-radius: 6}}
  kind: {style: {fill: "#ffffff"; stroke: "#adb5bd"; font-size: 14; border-radius: 4}}
  export: {style: {fill: "#ffffff"; stroke: "#364fc7"; stroke-width: 2; font-size: 16; bold: true; border-radius: 6}}
  outside: {style: {fill: transparent; stroke: "#868e96"; stroke-dash: 3; font-size: 14}}
  outside-export: {style: {fill: "#f8f9fa"; stroke: "#868e96"; stroke-dash: 3; font-size: 14; border-radius: 6}}
  workspacetype: {style: {fill: "#e7f5ff"; stroke: "#1971c2"; font-size: 15; border-radius: 6}}
  apibindings: {style: {fill: "#ebfbee"; stroke: "#2f9e44"; font-size: 15; border-radius: 6}}
  claimed: {style: {fill: transparent; stroke: "#adb5bd"; stroke-dash: 3; font-size: 15; border-radius: 6}}
  claimed-resource: {style: {fill: "#f8f9fa"; stroke: "#adb5bd"; stroke-dash: 3; font-size: 14; border-radius: 4; font-color: "#495057"}}
}
`

// overviewLegend is the legend under the overview, where it adds no width.
func overviewLegend(entries ...string) string {
	return strings.Replace(legend(entries...), "near: bottom-left", "near: bottom-center", 1)
}

const (
	legendServes = `  "serves:from": "APIExport"
  "serves:to": "kind"
  "serves:from" -> "serves:to": "serves" {style.stroke: "#868e96"}
`
	legendSharedKind = `  "shared": "kind more than one APIExport serves" {style.fill: "#f08c00"}
`
	legendReaches = `  "reaches:from": "APIExport"
  "reaches:to": "what it claims"
  "reaches:from" -> "reaches:to": "claims, through its virtual workspace: read, write or all verbs" {style.stroke: "#7048e8"; style.stroke-dash: 4}
`
)

func TestRenderOverviewD2(t *testing.T) {
	core := pie.Export{Path: "pms/export.yaml", Doc: 1, Name: "core.example.io"}
	exportOf := func(path, name string, resources ...pie.Resource) pie.Export {
		return pie.Export{Path: path, Doc: 1, Name: name, Resources: resources}
	}
	tests := []struct {
		name  string
		model pie.Model
		want  string
	}{
		{
			name: "an empty tree draws nothing but the layout and the looks",
		},
		{
			name:  "exports nothing binds stand alone, without BOUND AT and without a legend",
			model: pie.Model{Exports: []pie.Export{core}},
			want: `"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:pms/export.yaml#1": "core.example.io" {class: export}
}
`,
		},
		{
			name: "a WorkspaceType in BOUND AT points at the exports it binds",
			model: pie.Model{
				Exports: []pie.Export{core},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account",
					Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}}},
			},
			want: `"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:pms/export.yaml#1": "core.example.io" {class: export}
}
"bound-at": {
  label: "BOUND AT"
  class: column
  "workspacetype:types/account.yaml#1": "account" {class: workspacetype}
}
"bound-at"."workspacetype:types/account.yaml#1" -> "apiexports"."apiexport:pms/export.yaml#1" {style.stroke: "#1971c2"}
` + overviewLegend(legendBindsByDefault),
		},
		{
			name: "bindings through extend are dashed, and a directory of APIBinding files binds in green",
			model: pie.Model{
				Exports: []pie.Export{core},
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account", Extends: []pie.TypeRef{{Name: "security"}}},
					{Path: "types/security.yaml", Doc: 1, Name: "security", Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}},
				},
				APIBindings: []pie.APIBinding{{Path: "kcp/01/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}}},
			},
			want: `"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:pms/export.yaml#1": "core.example.io" {class: export}
}
"bound-at": {
  label: "BOUND AT"
  class: column
  "workspacetype:types/account.yaml#1": "account" {class: workspacetype}
  "workspacetype:types/security.yaml#1": "security" {class: workspacetype}
  "apibindings:kcp/01": "APIBindings in kcp/01" {class: apibindings}
}
"bound-at"."workspacetype:types/account.yaml#1" -> "apiexports"."apiexport:pms/export.yaml#1" {style.stroke: "#1971c2"; style.stroke-dash: 2}
"bound-at"."workspacetype:types/security.yaml#1" -> "apiexports"."apiexport:pms/export.yaml#1" {style.stroke: "#1971c2"}
"bound-at"."apibindings:kcp/01" -> "apiexports"."apiexport:pms/export.yaml#1" {style.stroke: "#2f9e44"}
` + overviewLegend(legendBindsByDefault, legendBindsThroughExtend, legendBindsByAPIBinding),
		},
		{
			name: "bindings kcp refuses together are red and name the exports they conflict with",
			model: pie.Model{
				Exports: []pie.Export{
					exportOf("ws/a.yaml", "a.example.io", widgetResource(), gadgetResource()),
					exportOf("ws/b.yaml", "b.example.io", widgetResource()),
					exportOf("ws/c.yaml", "c.example.io", gadgetResource()),
					exportOf("ws/d.yaml", "d.example.io"),
				},
				Schemas: []pie.Schema{gadgetSchemaAt("ws/gadgets.yaml"), widgetSchemaAt("ws/widgets.yaml")},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{
					{Path: "root:pms", Name: "a.example.io"}, {Path: "root:pms", Name: "b.example.io"},
					{Path: "root:pms", Name: "c.example.io"}, {Path: "root:pms", Name: "d.example.io"},
				}}},
			},
			want: `"resources": {
  label: "RESOURCES"
  class: column
  "group:core.example.io": {
    label: "core.example.io"
    class: group
    "widgets.core.example.io": "Widget" {class: kind; style.fill: "#f08c00"}
    "gadgets.core.example.io": "Gadget" {class: kind; style.fill: "#f08c00"}
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:ws/a.yaml#1": "a.example.io" {class: export}
  "apiexport:ws/b.yaml#1": "b.example.io" {class: export}
  "apiexport:ws/c.yaml#1": "c.example.io" {class: export}
  "apiexport:ws/d.yaml#1": "d.example.io" {class: export}
}
"bound-at": {
  label: "BOUND AT"
  class: column
  "workspacetype:types/account.yaml#1": "account" {class: workspacetype}
}
"apiexports"."apiexport:ws/a.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/a.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/b.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/c.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
"bound-at"."workspacetype:types/account.yaml#1" -> "apiexports"."apiexport:ws/a.yaml#1": "conflicts with b.example.io, c.example.io" {style.stroke: "#e03131"; style.stroke-width: 3}
"bound-at"."workspacetype:types/account.yaml#1" -> "apiexports"."apiexport:ws/b.yaml#1": "conflicts with a.example.io" {style.stroke: "#e03131"; style.stroke-width: 3}
"bound-at"."workspacetype:types/account.yaml#1" -> "apiexports"."apiexport:ws/c.yaml#1": "conflicts with a.example.io" {style.stroke: "#e03131"; style.stroke-width: 3}
"bound-at"."workspacetype:types/account.yaml#1" -> "apiexports"."apiexport:ws/d.yaml#1" {style.stroke: "#1971c2"}
` + overviewLegend(legendBindsByDefault, legendBindsRefused, legendServes, legendSharedKind),
		},
		{
			name: "an export that clashes on several resources is named once",
			model: pie.Model{
				Exports: []pie.Export{
					exportOf("ws/a.yaml", "a.example.io", widgetResource(), gadgetResource()),
					exportOf("ws/b.yaml", "b.example.io", widgetResource(), gadgetResource()),
				},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{
					{Path: "root:pms", Name: "a.example.io"}, {Path: "root:pms", Name: "b.example.io"},
				}}},
			},
			want: `"resources": {
  label: "RESOURCES"
  class: column
  "group:core.example.io": {
    label: "core.example.io"
    class: group
    "widgets.core.example.io": "widgets · not in tree" {class: kind; style.fill: "#f08c00"}
    "gadgets.core.example.io": "gadgets · not in tree" {class: kind; style.fill: "#f08c00"}
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:ws/a.yaml#1": "a.example.io" {class: export}
  "apiexport:ws/b.yaml#1": "b.example.io" {class: export}
}
"bound-at": {
  label: "BOUND AT"
  class: column
  "workspacetype:types/account.yaml#1": "account" {class: workspacetype}
}
"apiexports"."apiexport:ws/a.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/a.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/b.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/b.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
"bound-at"."workspacetype:types/account.yaml#1" -> "apiexports"."apiexport:ws/a.yaml#1": "conflicts with b.example.io" {style.stroke: "#e03131"; style.stroke-width: 3}
"bound-at"."workspacetype:types/account.yaml#1" -> "apiexports"."apiexport:ws/b.yaml#1": "conflicts with a.example.io" {style.stroke: "#e03131"; style.stroke-width: 3}
` + overviewLegend(legendBindsRefused, legendServes, legendSharedKind),
		},
		{
			name: "an export outside the tree is drawn once, in a dashed box among the exports",
			model: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}, {Name: "local.example.io"}}},
					{Path: "types/org.yaml", Doc: 1, Name: "org", Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}}},
				},
			},
			want: `"apiexports": {
  label: "APIEXPORTS"
  class: column
  "outside": {
    label: "outside the tree"
    class: outside
    "tenancy.kcp.io @ root": {class: outside-export}
    "local.example.io @ (own workspace)": {class: outside-export}
  }
}
"bound-at": {
  label: "BOUND AT"
  class: column
  "workspacetype:types/account.yaml#1": "account" {class: workspacetype}
  "workspacetype:types/org.yaml#1": "org" {class: workspacetype}
}
"bound-at"."workspacetype:types/account.yaml#1" -> "apiexports"."outside"."tenancy.kcp.io @ root" {style.stroke: "#1971c2"}
"bound-at"."workspacetype:types/account.yaml#1" -> "apiexports"."outside"."local.example.io @ (own workspace)" {style.stroke: "#1971c2"}
"bound-at"."workspacetype:types/org.yaml#1" -> "apiexports"."outside"."tenancy.kcp.io @ root" {style.stroke: "#1971c2"}
` + overviewLegend(legendBindsByDefault),
		},
		{
			name: "copies of an export or a WorkspaceType say where they are written, and a binding points at every copy",
			model: pie.Model{
				Exports: []pie.Export{
					{Path: "a/export.yaml", Doc: 1, Name: "core.example.io"},
					{Path: "b/export.yaml", Doc: 1, Name: "core.example.io"},
				},
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "a/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}},
					{Path: "b/account.yaml", Doc: 1, Name: "account"},
				},
			},
			want: `"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:a/export.yaml#1": "core.example.io · a/export.yaml#1" {class: export}
  "apiexport:b/export.yaml#1": "core.example.io · b/export.yaml#1" {class: export}
}
"bound-at": {
  label: "BOUND AT"
  class: column
  "workspacetype:a/account.yaml#1": "account · a/account.yaml#1" {class: workspacetype}
  "workspacetype:b/account.yaml#1": "account · b/account.yaml#1" {class: workspacetype}
}
"bound-at"."workspacetype:a/account.yaml#1" -> "apiexports"."apiexport:a/export.yaml#1" {style.stroke: "#1971c2"}
"bound-at"."workspacetype:a/account.yaml#1" -> "apiexports"."apiexport:b/export.yaml#1" {style.stroke: "#1971c2"}
` + overviewLegend(legendBindsByDefault),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderOverviewD2(tt.model)); got != overviewHead+tt.want {
				t.Errorf("RenderOverviewD2() =\n%s\nwant\n%s", got, overviewHead+tt.want)
			}
		})
	}
}

func TestRenderOverviewD2DrawsTheKindsExportsServe(t *testing.T) {
	uiResource := pie.Resource{Group: "ui.example.io", Name: "panels", Schema: "v1.panels.ui.example.io"}
	panelSchema := pie.Schema{Path: "ws/panels.yaml", Name: "v1.panels.ui.example.io", Group: "ui.example.io", Kind: "Panel", Plural: "panels"}
	tests := []struct {
		name  string
		model pie.Model
		want  string
	}{
		{
			name: "each kind stands once in RESOURCES, in a box of its API group, pointed at by the export serving it",
			model: pie.Model{
				Exports: []pie.Export{
					{Path: "ws/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource(), uiResource, gadgetResource()}},
				},
				Schemas: []pie.Schema{widgetSchemaAt("ws/widgets.yaml"), gadgetSchemaAt("ws/gadgets.yaml"), panelSchema},
			},
			want: `"resources": {
  label: "RESOURCES"
  class: column
  "group:core.example.io": {
    label: "core.example.io"
    class: group
    "widgets.core.example.io": "Widget" {class: kind}
    "gadgets.core.example.io": "Gadget" {class: kind}
  }
  "group:ui.example.io": {
    label: "ui.example.io"
    class: group
    "panels.ui.example.io": "Panel" {class: kind}
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:ws/core.yaml#1": "core.example.io" {class: export}
}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:ui.example.io"."panels.ui.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
` + overviewLegend(legendServes),
		},
		{
			name: "a kind two exports serve, as in a migration, is drawn once, orange, with an arrow from each",
			model: pie.Model{
				Exports: []pie.Export{
					{Path: "ws/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource(), uiResource}},
					{Path: "ws/ui.yaml", Doc: 1, Name: "ui.example.io", Resources: []pie.Resource{uiResource}},
				},
				Schemas: []pie.Schema{widgetSchemaAt("ws/widgets.yaml"), panelSchema},
			},
			want: `"resources": {
  label: "RESOURCES"
  class: column
  "group:core.example.io": {
    label: "core.example.io"
    class: group
    "widgets.core.example.io": "Widget" {class: kind}
  }
  "group:ui.example.io": {
    label: "ui.example.io"
    class: group
    "panels.ui.example.io": "Panel" {class: kind; style.fill: "#f08c00"}
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:ws/core.yaml#1": "core.example.io" {class: export}
  "apiexport:ws/ui.yaml#1": "ui.example.io" {class: export}
}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:ui.example.io"."panels.ui.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/ui.yaml#1" -> "resources"."group:ui.example.io"."panels.ui.example.io" {style.stroke: "#868e96"}
` + overviewLegend(legendServes, legendSharedKind),
		},
		{
			name: "a kind is named by the schema next to its export, when copies of the schema are elsewhere too",
			model: pie.Model{
				Exports: []pie.Export{{Path: "ws/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource()}}},
				Schemas: []pie.Schema{
					{Path: "old/widgets.yaml", Name: "v1.widgets.core.example.io", Group: "core.example.io", Kind: "OldWidget", Plural: "widgets"},
					widgetSchemaAt("ws/widgets.yaml"),
				},
			},
			want: `"resources": {
  label: "RESOURCES"
  class: column
  "group:core.example.io": {
    label: "core.example.io"
    class: group
    "widgets.core.example.io": "Widget" {class: kind}
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:ws/core.yaml#1": "core.example.io" {class: export}
}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
` + overviewLegend(legendServes),
		},
		{
			name: "copies of one export serving a kind do not make it orange",
			model: pie.Model{
				Exports: []pie.Export{coreExportAt("a/export.yaml"), coreExportAt("b/export.yaml")},
				Schemas: []pie.Schema{widgetSchemaAt("a/widgets.yaml"), gadgetSchemaAt("a/gadgets.yaml")},
			},
			want: `"resources": {
  label: "RESOURCES"
  class: column
  "group:core.example.io": {
    label: "core.example.io"
    class: group
    "widgets.core.example.io": "Widget" {class: kind}
    "gadgets.core.example.io": "Gadget" {class: kind}
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:a/export.yaml#1": "core.example.io · a/export.yaml#1" {class: export}
  "apiexport:b/export.yaml#1": "core.example.io · b/export.yaml#1" {class: export}
}
"apiexports"."apiexport:a/export.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:a/export.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:b/export.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:b/export.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
` + overviewLegend(legendServes),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderOverviewD2(tt.model)); got != overviewHead+tt.want {
				t.Errorf("RenderOverviewD2() =\n%s\nwant\n%s", got, overviewHead+tt.want)
			}
		})
	}
}

func TestRenderOverviewD2DrawsWhatClaimsReach(t *testing.T) {
	claim := func(group, resource string, verbs ...string) pie.Claim {
		return pie.Claim{Group: group, Resource: resource, Verbs: verbs}
	}
	tests := []struct {
		name  string
		model pie.Model
		want  string
	}{
		{
			name: "a claim on a kind the tree serves points at the kind, or at its group's box when the claims take all of it",
			model: pie.Model{
				Exports: []pie.Export{
					coreExportAt("ws/core.yaml"),
					withClaims(pie.Export{Path: "ws/reader.yaml", Doc: 1, Name: "reader.example.io"}, claim("core.example.io", "widgets", "get", "list", "watch")),
					withClaims(pie.Export{Path: "ws/admin.yaml", Doc: 1, Name: "admin.example.io"},
						claim("core.example.io", "widgets", "*"), claim("core.example.io", "gadgets", "*")),
				},
				Schemas: []pie.Schema{widgetSchemaAt("ws/widgets.yaml"), gadgetSchemaAt("ws/gadgets.yaml")},
			},
			want: `"resources": {
  label: "RESOURCES"
  class: column
  "group:core.example.io": {
    label: "core.example.io"
    class: group
    "widgets.core.example.io": "Widget" {class: kind}
    "gadgets.core.example.io": "Gadget" {class: kind}
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:ws/core.yaml#1": "core.example.io" {class: export}
  "apiexport:ws/reader.yaml#1": "reader.example.io" {class: export}
  "apiexport:ws/admin.yaml#1": "admin.example.io" {class: export}
}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/reader.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}
"apiexports"."apiexport:ws/admin.yaml#1" -> "resources"."group:core.example.io": "all verbs" {style.stroke: "#7048e8"; style.stroke-dash: 4}
` + overviewLegend(legendServes, legendReaches),
		},
		{
			name: "what no export in the tree serves stands dashed, built in or from an export outside the tree as its claim names an identity or not",
			model: pie.Model{
				Exports: []pie.Export{
					withClaims(pie.Export{Path: "ws/core.yaml", Doc: 1, Name: "core.example.io"},
						pie.Claim{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}, FromExport: true},
						pie.Claim{Group: "tenancy.kcp.io", Resource: "workspacetypes", Verbs: []string{"*"}, FromExport: true},
						claim("apis.kcp.io", "apibindings", "*"),
						pie.Claim{Group: "", Resource: "secrets", Verbs: []string{"get"}, Selected: true},
						claim("", "configmaps", "get", "list"),
						claim("rbac.authorization.k8s.io", "roles", "get", "create"),
						claim("rbac.authorization.k8s.io", "rolebindings", "get", "create"),
					),
					withClaims(pie.Export{Path: "ws/other.yaml", Doc: 1, Name: "other.example.io"}, claim("", "configmaps", "watch")),
				},
			},
			want: `"resources": {
  label: "RESOURCES"
  class: column
  "exported": {
    label: "from an APIExport outside the tree"
    class: claimed
    "group:tenancy.kcp.io": {
      label: "tenancy.kcp.io"
      class: claimed
      grid-rows: 1
      grid-gap: 12
      "workspaces.tenancy.kcp.io": "workspaces" {class: claimed-resource}
      "workspacetypes.tenancy.kcp.io": "workspacetypes" {class: claimed-resource}
    }
  }
  "builtin": {
    label: "built-in · in every workspace"
    class: claimed
    "group:apis.kcp.io": {
      label: "apis.kcp.io"
      class: claimed
      grid-rows: 1
      grid-gap: 12
      "apibindings.apis.kcp.io": "apibindings" {class: claimed-resource}
    }
    "group:": {
      label: "core"
      class: claimed
      grid-rows: 1
      grid-gap: 12
      "secrets": "secrets" {class: claimed-resource}
      "configmaps": "configmaps" {class: claimed-resource}
    }
    "group:rbac.authorization.k8s.io": {
      label: "rbac.authorization.k8s.io"
      class: claimed
      grid-rows: 1
      grid-gap: 12
      "roles.rbac.authorization.k8s.io": "roles" {class: claimed-resource}
      "rolebindings.rbac.authorization.k8s.io": "rolebindings" {class: claimed-resource}
    }
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:ws/core.yaml#1": "core.example.io" {class: export}
  "apiexport:ws/other.yaml#1": "other.example.io" {class: export}
}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."exported": "all verbs" {style.stroke: "#7048e8"; style.stroke-dash: 4}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:apis.kcp.io": "all verbs" {style.stroke: "#7048e8"; style.stroke-dash: 4}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."secrets": "read · selected" {style.stroke: "#7048e8"; style.stroke-dash: 4}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."configmaps": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:rbac.authorization.k8s.io": "write" {style.stroke: "#7048e8"; style.stroke-dash: 4}
"apiexports"."apiexport:ws/other.yaml#1" -> "resources"."builtin"."group:"."configmaps": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}
` + overviewLegend(legendReaches),
		},
		{
			name: "a claimed resource the tree does not serve, of a Kubernetes group it does serve, stands dashed in that group's box",
			model: pie.Model{
				Exports: []pie.Export{
					{Path: "ws/gateway.yaml", Doc: 1, Name: "gateway.example.io", Resources: []pie.Resource{{Group: "gateway.networking.k8s.io", Name: "gateways"}}},
					withClaims(pie.Export{Path: "ws/other.yaml", Doc: 1, Name: "other.example.io"}, pie.Claim{Group: "gateway.networking.k8s.io", Resource: "httproutes", Verbs: []string{"get"}, FromExport: true}),
				},
			},
			want: `"resources": {
  label: "RESOURCES"
  class: column
  "group:gateway.networking.k8s.io": {
    label: "gateway.networking.k8s.io"
    class: group
    "gateways.gateway.networking.k8s.io": "gateways · not in tree" {class: kind}
    "httproutes.gateway.networking.k8s.io": "httproutes" {class: claimed-resource}
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:ws/gateway.yaml#1": "gateway.example.io" {class: export}
  "apiexport:ws/other.yaml#1": "other.example.io" {class: export}
}
"apiexports"."apiexport:ws/gateway.yaml#1" -> "resources"."group:gateway.networking.k8s.io"."gateways.gateway.networking.k8s.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/other.yaml#1" -> "resources"."group:gateway.networking.k8s.io"."httproutes.gateway.networking.k8s.io": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}
` + overviewLegend(legendServes, legendReaches),
		},
		{
			name: "a claimed resource the tree does not serve, of a group it does serve, stands dashed in that group's box",
			model: pie.Model{
				Exports: []pie.Export{
					{Path: "ws/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource()}},
					withClaims(pie.Export{Path: "ws/other.yaml", Doc: 1, Name: "other.example.io"}, pie.Claim{Group: "core.example.io", Resource: "sprockets", Verbs: []string{"get", "update"}, FromExport: true}),
				},
				Schemas: []pie.Schema{widgetSchemaAt("ws/widgets.yaml")},
			},
			want: `"resources": {
  label: "RESOURCES"
  class: column
  "group:core.example.io": {
    label: "core.example.io"
    class: group
    "widgets.core.example.io": "Widget" {class: kind}
    "sprockets.core.example.io": "sprockets" {class: claimed-resource}
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:ws/core.yaml#1": "core.example.io" {class: export}
  "apiexport:ws/other.yaml#1": "other.example.io" {class: export}
}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/other.yaml#1" -> "resources"."group:core.example.io"."sprockets.core.example.io": "write" {style.stroke: "#7048e8"; style.stroke-dash: 4}
` + overviewLegend(legendServes, legendReaches),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderOverviewD2(tt.model)); got != overviewHead+tt.want {
				t.Errorf("RenderOverviewD2() =\n%s\nwant\n%s", got, overviewHead+tt.want)
			}
		})
	}
}
