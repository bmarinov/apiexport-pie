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

func TestRenderD2DrawsConsumers(t *testing.T) {
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
					{Path: "types/orgs.yaml", Doc: 1, Name: "orgs", Extends: []pie.TypeRef{{Path: "root", Name: "shared"}}},
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
  label: "WorkspaceType orgs · extends shared (not in tree) · workspace: orgs"
}
"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account · workspaces: a, b"
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
  "tenancy.kcp.io @ root (kcp)"
  "local.example.io @ (own workspace)"
}
"workspacetype:types/account.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"}
"workspacetype:types/account.yaml#1"."local.example.io" -> "outside"."local.example.io @ (own workspace)" {style.stroke: "#1971c2"}
"workspacetype:types/org.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"}
` + legend(legendBindsByDefault),
		},
		{
			name: "bindings inherited through extend are listed once, after the type's own, naming the type that declares them",
			model: pie.Model{
				Exports: []pie.Export{coreHere},
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account",
						Binds:   []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}},
						Extends: []pie.TypeRef{{Path: "root", Name: "security"}, {Path: "root", Name: "shared"}}},
					{Path: "types/security.yaml", Doc: 1, Name: "security",
						Binds:   []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}, {Path: "root:audit", Name: "audit.example.io"}},
						Extends: []pie.TypeRef{{Path: "root", Name: "base"}}},
					{Path: "types/base.yaml", Doc: 1, Name: "base",
						Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}}},
				},
			},
			want: `"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account · extends security, shared (not in tree)"
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
  "tenancy.kcp.io @ root (kcp)"
}
"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#1971c2"}
"workspacetype:types/account.yaml#1"."audit.example.io" -> "outside"."audit.example.io @ root:audit" {style.stroke: "#1971c2"; style.stroke-dash: 2}
"workspacetype:types/account.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"; style.stroke-dash: 2}
"workspacetype:types/security.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#1971c2"}
"workspacetype:types/security.yaml#1"."audit.example.io" -> "outside"."audit.example.io @ root:audit" {style.stroke: "#1971c2"}
"workspacetype:types/security.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"; style.stroke-dash: 2}
"workspacetype:types/base.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"}
` + legend(legendBindsByDefault, legendBindsThroughExtend),
		},
		{
			name: "a type extending universal in root binds kcp's tenancy.kcp.io and topology.kcp.io through it",
			model: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/orgs.yaml", Doc: 1, Name: "orgs", Extends: []pie.TypeRef{{Path: "root", Name: "universal"}}}},
			},
			want: `"workspacetype:types/orgs.yaml#1": {
  shape: sql_table
  label: "WorkspaceType orgs · extends universal (kcp)"
  "tenancy.kcp.io": "root · via universal"
  "topology.kcp.io": "root · via universal"
}
"outside": {
  label: "APIExports outside the tree"
  style.stroke-dash: 3
  "tenancy.kcp.io @ root (kcp)"
  "topology.kcp.io @ root (kcp)"
}
"workspacetype:types/orgs.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"; style.stroke-dash: 2}
"workspacetype:types/orgs.yaml#1"."topology.kcp.io" -> "outside"."topology.kcp.io @ root (kcp)" {style.stroke: "#1971c2"; style.stroke-dash: 2}
` + legend(legendBindsThroughExtend),
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
  "tenancy.kcp.io @ root (kcp)"
}
"workspacetype:types/org.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"}
"apibindings:pms"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#2f9e44"}
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
  "tenancy.kcp.io @ root (kcp)"
}
"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#1971c2"}
"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:test/core.yaml#1" {style.stroke: "#1971c2"}
"workspacetype:types/account.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"}
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

func TestCheckWarnsAboutAPIExportsAsWritten(t *testing.T) {
	widgetsOfAll := pie.Claim{Group: "core.example.io", Resource: "widgets", Verbs: []string{"*"}}
	core := pie.Export{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource(), gadgetResource()}}
	ui := pie.Export{Path: "pms/ui.yaml", Doc: 1, Name: "ui.example.io", Resources: []pie.Resource{{Group: "ui.example.io", Name: "panels"}}}
	literal := func(group, resource string) pie.Claim {
		return pie.Claim{Group: group, Resource: resource, Verbs: []string{"get"}, FromExport: true, LiteralHash: true}
	}
	terminal := func(claims ...pie.Claim) pie.Export {
		return pie.Export{Path: "pms/terminal.yaml", Doc: 1, Name: "terminal.example.io", Claims: claims}
	}
	tests := []struct {
		name  string
		model pie.Model
		want  []string
	}{
		{
			name: "an APIExport written as v1alpha1 is a warning, as kcp deprecates the version, and says its claims get all verbs",
			model: pie.Model{Exports: []pie.Export{
				withV1alpha1(withClaims(pie.Export{Path: "ws/system.yaml", Doc: 1, Name: "system.example.io"}, widgetsOfAll)),
				withV1alpha1(pie.Export{Path: "ws/old.yaml", Doc: 2, Name: "old.example.io"}),
				coreExportAt("ws/core.yaml"),
			}},
			want: []string{
				"warning: ws/system.yaml#1: APIExport system.example.io: apis.kcp.io/v1alpha1 is deprecated; claims get verbs *",
				"warning: ws/old.yaml#2: APIExport old.example.io: apis.kcp.io/v1alpha1 is deprecated",
			},
		},
		{
			name: "each copy is warned about where it is written, before the bindings kcp refuses",
			model: pie.Model{
				Exports: []pie.Export{
					withV1alpha1(pie.Export{Path: "a/old.yaml", Doc: 1, Name: "old.example.io", Resources: []pie.Resource{widgetResource()}}),
					withV1alpha1(pie.Export{Path: "b/old.yaml", Doc: 1, Name: "old.example.io", Resources: []pie.Resource{widgetResource()}}),
					pie.Export{Path: "a/new.yaml", Doc: 1, Name: "new.example.io", Resources: []pie.Resource{widgetResource()}},
				},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account",
					Binds: []pie.ExportRef{{Path: "root:pms", Name: "old.example.io"}, {Path: "root:pms", Name: "new.example.io"}}}},
			},
			want: []string{
				"warning: a/old.yaml#1: APIExport old.example.io: apis.kcp.io/v1alpha1 is deprecated",
				"warning: b/old.yaml#1: APIExport old.example.io: apis.kcp.io/v1alpha1 is deprecated",
				"warning: types/account.yaml#1: WorkspaceType account binds old.example.io and new.example.io, which both serve widgets.core.example.io; kcp binds only one of them",
			},
		},
		{
			name: "a claim that writes out the identity hash of an export whose identity kcp generates is a warning: the hash matches one installation at most",
			model: pie.Model{Exports: []pie.Export{core,
				terminal(literal("core.example.io", "widgets"), pie.Claim{Group: "core.example.io", Resource: "gadgets", Verbs: []string{"get"}, FromExport: true}, literal("core.example.io", "gadgets")),
			}},
			want: []string{"warning: pms/terminal.yaml#1: APIExport terminal.example.io: identityHash for widgets, gadgets is a literal; core.example.io's identity differs per installation"},
		},
		{
			name: "an export that names its identity secret has the same identity everywhere, and a literal hash of what the export serves itself is kcp's to ignore",
			model: pie.Model{Exports: []pie.Export{
				{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource()}, IdentitySecret: true, Claims: []pie.Claim{literal("ui.example.io", "panels")}},
				withClaims(ui, literal("ui.example.io", "panels")),
				terminal(literal("core.example.io", "widgets")),
			}},
			want: []string{"warning: pms/core.yaml#1: APIExport core.example.io: identityHash for panels is a literal; ui.example.io's identity differs per installation"},
		},
		{
			name: "an export's warnings stand together, the version first, then a line per export it writes out a hash of",
			model: pie.Model{Exports: []pie.Export{core, ui,
				withV1alpha1(terminal(literal("ui.example.io", "panels"), literal("core.example.io", "widgets"))),
			}},
			want: []string{
				"warning: pms/terminal.yaml#1: APIExport terminal.example.io: apis.kcp.io/v1alpha1 is deprecated; claims get verbs *",
				"warning: pms/terminal.yaml#1: APIExport terminal.example.io: identityHash for panels is a literal; ui.example.io's identity differs per installation",
				"warning: pms/terminal.yaml#1: APIExport terminal.example.io: identityHash for widgets is a literal; core.example.io's identity differs per installation",
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
			want: []string{"notice: types/providers.yaml#1: WorkspaceType providers binds only APIExports from outside the tree: tenancy.kcp.io (kcp), topology.kcp.io (kcp), tenancy.kcp.io"},
		},
		{
			name: "a WorkspaceType that binds only what kcp's universal in root binds",
			model: pie.Model{
				Exports: []pie.Export{core},
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/orgs.yaml", Doc: 1, Name: "orgs", Extends: []pie.TypeRef{{Path: "root", Name: "universal"}}},
					typeBinding("account", inPMS("core.example.io")),
				},
			},
			want: []string{"notice: types/orgs.yaml#1: WorkspaceType orgs binds only kcp's APIExports: tenancy.kcp.io, topology.kcp.io"},
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
			name: "two exports a consumer binds together that claim the same resources, at the second",
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
			name: "an export that claims write on kcp's own resources says where, leaving out binding files that refuse",
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
			want: []string{"notice: pms/a.example.io.yaml#1: APIExport a.example.io claims write on kcp's own resources where account and APIBindings in pms bind it: * on workspaces; update on logicalclusters"},
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
		{
			name: "a claim that writes out the identity hash of an export whose identity kcp generates is a warning, not a notice",
			model: pie.Model{Exports: []pie.Export{
				core,
				export("terminal.example.io", nil,
					pie.Claim{Group: "core.example.io", Resource: "widgets", Verbs: []string{"get"}, FromExport: true, LiteralHash: true},
					pie.Claim{Group: "core.example.io", Resource: "gadgets", Verbs: []string{"get"}, FromExport: true},
				),
			}},
			want: []string{
				"warning: pms/terminal.example.io.yaml#1: APIExport terminal.example.io: identityHash for widgets is a literal; core.example.io's identity differs per installation",
				"notice: pms/terminal.example.io.yaml#1: APIExport terminal.example.io claims widgets, gadgets from core.example.io",
			},
		},
		{
			name: "an export that names its identity secret can have the same identity hash everywhere",
			model: pie.Model{Exports: []pie.Export{
				{Path: "pms/core.example.io.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource()}, IdentitySecret: true},
				export("terminal.example.io", nil, pie.Claim{Group: "core.example.io", Resource: "widgets", Verbs: []string{"get"}, FromExport: true, LiteralHash: true}),
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

func TestRenderSurfaceListsWarningsOverNotices(t *testing.T) {
	model := pie.Model{
		Exports: []pie.Export{withV1alpha1(pie.Export{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource()}})},
		Schemas: []pie.Schema{widgetSchemaAt("pms/widgets.yaml")},
		WorkspaceTypes: []pie.WorkspaceType{
			{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}},
			{Path: "types/security.yaml", Doc: 1, Name: "security"},
		},
	}
	want := `| APIExport | kind | account | security |
|---|---|:-:|:-:|
| **core.example.io** | Widget | ● |  |

**Warnings**

- APIExport core.example.io: apis.kcp.io/v1alpha1 is deprecated

**Notices**

- WorkspaceType security binds no APIExport
`
	if got := string(pie.RenderSurface(model)); got != want {
		t.Errorf("RenderSurface() =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderSurfaceListsNoticesUnderTheTable(t *testing.T) {
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

**Notices**

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
			name: "each export is a block of its kinds, marked for every consumer that binds it, and a kind without its schema says so",
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
			name: "an export APIBinding files bind from outside the tree gets one row, kcp's own in root saying so",
			model: pie.Model{APIBindings: []pie.APIBinding{
				{Path: "pms/tenancy.yaml", Doc: 1, Name: "tenancy", Export: pie.ExportRef{Path: "root", Name: "tenancy.kcp.io"}},
				{Path: "pms/audit.yaml", Doc: 1, Name: "audit", Export: pie.ExportRef{Path: "root:audit", Name: "audit.example.io"}},
			}},
			want: `| APIExport | kind | APIBindings in<br/>pms |
|---|---|:-:|
| **tenancy.kcp.io** | (kcp) | ● |
| **audit.example.io** | (outside the tree) | ● |
`,
		},
		{
			name: "inherited bindings count, and an export outside the tree gets one row, kcp's own in root saying so",
			model: pie.Model{
				Exports: []pie.Export{core},
				Schemas: []pie.Schema{widgetSchemaAt("pms/widgets.yaml"), gadgetSchemaAt("pms/gadgets.yaml")},
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: bindsInPMS("core.example.io"), Extends: []pie.TypeRef{{Name: "base"}}},
					{Path: "types/base.yaml", Doc: 1, Name: "base", Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}, {Path: "root:audit", Name: "audit.example.io"}}},
				},
			},
			want: `| APIExport | kind | account | base |
|---|---|:-:|:-:|
| **core.example.io** | Widget | ● |  |
|  | Gadget | ● |  |
| **tenancy.kcp.io** | (kcp) | ● | ● |
| **audit.example.io** | (outside the tree) | ● | ● |

**Notices**

- WorkspaceType base binds only APIExports from outside the tree: tenancy.kcp.io (kcp), audit.example.io
`,
		},
		{
			name: "a type extending universal in root binds kcp's tenancy.kcp.io and topology.kcp.io",
			model: pie.Model{
				Exports: []pie.Export{core},
				Schemas: []pie.Schema{widgetSchemaAt("pms/widgets.yaml"), gadgetSchemaAt("pms/gadgets.yaml")},
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: bindsInPMS("core.example.io"), Extends: []pie.TypeRef{{Path: "root", Name: "universal"}}},
				},
			},
			want: `| APIExport | kind | account |
|---|---|:-:|
| **core.example.io** | Widget | ● |
|  | Gadget | ● |
| **tenancy.kcp.io** | (kcp) | ● |
| **topology.kcp.io** | (kcp) | ● |
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

**Notices**

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

**Notices**

- APIExport core.example.io claims write on kcp's own resources where account binds it: * on workspaces, apibindings
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

