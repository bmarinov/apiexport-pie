package pie_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/bmarinov/apiexport-pie/internal/pie"
)

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
