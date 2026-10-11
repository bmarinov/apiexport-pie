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

// extendChain lists type t and the types it extends, transitively, each once, as kcp resolves them
// for a workspace of type t: depth first, in the order they are extended. Bases not in the tree are
// left out, and a cycle stops where it started.
func extendChain(t WorkspaceType, types []WorkspaceType) []WorkspaceType {
	var chain []WorkspaceType
	visited := map[string]bool{}
	var visit func(t WorkspaceType)
	visit = func(t WorkspaceType) {
		if visited[t.Name] {
			return
		}
		visited[t.Name] = true
		chain = append(chain, t)
		for _, base := range t.Extends {
			if bt, ok := typeNamed(base.Name, types); ok {
				visit(bt)
			}
		}
	}
	visit(t)
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

// lifecycleOf says which types initialize and terminate the workspaces of type t: those of its
// extend chain that set initializer or terminator, its own included (kcp v0.32.3). Bases not in the
// tree are unknown, so they say nothing.
func lifecycleOf(t WorkspaceType, types []WorkspaceType) []string {
	var initializers, terminators []string
	for _, ct := range extendChain(t, types) {
		if ct.Initializer {
			initializers = append(initializers, ct.Name)
		}
		if ct.Terminator {
			terminators = append(terminators, ct.Name)
		}
	}
	if len(initializers) > 0 && slices.Equal(initializers, terminators) {
		return []string{"initialized and terminated by " + strings.Join(initializers, ", ")}
	}
	var lines []string
	if len(initializers) > 0 {
		lines = append(lines, "initialized by "+strings.Join(initializers, ", "))
	}
	if len(terminators) > 0 {
		lines = append(lines, "terminated by "+strings.Join(terminators, ", "))
	}
	return lines
}

// consumer is a set of workspaces that bind the same exports: those of one WorkspaceType, or those of
// one directory of APIBinding files. withheld says, per export, the claims its binding file does not
// accept; kcp accepts every claim of a WorkspaceType's bindings, so those have none.
type consumer struct {
	name     string
	column   []string
	exports  []string
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
	var consumers []consumer
	for _, t := range m.WorkspaceTypes {
		consumers = append(consumers, consumer{name: t.Name, column: []string{t.Name}, exports: exportsOf(bindingsOf(t, m.WorkspaceTypes))})
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
			name:     "APIBindings in " + dir.dir,
			column:   append([]string{"APIBindings in"}, strings.SplitAfter(dir.dir, "/")...),
			exports:  exportsOf(dir.bound),
			withheld: withheld,
		})
	}
	return consumers
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