**Notices**

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

**Notices**

- APIExport core.example.io claims write on kcp's own resources where account and APIBindings in pms bind it: * on workspaces
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
"r4c1": "(kcp)" {style.fill: "#f4f6fb"; label.near: center-left; height: 34}
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
			name: "a kind that now sits in another export has moved, and the workspaces binding the old export keep it",
			a:    pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", widgetResource(), gadgetResource())}},
			b:    pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", widgetResource()), export("fga.example.io", gadgetResource())}},
			want: `added: APIExport fga.example.io
moved: kind Gadget.core.example.io from core.example.io to fga.example.io, existing workspaces keep it
added: notice: API group core.example.io is served by 2 APIExports: core.example.io, fga.example.io
`,
		},
		{
			name: "a kind new to the tree is added in its export, one gone from it removed from its own, which says nothing of its bindings when it is gone too",
			a:    pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", widgetResource()), export("old.example.io", things, gadgetResource())}},
			b:    pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", widgetResource(), gizmos)}},
			want: `removed: APIExport old.example.io
removed: kind things.old.example.io from old.example.io
removed: kind Gadget.core.example.io from old.example.io
added: kind Gizmo.core.example.io in core.example.io
removed: notice: APIExport old.example.io serves 2 API groups: old.example.io, core.example.io
removed: notice: API group core.example.io is served by 2 APIExports: core.example.io, old.example.io
`,
		},
		{
			name: "a kind one more export serves is added there, one fewer removed there, where existing workspaces keep it",
			a: pie.Model{Schemas: schemas, Exports: []pie.Export{
				export("core.example.io", widgetResource()), export("spare.example.io", widgetResource(), gadgetResource()),
			}},
			b: pie.Model{Schemas: schemas, Exports: []pie.Export{
				export("core.example.io", widgetResource()), export("spare.example.io", gadgetResource()), export("extra.example.io", gadgetResource()),
			}},
			want: `added: APIExport extra.example.io
removed: kind Widget.core.example.io from spare.example.io, existing workspaces keep it
added: kind Gadget.core.example.io in extra.example.io
changed: notice: API group core.example.io is served by 3 APIExports: core.example.io, spare.example.io, extra.example.io
`,
		},
		{
			name: "a kind two exports stop serving is removed from each, and only the one both trees have keeps it in existing workspaces",
			a: pie.Model{Schemas: schemas, Exports: []pie.Export{
				export("old.example.io", widgetResource()), export("spare.example.io", widgetResource(), gadgetResource()),
			}},
			b: pie.Model{Schemas: schemas, Exports: []pie.Export{export("spare.example.io", gadgetResource())}},
			want: `removed: APIExport old.example.io
removed: kind Widget.core.example.io from old.example.io
removed: kind Widget.core.example.io from spare.example.io, existing workspaces keep it
removed: notice: API group core.example.io is served by 2 APIExports: old.example.io, spare.example.io
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
moved: kind Widget.core.example.io from a.example.io and b.example.io to c.example.io, existing workspaces keep it
removed: notice: API group core.example.io is served by 2 APIExports: a.example.io, b.example.io
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

func TestRenderDiffListsClaims(t *testing.T) {
	claiming := func(path, name string, claims ...pie.Claim) pie.Export {
		return pie.Export{Path: path, Doc: 1, Name: name, Claims: claims}
	}
	claim := func(group, resource string, verbs ...string) pie.Claim {
		return pie.Claim{Group: group, Resource: resource, Verbs: verbs}
	}
	selected := func(c pie.Claim) pie.Claim { c.Selected = true; return c }
	tests := []struct {
		name string
		a, b pie.Model
		want string
	}{
		{
			name: "a claim only in B is added, one only in A removed, with its verbs and whether a selector narrows it; a resource of the core group by its plural, others with their group",
			a: pie.Model{Exports: []pie.Export{claiming("pms/core.yaml", "core.example.io",
				claim("", "secrets", "get", "list"), claim("core.example.io", "widgets", "get"))}},
			b: pie.Model{Exports: []pie.Export{claiming("pms/core.yaml", "core.example.io",
				claim("", "secrets", "get", "list"), selected(claim("", "configmaps", "get", "create")))}},
			want: `removed: claim by core.example.io on widgets.core.example.io: get
added: claim by core.example.io on configmaps: get, create (selected)
`,
		},
		{
			name: "a claim whose verbs or selector change is changed, its verbs compared as a set",
			a: pie.Model{Exports: []pie.Export{claiming("pms/reader.yaml", "reader.example.io",
				claim("core.example.io", "widgets", "get"), claim("", "secrets", "list", "get"), claim("", "events", "*"))}},
			b: pie.Model{Exports: []pie.Export{claiming("pms/reader.yaml", "reader.example.io",
				claim("core.example.io", "widgets", "get", "create"), claim("", "secrets", "get", "list"), selected(claim("", "events", "*")))}},
			want: `changed: claim by reader.example.io on widgets.core.example.io: from get to get, create
changed: claim by reader.example.io on events: from * to * (selected)
`,
		},
		{
			name: "the claims of an export only in A are removed, those of one only in B added; copies of an export count once, as the first",
			a:    pie.Model{Exports: []pie.Export{claiming("pms/old.yaml", "old.example.io", claim("", "secrets", "get"))}},
			b: pie.Model{Exports: []pie.Export{
				claiming("pms/new.yaml", "new.example.io", claim("", "secrets", "get")),
				claiming("test/new.yaml", "new.example.io", claim("", "configmaps", "get")),
			}},
			want: `removed: APIExport old.example.io
added: APIExport new.example.io
removed: claim by old.example.io on secrets: get
added: claim by new.example.io on secrets: get
`,
		},
		{
			name: "claims are told apart by group and resource, a resource claimed twice in an export counts once, as the first; the claim lines come before those of consumers",
			a: pie.Model{Exports: []pie.Export{claiming("pms/core.yaml", "core.example.io",
				claim("rbac.authorization.k8s.io", "roles", "get"), claim("core.example.io", "roles", "get"),
				claim("core.example.io", "widgets", "get"), claim("core.example.io", "widgets", "get", "create"))}},
			b: pie.Model{
				Exports: []pie.Export{claiming("pms/core.yaml", "core.example.io",
					claim("rbac.authorization.k8s.io", "roles", "get"), claim("core.example.io", "roles", "get", "create"),
					claim("core.example.io", "widgets", "get"), claim("core.example.io", "widgets", "get", "create"))},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account"}},
			},
			want: `changed: claim by core.example.io on roles.core.example.io: from get to get, create
added: WorkspaceType account
added: notice: WorkspaceType account binds no APIExport
added: notice: APIExport core.example.io is bound by no WorkspaceType or APIBinding in the tree
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

func TestRenderDiffListsConsumers(t *testing.T) {
	gizmos := pie.Resource{Group: "core.example.io", Name: "gizmos", Schema: "v1.gizmos.core.example.io"}
	gizmoSchema := pie.Schema{Path: "pms/gizmos.yaml", Name: "v1.gizmos.core.example.io", Group: "core.example.io", Kind: "Gizmo", Plural: "gizmos"}
	sprockets := pie.Resource{Group: "core.example.io", Name: "sprockets", Schema: "v1.sprockets.core.example.io"}
	sprocketSchema := pie.Schema{Path: "pms/sprockets.yaml", Name: "v1.sprockets.core.example.io", Group: "core.example.io", Kind: "Sprocket", Plural: "sprockets"}
	schemas := []pie.Schema{widgetSchemaAt("pms/widgets.yaml"), gadgetSchemaAt("pms/gadgets.yaml"), gizmoSchema, sprocketSchema}
	export := func(name string, resources ...pie.Resource) pie.Export {
		return pie.Export{Path: "pms/" + name + ".yaml", Doc: 1, Name: name, Resources: resources}
	}
	binds := func(name, path string, exports ...string) pie.WorkspaceType {
		wt := pie.WorkspaceType{Path: "types/" + name + ".yaml", Doc: 1, Name: name}
		for _, e := range exports {
			wt.Binds = append(wt.Binds, pie.ExportRef{Path: path, Name: e})
		}
		return wt
	}
	binding := func(file, export string) pie.APIBinding {
		return pie.APIBinding{Path: file, Doc: 1, Name: export, Export: pie.ExportRef{Path: "root:pms", Name: export}}
	}
	tests := []struct {
		name string
		a, b pie.Model
		want string
	}{
		{
			name: "a WorkspaceType or directory of APIBinding files only in A is removed, one only in B added with the exports it binds; a directory is no WorkspaceType of the same name",
			a: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{binds("org", "root:pms", "core.example.io"), binds("mixin", "")},
				APIBindings:    []pie.APIBinding{binding("old/dir/core.yaml", "core.example.io")},
			},
			b: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{binds("team", "root:pms", "core.example.io", "tools.example.io"), binds("solo", "")},
				APIBindings:    []pie.APIBinding{binding("new/dir/core.yaml", "core.example.io"), binding("org/core.yaml", "core.example.io")},
			},
			want: `removed: WorkspaceType org
removed: WorkspaceType mixin
removed: APIBinding directory old/dir
added: WorkspaceType team, binding core.example.io, tools.example.io
added: WorkspaceType solo
added: APIBinding directory new/dir, binding core.example.io
added: APIBinding directory org, binding core.example.io
removed: notice: WorkspaceType org binds only APIExports from outside the tree: core.example.io
removed: notice: WorkspaceType mixin binds no APIExport
added: notice: WorkspaceType team binds only APIExports from outside the tree: core.example.io, tools.example.io
added: notice: WorkspaceType solo binds no APIExport
`,
		},
		{
			name: "a binding a WorkspaceType gains reaches its new workspaces only, unless kcp maintains its bindings; one it loses stays in existing workspaces; exports are told apart by name, not path",
			a: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{binds("account", "root:pms", "core.example.io", "old.example.io"), binds("org", "root:pms", "core.example.io")},
				APIBindings:    []pie.APIBinding{binding("d/core.yaml", "core.example.io"), binding("d/old.yaml", "old.example.io")},
			},
			b: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{
					binds("account", "root:other", "core.example.io", "fga.example.io"),
					func() pie.WorkspaceType {
						wt := binds("org", "root:pms", "core.example.io", "fga.example.io")
						wt.Maintains = true
						return wt
					}(),
				},
				APIBindings: []pie.APIBinding{binding("d/core.yaml", "core.example.io"), binding("d/fga.yaml", "fga.example.io")},
			},
			want: `removed: binding of old.example.io by WorkspaceType account, existing workspaces keep it
added: binding of fga.example.io by WorkspaceType account, for new workspaces only
added: binding of fga.example.io by WorkspaceType org, for existing workspaces too
removed: binding of old.example.io in d
added: binding of fga.example.io in d
changed: notice: WorkspaceType account binds only APIExports from outside the tree: core.example.io, fga.example.io
changed: notice: WorkspaceType org binds only APIExports from outside the tree: core.example.io, fga.example.io
`,
		},
		{
			name: "copies of a WorkspaceType count once, as the first; a directory binds an export once, however many of its files bind it",
			a: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{binds("account", "root:pms", "core.example.io")},
				APIBindings:    []pie.APIBinding{binding("d/core.yaml", "core.example.io")},
			},
			b: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{
					binds("account", "root:pms", "core.example.io", "fga.example.io"),
					{Path: "test/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root:pms", Name: "extra.example.io"}}},
				},
				APIBindings: []pie.APIBinding{
					binding("d/core.yaml", "core.example.io"), binding("d/fga.yaml", "fga.example.io"),
					{Path: "d/fga-elsewhere.yaml", Doc: 1, Name: "fga", Export: pie.ExportRef{Path: "root:other", Name: "fga.example.io"}},
				},
			},
			want: `added: binding of fga.example.io by WorkspaceType account, for new workspaces only
added: binding of fga.example.io in d
changed: notice: WorkspaceType account binds only APIExports from outside the tree: core.example.io, fga.example.io
`,
		},
		{
			name: "a consumer both trees have whose kinds change, through the exports it binds and those of the types it extends, loses and gets them; a kind that moved to another export it binds is no change; a consumer only B has is added, not changed",
			a: pie.Model{
				Schemas: schemas,
				Exports: []pie.Export{export("core.example.io", widgetResource(), gadgetResource(), sprockets)},
				WorkspaceTypes: []pie.WorkspaceType{
					binds("base", "root:pms", "core.example.io"),
					{Path: "types/account.yaml", Doc: 1, Name: "account", Extends: []pie.TypeRef{{Path: "root", Name: "base"}}},
					binds("org", "root:pms", "core.example.io"),
				},
				APIBindings: []pie.APIBinding{binding("bind/core.yaml", "core.example.io")},
			},
			b: pie.Model{
				Schemas: schemas,
				Exports: []pie.Export{export("core.example.io", widgetResource(), gizmos), export("fga.example.io", gadgetResource())},
				WorkspaceTypes: []pie.WorkspaceType{
					binds("base", "root:pms", "core.example.io", "fga.example.io"),
					{Path: "types/account.yaml", Doc: 1, Name: "account", Extends: []pie.TypeRef{{Path: "root", Name: "base"}}},
					binds("org", "root:pms", "core.example.io"),
					binds("team", "root:pms", "core.example.io"),
				},
				APIBindings: []pie.APIBinding{binding("bind/core.yaml", "core.example.io")},
			},
			want: `added: APIExport fga.example.io
moved: kind Gadget.core.example.io from core.example.io to fga.example.io, existing workspaces keep it
removed: kind Sprocket.core.example.io from core.example.io, existing workspaces keep it
added: kind Gizmo.core.example.io in core.example.io
added: WorkspaceType team, binding core.example.io
added: binding of fga.example.io by WorkspaceType base, for new workspaces only
changed: WorkspaceType account loses Sprocket.core.example.io; gets Gizmo.core.example.io
changed: WorkspaceType org loses Gadget.core.example.io, Sprocket.core.example.io; gets Gizmo.core.example.io
changed: APIBinding directory bind loses Gadget.core.example.io, Sprocket.core.example.io; gets Gizmo.core.example.io
added: notice: API group core.example.io is served by 2 APIExports: core.example.io, fga.example.io
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

func TestRenderDiffListsWarningsAndNotices(t *testing.T) {
	schemas := []pie.Schema{widgetSchemaAt("pms/widgets.yaml"), gadgetSchemaAt("pms/gadgets.yaml")}
	panels := pie.Resource{Group: "ui.example.io", Name: "panels"}
	bolts := pie.Resource{Group: "billing.example.io", Name: "bolts"}
	sprockets := pie.Resource{Group: "core.example.io", Name: "sprockets"}
	export := func(name string, resources []pie.Resource, claims ...pie.Claim) pie.Export {
		return pie.Export{Path: "pms/" + name + ".yaml", Doc: 1, Name: name, Resources: resources, Claims: claims}
	}
	claim := func(group, resource string) pie.Claim {
		return pie.Claim{Group: group, Resource: resource, Verbs: []string{"get"}}
	}
	literal := func(c pie.Claim) pie.Claim {
		c.FromExport, c.LiteralHash = true, true
		return c
	}
	typeBinding := func(name string, exports ...string) pie.WorkspaceType {
		t := pie.WorkspaceType{Path: "types/" + name + ".yaml", Doc: 1, Name: name}
		for _, e := range exports {
			t.Binds = append(t.Binds, pie.ExportRef{Path: "root:pms", Name: e})
		}
		return t
	}
	core := export("core.example.io", []pie.Resource{widgetResource(), gadgetResource()})
	tests := []struct {
		name string
		a, b pie.Model
		want string
	}{
		{
			name: "after the other changes, a notice only A has is removed and one only B has added, each told apart by what it is about",
			a: pie.Model{Schemas: schemas, Exports: []pie.Export{core, export("old.example.io", []pie.Resource{panels})},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("org", "core.example.io")}},
			b: pie.Model{Schemas: schemas, Exports: []pie.Export{core, export("extra.example.io", []pie.Resource{panels})},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("org", "core.example.io")}},
			want: `removed: APIExport old.example.io
added: APIExport extra.example.io
moved: kind panels.ui.example.io from old.example.io to extra.example.io
removed: notice: APIExport old.example.io is bound by no WorkspaceType or APIBinding in the tree
added: notice: APIExport extra.example.io is bound by no WorkspaceType or APIBinding in the tree
`,
		},
		{
			name: "a notice about the same thing in other words has changed; in the same words it is no change",
			a: pie.Model{Schemas: schemas,
				Exports:        []pie.Export{export("core.example.io", []pie.Resource{widgetResource()}), export("fga.example.io", []pie.Resource{gadgetResource()})},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("org", "core.example.io", "fga.example.io"), typeBinding("empty")}},
			b: pie.Model{Schemas: schemas,
				Exports: []pie.Export{
					export("core.example.io", []pie.Resource{widgetResource()}), export("fga.example.io", []pie.Resource{gadgetResource()}),
					export("sys.example.io", []pie.Resource{sprockets}),
				},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("org", "core.example.io", "fga.example.io", "sys.example.io"), typeBinding("empty")}},
			want: `added: APIExport sys.example.io
added: kind sprockets.core.example.io in sys.example.io
added: binding of sys.example.io by WorkspaceType org, for new workspaces only
changed: WorkspaceType org gets sprockets.core.example.io
changed: notice: API group core.example.io is served by 3 APIExports: core.example.io, fga.example.io, sys.example.io
`,
		},
		{
			name: "a WorkspaceType that bound nothing and now binds only exports from outside the tree has changed",
			a:    pie.Model{WorkspaceTypes: []pie.WorkspaceType{typeBinding("lonely")}},
			b: pie.Model{WorkspaceTypes: []pie.WorkspaceType{
				{Path: "types/lonely.yaml", Doc: 1, Name: "lonely", Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}}},
			}},
			want: `added: binding of tenancy.kcp.io by WorkspaceType lonely, for new workspaces only
changed: notice: WorkspaceType lonely binds only kcp's APIExports: tenancy.kcp.io
`,
		},
		{
			name: "an export that serves another API group has changed",
			a:    pie.Model{Exports: []pie.Export{export("core.example.io", []pie.Resource{widgetResource(), panels})}},
			b:    pie.Model{Exports: []pie.Export{export("core.example.io", []pie.Resource{widgetResource(), panels, bolts})}},
			want: `added: kind bolts.billing.example.io in core.example.io
changed: notice: APIExport core.example.io serves 3 API groups: core.example.io, ui.example.io, billing.example.io
`,
		},
		{
			name: "two exports bound together that claim another resource both have changed",
			a: pie.Model{
				Exports: []pie.Export{
					export("a.example.io", nil, claim("", "secrets"), claim("", "configmaps")), export("b.example.io", nil, claim("", "secrets")),
				},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "a.example.io", "b.example.io")},
			},
			b: pie.Model{
				Exports: []pie.Export{
					export("a.example.io", nil, claim("", "secrets"), claim("", "configmaps")), export("b.example.io", nil, claim("", "secrets"), claim("", "configmaps")),
				},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "a.example.io", "b.example.io")},
			},
			want: `added: claim by b.example.io on configmaps: get
changed: notice: APIExports a.example.io and b.example.io both claim secrets, configmaps where account binds both
`,
		},
		{
			name: "an export that claims write on kcp's own resources in more places has changed",
			a: pie.Model{Exports: []pie.Export{export("a.example.io", nil, pie.Claim{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}})},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "a.example.io")}},
			b: pie.Model{Exports: []pie.Export{export("a.example.io", nil, pie.Claim{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}})},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "a.example.io"), typeBinding("org", "a.example.io")}},
			want: `added: WorkspaceType org, binding a.example.io
changed: notice: APIExport a.example.io claims write on kcp's own resources where account and org bind it: * on workspaces
`,
		},
		{
			name: "a claim on more of what another export serves has changed, and a literal hash only B writes out is added beside it",
			a:    pie.Model{Schemas: schemas, Exports: []pie.Export{core, export("terminal.example.io", nil, claim("core.example.io", "widgets"))}},
			b: pie.Model{Schemas: schemas, Exports: []pie.Export{core,
				export("terminal.example.io", nil, claim("core.example.io", "widgets"), literal(claim("core.example.io", "gadgets")))}},
			want: `added: claim by terminal.example.io on gadgets.core.example.io: get
added: warning: APIExport terminal.example.io: identityHash for gadgets is a literal; core.example.io's identity differs per installation
changed: notice: APIExport terminal.example.io claims widgets, gadgets from core.example.io
`,
		},
		{
			name: "notices of a kind about different exports are told apart: one serving several API groups",
			a:    pie.Model{Exports: []pie.Export{export("x.example.io", []pie.Resource{widgetResource(), panels})}},
			b: pie.Model{Exports: []pie.Export{
				export("x.example.io", []pie.Resource{widgetResource(), panels}),
				export("y.example.io", []pie.Resource{bolts, {Group: "dash.example.io", Name: "dials"}}),
			}},
			want: `added: APIExport y.example.io
added: kind bolts.billing.example.io in y.example.io
added: kind dials.dash.example.io in y.example.io
added: notice: APIExport y.example.io serves 2 API groups: billing.example.io, dash.example.io
`,
		},
		{
			name: "about different API groups: one several exports serve",
			a: pie.Model{Schemas: schemas, Exports: []pie.Export{
				export("x.example.io", []pie.Resource{widgetResource()}), export("z.example.io", []pie.Resource{gadgetResource()}),
			}},
			b: pie.Model{Schemas: schemas, Exports: []pie.Export{
				export("x.example.io", []pie.Resource{widgetResource()}), export("z.example.io", []pie.Resource{gadgetResource()}),
				export("p.example.io", []pie.Resource{panels}), export("q.example.io", []pie.Resource{{Group: "ui.example.io", Name: "sliders"}}),
			}},
			want: `added: APIExport p.example.io
added: APIExport q.example.io
added: kind panels.ui.example.io in p.example.io
added: kind sliders.ui.example.io in q.example.io
added: notice: API group ui.example.io is served by 2 APIExports: p.example.io, q.example.io
`,
		},
		{
			name: "about different pairs of exports: two that claim the same resources",
			a: pie.Model{
				Exports:        []pie.Export{export("a.example.io", nil, claim("", "secrets"), claim("", "configmaps")), export("b.example.io", nil, claim("", "secrets"))},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "a.example.io", "b.example.io", "c.example.io")},
			},
			b: pie.Model{
				Exports: []pie.Export{
					export("a.example.io", nil, claim("", "secrets"), claim("", "configmaps")), export("b.example.io", nil, claim("", "secrets")),
					export("c.example.io", nil, claim("", "configmaps")),
				},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "a.example.io", "b.example.io", "c.example.io")},
			},
			want: `added: APIExport c.example.io
added: claim by c.example.io on configmaps: get
added: notice: APIExports a.example.io and c.example.io both claim configmaps where account binds both
`,
		},
		{
			name: "about different exports: one that claims write on kcp's own resources",
			a: pie.Model{
				Exports:        []pie.Export{export("a.example.io", nil, pie.Claim{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}}), export("b.example.io", nil)},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "a.example.io", "b.example.io")},
			},
			b: pie.Model{
				Exports: []pie.Export{
					export("a.example.io", nil, pie.Claim{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}}),
					export("b.example.io", nil, pie.Claim{Group: "core.kcp.io", Resource: "logicalclusters", Verbs: []string{"update"}}),
				},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "a.example.io", "b.example.io")},
			},
			want: `added: claim by b.example.io on logicalclusters.core.kcp.io: update
added: notice: APIExport b.example.io claims write on kcp's own resources where account binds it: update on logicalclusters
`,
		},
		{
			name: "about different exports claimed from: a claim on what another serves, and one with a literal identity hash",
			a: pie.Model{Schemas: schemas, Exports: []pie.Export{
				core, export("ui.example.io", []pie.Resource{panels}),
				export("terminal.example.io", nil, literal(claim("core.example.io", "widgets"))),
			}},
			b: pie.Model{Schemas: schemas, Exports: []pie.Export{
				core, export("ui.example.io", []pie.Resource{panels}),
				export("terminal.example.io", nil, literal(claim("core.example.io", "widgets")), literal(claim("ui.example.io", "panels"))),
			}},
			want: `added: claim by terminal.example.io on panels.ui.example.io: get
added: warning: APIExport terminal.example.io: identityHash for panels is a literal; ui.example.io's identity differs per installation
added: notice: APIExport terminal.example.io claims panels from ui.example.io
`,
		},
		{
			name: "a warning only A has is removed, and one that reads differently in B is changed",
			a: pie.Model{Schemas: schemas, Exports: []pie.Export{withV1alpha1(core),
				withV1alpha1(export("terminal.example.io", nil))}},
			b: pie.Model{Schemas: schemas, Exports: []pie.Export{core,
				withV1alpha1(export("terminal.example.io", nil, pie.Claim{Resource: "secrets", Verbs: []string{"get"}}))}},
			want: `added: claim by terminal.example.io on secrets: get
removed: warning: APIExport core.example.io: apis.kcp.io/v1alpha1 is deprecated
changed: warning: APIExport terminal.example.io: apis.kcp.io/v1alpha1 is deprecated; claims get verbs *
`,
		},
		{
			name: "bindings kcp refuses only in B are an added warning",
			a: pie.Model{Schemas: schemas, Exports: []pie.Export{core, export("other.example.io", []pie.Resource{widgetResource()})},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "core.example.io"), typeBinding("org", "other.example.io")}},
			b: pie.Model{Schemas: schemas, Exports: []pie.Export{core, export("other.example.io", []pie.Resource{widgetResource()})},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "core.example.io", "other.example.io"), typeBinding("org", "other.example.io")}},
			want: `added: binding of other.example.io by WorkspaceType account, for new workspaces only
added: warning: WorkspaceType account binds core.example.io and other.example.io, which both serve widgets.core.example.io; kcp binds only one of them
`,
		},
		{
			name: "each refusal counts on its own",
			a: pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", []pie.Resource{widgetResource(), gadgetResource()}), export("other.example.io", []pie.Resource{widgetResource(), gadgetResource()})},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "core.example.io"), typeBinding("org", "other.example.io")}},
			b: pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", []pie.Resource{widgetResource(), gadgetResource()}), export("other.example.io", []pie.Resource{widgetResource(), gadgetResource()})},
				WorkspaceTypes: []pie.WorkspaceType{typeBinding("account", "core.example.io", "other.example.io"), typeBinding("org", "other.example.io")}},
			want: `added: binding of other.example.io by WorkspaceType account, for new workspaces only
added: warning: WorkspaceType account binds core.example.io and other.example.io, which both serve widgets.core.example.io; kcp binds only one of them
added: warning: WorkspaceType account binds core.example.io and other.example.io, which both serve gadgets.core.example.io; kcp binds only one of them
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

func TestRenderOverviewDiffD2WrapsTagsToTheTitle(t *testing.T) {
	serving := func(plurals ...string) pie.Model {
		e := pie.Export{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io"}
		for _, p := range plurals {
			e.Resources = append(e.Resources, pie.Resource{Group: "core.example.io", Name: p, Schema: "v1." + p + ".core.example.io"})
		}
		return pie.Model{Exports: []pie.Export{e}, WorkspaceTypes: []pie.WorkspaceType{
			{Path: "types/organizations.yaml", Doc: 1, Name: "organizations"},
			{Path: "types/organization.yaml", Doc: 1, Name: "organization", Parents: []pie.TypeRef{{Path: "root", Name: "organizations"}},
				Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}},
		}}
	}

	got := string(pie.RenderOverviewDiffD2(serving("anchorpoints", "gadgetassemblysets"), serving("boltassemblies", "sprocketassemblies")))

	// The title "organizations › organization" is 28 letters, 30 bytes: tags wrap at 42 characters.
	for _, want := range []string{
		`"lost": "had in A: anchorpoints, gadgetassemblysets" {`,
		`"got": "got in B: boltassemblies,\nsprocketassemblies" {`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderOverviewDiffD2() =\n%s\nwant a line with %s", got, want)
		}
	}
}

