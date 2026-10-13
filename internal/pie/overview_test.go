package pie_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bmarinov/apiexport-pie/internal/pie"
)

// TestRenderOverviewD2 pins whole overviews: the order of columns, boxes, edges and legend. The
// other tests of the overview assert only the parts each is about.
func TestRenderOverviewD2(t *testing.T) {
	core := pie.Export{Path: "pms/export.yaml", Doc: 1, Name: "core.example.io"}
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderOverviewD2(tt.model)); got != overviewHead+tt.want {
				t.Errorf("RenderOverviewD2() =\n%s\nwant\n%s", got, overviewHead+tt.want)
			}
		})
	}
}

func TestRenderOverviewD2DrawsConsumers(t *testing.T) {
	core := pie.Export{Path: "pms/export.yaml", Doc: 1, Name: "core.example.io"}
	// placed has exports the references to it agree on, disagree on, and say nothing about.
	placed := pie.Model{
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
	}
	// manyBinders has one export bound by two types of one name and by binding files in five directories.
	manyBinders := pie.Model{
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
	}
	// extended has a type binding through a base nothing else makes a consumer, and a binding directory.
	extended := pie.Model{
		Exports: []pie.Export{core},
		WorkspaceTypes: []pie.WorkspaceType{
			{Path: "types/account.yaml", Doc: 1, Name: "account", Extends: []pie.TypeRef{{Name: "security"}}},
			{Path: "types/security.yaml", Doc: 1, Name: "security", Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}},
		},
		APIBindings: []pie.APIBinding{{Path: "kcp/01/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}}},
	}
	// outsiders bind exports the tree does not define, some of them kcp's own, one through a base.
	outsiders := pie.Model{
		WorkspaceTypes: []pie.WorkspaceType{
			{Path: "types/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}, {Name: "local.example.io"}, {Path: "root", Name: "topology.kcp.io"}}},
			{Path: "types/org.yaml", Doc: 1, Name: "org", Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}},
				Extends: []pie.TypeRef{{Path: "root", Name: "base"}}},
			{Path: "types/base.yaml", Doc: 1, Name: "base", Binds: []pie.ExportRef{{Path: "root:kcp", Name: "shards.kcp.io"}}},
		},
		APIBindings: []pie.APIBinding{{Path: "x/tenancy.yaml", Doc: 1, Name: "tenancy", Export: pie.ExportRef{Path: "root", Name: "tenancy.kcp.io"}}},
	}
	// copies has an export and a WorkspaceType written twice.
	copies := pie.Model{
		Exports: []pie.Export{
			{Path: "a/export.yaml", Doc: 1, Name: "core.example.io"},
			{Path: "b/export.yaml", Doc: 1, Name: "core.example.io"},
		},
		WorkspaceTypes: []pie.WorkspaceType{
			{Path: "a/account.yaml", Doc: 1, Name: "account", Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}},
			{Path: "b/account.yaml", Doc: 1, Name: "account"},
		},
	}
	tests := []struct {
		name  string
		model pie.Model
		has   []string
	}{
		{
			name:  "an export stands in the box of the workspace the references to it agree on, and edges follow it there",
			model: placed,
			has: []string{
				`  "workspace:root:pms": "Workspace root:pms" {class: workspace}`,
				`  "workspace:root:pms"."apiexport:pms/core.yaml#1": {`,
				`"apiexports"."workspace:root:pms"."apiexport:pms/core.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}`,
				`"consumers"."workspacetype:types/account.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#1971c2"}`,
			},
		},
		{
			name:  "an export the references disagree on, or nothing refers to, stands alone in APIEXPORTS",
			model: placed,
			has: []string{
				`  "apiexport:pms/other.yaml#1": {`,
				`  "apiexport:pms/lone.yaml#1": {`,
				`"consumers"."workspacetype:types/account.yaml#1" -> "apiexports"."apiexport:pms/other.yaml#1" {style.stroke: "#1971c2"}`,
				`"consumers"."workspacetype:types/org.yaml#1" -> "apiexports"."apiexport:pms/other.yaml#1" {style.stroke: "#1971c2"}`,
			},
		},
		{
			name:  "an export's story says where it is bound, each consumer once, a WorkspaceType by its name",
			model: placed,
			has: []string{
				`    "story": "bound at account, org\nmaximalPermissionPolicy: none" {class: story; height: 34}`,
				`    "story": "maximalPermissionPolicy: none" {class: story; height: 17}`,
			},
		},
		{
			name:  "copies of a WorkspaceType say where they are written, and the story names them once",
			model: manyBinders,
			has: []string{
				`  "workspacetype:types/a.yaml#1": "account · types/a.yaml#1" {class: workspacetype}`,
				`  "workspacetype:types/b.yaml#1": "account · types/b.yaml#1" {class: workspacetype}`,
				`    "story": "bound at account, a/manifests, b/manifests, c/kcp, kcp, tools\nmaximalPermissionPolicy: none" {class: story; height: 34}`,
			},
		},
		{
			name:  "a directory of APIBinding files is named by the shortest end of its path no other such directory shares, on its box and in the story",
			model: manyBinders,
			has: []string{
				`  "apibindings:a/manifests": "APIBindings in a/manifests" {class: apibindings}`,
				`  "apibindings:b/manifests": "APIBindings in b/manifests" {class: apibindings}`,
				`  "apibindings:c/kcp": "APIBindings in c/kcp" {class: apibindings}`,
				`  "apibindings:kcp": "APIBindings in kcp" {class: apibindings}`,
				`  "apibindings:d/e/tools": "APIBindings in tools" {class: apibindings}`,
				`"consumers"."apibindings:d/e/tools" -> "apiexports"."workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#2f9e44"}`,
			},
		},
		{
			name:  "bindings through extend are dashed",
			model: extended,
			has: []string{
				`  "workspacetype:types/account.yaml#1": "account\nextends security" {class: workspacetype}`,
				`"consumers"."workspacetype:types/account.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#1971c2"; style.stroke-dash: 2}`,
				legendBindsThroughExtend,
			},
		},
		{
			name:  "a type only other types extend is a line on them, not a consumer",
			model: extended,
			has: []string{
				`"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:types/account.yaml#1": "account\nextends security" {class: workspacetype}
  "apibindings:kcp/01": "APIBindings in 01" {class: apibindings}
}`,
				`    "story": "bound at account, 01\nmaximalPermissionPolicy: none" {class: story; height: 34}`,
			},
		},
		{
			name:  "a directory of APIBinding files binds in green",
			model: extended,
			has: []string{
				`  "apibindings:kcp/01": "APIBindings in 01" {class: apibindings}`,
				`"consumers"."apibindings:kcp/01" -> "apiexports"."workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#2f9e44"}`,
				legendBindsByAPIBinding,
			},
		},
		{
			name:  "exports outside the tree are a line on each consumer binding them, by workspace, not boxes with arrows",
			model: outsiders,
			has: []string{
				`"consumers": {
  label: "CONSUMERS"
  class: column
  "workspacetype:types/account.yaml#1": "account\nbinds tenancy.kcp.io, topology.kcp.io @ root (kcp)\nbinds local.example.io @ (own workspace) (not in tree)" {class: workspacetype}`,
				`  "apibindings:x": "APIBindings in x\nbinds tenancy.kcp.io @ root (kcp)" {class: apibindings}
}
`,
			},
		},
		{
			name:  "an export outside the tree bound through a base says so",
			model: outsiders,
			has: []string{
				`  "workspacetype:types/org.yaml#1": "org\nextends base\nbinds tenancy.kcp.io @ root (kcp)\nbinds shards.kcp.io @ root:kcp (not in tree, through base)" {class: workspacetype}`,
			},
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
			has: []string{
				`  "workspacetype:types/account.yaml#1": "account\nbinds shards.core.kcp.io, cache.kcp.io, migration.kcp.io @ root (kcp)\nbinds tenancy.kcp.io @ root:org; local.example.io @ root (not in tree)" {class: workspacetype}`,
			},
		},
		{
			name:  "copies of an export say where they are written, and a binding points at every copy",
			model: copies,
			has: []string{
				`  "workspace:root:pms"."apiexport:a/export.yaml#1": {
    label: "core.example.io · a/export.yaml#1"`,
				`  "workspace:root:pms"."apiexport:b/export.yaml#1": {
    label: "core.example.io · b/export.yaml#1"`,
				`"consumers"."workspacetype:a/account.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:a/export.yaml#1" {style.stroke: "#1971c2"}`,
				`"consumers"."workspacetype:a/account.yaml#1" -> "apiexports"."workspace:root:pms"."apiexport:b/export.yaml#1" {style.stroke: "#1971c2"}`,
			},
		},
		{
			name:  "copies of a WorkspaceType say where they are written, each a consumer of its own",
			model: copies,
			has: []string{
				`  "workspacetype:a/account.yaml#1": "account · a/account.yaml#1" {class: workspacetype}`,
				`  "workspacetype:b/account.yaml#1": "account · b/account.yaml#1" {class: workspacetype}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantBlocks(t, string(pie.RenderOverviewD2(tt.model)), tt.has...)
		})
	}
}

func TestRenderOverviewD2DrawsTheKindsExportsServe(t *testing.T) {
	uiResource := pie.Resource{Group: "ui.example.io", Name: "panels", Schema: "v1.panels.ui.example.io"}
	panelSchema := pie.Schema{Path: "ws/panels.yaml", Name: "v1.panels.ui.example.io", Group: "ui.example.io", Kind: "Panel", Plural: "panels"}
	tests := []struct {
		name  string
		model pie.Model
		has   []string
	}{
		{
			name: "each kind stands once in RESOURCES, in a box of its API group, pointed at by the export serving it",
			model: pie.Model{
				Exports: []pie.Export{
					{Path: "ws/core.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource(), uiResource, gadgetResource()}},
				},
				Schemas: []pie.Schema{widgetSchemaAt("ws/widgets.yaml"), gadgetSchemaAt("ws/gadgets.yaml"), panelSchema},
			},
			has: []string{
				`"resources": {
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
}`,
				`"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}`,
				`"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:ui.example.io"."panels.ui.example.io" {style.stroke: "#868e96"}`,
				`"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#868e96"}`,
				legendServes, legendAPIGroup,
			},
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
			has: []string{
				`    "widgets.core.example.io": "Widget" {class: kind}`,
				`    "panels.ui.example.io": "Panel" {class: kind; style.fill: "#f08c00"}`,
				`"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."group:ui.example.io"."panels.ui.example.io" {style.stroke: "#868e96"}`,
				`"apiexports"."apiexport:ws/ui.yaml#1" -> "resources"."group:ui.example.io"."panels.ui.example.io" {style.stroke: "#868e96"}`,
				legendSharedKind,
			},
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
			has: []string{`    "widgets.core.example.io": "Widget" {class: kind}`},
		},
		{
			name: "copies of one export serving a kind do not make it orange",
			model: pie.Model{
				Exports: []pie.Export{coreExportAt("a/export.yaml"), coreExportAt("b/export.yaml")},
				Schemas: []pie.Schema{widgetSchemaAt("a/widgets.yaml"), gadgetSchemaAt("a/gadgets.yaml")},
			},
			has: []string{
				`  "group:core.example.io": {
    label: "core.example.io"
    class: group
    "widgets.core.example.io": "Widget" {class: kind}
    "gadgets.core.example.io": "Gadget" {class: kind}
  }`,
				`"apiexports"."apiexport:a/export.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}`,
				`"apiexports"."apiexport:b/export.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantBlocks(t, string(pie.RenderOverviewD2(tt.model)), tt.has...)
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
    "warnings": "! apis.kcp.io/v1alpha1 is deprecated\n→ claims get verbs *" {class: warning; height: 34}
  }
`,
		},
		{
			name: "a warning starts a line, and what follows from it takes the next behind an arrow, so a claim with a literal identity hash of an export whose identity kcp generates is two more",
			model: pie.Model{Exports: []pie.Export{coreExportAt("pms/core.yaml"),
				withV1alpha1(withClaims(system, pie.Claim{Group: "core.example.io", Resource: "widgets", Verbs: []string{"get"}, FromExport: true, LiteralHash: true}))}},
			want: `    "warnings": "! apis.kcp.io/v1alpha1 is deprecated\n→ claims get verbs *\n! identityHash for widgets is a literal\n→ core.example.io's identity differs per installation" {class: warning; height: 68}`,
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

func TestRenderOverviewD2DrawsWhatClaimsReach(t *testing.T) {
	claim := func(group, resource string, verbs ...string) pie.Claim {
		return pie.Claim{Group: group, Resource: resource, Verbs: verbs}
	}
	// readers has a reader claiming one kind of core and an admin claiming all of its kinds.
	readers := pie.Model{
		Exports: []pie.Export{
			coreExportAt("ws/core.yaml"),
			withClaims(pie.Export{Path: "ws/reader.yaml", Doc: 1, Name: "reader.example.io"}, claim("core.example.io", "widgets", "get", "list", "watch")),
			withClaims(pie.Export{Path: "ws/admin.yaml", Doc: 1, Name: "admin.example.io"},
				claim("core.example.io", "widgets", "*"), claim("core.example.io", "gadgets", "*")),
		},
		Schemas: []pie.Schema{widgetSchemaAt("ws/widgets.yaml"), gadgetSchemaAt("ws/gadgets.yaml")},
	}
	// weighted claims built-in resources with every reach, with and without a selector.
	weighted := pie.Model{Exports: []pie.Export{withClaims(pie.Export{Path: "ws/core.yaml", Doc: 1, Name: "core.example.io"},
		claim("", "configmaps", "get", "list", "watch"),
		pie.Claim{Group: "", Resource: "secrets", Verbs: []string{"get", "create"}, Selected: true},
		claim("", "serviceaccounts", "*"),
		pie.Claim{Group: "", Resource: "events", Verbs: []string{"*"}, Selected: true},
	)}}
	// unserved claims resources no export in the tree serves, some by an export's identity.
	unserved := pie.Model{
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
	}
	// stacked has a claimed box one arrow points into at a single resource, and one pointed at whole.
	stacked := pie.Model{Exports: []pie.Export{
		withClaims(pie.Export{Path: "ws/core.yaml", Doc: 1, Name: "core.example.io"},
			pie.Claim{Group: "", Resource: "secrets", Verbs: []string{"get", "create"}, Selected: true},
		),
		withClaims(pie.Export{Path: "ws/providers.yaml", Doc: 1, Name: "providers.example.io"},
			claim("", "secrets", "get", "create"),
			claim("", "configmaps", "get", "create"),
			claim("rbac.authorization.k8s.io", "roles", "get"),
		),
	}}
	tests := []struct {
		name  string
		model pie.Model
		has   []string
	}{
		{
			name:  "a claim on a kind the tree serves points at the kind",
			model: readers,
			has: []string{
				`"apiexports"."apiexport:ws/reader.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}`,
				legendClaimsRead,
			},
		},
		{
			name:  "claims taking every kind of a group point at the group's box",
			model: readers,
			has: []string{
				`"apiexports"."apiexport:ws/admin.yaml#1" -> "resources"."group:core.example.io" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 6}`,
				legendClaimsAll,
			},
		},
		{
			name:  "an export's story says what it needs of the kinds other exports serve, a line per export and reach",
			model: readers,
			has: []string{
				`    "story": "reads widgets from core.example.io\nmaximalPermissionPolicy: none" {class: story; height: 34}`,
				`    "story": "all verbs on widgets, gadgets from core.example.io\nmaximalPermissionPolicy: none" {class: story; height: 34}`,
			},
		},
		{
			name: "needs are a line per export and reach, and claims on an export's own kinds add nothing",
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
			has: []string{
				`    "story": "reads widgets from core.example.io\nwrites gadgets from core.example.io\nmaximalPermissionPolicy: none" {class: story; height: 51}`,
				`"apiexports"."apiexport:ws/terminal.yaml#1" -> "resources"."group:core.example.io"."widgets.core.example.io": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}`,
				`"apiexports"."apiexport:ws/terminal.yaml#1" -> "resources"."group:terminal.example.io": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}`,
				`"apiexports"."apiexport:ws/terminal.yaml#1" -> "resources"."group:core.example.io"."gadgets.core.example.io": "write" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 4}`,
			},
		},
		{
			name:  "the more a claim lets the provider do, the heavier its arrow: read, write, all verbs, which the weight says alone",
			model: weighted,
			has: []string{
				`"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."configmaps": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}`,
				`"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."serviceaccounts" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 6}`,
				legendClaimsRead, legendClaimsWrite, legendClaimsAll,
			},
		},
		{
			name:  "a selector does not lighten an arrow, and is named on it",
			model: weighted,
			has: []string{
				`"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."secrets": "write · defaultSelector" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 4}`,
				`"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."events": "defaultSelector" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 6}`,
			},
		},
		{
			name:  "a claim naming an export's identity is on a resource from an APIExport outside the tree, drawn dashed under that category",
			model: unserved,
			has: []string{
				`  "exported": {
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
  }`,
				`"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."exported" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 6}`,
			},
		},
		{
			name:  "any other claim no export in the tree serves is on a built-in resource, by group, kcp's and Kubernetes' alike",
			model: unserved,
			has: []string{
				`  "builtin": {
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
  }`,
				`"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:apis.kcp.io" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 6}`,
				`"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."secrets": "read · defaultSelector" {style.stroke: "#7048e8"; style.stroke-dash: 4}`,
				`"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."configmaps": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}`,
				`"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:rbac.authorization.k8s.io": "write" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 4}`,
				`"apiexports"."apiexport:ws/other.yaml#1" -> "resources"."builtin"."group:"."configmaps": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}`,
			},
		},
		{
			name:  "a claimed box an arrow points into, at one of its resources alone, stacks them, which d2 routes the arrow into",
			model: stacked,
			has: []string{
				`    "group:": {
      label: "core"
      class: claimed
      "secrets": "secrets" {class: claimed-resource}
      "configmaps": "configmaps" {class: claimed-resource}
    }`,
				`"apiexports"."apiexport:ws/core.yaml#1" -> "resources"."builtin"."group:"."secrets": "write · defaultSelector" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 4}`,
				`"apiexports"."apiexport:ws/providers.yaml#1" -> "resources"."builtin"."group:": "write" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 4}`,
			},
		},
		{
			name:  "a claimed box pointed at whole keeps its resources in a row",
			model: stacked,
			has: []string{
				`    "group:rbac.authorization.k8s.io": {
      label: "rbac.authorization.k8s.io"
      class: claimed
      grid-rows: 1
      grid-gap: 12
      "roles.rbac.authorization.k8s.io": "roles" {class: claimed-resource}
    }`,
				`"apiexports"."apiexport:ws/providers.yaml#1" -> "resources"."builtin"."group:rbac.authorization.k8s.io": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}`,
			},
		},
		{
			name: "a claimed resource the tree does not serve, of a Kubernetes group it does serve, stands dashed in that group's box",
			model: pie.Model{
				Exports: []pie.Export{
					{Path: "ws/gateway.yaml", Doc: 1, Name: "gateway.example.io", Resources: []pie.Resource{{Group: "gateway.networking.k8s.io", Name: "gateways"}}},
					withClaims(pie.Export{Path: "ws/other.yaml", Doc: 1, Name: "other.example.io"}, pie.Claim{Group: "gateway.networking.k8s.io", Resource: "httproutes", Verbs: []string{"get"}, FromExport: true}),
				},
			},
			has: []string{
				`  "group:gateway.networking.k8s.io": {
    label: "gateway.networking.k8s.io"
    class: group
    "gateways.gateway.networking.k8s.io": "gateways · not in tree" {class: kind}
    "httproutes.gateway.networking.k8s.io": "httproutes" {class: claimed-resource}
  }`,
				`"apiexports"."apiexport:ws/other.yaml#1" -> "resources"."group:gateway.networking.k8s.io"."httproutes.gateway.networking.k8s.io": "read" {style.stroke: "#7048e8"; style.stroke-dash: 4}`,
			},
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
			has: []string{
				`  "group:core.example.io": {
    label: "core.example.io"
    class: group
    "widgets.core.example.io": "Widget" {class: kind}
    "sprockets.core.example.io": "sprockets" {class: claimed-resource}
  }`,
				`"apiexports"."apiexport:ws/other.yaml#1" -> "resources"."group:core.example.io"."sprockets.core.example.io": "write" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 4}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantBlocks(t, string(pie.RenderOverviewD2(tt.model)), tt.has...)
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
				consumer("folder", "folder"),
				consumer("orgs", "orgs"),
				consumer("tools", "orgs › tools, providers › tools"),
				consumer("org", "orgs › org"),
				consumer("account", "orgs › org › account"),
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
