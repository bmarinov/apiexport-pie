package pie

import (
	"path"
	"slices"
)

// served is a resource an export serves, with the names kcp tells the resources of a group apart by:
// plural, singular and short names in names, kind and list kind in kinds. Without its schema in the
// tree, only the plural is known.
type served struct {
	group, resource string
	names, kinds    []string
}

// servedByExport lists the resources each export serves, copies of an export together.
func servedByExport(exports []Export, schemas []Schema) map[string][]served {
	byExport := map[string][]served{}
	for _, e := range exports {
		for _, r := range e.Resources {
			name := resourceName(r)
			if slices.ContainsFunc(byExport[e.Name], func(s served) bool { return s.resource == name }) {
				continue
			}
			sv := served{group: r.Group, resource: name, names: []string{r.Name}}
			if s, ok := schemaOf(r, path.Dir(e.Path), schemas); ok {
				sv.names = append(append(sv.names, s.Singular), s.ShortNames...)
				sv.kinds = []string{s.Kind, s.ListKind}
			}
			byExport[e.Name] = append(byExport[e.Name], sv)
		}
	}
	return byExport
}

// conflict is a resource of one bound export that kcp refuses next to a resource of another.
type conflict struct {
	export, other string
	mine, theirs  string
	// on is the name both resources use, empty when they are the same resource.
	on string
}

// String tells the conflict from the export's side, as its row does.
func (c conflict) String() string {
	if c.on == "" {
		return c.other + " on " + c.mine
	}
	return c.other + " on " + c.mine + " (both use " + c.on + ")"
}

// conflictsAmong finds the bindings of one consumer that kcp refuses together, from the side of
// each export. Exports outside the tree serve nothing known.
func conflictsAmong(bound []binding, resources map[string][]served) []conflict {
	var conflicts []conflict
	for _, bd := range bound {
		for _, mine := range resources[bd.ref.Name] {
			for _, other := range bound {
				if other.ref.Name == bd.ref.Name {
					continue
				}
				if theirs, on, ok := firstClash(mine, resources[other.ref.Name]); ok {
					conflicts = append(conflicts, conflict{export: bd.ref.Name, other: other.ref.Name, mine: mine.resource, theirs: theirs, on: on})
				}
			}
		}
	}
	return conflicts
}

func firstClash(mine served, theirs []served) (string, string, bool) {
	for _, t := range theirs {
		if on, ok := clash(mine, t); ok {
			return t.resource, on, true
		}
	}
	return "", "", false
}

// clash says whether kcp refuses two resources in one workspace, and on which name. Within a group,
// plurals, singulars and short names must all differ, and so must kinds and list kinds; names are
// never compared with kinds (namesConflict, pkg/reconciler/apis/apibinding/conflict_checker.go,
// kcp v0.32.3). The name is empty when both are the same resource.
func clash(a, b served) (string, bool) {
	if a.group != b.group {
		return "", false
	}
	if a.resource == b.resource {
		return "", true
	}
	for _, n := range a.names {
		if n != "" && slices.Contains(b.names, n) {
			return n, true
		}
	}
	for _, k := range a.kinds {
		if k != "" && slices.Contains(b.kinds, k) {
			return k, true
		}
	}
	return "", false
}
