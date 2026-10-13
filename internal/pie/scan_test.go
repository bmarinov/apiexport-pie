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
			name: "draws a v1alpha1 APIExport the same as its v1alpha2 equivalent, and records that it is v1alpha1",
			fsys: fstest.MapFS{
				"export.yaml": manifest(t, apiExportV1alpha1("core.example.io", "v1.widgets.core.example.io", "v1.gadgets.core.example.io")),
			},
			want: pie.Model{Exports: []pie.Export{withV1alpha1(withClaims(coreExportAt("export.yaml"), pie.Claim{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}}))}},
		},
		{
			name: "puts a v1alpha1 schema of the core group in the empty group, as kcp does",
			fsys: fstest.MapFS{
				"export.yaml": manifest(t, apiExportV1alpha1("legacy.example.io", "v1.configmaps.core")),
			},
			want: pie.Model{Exports: []pie.Export{withV1alpha1(withClaims(pie.Export{Path: "export.yaml", Doc: 1, Name: "legacy.example.io", Resources: []pie.Resource{
				{Group: "", Name: "configmaps", Schema: "v1.configmaps.core"},
			}}, pie.Claim{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}}))}},
		},
		{
			name: "records what an export claims, which claims a default selector narrows, which name an export's identity, and which write its hash out",
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
				{Group: "core.example.io", Resource: "accountinfos", Verbs: []string{"get", "list", "watch"}, FromExport: true, LiteralHash: true},
				{Group: "", Resource: "secrets", Verbs: []string{"*"}, Selected: true},
				{Group: "", Resource: "configmaps", Verbs: []string{"get"}},
			}}}},
		},
		{
			name: "records whether an export names the secret its identity comes from, in either version; without one kcp generates the identity",
			fsys: fstest.MapFS{
				"a.yaml": {Data: []byte("apiVersion: apis.kcp.io/v1alpha2\nkind: APIExport\nmetadata:\n  name: a.example.io\nspec:\n  identity:\n    secretRef:\n      name: a-identity\n      namespace: kcp-system\n")},
				"b.yaml": {Data: []byte("apiVersion: apis.kcp.io/v1alpha2\nkind: APIExport\nmetadata:\n  name: b.example.io\nspec:\n  identity: {}\n")},
				"c.yaml": {Data: []byte("apiVersion: apis.kcp.io/v1alpha1\nkind: APIExport\nmetadata:\n  name: c.example.io\nspec:\n  identity:\n    secretRef:\n      name: c-identity\n      namespace: kcp-system\n")},
			},
			want: pie.Model{Exports: []pie.Export{
				{Path: "a.yaml", Doc: 1, Name: "a.example.io", IdentitySecret: true},
				{Path: "b.yaml", Doc: 1, Name: "b.example.io"},
				{Path: "c.yaml", Doc: 1, Name: "c.example.io", IdentitySecret: true, V1alpha1: true},
			}},
		},
		{
			name: "records the maximal permission policy an export sets, in either version; kcp knows one kind, local, and accepts no policy without it",
			fsys: fstest.MapFS{
				"a.yaml": {Data: []byte("apiVersion: apis.kcp.io/v1alpha2\nkind: APIExport\nmetadata:\n  name: a.example.io\nspec:\n  maximalPermissionPolicy:\n    local: {}\n")},
				"b.yaml": {Data: []byte("apiVersion: apis.kcp.io/v1alpha2\nkind: APIExport\nmetadata:\n  name: b.example.io\nspec:\n  maximalPermissionPolicy: {}\n")},
				"c.yaml": {Data: []byte("apiVersion: apis.kcp.io/v1alpha1\nkind: APIExport\nmetadata:\n  name: c.example.io\nspec:\n  maximalPermissionPolicy:\n    local: {}\n")},
			},
			want: pie.Model{Exports: []pie.Export{
				{Path: "a.yaml", Doc: 1, Name: "a.example.io", MaximalPermissionPolicy: "local"},
				{Path: "b.yaml", Doc: 1, Name: "b.example.io"},
				{Path: "c.yaml", Doc: 1, Name: "c.example.io", MaximalPermissionPolicy: "local", V1alpha1: true},
			}},
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
			name: "records whether kcp keeps a WorkspaceType's default bindings in its existing workspaces too (Maintain); unset is InitializeOnly",
			fsys: fstest.MapFS{
				"init.yaml": manifest(t, func() *tenancyv1alpha1.WorkspaceType {
					wt := workspaceType("init", nil, nil)
					mode := tenancyv1alpha1.APIBindingLifecycleModeInitializeOnly
					wt.Spec.DefaultAPIBindingLifecycle = &mode
					return wt
				}()),
				"maintain.yaml": manifest(t, func() *tenancyv1alpha1.WorkspaceType {
					wt := workspaceType("maintain", nil, nil)
					mode := tenancyv1alpha1.APIBindingLifecycleModeMaintain
					wt.Spec.DefaultAPIBindingLifecycle = &mode
					return wt
				}()),
				"unset.yaml": manifest(t, workspaceType("unset", nil, nil)),
			},
			want: pie.Model{WorkspaceTypes: []pie.WorkspaceType{
				{Path: "init.yaml", Doc: 1, Name: "init"},
				{Path: "maintain.yaml", Doc: 1, Name: "maintain", Maintains: true},
				{Path: "unset.yaml", Doc: 1, Name: "unset"},
			}},
		},
		{
			name: "records whether a WorkspaceType initializes and terminates its workspaces",
			fsys: fstest.MapFS{
				"both.yaml": manifest(t, func() *tenancyv1alpha1.WorkspaceType {
					wt := workspaceType("both", nil, nil)
					wt.Spec.Initializer, wt.Spec.Terminator = true, true
					return wt
				}()),
				"init.yaml": manifest(t, func() *tenancyv1alpha1.WorkspaceType {
					wt := workspaceType("init", nil, nil)
					wt.Spec.Initializer = true
					return wt
				}()),
				"term.yaml": manifest(t, func() *tenancyv1alpha1.WorkspaceType {
					wt := workspaceType("term", nil, nil)
					wt.Spec.Terminator = true
					return wt
				}()),
			},
			want: pie.Model{WorkspaceTypes: []pie.WorkspaceType{
				{Path: "both.yaml", Doc: 1, Name: "both", Initializer: true, Terminator: true},
				{Path: "init.yaml", Doc: 1, Name: "init", Initializer: true},
				{Path: "term.yaml", Doc: 1, Name: "term", Terminator: true},
			}},
		},
		{
			name: "finds the types a WorkspaceType's workspaces may sit in and hold; none listed is none",
			fsys: fstest.MapFS{
				"types.yaml": manifest(t, func() *tenancyv1alpha1.WorkspaceType {
					wt := workspaceType("account", nil, nil)
					wt.Spec.LimitAllowedParents = &tenancyv1alpha1.WorkspaceTypeSelector{Types: []tenancyv1alpha1.WorkspaceTypeReference{{Name: "org", Path: "root"}, {Name: "account", Path: "root"}}}
					wt.Spec.LimitAllowedChildren = &tenancyv1alpha1.WorkspaceTypeSelector{Types: []tenancyv1alpha1.WorkspaceTypeReference{{Name: "project", Path: "root"}}}
					return wt
				}()),
				"leaf.yaml": manifest(t, func() *tenancyv1alpha1.WorkspaceType {
					wt := workspaceType("leaf", nil, nil)
					wt.Spec.LimitAllowedChildren = &tenancyv1alpha1.WorkspaceTypeSelector{None: true}
					return wt
				}()),
			},
			want: pie.Model{WorkspaceTypes: []pie.WorkspaceType{
				{Path: "leaf.yaml", Doc: 1, Name: "leaf"},
				{Path: "types.yaml", Doc: 1, Name: "account",
					Parents:  []pie.TypeRef{{Path: "root", Name: "org"}, {Path: "root", Name: "account"}},
					Children: []pie.TypeRef{{Path: "root", Name: "project"}}},
			}},
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
