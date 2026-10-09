package pie

import (
	"path"
	"slices"
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
// A directory of manifests is usually applied to one workspace, so it reads as one use case.
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

// binding is one export a use case binds. via names the WorkspaceType it is inherited from, and is
// empty when the use case declares the binding itself.
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

// bindingsOf lists what every workspace of type t binds, as kcp's initializer does: the type's own
// default bindings, then those of each type it extends, transitively, each binding once. Bases not
// in the tree add nothing, and a cycle stops where it started.
func bindingsOf(t WorkspaceType, types []WorkspaceType) []binding {
	var bound []binding
	listed := map[ExportRef]bool{}
	visited := map[string]bool{}
	var visit func(t WorkspaceType, via string, r relation)
	visit = func(t WorkspaceType, via string, r relation) {
		if visited[t.Name] {
			return
		}
		visited[t.Name] = true
		for _, ref := range t.Binds {
			if !listed[ref] {
				listed[ref] = true
				bound = append(bound, binding{ref: ref, via: via, relation: r})
			}
		}
		for _, base := range t.Extends {
			if bt, ok := typeNamed(base.Name, types); ok {
				visit(bt, bt.Name, bindsThroughExtend)
			}
		}
	}
	visit(t, "", bindsByDefault)
	return bound
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