func TestRenderOverviewDiffD2SaysWhatWorkspacesATypeAlreadyHasGet(t *testing.T) {
	core := pie.Export{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io"}
	fga := pie.Export{Path: "pms/fga.yaml", Doc: 1, Name: "fga.example.io"}
	wt := func(name string, exports ...string) pie.WorkspaceType {
		t := pie.WorkspaceType{Path: "types/" + name + ".yaml", Doc: 1, Name: name}
		for _, e := range exports {
			t.Binds = append(t.Binds, pie.ExportRef{Path: "root:pms", Name: e})
		}
		return t
	}
	maintained := func(t pie.WorkspaceType) pie.WorkspaceType {
		t.Maintains = true
		return t
	}
	extending := func(t pie.WorkspaceType, base string) pie.WorkspaceType {
		t.Extends = append(t.Extends, pie.TypeRef{Path: "root", Name: base})
		return t
	}
	model := func(exports []pie.Export, types ...pie.WorkspaceType) pie.Model {
		return pie.Model{Exports: exports, WorkspaceTypes: types}
	}
	tests := []struct {
		name string
		a, b pie.Model
		want string
	}{
		{
			name: "a type of both trees that starts binding an export by default gets it only in the workspaces kcp creates from then on",
			a:    model([]pie.Export{core}, wt("org")),
			b:    model([]pie.Export{core}, wt("org", "core.example.io")),
			want: "bound at +org (new workspaces only)",
		},
		{
			name: "unless the type maintains its default bindings",
			a:    model([]pie.Export{core}, wt("org")),
			b:    model([]pie.Export{core}, maintained(wt("org", "core.example.io"))),
			want: "bound at +org",
		},
		{
			name: "a type that stops binding an export leaves its workspaces the binding, maintained or not, as kcp deletes none",
			a:    model([]pie.Export{core}, wt("org", "core.example.io")),
			b:    model([]pie.Export{core}, maintained(wt("org"))),
			want: "bound at −org (existing workspaces keep it)",
		},
		{
			name: "an export only B has, bound by a type of both trees, says so without a sign",
			a:    model([]pie.Export{core}, wt("org")),
			b:    model([]pie.Export{core, fga}, wt("org", "fga.example.io")),
			want: "bound at org (new workspaces only)",
		},
		{
			name: "a binding a type gets through a type it extends follows the lifecycle of the type that declares it, not its own",
			a:    model([]pie.Export{core}, maintained(extending(wt("org"), "base")), wt("base")),
			b:    model([]pie.Export{core}, maintained(extending(wt("org"), "base")), wt("base", "core.example.io")),
			want: "bound at +org (new workspaces only)",
		},
		{
			name: "so a declaring type that maintains its bindings reaches the existing workspaces of the types extending it",
			a:    model([]pie.Export{core}, extending(wt("org"), "base"), wt("base")),
			b:    model([]pie.Export{core}, extending(wt("org"), "base"), maintained(wt("base", "core.example.io"))),
			want: "bound at +org",
		},
		{
			name: "copies of a type count as their first, as in the text diff",
			a:    model([]pie.Export{core}, wt("org")),
			b: model([]pie.Export{core}, wt("org"),
				pie.WorkspaceType{Path: "types/copy.yaml", Doc: 1, Name: "org", Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}}),
			want: "bound at +org",
		},
		{
			name: "an export of one name bound from two workspaces counts once, by the binding a type declares itself before those of its bases",
			a:    model([]pie.Export{core}, extending(wt("org"), "base"), wt("base")),
			b: model([]pie.Export{core}, extending(wt("org", "core.example.io"), "base"),
				maintained(pie.WorkspaceType{Path: "types/base.yaml", Doc: 1, Name: "base", Binds: []pie.ExportRef{{Path: "root:other", Name: "core.example.io"}}})),
			want: "bound at +org (new workspaces only)",
		},
		{
			name: "a binding both trees have is no change, though tree A has its export outside the tree",
			a:    model([]pie.Export{core}, wt("org", "fga.example.io")),
			b:    model([]pie.Export{core, fga}, wt("org", "fga.example.io")),
			want: "bound at org",
		},
		{
			name: "a directory of APIBinding files creates what it binds when it is applied, so says nothing",
			a:    pie.Model{Exports: []pie.Export{core}, APIBindings: []pie.APIBinding{{Path: "x/y/fga.yaml", Doc: 1, Name: "fga", Export: pie.ExportRef{Path: "root:pms", Name: "fga.example.io"}}}},
			b: pie.Model{Exports: []pie.Export{core}, APIBindings: []pie.APIBinding{
				{Path: "x/y/fga.yaml", Doc: 1, Name: "fga", Export: pie.ExportRef{Path: "root:pms", Name: "fga.example.io"}},
				{Path: "x/y/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}},
			}},
			want: "bound at +y",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(pie.RenderOverviewDiffD2(tt.a, tt.b))
			if want := fmt.Sprintf("\"story\": %q {class: story;", tt.want+"\nmaximalPermissionPolicy: none"); !strings.Contains(got, want) {
				t.Errorf("RenderOverviewDiffD2() =\n%s\nwant a line with %s", got, want)
			}
		})
	}
}

