package pie

import (
	"path"
	"slices"
	"strings"
)

// ownWorkspace stands in for an empty export path, which kcp reads as the workspace of the
// WorkspaceType that names it.
const ownWorkspace = "(own workspace)"

// bindingDir is a directory of APIBinding files and the exports they bind, each once.
type bindingDir struct {
	dir   string
	bound []binding
}

// bindingsByDir groups APIBinding files by directory, in the order the directories first appear.
// A directory of manifests is usually applied to one workspace, so it reads as one consumer.
func bindingsByDir(bindings []APIBinding) []bindingDir {
	var dirs []bindingDir
	at := map[string]int{}
	for _, ab := range bindings {
		dir := path.Dir(ab.Path)
		i, ok := at[dir]
		if !ok {
			i = len(dirs)
			at[dir] = i
			dirs = append(dirs, bindingDir{dir: dir})
		}
		if bd := (binding{ref: ab.Export, relation: bindsByAPIBinding}); !slices.Contains(dirs[i].bound, bd) {
			dirs[i].bound = append(dirs[i].bound, bd)
		}
	}
	return dirs
}

// binding is one export a consumer binds. via names the WorkspaceType it is inherited from, and is
// empty when the consumer declares the binding itself.
type binding struct {
	ref      ExportRef
	via      string
	relation relation
}

func (b binding) where() string {
	if b.via == "" {
		return workspaceOf(b.ref)
	}
	return workspaceOf(b.ref) + " · via " + b.via
}

func workspaceOf(ref ExportRef) string {
	if ref.Path == "" {
		return ownWorkspace
	}
	return ref.Path
}

// chained is a type in an extend chain, and whether the references place it in root: a reference
// with the path root, or one without a path from a type in root, where kcp looks first.
type chained struct {
	WorkspaceType
	inRoot bool
}

// extendChain lists type t and the types it extends, transitively, each once, as kcp resolves them
// for a workspace of type t: depth first, in the order they are extended. Bases neither in the tree
// nor kcp's universal are left out, and a cycle stops where it started.
func extendChain(t WorkspaceType, types []WorkspaceType) []chained {
	var chain []chained
	visited := map[string]bool{}
	var visit func(t WorkspaceType, inRoot bool)
	visit = func(t WorkspaceType, inRoot bool) {
		if visited[t.Name] {
			return
		}
		visited[t.Name] = true
		chain = append(chain, chained{WorkspaceType: t, inRoot: inRoot})
		for _, base := range t.Extends {
			if bt, ok := baseOf(base, types); ok {
				visit(bt, base.Path == "root" || base.Path == "" && inRoot)
			}
		}
	}
	visit(t, false)
	return chain
}

// bindingsOf lists what every workspace of type t binds, as kcp's initializer does: the type's own
// default bindings, then those of each type it extends, transitively, each binding once.
func bindingsOf(t WorkspaceType, types []WorkspaceType) []binding {
	var bound []binding
	listed := map[ExportRef]bool{}
	for i, ct := range extendChain(t, types) {
		via, r := ct.Name, bindsThroughExtend
		if i == 0 {
			via, r = "", bindsByDefault
		}
		for _, ref := range ct.Binds {
			if !listed[ref] {
				listed[ref] = true
				bound = append(bound, binding{ref: ref, via: via, relation: r})
			}
		}
	}
	return bound
}

// lifecycleOf lists the initializers and terminators kcp gives the workspaces of type t: one for each
// type of its extend chain that sets initializer or terminator, its own included (kcp v0.32.3). Bases
// not in the tree say nothing: kcp's universal is initialized by kcp itself, the others are unknown.
func lifecycleOf(t WorkspaceType, types []WorkspaceType) []string {
	var initializers, terminators []string
	for _, ct := range extendChain(t, types) {
		if _, inTree := typeNamed(ct.Name, types); !inTree {
			continue
		}
		if ct.Initializer {
			initializers = append(initializers, lifecycleName(ct))
		}
		if ct.Terminator {
			terminators = append(terminators, lifecycleName(ct))
		}
	}
	if len(initializers) > 0 && slices.Equal(initializers, terminators) {
		return []string{"initializers, terminators: " + strings.Join(initializers, ", ")}
	}
	var lines []string
	if len(initializers) > 0 {
		lines = append(lines, "initializers: "+strings.Join(initializers, ", "))
	}
	if len(terminators) > 0 {
		lines = append(lines, "terminators: "+strings.Join(terminators, ", "))
	}
	return lines
}

// lifecycleName is how kcp names the initializer or terminator of a type: its logical cluster, a
// colon and its name (InitializerForType). root's logical cluster is named root; any other has a
// generated name no manifest holds, so there the type's name stands alone.
func lifecycleName(ct chained) string {
	if ct.inRoot {
		return "root:" + ct.Name
	}
	return ct.Name
}

