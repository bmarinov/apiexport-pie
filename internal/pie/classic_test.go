package pie_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/bmarinov/apiexport-pie/internal/pie"
)

// TestRenderD2 pins whole diagrams: the order of tables, edges and legend. The other tests of the
// classic style assert only the parts each is about.
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
			name: "a WorkspaceType lists the exports it binds, where they live, and points at them",
			model: pie.Model{
				Exports: []pie.Export{{Path: "pms/export.yaml", Doc: 1, Name: "core.example.io"}},
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account",
					Binds: []pie.ExportRef{{Path: "root:pms", Name: "core.example.io"}}}},
			},
			want: `"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account"
  "core.example.io": "root:pms"
}
"workspace:root:pms": {
  label: "Workspace root:pms"
}
"workspace:root:pms"."apiexport:pms/export.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io"
}
"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#1971c2"}
` + legend(legendBindsByDefault),
		},
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderD2(tt.model)); got != elkLayout+tt.want {
				t.Errorf("RenderD2() =\n%s\nwant\n%s", got, elkLayout+tt.want)
			}
		})
	}
}

func TestRenderD2DrawsExports(t *testing.T) {
	tests := []struct {
		name  string
		model pie.Model
		has   []string
	}{
		{
			name: "marks a resource whose schema is not in the tree, naming it by its plural",
			model: pie.Model{Exports: []pie.Export{{Path: "ws/export.yaml", Doc: 1, Name: "legacy.example.io", Resources: []pie.Resource{
				{Group: "", Name: "configmaps", Schema: "v1.configmaps.core"},
				{Group: "legacy.example.io", Name: "things", Schema: "v260213-fbdf981.things.legacy.example.io"},
			}}}},
			has: []string{
				`  "configmaps": "configmaps" {constraint: "v1 · not in tree"}`,
				`  "things": "things.legacy.example.io" {constraint: "v260213-fbdf981 · not in tree"}`,
			},
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
			has: []string{
				`  "Widget (core.example.io)": "widgets.core.example.io" {constraint: "v1"}`,
				`  "Widget (other.example.io)": "widgets.other.example.io" {constraint: "v1"}`,
			},
		},
		{
			name: "takes the kind from the schema in the export's own directory first",
			model: pie.Model{
				Exports: []pie.Export{{Path: "b/export.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource()}}},
				Schemas: []pie.Schema{widgetSchemaKindAt("a/widgets.yaml", "WidgetInA"), widgetSchemaKindAt("b/widgets.yaml", "WidgetInB")},
			},
			has: []string{`  "WidgetInB": "widgets.core.example.io" {constraint: "v1"}`},
		},
		{
			name: "takes the kind from the first schema of that name elsewhere when its directory has none",
			model: pie.Model{
				Exports: []pie.Export{{Path: "c/export.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource()}}},
				Schemas: []pie.Schema{widgetSchemaKindAt("a/widgets.yaml", "WidgetInA"), widgetSchemaKindAt("b/widgets.yaml", "WidgetInB")},
			},
			has: []string{`  "WidgetInA": "widgets.core.example.io" {constraint: "v1"}`},
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
			has: []string{
				"\"apiexport:config/export.yaml#1\": {\n  shape: sql_table\n  label: \"APIExport core.example.io · config/export.yaml#1\"\n}",
				"\"apiexport:test/setup/export.yaml#1\": {\n  shape: sql_table\n  label: \"APIExport core.example.io · test/setup/export.yaml#1\"\n}",
				"\"apiexport:test/setup/provider1.yaml#1\": {\n  shape: sql_table\n  label: \"APIExport provider.example.io · test/setup/provider1.yaml#1\"\n}",
				"\"apiexport:test/setup/provider2.yaml#1\": {\n  shape: sql_table\n  label: \"APIExport provider.example.io · test/setup/provider2.yaml#1\"\n}",
				"\"apiexport:test/setup/both.yaml#1\": {\n  shape: sql_table\n  label: \"APIExport twice.example.io · test/setup/both.yaml#1\"\n}",
				"\"apiexport:test/setup/both.yaml#2\": {\n  shape: sql_table\n  label: \"APIExport twice.example.io · test/setup/both.yaml#2\"\n}",
				"\"apiexport:config/export.yaml#2\": {\n  shape: sql_table\n  label: \"APIExport other.example.io\"\n}",
			},
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
			has: []string{
				`"apiexport:ws/a.yaml#1": {
  shape: sql_table
  label: "APIExport a.example.io"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in b.example.io, c.example.io"}
  "Gadget": "gadgets.core.example.io" {constraint: "v1"}
}`,
				`"apiexport:ws/b.yaml#1": {
  shape: sql_table
  label: "APIExport b.example.io"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in a.example.io, c.example.io"}
}`,
				legendShared,
			},
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
			has: []string{
				`"apiexport:config/core.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · config/core.yaml#1"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in other.example.io"}
}`,
				`"apiexport:test/core.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · test/core.yaml#1"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in other.example.io"}
}`,
				`"apiexport:ws/other.yaml#1": {
  shape: sql_table
  label: "APIExport other.example.io"
  style.fill: "#f08c00"
  "Widget": "widgets.core.example.io" {constraint: "v1 · also in core.example.io"}
}`,
			},
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
			has: []string{
				`"apiexport:ws/a.yaml#1": {
  shape: sql_table
  label: "APIExport a.example.io"
  "Widget": "widgets.core.example.io" {constraint: "v1"}
}`,
				`"apiexport:ws/b.yaml#1": {
  shape: sql_table
  label: "APIExport b.example.io"
  "Widget": "widgets.other.example.io" {constraint: "v1"}
}`,
			},
		},
		{
			name: "lists schemas no export serves by name, with their kind, in a muted table",
			model: pie.Model{
				Exports: []pie.Export{{Path: "ws/export.yaml", Doc: 1, Name: "core.example.io", Resources: []pie.Resource{widgetResource()}}},
				Schemas: []pie.Schema{gadgetSchemaAt("ws/gadgets.yaml"), widgetSchemaAt("ws/widgets.yaml")},
			},
			has: []string{`"unserved": {
  shape: sql_table
  label: "APIResourceSchemas no export serves"
  style.fill: "#868e96"
  style.stroke-dash: 3
  "v1.gadgets.core.example.io": "Gadget"
}`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantBlocks(t, string(pie.RenderD2(tt.model)), tt.has...)
		})
	}
}

func TestRenderD2DrawsConsumers(t *testing.T) {
	coreHere := pie.Export{Path: "pms/export.yaml", Doc: 1, Name: "core.example.io"}
	tests := []struct {
		name  string
		model pie.Model
		has   []string
	}{
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
			has: []string{
				`  label: "WorkspaceType orgs · extends shared (not in tree) · workspace: orgs"`,
				`  label: "WorkspaceType account · workspaces: a, b"`,
			},
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
			has: []string{
				`"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account"
  "tenancy.kcp.io": "root"
  "local.example.io": "(own workspace)"
}`,
				`"outside": {
  label: "APIExports outside the tree"
  style.stroke-dash: 3
  "tenancy.kcp.io @ root (kcp)"
  "local.example.io @ (own workspace)"
}`,
				`"workspacetype:types/account.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"}`,
				`"workspacetype:types/account.yaml#1"."local.example.io" -> "outside"."local.example.io @ (own workspace)" {style.stroke: "#1971c2"}`,
				`"workspacetype:types/org.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"}`,
			},
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
			has: []string{
				`"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account · extends security, shared (not in tree)"
  "core.example.io": "root:pms"
  "audit.example.io": "root:audit · via security"
  "tenancy.kcp.io": "root · via base"
}`,
				`"workspacetype:types/security.yaml#1": {
  shape: sql_table
  label: "WorkspaceType security · extends base"
  "core.example.io": "root:pms"
  "audit.example.io": "root:audit"
  "tenancy.kcp.io": "root · via base"
}`,
				`"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#1971c2"}`,
				`"workspacetype:types/account.yaml#1"."audit.example.io" -> "outside"."audit.example.io @ root:audit" {style.stroke: "#1971c2"; style.stroke-dash: 2}`,
				`"workspacetype:types/account.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"; style.stroke-dash: 2}`,
				`"workspacetype:types/security.yaml#1"."audit.example.io" -> "outside"."audit.example.io @ root:audit" {style.stroke: "#1971c2"}`,
				legendBindsThroughExtend,
			},
		},
		{
			name: "a type extending universal in root binds kcp's tenancy.kcp.io and topology.kcp.io through it",
			model: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/orgs.yaml", Doc: 1, Name: "orgs", Extends: []pie.TypeRef{{Path: "root", Name: "universal"}}}},
			},
			has: []string{
				`"workspacetype:types/orgs.yaml#1": {
  shape: sql_table
  label: "WorkspaceType orgs · extends universal (kcp)"
  "tenancy.kcp.io": "root · via universal"
  "topology.kcp.io": "root · via universal"
}`,
				`"workspacetype:types/orgs.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"; style.stroke-dash: 2}`,
				`"workspacetype:types/orgs.yaml#1"."topology.kcp.io" -> "outside"."topology.kcp.io @ root (kcp)" {style.stroke: "#1971c2"; style.stroke-dash: 2}`,
			},
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
			has: []string{
				`"apibindings:pms": {
  shape: sql_table
  label: "APIBindings in pms"
  "core.example.io": "root:pms"
  "system.example.io": "(own workspace)"
}`,
				`"apibindings:features/x": {
  shape: sql_table
  label: "APIBindings in features/x"
  "providers.example.io": "root:pms"
}`,
				`"outside": {
  label: "APIExports outside the tree"
  style.stroke-dash: 3
  "system.example.io @ (own workspace)"
  "providers.example.io @ root:pms"
}`,
				`"apibindings:pms"."core.example.io" -> "workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#2f9e44"}`,
				`"apibindings:pms"."system.example.io" -> "outside"."system.example.io @ (own workspace)" {style.stroke: "#2f9e44"}`,
				`"apibindings:features/x"."providers.example.io" -> "outside"."providers.example.io @ root:pms" {style.stroke: "#2f9e44"}`,
				legendBindsByAPIBinding,
			},
		},
		{
			name: "WorkspaceTypes and APIBinding files share the box of an export outside the tree",
			model: pie.Model{
				WorkspaceTypes: []pie.WorkspaceType{{Path: "types/org.yaml", Doc: 1, Name: "org", Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}}}},
				APIBindings:    []pie.APIBinding{{Path: "pms/t.yaml", Doc: 1, Name: "tenancy", Export: pie.ExportRef{Path: "root", Name: "tenancy.kcp.io"}}},
			},
			has: []string{
				`"outside": {
  label: "APIExports outside the tree"
  style.stroke-dash: 3
  "tenancy.kcp.io @ root (kcp)"
}`,
				`"workspacetype:types/org.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"}`,
				`"apibindings:pms"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#2f9e44"}`,
			},
		},
		{
			name: "types that extend each other stop where they started",
			model: pie.Model{WorkspaceTypes: []pie.WorkspaceType{
				{Path: "types/a.yaml", Doc: 1, Name: "a", Binds: []pie.ExportRef{{Path: "root", Name: "a.example.io"}}, Extends: []pie.TypeRef{{Name: "b"}}},
				{Path: "types/b.yaml", Doc: 1, Name: "b", Binds: []pie.ExportRef{{Path: "root", Name: "b.example.io"}}, Extends: []pie.TypeRef{{Name: "a"}}},
			}},
			has: []string{
				`"workspacetype:types/a.yaml#1": {
  shape: sql_table
  label: "WorkspaceType a · extends b"
  "a.example.io": "root"
  "b.example.io": "root · via b"
}`,
				`"workspacetype:types/b.yaml#1": {
  shape: sql_table
  label: "WorkspaceType b · extends a"
  "b.example.io": "root"
  "a.example.io": "root · via a"
}`,
			},
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
			has: []string{
				`  label: "WorkspaceType account · types/account.yaml#1"`,
				`  label: "WorkspaceType account · test/account.yaml#1"`,
				`"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/export.yaml#1" {style.stroke: "#1971c2"}`,
				`"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:test/export.yaml#1" {style.stroke: "#1971c2"}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantBlocks(t, string(pie.RenderD2(tt.model)), tt.has...)
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
		has   []string
	}{
		{
			name: "an export's header counts its permission claims",
			model: pie.Model{Exports: []pie.Export{{Path: "ws/export.yaml", Doc: 1, Name: "core.example.io", Claims: []pie.Claim{
				{Group: "tenancy.kcp.io", Resource: "workspaces", Verbs: []string{"*"}},
				{Group: "", Resource: "secrets", Verbs: []string{"get"}},
			}}}},
			has: []string{`  label: "APIExport core.example.io · 2 claims"`},
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
			has: []string{
				`  label: "APIExport terminal.example.io · 1 claim"`,
				`"apiexport:pms/terminal.yaml#1" -> "apiexport:pms/core.yaml#1"."AccountInfo": "claims get, list, watch" {style.stroke: "#7048e8"; style.stroke-dash: 4}`,
				legendClaims,
			},
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
			has: []string{
				`"apiexport:pms/terminal.yaml#1" -> "apiexport:pms/core.yaml#1"."accountinfos": "claims get, list, watch" {style.stroke: "#7048e8"; style.stroke-dash: 4}`,
				`"apiexport:pms/terminal.yaml#1" -> "apiexport:test/core.yaml#1"."accountinfos": "claims get, list, watch" {style.stroke: "#7048e8"; style.stroke-dash: 4}`,
			},
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
			has: []string{`"apiexport:ws/user.yaml#1" -> "apiexport:ws/mixed.yaml#1"."Widget (other.example.io)": "claims *" {style.stroke: "#7048e8"; style.stroke-dash: 4}`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantBlocks(t, string(pie.RenderD2(tt.model)), tt.has...)
		})
	}
}

func TestRenderD2FlagsBindingsKcpRefuses(t *testing.T) {
	tests := []struct {
		name  string
		model pie.Model
		has   []string
	}{
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
			has: []string{
				`"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account · extends base"
  style.fill: "#e03131"
  "a.example.io": "root:pms · conflicts with b.example.io on widgets.core.example.io"
  "b.example.io": "root:pms · via base · conflicts with a.example.io on widgets.core.example.io"
}`,
				`"workspacetype:types/base.yaml#1": {
  shape: sql_table
  label: "WorkspaceType base"
  "b.example.io": "root:pms"
}`,
				`"workspacetype:types/account.yaml#1"."b.example.io" -> "workspace:root:pms"."apiexport:ws/b.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}`,
				`"workspacetype:types/base.yaml#1"."b.example.io" -> "workspace:root:pms"."apiexport:ws/b.yaml#1" {style.stroke: "#1971c2"}`,
			},
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
			has: []string{
				`"apibindings:pms": {
  shape: sql_table
  label: "APIBindings in pms"
  style.fill: "#e03131"
  "a.example.io": "root:pms · conflicts with b.example.io on widgets.core.example.io"
  "b.example.io": "root:pms · conflicts with a.example.io on widgets.core.example.io"
}`,
				`"apibindings:pms"."a.example.io" -> "workspace:root:pms"."apiexport:ws/a.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}`,
				`"apibindings:pms"."b.example.io" -> "workspace:root:pms"."apiexport:ws/b.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}`,
			},
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
			has: []string{
				`"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account"
  style.fill: "#e03131"
  "core.example.io": "root:pms · conflicts with other.example.io on widgets.core.example.io"
  "other.example.io": "root:pms · conflicts with core.example.io on widgets.core.example.io"
}`,
				`"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}`,
				`"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:test/core.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}`,
				`"workspacetype:types/account.yaml#1"."other.example.io" -> "workspace:root:pms"."apiexport:ws/other.yaml#1" {style.stroke: "#e03131"; style.stroke-width: 3}`,
			},
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
			has: []string{
				`"workspacetype:types/account.yaml#1": {
  shape: sql_table
  label: "WorkspaceType account"
  "core.example.io": "root:pms"
  "tenancy.kcp.io": "root"
}`,
				`"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#1971c2"}`,
				`"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:test/core.yaml#1" {style.stroke: "#1971c2"}`,
				`"workspacetype:types/account.yaml#1"."tenancy.kcp.io" -> "outside"."tenancy.kcp.io @ root (kcp)" {style.stroke: "#1971c2"}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantBlocks(t, string(pie.RenderD2(tt.model)), tt.has...)
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
			wantBlocks(t, string(pie.RenderD2(model)), "  "+tt.wantRow)
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
		has   []string
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
			has: []string{
				`"workspace:root:pms": {
  label: "Workspace root:pms"
}`,
				`"workspace:root:pms"."apiexport:pms/core.yaml#1": {`,
				`"workspace:root:pms"."apiexport:pms/system.yaml#1": {`,
				`"apiexport:x/term.yaml#1": {`,
				`"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#1971c2"}`,
				`"apibindings:pms"."system.example.io" -> "workspace:root:pms"."apiexport:pms/system.yaml#1" {style.stroke: "#2f9e44"}`,
				`"apiexport:x/term.yaml#1" -> "workspace:root:pms"."apiexport:pms/core.yaml#1"."AccountInfo": "claims get" {style.stroke: "#7048e8"; style.stroke-dash: 4}`,
			},
		},
		{
			name: "an empty path names no workspace, so it neither places an export nor contradicts a path",
			model: pie.Model{
				Exports:        []pie.Export{core},
				WorkspaceTypes: account(pie.ExportRef{Name: "core.example.io"}),
				APIBindings:    bindingInPMS(pie.ExportRef{Path: "root:pms", Name: "core.example.io"}),
			},
			has: []string{
				`  "core.example.io": "(own workspace)"`,
				`"workspace:root:pms"."apiexport:pms/core.yaml#1": {`,
				`"workspacetype:types/account.yaml#1"."core.example.io" -> "workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#1971c2"}`,
				`"apibindings:pms"."core.example.io" -> "workspace:root:pms"."apiexport:pms/core.yaml#1" {style.stroke: "#2f9e44"}`,
			},
		},
		{
			name: "references that disagree on the workspace leave the export ungrouped, and say so",
			model: pie.Model{
				Exports:        []pie.Export{core},
				WorkspaceTypes: account(pie.ExportRef{Path: "root:a", Name: "core.example.io"}),
				APIBindings:    bindingInPMS(pie.ExportRef{Path: "root:b", Name: "core.example.io"}),
			},
			has: []string{
				`"apiexport:pms/core.yaml#1": {
  shape: sql_table
  label: "APIExport core.example.io · references disagree: root:a, root:b"
}`,
				`"workspacetype:types/account.yaml#1"."core.example.io" -> "apiexport:pms/core.yaml#1" {style.stroke: "#1971c2"}`,
				`"apibindings:pms"."core.example.io" -> "apiexport:pms/core.yaml#1" {style.stroke: "#2f9e44"}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantBlocks(t, string(pie.RenderD2(tt.model)), tt.has...)
		})
	}
}

func TestRenderD2ExplainsTheRelationsItDraws(t *testing.T) {
	t.Run("the legend lists relations in a fixed order, whichever is drawn first", func(t *testing.T) {
		model := pie.Model{WorkspaceTypes: []pie.WorkspaceType{
			{Path: "types/account.yaml", Doc: 1, Name: "account", Extends: []pie.TypeRef{{Name: "base"}}},
			{Path: "types/base.yaml", Doc: 1, Name: "base", Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}}},
		}}

		wantBlocks(t, string(pie.RenderD2(model)), legend(legendBindsByDefault, legendBindsThroughExtend))
	})

	t.Run("a diagram with no edges and nothing orange has no legend", func(t *testing.T) {
		got := string(pie.RenderD2(pie.Model{Exports: []pie.Export{coreExportAt("ws/export.yaml")}}))

		if strings.Contains(got, `"legend"`) {
			t.Errorf("RenderD2() =\n%s\nwant no legend", got)
		}
	})
}

// exportOf is an export at path, by name, serving resources.
func exportOf(path, name string, resources ...pie.Resource) pie.Export {
	return pie.Export{Path: path, Doc: 1, Name: name, Resources: resources}
}