func TestRenderOverviewDiffD2SaysWhatAnExportKeepsServingToItsBindings(t *testing.T) {
	schemas := []pie.Schema{widgetSchemaAt("pms/widgets.yaml"), gadgetSchemaAt("pms/gadgets.yaml")}
	things := pie.Resource{Group: "core.example.io", Name: "things"}
	export := func(name string, resources ...pie.Resource) pie.Export {
		return pie.Export{Path: "pms/" + name + ".yaml", Doc: 1, Name: name, Resources: resources}
	}
	org := func(exports ...string) pie.WorkspaceType {
		t := pie.WorkspaceType{Path: "types/org.yaml", Doc: 1, Name: "org"}
		for _, e := range exports {
			t.Binds = append(t.Binds, pie.ExportRef{Path: "root:pms", Name: e})
		}
		return t
	}
	readsGadgets := pie.Claim{Group: "core.example.io", Resource: "gadgets", Verbs: []string{"get"}}
	tests := []struct {
		name string
		a, b pie.Model
		want string
	}{
		{
			name: "an export of both trees keeps serving the kinds it stops serving to the workspaces that bind it already, named and sorted",
			a: pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", widgetResource(), gadgetResource(), things)},
				WorkspaceTypes: []pie.WorkspaceType{org("core.example.io")}},
			b: pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", widgetResource()), export("fga.example.io", gadgetResource())},
				WorkspaceTypes: []pie.WorkspaceType{org("core.example.io")}},
			want: "bound at org\nexisting workspaces keep Gadget, things",
		},
		{
			name: "under where it is bound, before what it needs of other exports",
			a: pie.Model{Schemas: schemas, Exports: []pie.Export{withClaims(export("core.example.io", widgetResource(), gadgetResource()), readsGadgets)},
				WorkspaceTypes: []pie.WorkspaceType{org("core.example.io")}},
			b: pie.Model{Schemas: schemas, Exports: []pie.Export{withClaims(export("core.example.io", widgetResource()), readsGadgets), export("fga.example.io", gadgetResource())},
				WorkspaceTypes: []pie.WorkspaceType{org("core.example.io")}},
			want: "bound at org\nexisting workspaces keep Gadget\n+reads gadgets from fga.example.io",
		},
		{
			name: "an export that moved to another workspace counts what it served where it was",
			a: pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", widgetResource(), gadgetResource())},
				WorkspaceTypes: []pie.WorkspaceType{org("core.example.io")}},
			b: pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", widgetResource())},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/org.yaml", Doc: 1, Name: "org", Binds: []pie.ExportRef{{Path: "root:other", Name: "core.example.io"}}}}},
			want: "bound at org\nexisting workspaces keep Gadget",
		},
		{
			name: "an export only A has says nothing of its kinds, as the tree no longer says whether it exists",
			a: pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", widgetResource()), export("old.example.io", gadgetResource())},
				WorkspaceTypes: []pie.WorkspaceType{org("old.example.io")}},
			b:    pie.Model{Schemas: schemas, Exports: []pie.Export{export("core.example.io", widgetResource())}, WorkspaceTypes: []pie.WorkspaceType{org()}},
			want: "bound at org (existing workspaces keep it)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(pie.RenderOverviewDiffD2(tt.a, tt.b))
			if want := fmt.Sprintf("\"story\": %q {class: story;", tt.want+"\nmaximalPermissionPolicy: none"); !strings.Contains(got, want) {
				t.Errorf("RenderOverviewDiffD2() =\n%s\nwant a line with %s", got, want)
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

	t.Run("a kind that moved shows in both exports, and each consumer says whether it still gets it", func(t *testing.T) {
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

	t.Run("a kind gone for good is lost under the export each consumer had bound", func(t *testing.T) {
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

	t.Run("a consumer that is new but binds nothing is a change too", func(t *testing.T) {
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
  "tenancy.kcp.io @ root (kcp)"
}
"workspacetype:types/account.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"; style.stroke-dash: 2}
"workspacetype:types/base.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"}
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

// elkLayout opens every diagram: ELK routes the edges between the consumers and the exports
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
	legendBindsRefused = `  "refused:from": "consumer"
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

func withV1alpha1(e pie.Export) pie.Export {
	e.V1alpha1 = true
	return e
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
  story: {style: {fill: transparent; stroke: transparent; font-size: 13; bold: false; font-color: "#343a40"}}
  warning: {style: {fill: transparent; stroke: transparent; font-size: 13; bold: true; font-color: "#e8590c"}}
  workspace: {style: {fill: "#f8f9fa"; stroke: "#868e96"; font-size: 15; border-radius: 8}}
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
	legendAPIGroup = `  "api-group": {
    label: "API group"
    class: group
    "kind": "kind" {class: kind}
  }
`
	legendClaimsRead = `  "reads:from": "APIExport"
  "reads:to": "what it claims"
  "reads:from" -> "reads:to": "claims to read, through its virtual workspace" {style.stroke: "#7048e8"; style.stroke-dash: 4}
`
	legendClaimsWrite = `  "writes:from": "APIExport"
  "writes:to": "what it claims"
  "writes:from" -> "writes:to": "claims to write, through its virtual workspace" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 4}
`
	legendClaimsAll = `  "all-verbs:from": "APIExport"
  "all-verbs:to": "what it claims"
  "all-verbs:from" -> "all-verbs:to": "claims all verbs, through its virtual workspace" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 6}
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
			name:  "exports nothing binds stand alone, without CONSUMERS and without a legend",
			model: pie.Model{Exports: []pie.Export{core}},
			want: `"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:pms/export.yaml#1": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
}
`,
		},
		{
			name: "a WorkspaceType in CONSUMERS points at the exports it binds",
			model: pie.Model{
				Exports: []pie.Export{core},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account",
					Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}}},
			},
			want: `"apiexports": {
  label: "APIEXPORTS"
  class: column
  "workspace:root:pms": "Workspace root:pms" {class: workspace}
  "workspace:root:pms"."apiexport:pms/export.yaml#1": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
}
"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:types/account.yaml#1": "account" {class: workspacetype}
}
"consumers"."workspacetype:types/account.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#1971c2"}
` + overviewLegend(legendBindsByDefault),
		},
		{
			name: "an export stands in the box of the workspace the references to it agree on; one they disagree on, or nothing refers to, stands alone",
			model: pie.Model{
				Exports: []pie.Export{
					withClaims(exportOf("pms/core.yaml", "core.example.io", widgetResource()), pie.Claim{Group: "core.example.io", Resource: "widgets", Verbs: []string{"get"}}),
					exportOf("pms/other.yaml", "other.example.io"),
					exportOf("pms/lone.yaml", "lone.example.io"),
				},
				Schemas: []pie.Schema{widgetSchemaAt("pms/widgets.yaml")},
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}, {Path: "root:a", Name: "other.example.io"}}},
					{Path: "types/org.yaml", Doc: 1, Name: "org", Binds: []pie.ExportRef{{Path: "root:b", Name: "other.example.io"}}},
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
  "workspace:root:pms": "Workspace root:pms" {class: workspace}
  "workspace:root:pms"."apiexport:pms/core.yaml#1": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
  "apiexport:pms/other.yaml#1": {
    label: "other.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account, org\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
  "apiexport:pms/lone.yaml#1": {
    label: "lone.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
}
"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:types/account.yaml#1": "account" {class: workspacetype}
  "workspacetype:types/org.yaml#1": "org" {class: workspacetype}
}
"apiexports"."workspace:root:pms"."apiexport:pms/core.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."workspace:root:pms"."apiexport:pms/core.yaml#1" -> "resources"."group:core.example.io": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}
"consumers"."workspacetype:types/account.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#1971c2"}
"consumers"."workspacetype:types/account.yaml#1" -> "apiexports"."apiexport:pms/other.yaml#1" {style.stroke: "#1971c2"}
"consumers"."workspacetype:types/org.yaml#1" -> "apiexports"."apiexport:pms/other.yaml#1" {style.stroke: "#1971c2"}
` + overviewLegend(legendBindsByDefault, legendServes, legendClaimsRead, legendAPIGroup),
		},
		{
			name: "an export's box says where it is bound, each consumer once: a WorkspaceType by its name, a directory of APIBinding files by the shortest end of its path no other such directory shares, as its own box says it",
			model: pie.Model{
				Exports: []pie.Export{{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io"}},
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/a.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}},
					{Path: "types/b.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}},
				},
				APIBindings: []pie.APIBinding{
					{Path: "a/manifests/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}},
					{Path: "b/manifests/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}},
					{Path: "c/kcp/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}},
					{Path: "kcp/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}},
					{Path: "d/e/tools/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}},
				},
			},
			want: `"apiexports": {
  label: "APIEXPORTS"
  class: column
  "workspace:root:pms": "Workspace root:pms" {class: workspace}
  "workspace:root:pms"."apiexport:pms/core.yaml#1": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account, a/manifests, b/manifests, c/kcp, kcp, tools\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
}
"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:types/a.yaml#1": "account · types/a.yaml#1" {class: workspacetype}
  "workspacetype:types/b.yaml#1": "account · types/b.yaml#1" {class: workspacetype}
  "apibindings:a/manifests": "APIBindings in a/manifests" {class: apibindings}
  "apibindings:b/manifests": "APIBindings in b/manifests" {class: apibindings}
  "apibindings:c/kcp": "APIBindings in c/kcp" {class: apibindings}
  "apibindings:kcp": "APIBindings in kcp" {class: apibindings}
  "apibindings:d/e/tools": "APIBindings in tools" {class: apibindings}
}
"consumers"."workspacetype:types/a.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#1971c2"}
"consumers"."workspacetype:types/b.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#1971c2"}
"consumers"."apibindings:a/manifests" -> "apiexports"."workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#2f9e44"}
"consumers"."apibindings:b/manifests" -> "apiexports"."workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#2f9e44"}
"consumers"."apibindings:c/kcp" -> "apiexports"."workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#2f9e44"}
"consumers"."apibindings:kcp" -> "apiexports"."workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#2f9e44"}
"consumers"."apibindings:d/e/tools" -> "apiexports"."workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#2f9e44"}
` + overviewLegend(legendBindsByDefault, legendBindsByAPIBinding),
		},
		{
			name: "bindings through extend are dashed, a type only extended is a tag, not a consumer, and a directory of APIBinding files binds in green",
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
  "workspace:root:pms": "Workspace root:pms" {class: workspace}
  "workspace:root:pms"."apiexport:pms/export.yaml#1": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account, 01\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
}
"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:types/account.yaml#1": "account\nextends security" {class: workspacetype}
  "apibindings:kcp/01": "APIBindings in 01" {class: apibindings}
}
"consumers"."workspacetype:types/account.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#1971c2"; style.stroke-dash: 2}
"consumers"."apibindings:kcp/01" -> "apiexports"."workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#2f9e44"}
` + overviewLegend(legendBindsThroughExtend, legendBindsByAPIBinding),
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
  "workspace:root:pms": "Workspace root:pms" {class: workspace}
  "workspace:root:pms"."apiexport:ws/a.yaml#1": {
    label: "a.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
  "workspace:root:pms"."apiexport:ws/b.yaml#1": {
    label: "b.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
  "workspace:root:pms"."apiexport:ws/c.yaml#1": {
    label: "c.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
  "workspace:root:pms"."apiexport:ws/d.yaml#1": {
    label: "d.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
}
"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:types/account.yaml#1": "account" {class: workspacetype}
}
"apiexports"."workspace:root:pms"."apiexport:ws/a.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."workspace:root:pms"."apiexport:ws/a.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."workspace:root:pms"."apiexport:ws/b.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."workspace:root:pms"."apiexport:ws/c.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
"consumers"."workspacetype:types/account.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:ws/a.yaml#1": "conflicts with b.example.io, c.example.io" {style.stroke: "#e03131"; style.stroke-width: 3}
"consumers"."workspacetype:types/account.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:ws/b.yaml#1": "conflicts with a.example.io" {style.stroke: "#e03131"; style.stroke-width: 3}
"consumers"."workspacetype:types/account.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:ws/c.yaml#1": "conflicts with a.example.io" {style.stroke: "#e03131"; style.stroke-width: 3}
"consumers"."workspacetype:types/account.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:ws/d.yaml#1" {style.stroke: "#1971c2"}
` + overviewLegend(legendBindsByDefault, legendBindsRefused, legendServes, legendAPIGroup, legendSharedKind),
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
  "workspace:root:pms": "Workspace root:pms" {class: workspace}
  "workspace:root:pms"."apiexport:ws/a.yaml#1": {
    label: "a.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
  "workspace:root:pms"."apiexport:ws/b.yaml#1": {
    label: "b.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
}
"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:types/account.yaml#1": "account" {class: workspacetype}
}
"apiexports"."workspace:root:pms"."apiexport:ws/a.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."workspace:root:pms"."apiexport:ws/a.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."workspace:root:pms"."apiexport:ws/b.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."workspace:root:pms"."apiexport:ws/b.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
"consumers"."workspacetype:types/account.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:ws/a.yaml#1": "conflicts with b.example.io" {style.stroke: "#e03131"; style.stroke-width: 3}
"consumers"."workspacetype:types/account.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:ws/b.yaml#1": "conflicts with a.example.io" {style.stroke: "#e03131"; style.stroke-width: 3}
` + overviewLegend(legendBindsRefused, legendServes, legendAPIGroup, legendSharedKind),
		},
		{
			name: "exports outside the tree are a line on each consumer binding them, by workspace, not boxes with arrows; one bound through a base says so",
			model: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}, {Name: "local.example.io"}, {Path: "root", Name: "topology.kcp.io"}}},
					{Path: "types/org.yaml", Doc: 1, Name: "org", Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}},
						Extends: []pie.TypeRef{{Path: "root", Name: "base"}}},
					{Path: "types/base.yaml", Doc: 1, Name: "base", Binds: []pie.ExportRef{{Path: "root:kcp", Name: "shards.kcp.io"}}},
				},
				APIBindings: []pie.APIBinding{{Path: "x/tenancy.yaml", Doc: 1, Name: "tenancy", Export: pie.ExportRef{Path: "root", Name: "tenancy.kcp.io"}}},
			},
			want: `"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:types/account.yaml#1": "account\nbinds tenancy.kcp.io, topology.kcp.io @ root (kcp)\nbinds local.example.io @ (own workspace) (not in tree)" {class: workspacetype}
  "workspacetype:types/org.yaml#1": "org\nextends base\nbinds tenancy.kcp.io @ root (kcp)\nbinds shards.kcp.io @ root:kcp (not in tree, through base)" {class: workspacetype}
  "apibindings:x": "APIBindings in x\nbinds tenancy.kcp.io @ root (kcp)" {class: apibindings}
}
`,
		},
		{
			name: "kcp's own APIExports in root say kcp, the same names in another workspace do not",
			model: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{
					{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{
						{Path: "root", Name: "shards.core.kcp.io"}, {Path: "root:org", Name: "tenancy.kcp.io"}, {Path: "root", Name: "cache.kcp.io"},
						{Path: "root", Name: "local.example.io"}, {Path: "root", Name: "migration.kcp.io"}}},
				},
			},
			want: `"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:types/account.yaml#1": "account\nbinds shards.core.kcp.io, cache.kcp.io, migration.kcp.io @ root (kcp)\nbinds tenancy.kcp.io @ root:org; local.example.io @ root (not in tree)" {class: workspacetype}
}
`,
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
  "workspace:root:pms": "Workspace root:pms" {class: workspace}
  "workspace:root:pms"."apiexport:a/export.yaml#1": {
    label: "core.example.io · a/export.yaml#1"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
  "workspace:root:pms"."apiexport:b/export.yaml#1": {
    label: "core.example.io · b/export.yaml#1"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
}
"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:a/account.yaml#1": "account · a/account.yaml#1" {class: workspacetype}
  "workspacetype:b/account.yaml#1": "account · b/account.yaml#1" {class: workspacetype}
}
"consumers"."workspacetype:a/account.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:a/export.yaml#1" {style.stroke: "#1971c2"}
"consumers"."workspacetype:a/account.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:b/export.yaml#1" {style.stroke: "#1971c2"}
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
  "apiexport:ws/core.yaml#1": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:ui.example.io"."panels.ui.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
