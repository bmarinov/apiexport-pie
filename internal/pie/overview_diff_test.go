package pie_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bmarinov/apiexport-pie/internal/pie"
)

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
		{
			name: "a warning on two rows is marked once, on its first",
			a:    pie.Model{Exports: []pie.Export{core}},
			b:    pie.Model{Exports: []pie.Export{withV1alpha1(withClaims(core, pie.Claim{Resource: "secrets", Verbs: []string{"*"}}))}},
			want: `"warnings": "+! apis.kcp.io/v1alpha1 is deprecated\n→ claims get verbs *" {class: warning; height: 34}`,
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