// consumer is a set of workspaces that bind the same exports: those of one WorkspaceType, or those of
// one directory of APIBinding files. kcp names those of the exports that are kcp's own. withheld says,
// per export, the claims its binding file does not accept; kcp accepts every claim of a WorkspaceType's
// bindings, so those have none.
type consumer struct {
	name     string
	column   []string
	exports  []string
	kcp      []string
	withheld map[string][]GroupResource
}

// consumersOf lists the consumers of a tree: its WorkspaceTypes, then its directories of APIBindings.
func consumersOf(m Model) []consumer {
	exportsOf := func(bindings []binding) []string {
		var names []string
		for _, bd := range bindings {
			names = append(names, bd.ref.Name)
		}
		return names
	}
	kcpOf := func(bindings []binding) []string {
		var names []string
		for _, bd := range bindings {
			if isKcpExport(bd.ref) {
				names = append(names, bd.ref.Name)
			}
		}
		return names
	}
	var consumers []consumer
	for _, t := range m.WorkspaceTypes {
		bindings := bindingsOf(t, m.WorkspaceTypes)
		consumers = append(consumers, consumer{name: t.Name, column: []string{t.Name}, exports: exportsOf(bindings), kcp: kcpOf(bindings)})
	}
	for _, dir := range bindingsByDir(m.APIBindings) {
		withheld := map[string][]GroupResource{}
		for _, bd := range dir.bound {
			named := exportsNamed(bd.ref.Name, m.Exports)
			if len(named) == 0 {
				continue
			}
			accepts := firstBindingOf(bd.ref.Name, dir.dir, m.APIBindings).Accepts
			for _, c := range named[0].Claims {
				if gr := (GroupResource{Group: c.Group, Resource: c.Resource}); !slices.Contains(accepts, gr) {
					withheld[bd.ref.Name] = append(withheld[bd.ref.Name], gr)
				}
			}
		}
		consumers = append(consumers, consumer{
			name:     dirConsumerName(dir.dir),
			column:   append([]string{"APIBindings in"}, strings.SplitAfter(dir.dir, "/")...),
			exports:  exportsOf(dir.bound),
			kcp:      kcpOf(dir.bound),
			withheld: withheld,
		})
	}
	return consumers
}

// dirConsumerName names the consumer a directory of APIBinding files is.
func dirConsumerName(dir string) string { return "APIBindings in " + dir }

// universalRef is how a type extends root:universal, the WorkspaceType kcp creates in every
// installation (config/root/workspacetype-universal.yaml, the same from kcp v0.31 to v0.33).
var universalRef = TypeRef{Path: "root", Name: "universal"}

// kcpUniversal is root:universal as kcp creates it. Its initializer is kcp's own controller, which
// creates the default and kube-system namespaces. The integration test compares it with kcp at the
// version of the SDK in go.mod.
func kcpUniversal() WorkspaceType {
	return WorkspaceType{
		Name:        universalRef.Name,
		Binds:       []ExportRef{{Path: "root", Name: "tenancy.kcp.io"}, {Path: "root", Name: "topology.kcp.io"}},
		Initializer: true,
	}
}

// kcpExports are the APIExports kcp creates in root in every installation (config/root-phase0, the
// same in kcp v0.32 and v0.33; v0.31 has no migration.kcp.io). The integration test compares them with
// kcp at the version of the SDK in go.mod.
var kcpExports = []string{"cache.kcp.io", "migration.kcp.io", "shards.core.kcp.io", "tenancy.kcp.io", "topology.kcp.io"}

// isKcpExport tells whether a reference names one of kcp's own APIExports. It counts only where the
// tree has no export of that name.
func isKcpExport(ref ExportRef) bool {
	return ref.Path == "root" && slices.Contains(kcpExports, ref.Name)
}

// baseOf finds the type a WorkspaceType extends: the tree's type of that name, or else kcp's
// universal when it names root:universal.
func baseOf(ref TypeRef, types []WorkspaceType) (WorkspaceType, bool) {
	if t, ok := typeNamed(ref.Name, types); ok {
		return t, true
	}
	if ref == universalRef {
		return kcpUniversal(), true
	}
	return WorkspaceType{}, false
}

func typeNamed(name string, types []WorkspaceType) (WorkspaceType, bool) {
	for _, t := range types {
		if t.Name == name {
			return t, true
		}
	}
	return WorkspaceType{}, false
}

func exportsNamed(name string, exports []Export) []Export {
	var named []Export
	for _, e := range exports {
		if e.Name == name {
			named = append(named, e)
		}
	}
	return named
}