` + overviewLegend(legendServes, legendAPIGroup),
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
  "apiexport:ws/core.yaml#1": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
  "apiexport:ws/ui.yaml#1": {
    label: "ui.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:ui.example.io"."panels.ui.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/ui.yaml#1" -> "resources"."group:ui.example.io"."panels.ui.example.io" {style.stroke: "#868e96"}
` + overviewLegend(legendServes, legendAPIGroup, legendSharedKind),
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
  "apiexport:ws/core.yaml#1": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
` + overviewLegend(legendServes, legendAPIGroup),
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
  "apiexport:a/export.yaml#1": {
    label: "core.example.io · a/export.yaml#1"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
  "apiexport:b/export.yaml#1": {
    label: "core.example.io · b/export.yaml#1"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
}
"apiexports"."apiexport:a/export.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:a/export.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:b/export.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:b/export.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
` + overviewLegend(legendServes, legendAPIGroup),
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

func TestRenderOverviewD2SaysWhatAnExportLeavesOpen(t *testing.T) {
	core := coreExportAt("pms/core.yaml")
	terminal := pie.Export{Path: "pms/terminal.yaml", Doc: 1, Name: "terminal.example.io"}
	allOfWidgets := pie.Claim{Group: "core.example.io", Resource: "widgets", Verbs: []string{"*"}}
	withPolicy := func(e pie.Export) pie.Export {
		e.MaximalPermissionPolicy = "local"
		return e
	}
	tests := []struct {
		name  string
		model pie.Model
		want  string
	}{
		{
			name:  "an export without a maximal permission policy says none, bound or not",
			model: pie.Model{Exports: []pie.Export{core}},
			want:  "maximalPermissionPolicy: none",
		},
		{
			name:  "an export with one names its kind",
			model: pie.Model{Exports: []pie.Export{withPolicy(core)}},
			want:  "maximalPermissionPolicy: local",
		},
		{
			name: "the policy comes after where an export is bound and what it needs",
			model: pie.Model{
				Exports: []pie.Export{core, withPolicy(withClaims(terminal, allOfWidgets))},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account",
					Binds: []pie.ExportRef{{Path: "root:pms", Name: "terminal.example.io"}}}},
			},
			want: "bound at account\nall verbs on widgets from core.example.io\nmaximalPermissionPolicy: local",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(pie.RenderOverviewD2(tt.model))
			if want := fmt.Sprintf("\"story\": %q {class: story;", tt.want); !strings.Contains(got, want) {
				t.Errorf("RenderOverviewD2() =\n%s\nwant a line with %s", got, want)
			}
		})
	}
}

