package pie_test

import (
	"testing"

	"github.com/bmarinov/apiexport-pie/internal/pie"
)

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
added: claim by core.example.io on configmaps: get, create (defaultSelector)
`,
		},
		{
			name: "a claim whose verbs or selector change is changed, its verbs compared as a set",
			a: pie.Model{Exports: []pie.Export{claiming("pms/reader.yaml", "reader.example.io",
				claim("core.example.io", "widgets", "get"), claim("", "secrets", "list", "get"), claim("", "events", "*"))}},
			b: pie.Model{Exports: []pie.Export{claiming("pms/reader.yaml", "reader.example.io",
				claim("core.example.io", "widgets", "get", "create"), claim("", "secrets", "get", "list"), selected(claim("", "events", "*")))}},
			want: `changed: claim by reader.example.io on widgets.core.example.io: from get to get, create
changed: claim by reader.example.io on events: from * to * (defaultSelector)
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
