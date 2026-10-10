package pie

import (
	"slices"
	"strings"
)

// typeTree is the tree of workspaces the WorkspaceTypes spell out: a type's workspaces sit in those
// of the types its limitAllowedParents lists, and hold those of the types its limitAllowedChildren
// lists. Only listed types count; that kcp also lets a type stand for the types it extends is left
// out. parents names, by type, the types its workspaces may sit in.
type typeTree struct {
	parents map[string][]string
}

func typeTreeOf(types []WorkspaceType) typeTree {
	tr := typeTree{parents: map[string][]string{}}
	link := func(parent, child string) {
		if !slices.Contains(tr.parents[child], parent) {
			tr.parents[child] = append(tr.parents[child], parent)
		}
	}
	for _, t := range types {
		for _, p := range t.Parents {
			link(p.Name, t.Name)
		}
		for _, c := range t.Children {
			link(t.Name, c.Name)
		}
	}
	return tr
}

// paths names the ways down the tree to a type, from a type in nothing, as far as a loop allows.
func (tr typeTree) paths(name string, seen []string) []string {
	seen = append(slices.Clone(seen), name)
	var paths []string
	for _, p := range tr.parents[name] {
		if slices.Contains(seen, p) {
			continue
		}
		for _, up := range tr.paths(p, seen) {
			paths = append(paths, up+" › "+name)
		}
	}
	if len(paths) == 0 {
		return []string{name}
	}
	return paths
}

// placed says whether the tree says anything about where a type's workspaces sit or what they hold.
func (tr typeTree) placed(name string) bool {
	if len(tr.parents[name]) > 0 {
		return true
	}
	for _, parents := range tr.parents {
		if slices.Contains(parents, name) {
			return true
		}
	}
	return false
}

// place is a WorkspaceType as CONSUMERS shows it: where in the tree its workspaces sit.
type place struct {
	t     WorkspaceType
	label string
}

// placesOf lists the WorkspaceTypes that are consumers, in tree order: each type with no parent in the tree,
// in the order of the files, followed by what its workspaces hold. A label is the type's paths down
// the tree, "(nests)" when its workspaces may hold their own kind, and the types it extends. A type
// only other types extend is not a consumer unless the tree places it or has a workspace of it: its
// bindings are the extending types', drawn dashed.
func placesOf(m Model) []place {
	types := m.WorkspaceTypes
	tr := typeTreeOf(types)
	extended := map[string]bool{}
	for _, t := range types {
		for _, e := range t.Extends {
			extended[e.Name] = true
		}
	}
	isConsumer := func(t WorkspaceType) bool {
		return !extended[t.Name] || tr.placed(t.Name) ||
			slices.ContainsFunc(m.Workspaces, func(w Workspace) bool { return w.Type.Name == t.Name })
	}
	isRoot := func(t WorkspaceType) bool {
		return !slices.ContainsFunc(tr.parents[t.Name], func(p string) bool {
			_, inTree := typeNamed(p, types)
			return p != t.Name && inTree
		})
	}

	var order []int
	visited := map[int]bool{}
	var visit func(i int)
	visit = func(i int) {
		if visited[i] {
			return
		}
		visited[i] = true
		order = append(order, i)
		for j, child := range types {
			if slices.Contains(tr.parents[child.Name], types[i].Name) {
				visit(j)
			}
		}
	}
	for i, t := range types {
		if isRoot(t) {
			visit(i)
		}
	}
	// Types in a loop have no root to be found from.
	for i := range types {
		visit(i)
	}

	named := countNames(types, func(t WorkspaceType) string { return t.Name })
	var places []place
	for _, i := range order {
		t := types[i]
		if !isConsumer(t) {
			continue
		}
		label := strings.Join(tr.paths(t.Name, nil), ", ")
		if slices.Contains(tr.parents[t.Name], t.Name) {
			label += " (nests)"
		}
		if named[t.Name] > 1 {
			label += " · " + location(t.Path, t.Doc)
		}
		if len(t.Extends) > 0 {
			bases := make([]string, len(t.Extends))
			for k, e := range t.Extends {
				bases[k] = e.Name
			}
			label += "\nextends " + strings.Join(bases, ", ")
		}
		places = append(places, place{t: t, label: label})
	}
	return places
}
