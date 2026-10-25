package pie_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bmarinov/apiexport-pie/internal/pie"
)

func TestOverviewDiffWithLongKindNames(t *testing.T) {
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

func TestOverviewDiffWhenATypeChangesItsBindings(t *testing.T) {
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

func TestOverviewDiffWhenExistingWorkspacesKeepKindsThatMoved(t *testing.T) {
	export := func(name string, resources ...pie.Resource) pie.Export {
		return pie.Export{Path: "pms/" + name + ".yaml", Doc: 1, Name: name, Resources: resources}
	}
	provider := func(exports ...string) pie.WorkspaceType {
		wt := pie.WorkspaceType{Path: "types/provider.yaml", Doc: 1, Name: "provider"}
		for _, e := range exports {
			wt.Binds = append(wt.Binds, pie.ExportRef{Path: "root:pms", Name: e})
		}
		return wt
	}
	binding := func(file, export string) pie.APIBinding {
		return pie.APIBinding{Path: file, Doc: 1, Name: export, Export: pie.ExportRef{Path: "root:pms", Name: export}}
	}
	schemas := []pie.Schema{widgetSchemaAt("pms/widgets.yaml"), gadgetSchemaAt("pms/gadgets.yaml")}
	a := pie.Model{
		Schemas:        schemas,
		Exports:        []pie.Export{export("core.example.io", widgetResource(), gadgetResource())},
		WorkspaceTypes: []pie.WorkspaceType{provider("core.example.io")},
		APIBindings:    []pie.APIBinding{binding("d/core.yaml", "core.example.io")},
	}
	b := pie.Model{
		Schemas:        schemas,
		Exports:        []pie.Export{export("core.example.io", widgetResource()), export("ui.example.io", gadgetResource())},
		WorkspaceTypes: []pie.WorkspaceType{provider("core.example.io", "ui.example.io")},
		APIBindings:    []pie.APIBinding{binding("d/core.yaml", "core.example.io"), binding("d/ui.yaml", "ui.example.io")},
	}

	got := string(pie.RenderOverviewDiffD2(a, b))

	t.Run("the binding of the new export that existing workspaces have is red, and says kcp leaves it without the kinds", func(t *testing.T) {
		wantBlocks(t, got,
			`"consumers"."apibindings:d" -> "apiexports"."workspace:root:pms"."apiexport:ui.example.io" {style.stroke: "#e03131"; style.stroke-width: 3}`,
			legendBindsRefused)
		if want := fmt.Sprintf("\"story\": %q {class: story;", "bound at provider (new workspaces only), d (existing: NamingConflicts)\nmaximalPermissionPolicy: none"); !strings.Contains(got, want) {
			t.Errorf("RenderOverviewDiffD2() =\n%s\nwant a line with %s", got, want)
		}
	})

	t.Run("a binding existing workspaces do not get stays as it is", func(t *testing.T) {
		wantBlocks(t, got, `"consumers"."workspacetype:provider" -> "apiexports"."workspace:root:pms"."apiexport:ui.example.io" {style.stroke: "#1098ad"}`)
	})
}

func TestOverviewDiffWhenAnExportStopsServingKinds(t *testing.T) {
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

func TestOverviewDiffOfAConsumerBindingOutsideTheTree(t *testing.T) {
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

func TestOverviewDiffOfAnExportsPolicyAndWarnings(t *testing.T) {
	core := coreExportAt("pms/core.yaml")
	local := core
	local.MaximalPermissionPolicy = "local"
	tests := []struct {
		name string
		a, b pie.Model
		has  string
	}{
		{
			name: "B sets a maximal permission policy A did not",
			a:    pie.Model{Exports: []pie.Export{core}},
			b:    pie.Model{Exports: []pie.Export{local}},
			has:  `    "story": "+maximalPermissionPolicy: local\n−maximalPermissionPolicy: none" {class: story; height: 34}`,
		},
		{
			name: "only A has the v1alpha1 warning",
			a:    pie.Model{Exports: []pie.Export{withV1alpha1(core)}},
			b:    pie.Model{Exports: []pie.Export{core}},
			has:  `    "warnings": "−! apis.kcp.io/v1alpha1 is deprecated" {class: warning; height: 17}`,
		},
		{
			name: "only B has the v1alpha1 warning",
			a:    pie.Model{Exports: []pie.Export{core}},
			b:    pie.Model{Exports: []pie.Export{withV1alpha1(core)}},
			has:  `    "warnings": "+! apis.kcp.io/v1alpha1 is deprecated" {class: warning; height: 17}`,
		},
		{
			name: "only B has a warning that takes two rows",
			a:    pie.Model{Exports: []pie.Export{core}},
			b:    pie.Model{Exports: []pie.Export{withV1alpha1(withClaims(core, pie.Claim{Resource: "secrets", Verbs: []string{"*"}}))}},
			has:  `    "warnings": "+! apis.kcp.io/v1alpha1 is deprecated\n→ claims get verbs *" {class: warning; height: 34}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantBlocks(t, string(pie.RenderOverviewDiffD2(tt.a, tt.b)), tt.has)
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

// TestOverviewDiff pins whole diffs: how both trees merge into one overview, in B's
// order then A's. The other tests of the diff assert only the parts each is about.
func TestOverviewDiff(t *testing.T) {
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
				WorkspaceTypes: []pie.WorkspaceType{bindsInPMS("types/account.yaml", "account", "core.example.io")},
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
				WorkspaceTypes: []pie.WorkspaceType{bindsInPMS("types/account.yaml", "account", "core.example.io")},
			},
			b: pie.Model{
				Exports: []pie.Export{
					exportOf("pms/core.yaml", "core.example.io", widgetResource()),
					exportOf("pms/other.yaml", "other.example.io", gadgetResource()),
				},
				WorkspaceTypes: []pie.WorkspaceType{bindsInPMS("types/account.yaml", "account", "core.example.io", "other.example.io")},
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pie.RenderOverviewDiffD2(tt.a, tt.b)); got != overviewHead+tt.want {
				t.Errorf("RenderOverviewDiffD2() =\n%s\nwant\n%s", got, overviewHead+tt.want)
			}
		})
	}
}

func TestOverviewDiffWhenOneTreeHasMore(t *testing.T) {
	panels := pie.Resource{Group: "ui.example.io", Name: "panels"}
	// gone has, in A only, a WorkspaceType binding an export of its own group.
	gone := [2]pie.Model{
		{
			Exports: []pie.Export{
				exportOf("pms/core.yaml", "core.example.io", widgetResource()),
				exportOf("pms/old.yaml", "old.example.io", panels),
			},
			WorkspaceTypes: []pie.WorkspaceType{
				bindsInPMS("types/account.yaml", "account", "core.example.io"),
				bindsInPMS("types/org.yaml", "org", "old.example.io"),
			},
		},
		{
			Exports:        []pie.Export{exportOf("pms/core.yaml", "core.example.io", widgetResource())},
			WorkspaceTypes: []pie.WorkspaceType{bindsInPMS("types/account.yaml", "account", "core.example.io")},
		},
	}
	// reach has a claim that reads in A and writes in B.
	reach := [2]pie.Model{
		{Exports: []pie.Export{
			exportOf("pms/core.yaml", "core.example.io", widgetResource()),
			withClaims(exportOf("pms/reader.yaml", "reader.example.io"), pie.Claim{Group: "core.example.io", Resource: "widgets", Verbs: []string{"get"}}),
		}},
		{Exports: []pie.Export{
			exportOf("pms/core.yaml", "core.example.io", widgetResource()),
			withClaims(exportOf("pms/reader.yaml", "reader.example.io"), pie.Claim{Group: "core.example.io", Resource: "widgets", Verbs: []string{"update"}}),
		}},
	}
	// verdicts has, in B only, a second export serving widgets, bound next to the first.
	verdicts := [2]pie.Model{
		{
			Exports:        []pie.Export{exportOf("ws/a.yaml", "a.example.io", widgetResource())},
			WorkspaceTypes: []pie.WorkspaceType{bindsInPMS("types/account.yaml", "account", "a.example.io")},
		},
		{
			Exports: []pie.Export{
				exportOf("ws/a.yaml", "a.example.io", widgetResource()),
				exportOf("ws/b.yaml", "b.example.io", widgetResource()),
			},
			WorkspaceTypes: []pie.WorkspaceType{bindsInPMS("types/account.yaml", "account", "a.example.io", "b.example.io")},
		},
	}
	// copies has, in B, an export written twice and a new export sharing a kind with it.
	copies := [2]pie.Model{
		{
			Exports:        []pie.Export{exportOf("a/core.yaml", "core.example.io", widgetResource())},
			WorkspaceTypes: []pie.WorkspaceType{bindsInPMS("types/account.yaml", "account", "core.example.io")},
		},
		{
			Exports: []pie.Export{
				exportOf("a/core.yaml", "core.example.io", widgetResource(), gadgetResource()),
				exportOf("b/core.yaml", "core.example.io", widgetResource(), gadgetResource()),
				exportOf("c/other.yaml", "other.example.io", gadgetResource()),
			},
			WorkspaceTypes: []pie.WorkspaceType{bindsInPMS("types/account.yaml", "account", "core.example.io")},
		},
	}
	tests := []struct {
		name string
		a, b pie.Model
		has  []string
	}{
		{
			name: "what only A has is grape: a WorkspaceType, an export with its story, a group and its kinds, and their arrows",
			a:    gone[0], b: gone[1],
			has: []string{
				`  "group:ui.example.io": {
    label: "ui.example.io"
    class: group
    style.stroke: "#ae3ec9"
    style.fill: "#f8f0fc"
    "panels.ui.example.io": "panels · not in tree" {class: kind; style.stroke: "#ae3ec9"; style.fill: "#f8f0fc"}
  }`,
				`  "workspace:root:pms"."apiexport:old.example.io": {
    label: "old.example.io"
    class: export
    style.stroke: "#ae3ec9"
    style.fill: "#f8f0fc"
    grid-columns: 1
    grid-gap: 0
    "story": "bound at org\nmaximalPermissionPolicy: none" {class: story; height: 34}
  }`,
				`  "workspacetype:org": "org" {class: workspacetype; style.stroke: "#ae3ec9"; style.fill: "#f8f0fc"}`,
				`"apiexports"."workspace:root:pms"."apiexport:old.example.io" -> "resources"."group:ui.example.io"."panels.ui.example.io" {style.stroke: "#ae3ec9"}`,
				`"consumers"."workspacetype:org" -> "apiexports"."workspace:root:pms"."apiexport:old.example.io" {style.stroke: "#ae3ec9"}`,
				legendOnlyInA,
			},
		},
		{
			name: "what both trees have keeps its colours next to what only A has",
			a:    gone[0], b: gone[1],
			has: []string{
				`    "widgets.core.example.io": "widgets · not in tree" {class: kind}`,
				`  "workspacetype:account": "account" {class: workspacetype}`,
				`"consumers"."workspacetype:account" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#1971c2"}`,
			},
		},
		{
			name: "a claim whose reach changed shows both arrows, the old grape and the new cyan, each with its weight",
			a:    reach[0], b: reach[1],
			has: []string{
				`"apiexports"."apiexport:reader.example.io" -> "resources"."group:core.example.io": "write" {style.stroke: "#1098ad"; style.stroke-dash: 4; style.stroke-width: 4}`,
				`"apiexports"."apiexport:reader.example.io" -> "resources"."group:core.example.io": "read" {style.stroke: "#ae3ec9"; style.stroke-dash: 4}`,
				legendClaimsRead, legendClaimsWrite, legendOnlyInB, legendOnlyInA,
			},
		},
		{
			name: "a needs line only in B is marked +, only in A −, in the story",
			a:    reach[0], b: reach[1],
			has: []string{`    "story": "+writes widgets from core.example.io\n−reads widgets from core.example.io\nmaximalPermissionPolicy: none" {class: story; height: 51}`},
		},
		{
			name: "a new conflict stays red, as kcp's verdict says more than which tree it is in",
			a:    verdicts[0], b: verdicts[1],
			has: []string{
				`"consumers"."workspacetype:account" -> "apiexports"."workspace:root:pms"."apiexport:a.example.io": "conflicts with b.example.io" {style.stroke: "#e03131"; style.stroke-width: 3}`,
				`"consumers"."workspacetype:account" -> "apiexports"."workspace:root:pms"."apiexport:b.example.io": "conflicts with a.example.io" {style.stroke: "#e03131"; style.stroke-width: 3}`,
				`"consumers"."workspacetype:account" -> "apiexports"."workspace:root:pms"."apiexport:a.example.io" {style.stroke: "#ae3ec9"}`,
				legendBindsRefused,
			},
		},
		{
			name: "a kind two exports serve stays orange, and one only B serves is stroked cyan",
			a:    verdicts[0], b: verdicts[1],
			has: []string{
				`    "widgets.core.example.io": "widgets · not in tree" {class: kind; style.fill: "#f08c00"}`,
				`"apiexports"."workspace:root:pms"."apiexport:b.example.io" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#1098ad"}`,
				legendSharedKind,
			},
		},
		{
			name: "copies of an export count once, named by name, their arrows drawn once",
			a:    copies[0], b: copies[1],
			has: []string{
				`"apiexports": {
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
  "apiexport:other.example.io": {`,
				`"apiexports"."workspace:root:pms"."apiexport:core.example.io" -> "resources"."group:core.example.io"."widgets.core.example.io" {style.stroke: "#868e96"}
"apiexports"."workspace:root:pms"."apiexport:core.example.io" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#1098ad"}
"apiexports"."apiexport:other.example.io" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#1098ad"}
"consumers"."workspacetype:account" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#1971c2"}`,
			},
		},
		{
			name: "a new kind two exports serve is orange, stroked cyan, and tagged on the consumers that get it",
			a:    copies[0], b: copies[1],
			has: []string{
				`    "gadgets.core.example.io": "gadgets · not in tree" {class: kind; style.fill: "#f08c00"; style.stroke: "#1098ad"}`,
				`    "got": "got in B: gadgets" {style.stroke: "#1098ad"; style.stroke-dash: 3; style.fill: transparent; style.font-size: 11; style.bold: false; style.font-color: "#1098ad"}`,
				legendGotInB, legendSharedKind,
			},
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
			has: []string{
				`  "builtin": {
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
  }`,
				`"apiexports"."apiexport:reader.example.io" -> "resources"."builtin": "read" {style.stroke: "#1098ad"; style.stroke-dash: 4}`,
				legendOnlyInB,
			},
		},
		{
			name: "an export from outside the tree that a consumer of both trees binds only in B is marked +, only in A − at the end, in its line",
			a: pie.Model{WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account",
				Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}, {Path: "root", Name: "gone.kcp.io"}}}}},
			b: pie.Model{WorkspaceTypes: []pie.WorkspaceType{{Path: "types/account.yaml", Doc: 1, Name: "account",
				Binds: []pie.ExportRef{{Path: "root", Name: "tenancy.kcp.io"}, {Path: "root", Name: "topology.kcp.io"}}}}},
			has: []string{`  "workspacetype:account": "account\nbinds tenancy.kcp.io, +topology.kcp.io @ root (kcp)\nbinds −gone.kcp.io @ root (not in tree)" {class: workspacetype}`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantBlocks(t, string(pie.RenderOverviewDiffD2(tt.a, tt.b)), tt.has...)
		})
	}
}

