package pie

import (
	"bytes"
	"fmt"
	"path"
	"strings"
)

// RenderD2 draws the model as d2 source: one table per APIExport listing the kinds it serves,
// then one table of the schemas no export serves.
func RenderD2(m Model) []byte {
	var b bytes.Buffer
	exportsNamed := map[string]int{}
	for _, e := range m.Exports {
		exportsNamed[e.Name]++
	}
	for _, e := range m.Exports {
		label := "APIExport " + e.Name
		if exportsNamed[e.Name] > 1 {
			label += " · " + e.location()
		}
		fmt.Fprintf(&b, "%q: {\n  shape: sql_table\n  label: %q\n", "apiexport:"+e.location(), label)
		for _, r := range rowsOf(e, m.Schemas) {
			fmt.Fprintf(&b, "  %q: %q {constraint: %q}\n", r.kind, r.resource, r.version)
		}
		b.WriteString("}\n")
	}
	if unserved := unservedSchemas(m); len(unserved) > 0 {
		b.WriteString("\"unserved\": {\n  shape: sql_table\n  label: \"APIResourceSchemas no export serves\"\n")
		for _, s := range unserved {
			fmt.Fprintf(&b, "  %q: %q\n", s.Name, s.Kind)
		}
		b.WriteString("}\n")
	}
	return b.Bytes()
}

// row is one served resource. kind is also the row's key, so it must be unique in its table.
type row struct {
	kind     string
	resource string
	version  string
}

func rowsOf(e Export, schemas []Schema) []row {
	rows := make([]row, 0, len(e.Resources))
	rowsOfKind := map[string]int{}
	for _, r := range e.Resources {
		kind := kindOf(r, path.Dir(e.Path), schemas)
		rowsOfKind[kind]++
		version, _, _ := strings.Cut(r.Schema, ".")
		rows = append(rows, row{kind: kind, resource: resourceName(r), version: version})
	}
	for i, r := range e.Resources {
		if rowsOfKind[rows[i].kind] > 1 {
			rows[i].kind = fmt.Sprintf("%s (%s)", rows[i].kind, r.Group)
		}
	}
	return rows
}

// kindOf names a resource by the kind its schema defines. kcp requires an export and its schemas
// to share a workspace, so the schema in the export's own directory wins; otherwise the first
// schema of that name in the tree. Without one, the plural stands in.
func kindOf(r Resource, dir string, schemas []Schema) string {
	var elsewhere *Schema
	for i := range schemas {
		s := &schemas[i]
		if s.Name != r.Schema {
			continue
		}
		if path.Dir(s.Path) == dir {
			return s.Kind
		}
		if elsewhere == nil {
			elsewhere = s
		}
	}
	if elsewhere == nil {
		return r.Name
	}
	return elsewhere.Kind
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
