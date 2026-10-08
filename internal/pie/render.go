package pie

import (
	"bytes"
	"fmt"
	"path"
	"slices"
	"strings"
)

// outsideID is the box holding exports that use cases bind but the tree does not define.
const outsideID = "outside"

// ownWorkspace stands in for an empty export path, which kcp reads as the workspace of the
// WorkspaceType that names it.
const ownWorkspace = "(own workspace)"

// RenderD2 draws the model as d2 source: a table per WorkspaceType listing the exports it binds,
// a table per APIExport listing the kinds it serves, the schemas no export serves, the exports
// bound but not in the tree, then edges from each binding to its export.
func RenderD2(m Model) []byte {
	var b bytes.Buffer
	var edges, outside []string

	typesNamed := countNames(m.WorkspaceTypes, func(t WorkspaceType) string { return t.Name })
	for _, t := range m.WorkspaceTypes {
		id := "workspacetype:" + location(t.Path, t.Doc)
		fmt.Fprintf(&b, "%q: {\n  shape: sql_table\n  label: %q\n", id, typeLabel(t, typesNamed[t.Name] > 1, m.WorkspaceTypes))
		for _, bound := range bindingsOf(t, m.WorkspaceTypes) {
			fmt.Fprintf(&b, "  %q: %q\n", bound.ref.Name, bound.where())
			targets := exportsNamed(bound.ref.Name, m.Exports)
			for _, e := range targets {
				edges = append(edges, fmt.Sprintf("%q.%q -> %q", id, bound.ref.Name, exportID(e)))
			}
			if len(targets) == 0 {
				box := bound.ref.Name + " @ " + workspaceOf(bound.ref)
				if !slices.Contains(outside, box) {
					outside = append(outside, box)
				}
				edges = append(edges, fmt.Sprintf("%q.%q -> %q.%q", id, bound.ref.Name, outsideID, box))
			}
		}
		b.WriteString("}\n")
	}

	exportsCalled := countNames(m.Exports, func(e Export) string { return e.Name })
	for _, e := range m.Exports {
		label := "APIExport " + e.Name
		if exportsCalled[e.Name] > 1 {
			label += " · " + location(e.Path, e.Doc)
		}
		fmt.Fprintf(&b, "%q: {\n  shape: sql_table\n  label: %q\n", exportID(e), label)
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

	if len(outside) > 0 {
		fmt.Fprintf(&b, "%q: {\n  label: \"APIExports outside the tree\"\n  style.stroke-dash: 3\n", outsideID)
		for _, box := range outside {
			fmt.Fprintf(&b, "  %q\n", box)
		}
		b.WriteString("}\n")
	}

	for _, edge := range edges {
		b.WriteString(edge + "\n")
	}
	return b.Bytes()
}

func exportID(e Export) string { return "apiexport:" + location(e.Path, e.Doc) }

func countNames[T any](items []T, name func(T) string) map[string]int {
	counts := map[string]int{}
	for _, item := range items {
		counts[name(item)]++
	}
	return counts
}

func typeLabel(t WorkspaceType, repeated bool, types []WorkspaceType) string {
	label := "WorkspaceType " + t.Name
	if repeated {
		label += " · " + location(t.Path, t.Doc)
	}
	if len(t.Extends) == 0 {
		return label
	}
	bases := make([]string, 0, len(t.Extends))
	for _, base := range t.Extends {
		name := base.Name
		if _, ok := typeNamed(base.Name, types); !ok {
			name += " (not in tree)"
		}
		bases = append(bases, name)
	}
	return label + " · extends " + strings.Join(bases, ", ")
}

// binding is one export a WorkspaceType binds. via names the type it is inherited from, and is
// empty when the type declares the binding itself.
type binding struct {
	ref ExportRef
	via string
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
	var visit func(t WorkspaceType, via string)
	visit = func(t WorkspaceType, via string) {
		if visited[t.Name] {
			return
		}
		visited[t.Name] = true
		for _, ref := range t.Binds {
			if !listed[ref] {
				listed[ref] = true
				bound = append(bound, binding{ref: ref, via: via})
			}
		}
		for _, base := range t.Extends {
			if bt, ok := typeNamed(base.Name, types); ok {
				visit(bt, bt.Name)
			}
		}
	}
	visit(t, "")
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