func TestOverviewDiffWhenKindsMoveBetweenExports(t *testing.T) {
	// changed is a consumer of both trees whose kinds change, its title bold over a tag per direction.
	changed := func(id, title, class string, tags ...string) string {
		return fmt.Sprintf("  %q: {\n    label: %q\n    class: %s\n    style.bold: true\n    grid-columns: 1\n    grid-gap: 4\n%s  }\n", id, title, class, strings.Join(tags, ""))
	}
	tag := func(key, text, colour string) string {
		return fmt.Sprintf("    %q: %q {style.stroke: %q; style.stroke-dash: 3; style.fill: transparent; style.font-size: 11; style.bold: false; style.font-color: %q}\n", key, text, colour, colour)
	}
	lost := func(text string) string { return tag("lost", text, "#ae3ec9") }
	got := func(text string) string { return tag("got", text, "#1098ad") }
	sprockets := pie.Resource{Group: "core.example.io", Name: "sprockets", Schema: "v1.sprockets.core.example.io"}
	anchors := pie.Resource{Group: "core.example.io", Name: "anchors", Schema: "v1.anchors.core.example.io"}
	bolts := pie.Resource{Group: "core.example.io", Name: "bolts", Schema: "v1.bolts.core.example.io"}
	dirBinding := pie.APIBinding{Path: "x/y/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}}
	// Between a and b, core stops serving gadgets and anchors, which move to the new tools export,
	// and starts serving sprockets and bolts, and claiming configmaps. account keeps core; org adds
	// tools; guest swaps core for tools; team is new on tools; the binding directory keeps core.
	a := pie.Model{
		Exports:        []pie.Export{exportOf("pms/core.yaml", "core.example.io", widgetResource(), gadgetResource(), anchors)},
		Schemas:        []pie.Schema{widgetSchemaAt("pms/widgets.yaml")},
		WorkspaceTypes: []pie.WorkspaceType{bindsInPMS("types/account.yaml", "account", "core.example.io"), bindsInPMS("types/org.yaml", "org", "core.example.io"), bindsInPMS("types/guest.yaml", "guest", "core.example.io")},
		APIBindings:    []pie.APIBinding{dirBinding},
	}
	b := pie.Model{
		Exports: []pie.Export{
			withClaims(exportOf("pms/core.yaml", "core.example.io", widgetResource(), sprockets, bolts), pie.Claim{Resource: "configmaps", Verbs: []string{"get"}}),
			exportOf("pms/tools.yaml", "tools.example.io", gadgetResource(), anchors),
		},
		Schemas: []pie.Schema{widgetSchemaAt("pms/widgets.yaml")},
		WorkspaceTypes: []pie.WorkspaceType{
			bindsInPMS("types/account.yaml", "account", "core.example.io"),
			bindsInPMS("types/org.yaml", "org", "core.example.io", "tools.example.io"),
			bindsInPMS("types/guest.yaml", "guest", "tools.example.io"),
			bindsInPMS("types/team.yaml", "team", "tools.example.io"),
		},
		APIBindings: []pie.APIBinding{dirBinding},
	}
	tests := []struct {
		name string
		has  []string
	}{
		{
			name: "a consumer both trees have whose kinds change keeps its look and gets a dashed tag per direction: the kinds it had in A in grape, those it got in B in cyan, by name",
			has: []string{
				changed("workspacetype:account", "account", "workspacetype", lost("had in A: anchors,\ngadgets"), got("got in B: bolts,\nsprockets")),
				changed("apibindings:x/y", "APIBindings in y", "apibindings", lost("had in A: anchors,\ngadgets"), got("got in B: bolts,\nsprockets")),
				legendGotInB, legendHadInA,
			},
		},
		{
			name: "a tag counts the kinds the exports a consumer binds serve, not what they claim, so configmaps is no gain",
			has:  []string{got("got in B: bolts,\nsprockets")},
		},
		{
			name: "a consumer that only gains or only loses kinds has one tag",
			has: []string{
				changed("workspacetype:org", "org", "workspacetype", got("got in B: bolts,\nsprockets")),
				changed("workspacetype:guest", "guest", "workspacetype", lost("had in A: Widget")),
			},
		},
		{
			name: "tags wrap to about the title's width",
			has:  []string{lost("had in A: anchors,\ngadgets")},
		},
		{
			name: "a consumer only B has is filled cyan, without tags",
			has:  []string{`  "workspacetype:team": "team" {class: workspacetype; style.stroke: "#1098ad"; style.fill: "#e3fafc"}`},
		},
		{
			name: "the kinds themselves are drawn by tree: new ones cyan, moved ones uncoloured with their old arrow grape",
			has: []string{
				`    "sprockets.core.example.io": "sprockets · not in tree" {class: kind; style.stroke: "#1098ad"; style.fill: "#e3fafc"}`,
				`    "gadgets.core.example.io": "gadgets · not in tree" {class: kind}`,
				`"apiexports"."workspace:root:pms"."apiexport:core.example.io" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#ae3ec9"}`,
				`"apiexports"."workspace:root:pms"."apiexport:tools.example.io" -> "resources"."group:core.example.io"."gadgets.core.example.io" {style.stroke: "#1098ad"}`,
			},
		},
		{
			name: "the stories of both exports say who binds them, with the lifecycle of each change",
			has: []string{
				`    "story": "bound at account, org, y, −guest (existing workspaces keep it)\nexisting workspaces keep anchors, gadgets\nmaximalPermissionPolicy: none" {class: story; height: 51}`,
				`    "story": "bound at org (new workspaces only), guest (new workspaces only), team\nmaximalPermissionPolicy: none" {class: story; height: 34}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantBlocks(t, string(pie.RenderOverviewDiffD2(a, b)), tt.has...)
		})
	}
}

func TestOverviewDiffWhenBindersAndClaimsChange(t *testing.T) {
	// Between a and b, team and a second binding directory start binding core, org stops, and the
	// reader's claim grows from get to create.
	a := pie.Model{
		Exports: []pie.Export{
			exportOf("pms/core.yaml", "core.example.io", widgetResource()),
			withClaims(exportOf("pms/reader.yaml", "reader.example.io"), pie.Claim{Group: "core.example.io", Resource: "widgets", Verbs: []string{"get"}}),
		},
		WorkspaceTypes: []pie.WorkspaceType{bindsInPMS("types/account.yaml", "account", "core.example.io"), bindsInPMS("types/org.yaml", "org", "core.example.io")},
		APIBindings:    []pie.APIBinding{{Path: "a/bind/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}}},
	}
	b := pie.Model{
		Exports: []pie.Export{
			exportOf("pms/core.yaml", "core.example.io", widgetResource()),
			withClaims(exportOf("pms/reader.yaml", "reader.example.io"), pie.Claim{Group: "core.example.io", Resource: "widgets", Verbs: []string{"get", "create"}}),
		},
		WorkspaceTypes: []pie.WorkspaceType{bindsInPMS("types/account.yaml", "account", "core.example.io"), bindsInPMS("types/team.yaml", "team", "core.example.io")},
		APIBindings: []pie.APIBinding{
			{Path: "a/bind/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}},
			{Path: "b/bind/core.yaml", Doc: 1, Name: "core", Export: pie.ExportRef{Path: "root:pms", Name: "core.example.io"}},
		},
	}
	tests := []struct {
		name string
		has  []string
	}{
		{
			name: "a consumer binding it only in B is marked +, one only in A − at the end",
			has:  []string{`    "story": "bound at account, +team, a/bind, +b/bind, −org\nmaximalPermissionPolicy: none" {class: story; height: 34}`},
		},
		{
			name: "binding directories are told apart by id, not by their short name, which changes with the other directories",
			has: []string{
				`  "apibindings:a/bind": "APIBindings in a/bind" {class: apibindings}`,
				`  "apibindings:b/bind": "APIBindings in b/bind" {class: apibindings; style.stroke: "#1098ad"; style.fill: "#e3fafc"}`,
				`"consumers"."apibindings:a/bind" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#2f9e44"}`,
				`"consumers"."apibindings:b/bind" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#1098ad"}`,
			},
		},
		{
			name: "the consumers themselves are coloured by tree, and so are their arrows",
			has: []string{
				`  "workspacetype:team": "team" {class: workspacetype; style.stroke: "#1098ad"; style.fill: "#e3fafc"}`,
				`  "workspacetype:org": "org" {class: workspacetype; style.stroke: "#ae3ec9"; style.fill: "#f8f0fc"}`,
				`"consumers"."workspacetype:team" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#1098ad"}`,
				`"consumers"."workspacetype:org" -> "apiexports"."workspace:root:pms"."apiexport:core.example.io" {style.stroke: "#ae3ec9"}`,
			},
		},
		{
			name: "a needs line only in B is marked +, only in A −, and both claim arrows are drawn in their tree's colour",
			has: []string{
				`    "story": "+writes widgets from core.example.io\n−reads widgets from core.example.io\nmaximalPermissionPolicy: none" {class: story; height: 51}`,
				`"apiexports"."apiexport:reader.example.io" -> "resources"."group:core.example.io": "write" {style.stroke: "#1098ad"; style.stroke-dash: 4; style.stroke-width: 4}`,
				`"apiexports"."apiexport:reader.example.io" -> "resources"."group:core.example.io": "read" {style.stroke: "#ae3ec9"; style.stroke-dash: 4}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantBlocks(t, string(pie.RenderOverviewDiffD2(a, b)), tt.has...)
		})
	}
}

// bindsInPMS is a WorkspaceType at path, by name, binding exports in root:pms.
func bindsInPMS(path, name string, exports ...string) pie.WorkspaceType {
	wt := pie.WorkspaceType{Path: path, Doc: 1, Name: name}
	for _, e := range exports {
		wt.Binds = append(wt.Binds, pie.ExportRef{Path: "root:pms", Name: e})
	}
	return wt
}
