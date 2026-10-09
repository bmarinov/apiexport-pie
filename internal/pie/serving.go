package pie

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// row is one served resource. kind is also the row's key, so it must be unique in its table. inTree
// says whether its schema is in the tree; without it, kind is the plural.
type row struct {
	kind     string
	group    string
	resource string
	version  string
	shared   bool
	inTree   bool
}

func rowsOf(e Export, schemas []Schema, serving map[string][]string) []row {
	rows := make([]row, 0, len(e.Resources))
	rowsOfKind := map[string]int{}
	for _, r := range e.Resources {
		kind := r.Name
		s, found := schemaOf(r, path.Dir(e.Path), schemas)
		if found {
			kind = s.Kind
		}
		rowsOfKind[kind]++
		version, _, _ := strings.Cut(r.Schema, ".")
		// d2 drops styles on single table rows, so a missing schema is marked in words.
		if !found {
			version += " · not in tree"
		}
		var others []string
		for _, name := range serving[resourceName(r)] {
			if name != e.Name {
				others = append(others, name)
			}
		}
		if len(others) > 0 {
			version += " · also in " + strings.Join(others, ", ")
		}
		rows = append(rows, row{kind: kind, group: r.Group, resource: resourceName(r), version: version, shared: len(others) > 0, inTree: found})
	}
	for i, r := range e.Resources {
		if rowsOfKind[rows[i].kind] > 1 {
			rows[i].kind = fmt.Sprintf("%s (%s)", rows[i].kind, r.Group)
		}
	}
	return rows
}

// schemaOf finds the schema a resource is served from. kcp requires an export and its schemas to
// share a workspace, so the schema in the export's own directory wins; otherwise the first schema of
// that name in the tree.
func schemaOf(r Resource, dir string, schemas []Schema) (Schema, bool) {
	var elsewhere *Schema
	for i := range schemas {
		s := &schemas[i]
		if s.Name != r.Schema {
			continue
		}
		if path.Dir(s.Path) == dir {
			return *s, true
		}
		if elsewhere == nil {
			elsewhere = s
		}
	}
	if elsewhere == nil {
		return Schema{}, false
	}
	return *elsewhere, true
}

// exportsServing names the exports serving each resource, in the order they appear. Copies of an
// export share its name, so they count once.
func exportsServing(exports []Export) map[string][]string {
	serving := map[string][]string{}
	for _, e := range exports {
		for _, r := range e.Resources {
			name := resourceName(r)
			if !slices.Contains(serving[name], e.Name) {
				serving[name] = append(serving[name], e.Name)
			}
		}
	}
	return serving
}

func resourceName(r Resource) string {
	if r.Group == "" {
		return r.Name
	}
	return r.Name + "." + r.Group
}

func unservedSchemas(m Model) []Schema {
	served := map[string]bool{}
	for _, e := range m.Exports {
		for _, r := range e.Resources {
			served[r.Schema] = true
		}
	}
	var unserved []Schema
	for _, s := range m.Schemas {
		if !served[s.Name] {
			unserved = append(unserved, s)
		}
	}
	return unserved
}
