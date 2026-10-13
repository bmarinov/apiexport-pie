package pie_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/bmarinov/apiexport-pie/internal/pie"
)

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
			name: "an export's claims follow its kinds, a row per set of verbs, narrowed ones marked defaultSelector",
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
|  | grants: get, create on secrets (defaultSelector) | ● |

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