func TestRenderOverviewD2WarnsOnTheExportBox(t *testing.T) {
	system := pie.Export{Path: "pms/system.yaml", Doc: 1, Name: "system.example.io"}
	account := pie.WorkspaceType{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root:pms", Name: "system.example.io"}}}
	tests := []struct {
		name  string
		model pie.Model
		want  string
	}{
		{
			name:  "a v1alpha1 export says in orange, right under its story, that kcp deprecates the version and its claims get all verbs",
			model: pie.Model{Exports: []pie.Export{withV1alpha1(withClaims(system, pie.Claim{Resource: "secrets", Verbs: []string{"*"}}))}, WorkspaceTypes: []pie.WorkspaceType{account}},
			want: `  "workspace:root:pms"."apiexport:pms/system.yaml#1": {
    label: "system.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nmaximalPermissionPolicy: none" {class: story; height: 34}
    "warnings": "! apis.kcp.io/v1alpha1 is deprecated; claims get verbs *" {class: warning; height: 17}
  }
`,
		},
		{
			name: "a line per warning, so a claim with a literal identity hash of an export whose identity kcp generates is one more",
			model: pie.Model{Exports: []pie.Export{coreExportAt("pms/core.yaml"),
				withV1alpha1(withClaims(system, pie.Claim{Group: "core.example.io", Resource: "widgets", Verbs: []string{"get"}, FromExport: true, LiteralHash: true}))}},
			want: `    "warnings": "! apis.kcp.io/v1alpha1 is deprecated; claims get verbs *\n! identityHash for widgets is a literal; core.example.io's identity differs per installation" {class: warning; height: 34}`,
		},
		{
			name: "an export in a later document of its file has its own warnings",
			model: pie.Model{Exports: []pie.Export{{Path: "pms/system.yaml", Doc: 1, Name: "first.example.io"},
				withV1alpha1(pie.Export{Path: "pms/system.yaml", Doc: 2, Name: "second.example.io"})}},
			want: `  "apiexport:pms/system.yaml#2": {
    label: "second.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
    "warnings": "! apis.kcp.io/v1alpha1 is deprecated" {class: warning; height: 17}
  }
`,
		},
		{
			name:  "without claims it says only that the version is deprecated",
			model: pie.Model{Exports: []pie.Export{withV1alpha1(system)}},
			want:  `    "warnings": "! apis.kcp.io/v1alpha1 is deprecated" {class: warning; height: 17}`,
		},
		{
			name:  "the legend shows an export box with a warning when one is drawn",
			model: pie.Model{Exports: []pie.Export{withV1alpha1(system)}, WorkspaceTypes: []pie.WorkspaceType{account}},
			want: `  "warning": {
    label: "APIExport"
    class: export
    grid-columns: 1
    grid-gap: 0
    "warnings": "! a warning, also printed on stderr" {class: warning; height: 17}
  }
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderOverviewD2(tt.model)); !strings.Contains(got, tt.want) {
				t.Errorf("RenderOverviewD2() =\n%s\nwant it to hold\n%s", got, tt.want)
			}
		})
	}
}

func TestRenderOverviewDiffD2NamesExportsOutsideTheTreeOnAConsumerWithTags(t *testing.T) {
	core := func(resources ...pie.Resource) pie.Export {
		return pie.Export{Path: "pms/core.yaml", Doc: 1, Name: "core.example.io", Resources: resources}
	}
	account := pie.WorkspaceType{Path: "types/account.yaml", Doc: 1, Name: "account",
		Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}, {Path: "root", Name: "tenancy.kcp.io"}}}
	anchors := pie.Resource{Group: "core.example.io", Name: "anchors"}
	sprockets := pie.Resource{Group: "core.example.io", Name: "sprockets"}
	a := pie.Model{Exports: []pie.Export{core(widgetResource(), gadgetResource(), anchors, sprockets)}, Schemas: []pie.Schema{gadgetSchemaAt("pms/gadgets.yaml")}, WorkspaceTypes: []pie.WorkspaceType{account}}
	b := pie.Model{Exports: []pie.Export{core(widgetResource())}, Schemas: []pie.Schema{gadgetSchemaAt("pms/gadgets.yaml")}, WorkspaceTypes: []pie.WorkspaceType{account}}
	want := `  "workspacetype:account": {
    label: "account\nbinds tenancy.kcp.io @ root (kcp)"
    class: workspacetype
    style.bold: true
    grid-columns: 1
    grid-gap: 4
    "lost": "had in A: Gadget, anchors, sprockets" {`
	if got := string(pie.RenderOverviewDiffD2(a, b)); !strings.Contains(got, want) {
		t.Errorf("RenderOverviewDiffD2() =\n%s\nwant it to hold\n%s", got, want)
	}
}

func TestRenderOverviewDiffD2MarksWhatAnExportLeavesOpenAsItChanges(t *testing.T) {
	core := coreExportAt("pms/core.yaml")
	tests := []struct {
		name string
		a, b pie.Model
		want string
	}{
		{
			name: "a policy B sets is marked as the other lines of the box are",
			a:    pie.Model{Exports: []pie.Export{core}},
			b: pie.Model{Exports: []pie.Export{func() pie.Export {
				e := core
				e.MaximalPermissionPolicy = "local"
				return e
			}()}},
			want: "+maximalPermissionPolicy: local\n−maximalPermissionPolicy: none",
		},
	}
	warnings := []struct {
		name string
		a, b pie.Model
		want string
	}{
		{
			name: "a warning only A has is marked gone, in its orange line",
			a:    pie.Model{Exports: []pie.Export{withV1alpha1(core)}},
			b:    pie.Model{Exports: []pie.Export{core}},
			want: `"warnings": "−! apis.kcp.io/v1alpha1 is deprecated" {class: warning; height: 17}`,
		},
		{
			name: "one only B has is marked new",
			a:    pie.Model{Exports: []pie.Export{core}},
			b:    pie.Model{Exports: []pie.Export{withV1alpha1(core)}},
			want: `"warnings": "+! apis.kcp.io/v1alpha1 is deprecated" {class: warning; height: 17}`,
		},
	}
	for _, tt := range warnings {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderOverviewDiffD2(tt.a, tt.b)); !strings.Contains(got, tt.want) {
				t.Errorf("RenderOverviewDiffD2() =\n%s\nwant a line with %s", got, tt.want)
			}
		})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(pie.RenderOverviewDiffD2(tt.a, tt.b))
			if want := fmt.Sprintf("\"story\": %q {class: story;", tt.want); !strings.Contains(got, want) {
				t.Errorf("RenderOverviewDiffD2() =\n%s\nwant a line with %s", got, want)
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
  "apiexport:ws/core.yaml#1": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
  "apiexport:ws/reader.yaml#1": {
    label: "reader.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "reads widgets from core.example.io\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
  "apiexport:ws/admin.yaml#1": {
    label: "admin.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "all verbs on widgets, gadgets from core.example.io\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/reader.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}
"apiexports"."apiexport:ws/admin.yaml#1" -> "resources"."group:core.example.io" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 6}
` + overviewLegend(legendServes, legendClaimsRead, legendClaimsAll, legendAPIGroup),
		},
		{
			name: "an export's box says what it needs of the kinds other exports serve, a line per export and reach; claims on its own kinds add nothing",
			model: pie.Model{
				Exports: []pie.Export{
					coreExportAt("ws/core.yaml"),
					withClaims(pie.Export{Path: "ws/terminal.yaml", Doc: 1, Name: "terminal.example.io", Resources: []pie.Resource{{Group: "terminal.example.io", Name: "terminals"}}},
						claim("core.example.io", "widgets", "get"),
						claim("terminal.example.io", "terminals", "get"),
						claim("core.example.io", "gadgets", "update"),
					),
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
  "group:terminal.example.io": {
    label: "terminal.example.io"
    class: group
    "terminals.terminal.example.io": "terminals · not in tree" {class: kind}
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:ws/core.yaml#1": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
  "apiexport:ws/terminal.yaml#1": {
    label: "terminal.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "reads widgets from core.example.io\nwrites gadgets from core.example.io\nmaximalPermissionPolicy: none" {class: story; height: 51}
  }
}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/terminal.yaml#1" -> "resources"."group:terminal.example.io"."terminals.terminal.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/terminal.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}
"apiexports"."apiexport:ws/terminal.yaml#1" -> "resources"."group:terminal.example.io": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}
"apiexports"."apiexport:ws/terminal.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io": "write" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 4}
` + overviewLegend(legendServes, legendClaimsRead, legendClaimsWrite, legendAPIGroup),
		},
		{
			name: "the more a claim lets the provider do, the heavier its arrow: read, write, all verbs, which the weight says alone; a selector does not lighten it and is named",
			model: pie.Model{Exports: []pie.Export{withClaims(pie.Export{Path: "ws/core.yaml", Doc: 1, Name: "core.example.io"},
				claim("", "configmaps", "get", "list", "watch"),
				pie.Claim{Group: "", Resource: "secrets", Verbs: []string{"get", "create"}, Selected: true},
				claim("", "serviceaccounts", "*"),
				pie.Claim{Group: "", Resource: "events", Verbs: []string{"*"}, Selected: true},
			)}},
			want: `"resources": {
  label: "RESOURCES"
  class: column
  "builtin": {
    label: "built-in · in every workspace"
    class: claimed
    "group:": {
      label: "core"
      class: claimed
      "configmaps": "configmaps" {class: claimed-resource}
      "secrets": "secrets" {class: claimed-resource}
      "serviceaccounts": "serviceaccounts" {class: claimed-resource}
      "events": "events" {class: claimed-resource}
    }
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:ws/core.yaml#1": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."configmaps": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."secrets": "write · selected" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 4}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."serviceaccounts" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 6}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."events": "selected" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 6}
` + overviewLegend(legendClaimsRead, legendClaimsWrite, legendClaimsAll, legendAPIGroup),
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
  "apiexport:ws/core.yaml#1": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
  "apiexport:ws/other.yaml#1": {
    label: "other.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."exported" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 6}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:apis.kcp.io" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 6}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."secrets": "read · selected" {style.stroke: "#7048e8"; style.stroke-dash: 4}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."configmaps": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:rbac.authorization.k8s.io": "write" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 4}
"apiexports"."apiexport:ws/other.yaml#1" -> "resources"."builtin"."group:"."configmaps": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}
` + overviewLegend(legendClaimsRead, legendClaimsWrite, legendClaimsAll, legendAPIGroup),
		},
		{
			name: "a claimed box an arrow points into, at one of its resources alone, stacks them, which d2 routes the arrow into; one pointed at whole keeps its row",
			model: pie.Model{Exports: []pie.Export{
				withClaims(pie.Export{Path: "ws/core.yaml", Doc: 1, Name: "core.example.io"},
					pie.Claim{Group: "", Resource: "secrets", Verbs: []string{"get", "create"}, Selected: true},
				),
				withClaims(pie.Export{Path: "ws/providers.yaml", Doc: 1, Name: "providers.example.io"},
					claim("", "secrets", "get", "create"),
					claim("", "configmaps", "get", "create"),
					claim("rbac.authorization.k8s.io", "roles", "get"),
				),
			}},
			want: `"resources": {
  label: "RESOURCES"
  class: column
  "builtin": {
    label: "built-in · in every workspace"
    class: claimed
    "group:": {
      label: "core"
      class: claimed
      "secrets": "secrets" {class: claimed-resource}
      "configmaps": "configmaps" {class: claimed-resource}
    }
    "group:rbac.authorization.k8s.io": {
      label: "rbac.authorization.k8s.io"
      class: claimed
      grid-rows: 1
      grid-gap: 12
      "roles.rbac.authorization.k8s.io": "roles" {class: claimed-resource}
    }
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:ws/core.yaml#1": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
  "apiexport:ws/providers.yaml#1": {
    label: "providers.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."secrets": "write · selected" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 4}
"apiexports"."apiexport:ws/providers.yaml#1" -> "resources"."builtin"."group:": "write" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 4}
"apiexports"."apiexport:ws/providers.yaml#1" -> "resources"."builtin"."group:rbac.authorization.k8s.io": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}
` + overviewLegend(legendClaimsRead, legendClaimsWrite, legendAPIGroup),
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
  "apiexport:ws/gateway.yaml#1": {
    label: "gateway.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
  "apiexport:ws/other.yaml#1": {
    label: "other.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
}
"apiexports"."apiexport:ws/gateway.yaml#1" -> "resources"."group:gateway.networking.k8s.io"."gateways.gateway.networking.k8s.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/other.yaml#1" -> "resources"."group:gateway.networking.k8s.io"."httproutes.gateway.networking.k8s.io": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}
` + overviewLegend(legendServes, legendClaimsRead, legendAPIGroup),
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
  "apiexport:ws/core.yaml#1": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
  "apiexport:ws/other.yaml#1": {
    label: "other.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
}
"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:ws/other.yaml#1" -> "resources"."group:core.example.io"."sprockets.core.example.io": "write" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 4}
` + overviewLegend(legendServes, legendClaimsWrite, legendAPIGroup),
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

func TestRenderOverviewD2PlacesConsumersInTheWorkspaceTree(t *testing.T) {
	typ := func(name string, parents, children []string, extends ...string) pie.WorkspaceType {
		wt := pie.WorkspaceType{Path: "types/" + name + ".yaml", Doc: 1, Name: name}
		for _, p := range parents {
			wt.Parents = append(wt.Parents, pie.TypeRef{Path: "root", Name: p})
		}
		for _, c := range children {
			wt.Children = append(wt.Children, pie.TypeRef{Path: "root", Name: c})
		}
		for _, e := range extends {
			wt.Extends = append(wt.Extends, pie.TypeRef{Path: "root", Name: e})
		}
		return wt
	}
	column := func(consumers ...string) string {
		return "\"consumers\": {\n  label: \"CONSUMERS\"\n  class: column\n" + strings.Join(consumers, "") + "}\n"
	}
	consumer := func(name, label string) string {
		return fmt.Sprintf("  %q: %q {class: workspacetype}\n", "workspacetype:types/"+name+".yaml#1", label)
	}
	withLifecycle := func(wt pie.WorkspaceType, initializer, terminator bool) pie.WorkspaceType {
		wt.Initializer, wt.Terminator = initializer, terminator
		return wt
	}
	tests := []struct {
		name  string
		model pie.Model
		want  string
	}{
		{
			name: "a consumer is named by its path in the tree the types' allowed parents and children spell out, either side or both, and listed in tree order from the types with no other parent in the tree",
			model: pie.Model{WorkspaceTypes: []pie.WorkspaceType{
				typ("team", []string{"universal"}, nil),
				typ("folder", []string{"folder"}, nil),
				typ("account", []string{"org", "account"}, nil),
				typ("tools", []string{"orgs", "providers"}, nil),
				typ("provider", []string{"providers"}, nil),
				typ("org", nil, nil),
				typ("orgs", nil, []string{"org"}),
				typ("providers", nil, []string{"provider"}),
			}},
			want: column(
				consumer("team", "universal › team"),
				consumer("folder", "folder (nests)"),
				consumer("orgs", "orgs"),
				consumer("tools", "orgs › tools, providers › tools"),
				consumer("org", "orgs › org"),
				consumer("account", "orgs › org › account (nests)"),
				consumer("providers", "providers"),
				consumer("provider", "providers › provider"),
			),
		},
		{
			name: "a type others extend stays a consumer when the tree has a workspace of it, or puts it in or over another type",
			model: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{
					typ("account", nil, nil, "base", "mixin", "hub", "shared"),
					typ("base", nil, nil),
					typ("mixin", nil, nil),
					typ("holder", nil, []string{"mixin"}),
					typ("hub", nil, []string{"spoke"}),
					typ("spoke", nil, nil),
				},
				Workspaces: []pie.Workspace{{Path: "ws/b.yaml", Doc: 1, Name: "b", Type: pie.TypeRef{Path: "root", Name: "base"}}},
			},
			want: column(
				consumer("account", "account\nextends base, mixin, hub, shared (not in tree)"),
				consumer("base", "base\nworkspace: b"),
				consumer("holder", "holder"),
				consumer("mixin", "holder › mixin"),
				consumer("hub", "hub"),
				consumer("spoke", "hub › spoke"),
			),
		},
		{
			name: "a consumer says which types initialize and terminate its workspaces, itself or through the types it extends, and which workspaces the tree has of its type",
			model: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{
					typ("orgs", nil, []string{"org"}),
					withLifecycle(typ("security", nil, nil), true, true),
					typ("org", nil, nil, "security"),
					typ("account", []string{"org"}, nil, "org"),
					withLifecycle(typ("gate", nil, nil), true, false),
					withLifecycle(typ("init", nil, nil), true, false),
					withLifecycle(typ("term", nil, nil), false, true),
					typ("mixed", nil, nil, "init", "term", "shared"),
				},
				Workspaces: []pie.Workspace{
					{Path: "ws/orgs.yaml", Doc: 1, Name: "orgs", Type: pie.TypeRef{Path: "root", Name: "orgs"}},
					{Path: "ws/a1.yaml", Doc: 1, Name: "a1", Type: pie.TypeRef{Path: "root", Name: "account"}},
					{Path: "ws/a2.yaml", Doc: 1, Name: "a2", Type: pie.TypeRef{Path: "root", Name: "account"}},
				},
			},
			want: column(
				consumer("orgs", "orgs\nworkspace: orgs"),
				consumer("org", "orgs › org\nextends security\ninitializers, terminators: root:security"),
				consumer("account", "orgs › org › account\nextends org\ninitializers, terminators: root:security\nworkspaces: a1, a2"),
				consumer("gate", "gate\ninitializers: gate"),
				consumer("mixed", "mixed\nextends init, term, shared (not in tree)\ninitializers: root:init\nterminators: root:term"),
			),
		},
		{
			name: "a type extending universal in root, which kcp creates in every installation, binds tenancy.kcp.io and topology.kcp.io through it, each once; universal's initializer is kcp's own",
			model: pie.Model{WorkspaceTypes: []pie.WorkspaceType{
				typ("orgs", nil, nil, "universal"),
				func() pie.WorkspaceType {
					wt := withLifecycle(typ("account", nil, nil, "universal"), true, true)
					wt.Binds = []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}}
					return wt
				}(),
				{Path: "types/local.yaml", Doc: 1, Name: "local", Extends: []pie.TypeRef{{Path: "root:elsewhere", Name: "universal"}}},
			}},
			want: column(
				consumer("orgs", "orgs\nextends universal (kcp)\nbinds tenancy.kcp.io, topology.kcp.io @ root (kcp, through universal)"),
				consumer("account", "account\nextends universal (kcp)\ninitializers, terminators: account\nbinds tenancy.kcp.io @ root (kcp)\nbinds topology.kcp.io @ root (kcp, through universal)"),
				consumer("local", "local\nextends universal (not in tree)"),
			),
		},
		{
			name: "a tree that has a type named universal is drawn from its own",
			model: pie.Model{WorkspaceTypes: []pie.WorkspaceType{
				typ("orgs", nil, nil, "universal"),
				func() pie.WorkspaceType {
					wt := typ("universal", nil, nil)
					wt.Binds = []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}}
					return wt
				}(),
			}},
			want: column(
				consumer("orgs", "orgs\nextends universal\nbinds tenancy.kcp.io @ root (kcp, through universal)"),
			),
		},
		{
			name: "a loop of types extending each other is followed once",
			model: pie.Model{WorkspaceTypes: []pie.WorkspaceType{
				withLifecycle(typ("a", nil, []string{"b"}, "b"), true, false),
				typ("b", nil, nil, "a"),
			}},
			want: column(
				consumer("a", "a\nextends b\ninitializers: a"),
				consumer("b", "a › b\nextends a\ninitializers: root:a"),
			),
		},
		{
			name: "an initializer is named as kcp names it, root:type, where the references place its type in root",
			model: pie.Model{WorkspaceTypes: []pie.WorkspaceType{
				{Path: "types/team.yaml", Doc: 1, Name: "team", Extends: []pie.TypeRef{{Path: "root:org", Name: "gatekeeper"}}},
				{Path: "types/unit.yaml", Doc: 1, Name: "unit", Extends: []pie.TypeRef{{Name: "keeper"}}},
				{Path: "types/dept.yaml", Doc: 1, Name: "dept", Extends: []pie.TypeRef{{Path: "root", Name: "mid"}}},
				{Path: "types/mid.yaml", Doc: 1, Name: "mid", Extends: []pie.TypeRef{{Name: "keeper"}, {Path: "root:org", Name: "gatekeeper"}}},
				withLifecycle(typ("gatekeeper", nil, nil), true, false),
				withLifecycle(typ("keeper", nil, nil), true, false),
			}},
			want: column(
				consumer("team", "team\nextends gatekeeper\ninitializers: gatekeeper"),
				consumer("unit", "unit\nextends keeper\ninitializers: keeper"),
				consumer("dept", "dept\nextends mid\ninitializers: root:keeper, gatekeeper"),
			),
		},
		{
			name: "types allowed in each other are named as far as the loop goes",
			model: pie.Model{WorkspaceTypes: []pie.WorkspaceType{
				typ("a", []string{"b"}, nil),
				typ("b", []string{"a"}, nil),
			}},
			want: column(
				consumer("a", "b › a"),
				consumer("b", "a › b"),
			),
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

const (
	legendOnlyInB = `  "only-b": "only in B" {style.stroke: "#1098ad"; style.fill: "#e3fafc"}
`
	legendOnlyInA = `  "only-a": "only in A" {style.stroke: "#ae3ec9"; style.fill: "#f8f0fc"}
`
	legendGotInB = `  "got": "got in B: kinds" {style.stroke: "#1098ad"; style.stroke-dash: 3; style.fill: transparent; style.font-size: 11; style.bold: false; style.font-color: "#1098ad"}
`
	legendHadInA = `  "lost": "had in A: kinds" {style.stroke: "#ae3ec9"; style.stroke-dash: 3; style.fill: transparent; style.font-size: 11; style.bold: false; style.font-color: "#ae3ec9"}
`
)

func TestRenderOverviewDiffD2(t *testing.T) {
	exportOf := func(path, name string, resources ...pie.Resource) pie.Export {
		return pie.Export{Path: path, Doc: 1, Name: name, Resources: resources}
	}
	binds := func(path, name string, exports ...string) pie.WorkspaceType {
		wt := pie.WorkspaceType{Path: path, Doc: 1, Name: name}
		for _, e := range exports {
			wt.Binds = append(wt.Binds, pie.ExportRef{Path: "root:pms", Name: e})
		}
		return wt
	}
	panels := pie.Resource{Group: "ui.example.io", Name: "panels"}
	sprockets := pie.Resource{Group: "core.example.io", Name: "sprockets", Schema: "v1.sprockets.core.example.io"}
	anchors := pie.Resource{Group: "core.example.io", Name: "anchors", Schema: "v1.anchors.core.example.io"}
	bolts := pie.Resource{Group: "core.example.io", Name: "bolts", Schema: "v1.bolts.core.example.io"}
	dirBinding := pie.APIBinding{Path: "x/y/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}}
	onlyInB := `; style.stroke: "#1098ad"; style.fill: "#e3fafc"`
	// changed is a consumer of both trees whose kinds change, its title bold over a tag per direction.
	changed := func(id, title, class string, tags ...string) string {
		return fmt.Sprintf("  %q: {\n    label: %q\n    class: %s\n    style.bold: true\n    grid-columns: 1\n    grid-gap: 4\n%s  }\n", id, title, class, strings.Join(tags, ""))
	}
	tag := func(key, text, colour string) string {
		return fmt.Sprintf("    %q: %q {style.stroke: %q; style.stroke-dash: 3; style.fill: transparent; style.font-size: 11; style.bold: false; style.font-color: %q}\n", key, text, colour, colour)
	}
	lost := func(text string) string { return tag("lost", text, "#ae3ec9") }
	got := func(text string) string { return tag("got", text, "#1098ad") }
	widgetsGroup := func(kinds ...string) string {
		return "\"resources\": {\n  label: \"RESOURCES\"\n  class: column\n  \"group:core.example.io\": {\n    label: \"core.example.io\"\n    class: group\n" + strings.Join(kinds, "") + "  }\n"
	}
	kind := func(plural, look string) string {
		return fmt.Sprintf("    %q: %q {class: kind%s}\n", plural+".core.example.io", plural+" · not in tree", look)
	}
	tests := []struct {
		name string
		a, b pie.Model
		want string
	}{
		{
			name: "what both trees have is drawn as B has it, uncoloured; an export or type moved to another file or workspace is no change",
			a: pie.Model{
				Exports:        []pie.Export{exportOf("pms/core.yaml", "core.example.io", widgetResource())},
				WorkspaceTypes: []pie.WorkspaceType{binds("types/account.yaml", "account", "core.example.io")},
			},
			b: pie.Model{
				Exports: []pie.Export{exportOf("pms/moved.yaml", "core.example.io", widgetResource())},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/moved.yaml", Doc: 1, Name: "account",
					Binds: []pie.ExportRef{{Path: "root:other", Name: "core.example.io"}}}},
			},
			want: widgetsGroup(kind("widgets", "")) + `}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "workspace:root:other": "Workspace root:other" {class: workspace}
  "workspace:root:other"."apiexport:core.example.io": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
}
"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:account": "account" {class: workspacetype}
}
"apiexports"."workspace:root:other"."apiexport:core.example.io" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"consumers"."workspacetype:account" -> "apiexports"."workspace:root:other"."apiexport:core.example.io" {style.stroke: "#1971c2"}
` + overviewLegend(legendBindsByDefault, legendServes, legendAPIGroup),
		},
		{
			name: "a kind that moved to a new export: the export and its arrows only in B are cyan, the old export's arrow grape",
			a: pie.Model{
				Exports:        []pie.Export{exportOf("pms/core.yaml", "core.example.io", widgetResource(), gadgetResource())},
				WorkspaceTypes: []pie.WorkspaceType{binds("types/account.yaml", "account", "core.example.io")},
			},
			b: pie.Model{
				Exports: []pie.Export{
					exportOf("pms/core.yaml", "core.example.io", widgetResource()),
					exportOf("pms/other.yaml", "other.example.io", gadgetResource()),
				},
				WorkspaceTypes: []pie.WorkspaceType{binds("types/account.yaml", "account", "core.example.io", "other.example.io")},
			},
			want: widgetsGroup(kind("widgets", ""), kind("gadgets", "")) + `}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "workspace:root:pms": "Workspace root:pms" {class: workspace}
  "workspace:root:pms"."apiexport:core.example.io": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nexisting workspaces keep gadgets\nmaximalPermissionPolicy: none" {class: story; height: 51}
  }
  "workspace:root:pms"."apiexport:other.example.io": {
    label: "other.example.io"
    class: export
    style.stroke: "#1098ad"
    style.fill: "#e3fafc"
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account (new workspaces only)\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
}
"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:account": "account" {class: workspacetype}
}
"apiexports"."workspace:root:pms"."apiexport:core.example.io" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."workspace:root:pms"."apiexport:other.example.io" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#1098ad"}
"consumers"."workspacetype:account" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#1971c2"}
"consumers"."workspacetype:account" -> "apiexports"."workspace:root:pms"."apiexport:other.example.io" {style.stroke: "#1098ad"}
"apiexports"."workspace:root:pms"."apiexport:core.example.io" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#ae3ec9"}
` + overviewLegend(legendBindsByDefault, legendServes, legendAPIGroup, legendOnlyInB, legendOnlyInA),
		},
		{
			name: "what only A has is grape: a WorkspaceType, an export with its story, a group and its kinds, and their arrows",
			a: pie.Model{
				Exports: []pie.Export{
					exportOf("pms/core.yaml", "core.example.io", widgetResource()),
					exportOf("pms/old.yaml", "old.example.io", panels),
				},
				WorkspaceTypes: []pie.WorkspaceType{
					binds("types/account.yaml", "account", "core.example.io"),
					binds("types/org.yaml", "org", "old.example.io"),
				},
			},
			b: pie.Model{
				Exports:        []pie.Export{exportOf("pms/core.yaml", "core.example.io", widgetResource())},
				WorkspaceTypes: []pie.WorkspaceType{binds("types/account.yaml", "account", "core.example.io")},
			},
			want: widgetsGroup(kind("widgets", "")) + `  "group:ui.example.io": {
    label: "ui.example.io"
    class: group
    style.stroke: "#ae3ec9"
    style.fill: "#f8f0fc"
    "panels.ui.example.io": "panels · not in tree" {class: kind; style.stroke: "#ae3ec9"; style.fill: "#f8f0fc"}
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "workspace:root:pms": "Workspace root:pms" {class: workspace}
  "workspace:root:pms"."apiexport:core.example.io": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
  "workspace:root:pms"."apiexport:old.example.io": {
    label: "old.example.io"
    class: export
    style.stroke: "#ae3ec9"
    style.fill: "#f8f0fc"
    grid-columns: 1
    grid-gap: 0
    "story": "bound at org\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
}
"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:account": "account" {class: workspacetype}
  "workspacetype:org": "org" {class: workspacetype; style.stroke: "#ae3ec9"; style.fill: "#f8f0fc"}
}
"apiexports"."workspace:root:pms"."apiexport:core.example.io" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"consumers"."workspacetype:account" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#1971c2"}
"apiexports"."workspace:root:pms"."apiexport:old.example.io" -> "resources"."group:ui.example.io"."panels.ui.example.io" {style.stroke: "#ae3ec9"}
"consumers"."workspacetype:org" -> "apiexports"."workspace:root:pms"."apiexport:old.example.io" {style.stroke: "#ae3ec9"}
` + overviewLegend(legendBindsByDefault, legendServes, legendAPIGroup, legendOnlyInA),
		},
		{
			name: "a claim whose reach changed shows both arrows, the old grape and the new cyan, each with its weight",
			a: pie.Model{Exports: []pie.Export{
				exportOf("pms/core.yaml", "core.example.io", widgetResource()),
				withClaims(exportOf("pms/reader.yaml", "reader.example.io"), pie.Claim{Group: "core.example.io", Resource: "widgets", Verbs: []string{"get"}}),
			}},
			b: pie.Model{Exports: []pie.Export{
				exportOf("pms/core.yaml", "core.example.io", widgetResource()),
				withClaims(exportOf("pms/reader.yaml", "reader.example.io"), pie.Claim{Group: "core.example.io", Resource: "widgets", Verbs: []string{"update"}}),
			}},
			want: widgetsGroup(kind("widgets", "")) + `}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:core.example.io": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
  "apiexport:reader.example.io": {
    label: "reader.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "+writes widgets from core.example.io\n−reads widgets from core.example.io\nmaximalPermissionPolicy: none" {class: story; height: 51}
  }
}
"apiexports"."apiexport:core.example.io" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:reader.example.io" -> "resources"."group:core.example.io": "write" {style.stroke: "#1098ad"; style.stroke-dash: 4; style.stroke-width: 4}
"apiexports"."apiexport:reader.example.io" -> "resources"."group:core.example.io": "read" {style.stroke: "#ae3ec9"; style.stroke-dash: 4}
` + overviewLegend(legendServes, legendClaimsRead, legendClaimsWrite, legendAPIGroup, legendOnlyInB, legendOnlyInA),
		},
		{
			name: "kcp's verdicts keep their colours: a new conflict stays red, a kind two exports serve stays orange",
			a: pie.Model{
				Exports:        []pie.Export{exportOf("ws/a.yaml", "a.example.io", widgetResource())},
				WorkspaceTypes: []pie.WorkspaceType{binds("types/account.yaml", "account", "a.example.io")},
			},
			b: pie.Model{
				Exports: []pie.Export{
					exportOf("ws/a.yaml", "a.example.io", widgetResource()),
					exportOf("ws/b.yaml", "b.example.io", widgetResource()),
				},
				WorkspaceTypes: []pie.WorkspaceType{binds("types/account.yaml", "account", "a.example.io", "b.example.io")},
			},
			want: widgetsGroup(kind("widgets", `; style.fill: "#f08c00"`)) + `}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "workspace:root:pms": "Workspace root:pms" {class: workspace}
  "workspace:root:pms"."apiexport:a.example.io": {
    label: "a.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
  "workspace:root:pms"."apiexport:b.example.io": {
    label: "b.example.io"
    class: export
    style.stroke: "#1098ad"
    style.fill: "#e3fafc"
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account (new workspaces only)\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
}
"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:account": "account" {class: workspacetype}
}
"apiexports"."workspace:root:pms"."apiexport:a.example.io" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."workspace:root:pms"."apiexport:b.example.io" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#1098ad"}
"consumers"."workspacetype:account" -> "apiexports"."workspace:root:pms"."apiexport:a.example.io": "conflicts with b.example.io" {style.stroke: "#e03131"; style.stroke-width: 3}
"consumers"."workspacetype:account" -> "apiexports"."workspace:root:pms"."apiexport:b.example.io": "conflicts with a.example.io" {style.stroke: "#e03131"; style.stroke-width: 3}
"consumers"."workspacetype:account" -> "apiexports"."workspace:root:pms"."apiexport:a.example.io" {style.stroke: "#ae3ec9"}
` + overviewLegend(legendBindsByDefault, legendBindsRefused, legendServes, legendAPIGroup, legendOnlyInB, legendOnlyInA, legendSharedKind),
		},
		{
			name: "copies of an export count once, named by name, their arrows drawn once; a new kind two exports serve is orange, stroked cyan",
			a: pie.Model{
				Exports:        []pie.Export{exportOf("a/core.yaml", "core.example.io", widgetResource())},
				WorkspaceTypes: []pie.WorkspaceType{binds("types/account.yaml", "account", "core.example.io")},
			},
			b: pie.Model{
				Exports: []pie.Export{
					exportOf("a/core.yaml", "core.example.io", widgetResource(), gadgetResource()),
					exportOf("b/core.yaml", "core.example.io", widgetResource(), gadgetResource()),
					exportOf("c/other.yaml", "other.example.io", gadgetResource()),
				},
				WorkspaceTypes: []pie.WorkspaceType{binds("types/account.yaml", "account", "core.example.io")},
			},
			want: widgetsGroup(kind("widgets", ""), kind("gadgets", `; style.fill: "#f08c00"; style.stroke: "#1098ad"`)) + `}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "workspace:root:pms": "Workspace root:pms" {class: workspace}
  "workspace:root:pms"."apiexport:core.example.io": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
  "apiexport:other.example.io": {
    label: "other.example.io"
    class: export
    style.stroke: "#1098ad"
    style.fill: "#e3fafc"
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
}
"consumers": {
  label: "CONSUMERS"
  class: column
` + changed("workspacetype:account", "account", "workspacetype", got("got in B: gadgets")) + `}
"apiexports"."workspace:root:pms"."apiexport:core.example.io" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."workspace:root:pms"."apiexport:core.example.io" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#1098ad"}
"apiexports"."apiexport:other.example.io" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#1098ad"}
"consumers"."workspacetype:account" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#1971c2"}
` + overviewLegend(legendBindsByDefault, legendServes, legendAPIGroup, legendOnlyInB, legendGotInB, legendSharedKind),
		},
		{
			name: "a claim new in B on what no export serves: its arrow and the resource cyan, under the category only B has",
			a: pie.Model{Exports: []pie.Export{
				exportOf("pms/core.yaml", "core.example.io", widgetResource()),
				exportOf("pms/reader.yaml", "reader.example.io"),
			}},
			b: pie.Model{Exports: []pie.Export{
				exportOf("pms/core.yaml", "core.example.io", widgetResource()),
				withClaims(exportOf("pms/reader.yaml", "reader.example.io"), pie.Claim{Resource: "secrets", Verbs: []string{"get"}}),
			}},
			want: widgetsGroup(kind("widgets", "")) + `  "builtin": {
    label: "built-in · in every workspace"
    class: claimed
    "group:": {
      label: "core"
      class: claimed
      style.stroke: "#1098ad"
      style.fill: "#e3fafc"
      grid-rows: 1
      grid-gap: 12
      "secrets": "secrets" {class: claimed-resource; style.stroke: "#1098ad"; style.fill: "#e3fafc"}
    }
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "apiexport:core.example.io": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
  "apiexport:reader.example.io": {
    label: "reader.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "maximalPermissionPolicy: none" {class: story; height: 17}
  }
}
"apiexports"."apiexport:core.example.io" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:reader.example.io" -> "resources"."builtin": "read" {style.stroke: "#1098ad"; style.stroke-dash: 4}
` + overviewLegend(legendServes, legendClaimsRead, legendAPIGroup, legendOnlyInB),
		},
		{
			name: "an export from outside the tree that a consumer of both trees binds only in B is marked +, one only in A − at the end, in its line",
			a: pie.Model{WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account",
				Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}, {Path: "root", Name: "gone.kcp.io"}}}}},
			b: pie.Model{WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account",
				Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}, {Path: "root", Name: "topology.kcp.io"}}}}},
			want: `"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:account": "account\nbinds tenancy.kcp.io, +topology.kcp.io @ root (kcp)\nbinds −gone.kcp.io @ root (not in tree)" {class: workspacetype}
}
`,
		},
		{
			name: "a consumer both trees have whose kinds change, those the exports it binds serve and not what they claim, keeps its look and gets a dashed tag of the kinds it had in A, grape, and one of those it got in B, cyan, by name and wrapped to about its title's width; a consumer only B has is filled cyan, without tags",
			a: pie.Model{
				Exports:        []pie.Export{exportOf("pms/core.yaml", "core.example.io", widgetResource(), gadgetResource(), anchors)},
				Schemas:        []pie.Schema{widgetSchemaAt("pms/widgets.yaml")},
				WorkspaceTypes: []pie.WorkspaceType{binds("types/account.yaml", "account", "core.example.io"), binds("types/org.yaml", "org", "core.example.io"), binds("types/guest.yaml", "guest", "core.example.io")},
				APIBindings:    []pie.APIBinding{dirBinding},
			},
			b: pie.Model{
				Exports: []pie.Export{
					withClaims(exportOf("pms/core.yaml", "core.example.io", widgetResource(), sprockets, bolts), pie.Claim{Resource: "configmaps", Verbs: []string{"get"}}),
					exportOf("pms/tools.yaml", "tools.example.io", gadgetResource(), anchors),
				},
				Schemas: []pie.Schema{widgetSchemaAt("pms/widgets.yaml")},
				WorkspaceTypes: []pie.WorkspaceType{
					binds("types/account.yaml", "account", "core.example.io"),
					binds("types/org.yaml", "org", "core.example.io", "tools.example.io"),
					binds("types/guest.yaml", "guest", "tools.example.io"),
					binds("types/team.yaml", "team", "tools.example.io"),
				},
				APIBindings: []pie.APIBinding{dirBinding},
			},
			want: widgetsGroup(`    "widgets.core.example.io": "Widget" {class: kind}
`, kind("sprockets", onlyInB), kind("bolts", onlyInB), kind("gadgets", ""), kind("anchors", "")) + `  "builtin": {
    label: "built-in · in every workspace"
    class: claimed
    "group:": {
      label: "core"
      class: claimed
      style.stroke: "#1098ad"
      style.fill: "#e3fafc"
      grid-rows: 1
      grid-gap: 12
      "configmaps": "configmaps" {class: claimed-resource; style.stroke: "#1098ad"; style.fill: "#e3fafc"}
    }
  }
}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "workspace:root:pms": "Workspace root:pms" {class: workspace}
  "workspace:root:pms"."apiexport:core.example.io": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account, org, y, −guest (existing workspaces keep it)\nexisting workspaces keep anchors, gadgets\nmaximalPermissionPolicy: none" {class: story; height: 51}
  }
  "workspace:root:pms"."apiexport:tools.example.io": {
    label: "tools.example.io"
    class: export
    style.stroke: "#1098ad"
    style.fill: "#e3fafc"
    grid-columns: 1
    grid-gap: 0
    "story": "bound at org (new workspaces only), guest (new workspaces only), team\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
}
"consumers": {
  label: "CONSUMERS"
  class: column
` + changed("workspacetype:account", "account", "workspacetype", lost("had in A: anchors,\ngadgets"), got("got in B: bolts,\nsprockets")) +
				changed("workspacetype:org", "org", "workspacetype", got("got in B: bolts,\nsprockets")) +
				changed("workspacetype:guest", "guest", "workspacetype", lost("had in A: Widget")) + `  "workspacetype:team": "team" {class: workspacetype; style.stroke: "#1098ad"; style.fill: "#e3fafc"}
` + changed("apibindings:x/y", "APIBindings in y", "apibindings",
				lost("had in A: anchors,\ngadgets"), got("got in B: bolts,\nsprockets")) + `}
"apiexports"."workspace:root:pms"."apiexport:core.example.io" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."workspace:root:pms"."apiexport:core.example.io" -> "resources"."group:core.example.io"."sprockets.core.example.io" {style.stroke: "#1098ad"}
"apiexports"."workspace:root:pms"."apiexport:core.example.io" -> "resources"."group:core.example.io"."bolts.core.example.io" {style.stroke: "#1098ad"}
"apiexports"."workspace:root:pms"."apiexport:tools.example.io" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#1098ad"}
"apiexports"."workspace:root:pms"."apiexport:tools.example.io" -> "resources"."group:core.example.io"."anchors.core.example.io" {style.stroke: "#1098ad"}
"apiexports"."workspace:root:pms"."apiexport:core.example.io" -> "resources"."builtin": "read" {style.stroke: "#1098ad"; style.stroke-dash: 4}
"consumers"."workspacetype:account" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#1971c2"}
"consumers"."workspacetype:org" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#1971c2"}
"consumers"."workspacetype:org" -> "apiexports"."workspace:root:pms"."apiexport:tools.example.io" {style.stroke: "#1098ad"}
"consumers"."workspacetype:guest" -> "apiexports"."workspace:root:pms"."apiexport:tools.example.io" {style.stroke: "#1098ad"}
"consumers"."workspacetype:team" -> "apiexports"."workspace:root:pms"."apiexport:tools.example.io" {style.stroke: "#1098ad"}
"consumers"."apibindings:x/y" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#2f9e44"}
"apiexports"."workspace:root:pms"."apiexport:core.example.io" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#ae3ec9"}
"apiexports"."workspace:root:pms"."apiexport:core.example.io" -> "resources"."group:core.example.io"."anchors.core.example.io" {style.stroke: "#ae3ec9"}
"consumers"."workspacetype:guest" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#ae3ec9"}
` + overviewLegend(legendBindsByDefault, legendBindsByAPIBinding, legendServes, legendClaimsRead, legendAPIGroup, legendOnlyInB, legendOnlyInA, legendGotInB, legendHadInA),
		},
		{
			name: "an export both trees have marks its story: a consumer binding it only in B with +, only in A with − at the end, told apart by id, not by its short name, which changes with the other directories; a needs line only in B with +, only in A with −",
			a: pie.Model{
				Exports: []pie.Export{
					exportOf("pms/core.yaml", "core.example.io", widgetResource()),
					withClaims(exportOf("pms/reader.yaml", "reader.example.io"), pie.Claim{Group: "core.example.io", Resource: "widgets", Verbs: []string{"get"}}),
				},
				WorkspaceTypes: []pie.WorkspaceType{binds("types/account.yaml", "account", "core.example.io"), binds("types/org.yaml", "org", "core.example.io")},
				APIBindings:    []pie.APIBinding{{Path: "a/bind/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}}},
			},
			b: pie.Model{
				Exports: []pie.Export{
					exportOf("pms/core.yaml", "core.example.io", widgetResource()),
					withClaims(exportOf("pms/reader.yaml", "reader.example.io"), pie.Claim{Group: "core.example.io", Resource: "widgets", Verbs: []string{"get", "create"}}),
				},
				WorkspaceTypes: []pie.WorkspaceType{binds("types/account.yaml", "account", "core.example.io"), binds("types/team.yaml", "team", "core.example.io")},
				APIBindings: []pie.APIBinding{
					{Path: "a/bind/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}},
					{Path: "b/bind/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}},
				},
			},
			want: widgetsGroup(kind("widgets", "")) + `}
"apiexports": {
  label: "APIEXPORTS"
  class: column
  "workspace:root:pms": "Workspace root:pms" {class: workspace}
  "workspace:root:pms"."apiexport:core.example.io": {
    label: "core.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "bound at account, +team, a/bind, +b/bind, −org\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }
  "apiexport:reader.example.io": {
    label: "reader.example.io"
    class: export
    grid-columns: 1
    grid-gap: 0
    "story": "+writes widgets from core.example.io\n−reads widgets from core.example.io\nmaximalPermissionPolicy: none" {class: story; height: 51}
  }
}
"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:account": "account" {class: workspacetype}
  "workspacetype:team": "team" {class: workspacetype; style.stroke: "#1098ad"; style.fill: "#e3fafc"}
  "apibindings:a/bind": "APIBindings in a/bind" {class: apibindings}
  "apibindings:b/bind": "APIBindings in b/bind" {class: apibindings; style.stroke: "#1098ad"; style.fill: "#e3fafc"}
  "workspacetype:org": "org" {class: workspacetype; style.stroke: "#ae3ec9"; style.fill: "#f8f0fc"}
}
"apiexports"."workspace:root:pms"."apiexport:core.example.io" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."apiexport:reader.example.io" -> "resources"."group:core.example.io": "write" {style.stroke: "#1098ad"; style.stroke-dash: 4; style.stroke-width: 4}
"consumers"."workspacetype:account" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#1971c2"}
"consumers"."workspacetype:team" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#1098ad"}
"consumers"."apibindings:a/bind" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#2f9e44"}
"consumers"."apibindings:b/bind" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#1098ad"}
"apiexports"."apiexport:reader.example.io" -> "resources"."group:core.example.io": "read" {style.stroke: "#ae3ec9"; style.stroke-dash: 4}
"consumers"."workspacetype:org" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#ae3ec9"}
` + overviewLegend(legendBindsByDefault, legendBindsByAPIBinding, legendServes, legendClaimsRead, legendClaimsWrite, legendAPIGroup, legendOnlyInB, legendOnlyInA),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderOverviewDiffD2(tt.a, tt.b)); got != overviewHead+tt.want {
				t.Errorf("RenderOverviewDiffD2() =\n%s\nwant\n%s", got, overviewHead+tt.want)
			}
		})
	}
}
