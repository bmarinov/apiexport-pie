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

// Header fills say what kcp makes of a table: orange is allowed but worth a look, red is refused.
const (
	sharedFill  = "#f08c00"
	refusedFill = "#e03131"
)

// relation is a kind of edge. Each is drawn in its own style, which the legend shows by example.
type relation struct {
	key, from, to, label, style string
}

var (
	bindsByDefault     = relation{"default", "WorkspaceType", "APIExport", "binds by default", `style.stroke: "#1971c2"`}
	bindsThroughExtend = relation{"extend", "WorkspaceType", "APIExport", "binds through extend", `style.stroke: "#1971c2"; style.stroke-dash: 2`}
	bindsByAPIBinding  = relation{"apibinding", "APIBinding file", "APIExport", "binds", `style.stroke: "#2f9e44"`}
	bindsRefused       = relation{"refused", "use case", "APIExport", "conflicts", fmt.Sprintf("style.stroke: %q; style.stroke-width: 3", refusedFill)}
	claimsKind         = relation{"claims", "APIExport", "claimed kind", "claims", `style.stroke: "#7048e8"; style.stroke-dash: 4`}
)

// relations is the order the legend lists them in.
var relations = []relation{bindsByDefault, bindsThroughExtend, bindsByAPIBinding, bindsRefused, claimsKind}

// RenderD2 draws the model as d2 source: a table per WorkspaceType and per directory of APIBinding
// files listing the exports they bind, a table per APIExport listing the kinds it serves, the
// schemas no export serves, the exports bound but not in the tree, edges from each binding to its
// export, and a legend of the relations and colours drawn.
func RenderD2(m Model) []byte {
	var b bytes.Buffer
	var edges, outside []string
	drawn := map[relation]bool{}
	sharedDrawn := false

	edge := func(r relation, line string) {
		drawn[r] = true
		edges = append(edges, line+" {"+r.style+"}")
	}

	// link draws an edge from a binding row to every export of that name in the tree or, when the
	// tree has none, to the export's box outside it.
	link := func(r relation, from string, ref ExportRef) {
		targets := exportsNamed(ref.Name, m.Exports)
		for _, e := range targets {
			edge(r, fmt.Sprintf("%q.%q -> %q", from, ref.Name, exportID(e)))
		}
		if len(targets) == 0 {
			box := ref.Name + " @ " + workspaceOf(ref)
			if !slices.Contains(outside, box) {
				outside = append(outside, box)
			}
			edge(r, fmt.Sprintf("%q.%q -> %q.%q", from, ref.Name, outsideID, box))
		}
	}

	resources := resourcesByExport(m.Exports)

	// useCase draws the table of exports a use case binds. Bindings kcp refuses together turn the
	// table red, and their rows say what they conflict with.
	useCase := func(id, label string, bound []binding) {
		conflicts := conflictsAmong(bound, resources)
		fmt.Fprintf(&b, "%q: {\n  shape: sql_table\n  label: %q\n", id, label)
		if len(conflicts) > 0 {
			fmt.Fprintf(&b, "  style.fill: %q\n", refusedFill)
		}
		for _, bd := range bound {
			where, r := bd.where(), bd.relation
			if with, ok := conflicts[bd.ref.Name]; ok {
				where += " · conflicts with " + strings.Join(with, ", ")
				r = bindsRefused
			}
			fmt.Fprintf(&b, "  %q: %q\n", bd.ref.Name, where)
			link(r, id, bd.ref)
		}
		b.WriteString("}\n")
	}

	typesNamed := countNames(m.WorkspaceTypes, func(t WorkspaceType) string { return t.Name })
	for _, t := range m.WorkspaceTypes {
		useCase("workspacetype:"+location(t.Path, t.Doc), typeLabel(t, typesNamed[t.Name] > 1, m.WorkspaceTypes), bindingsOf(t, m.WorkspaceTypes))
	}
	for _, dir := range bindingsByDir(m.APIBindings) {
		useCase("apibindings:"+dir.dir, "APIBindings in "+dir.dir, dir.bound)
	}

	exportsCalled := countNames(m.Exports, func(e Export) string { return e.Name })
	serving := exportsServing(m.Exports)
	rows := make([][]row, len(m.Exports))
	for i, e := range m.Exports {
		label := "APIExport " + e.Name
		if exportsCalled[e.Name] > 1 {
			label += " · " + location(e.Path, e.Doc)
		}
		if n := len(e.Claims); n == 1 {
			label += " · 1 claim"
		} else if n > 1 {
			label += fmt.Sprintf(" · %d claims", n)
		}
		rows[i] = rowsOf(e, m.Schemas, serving)
		fmt.Fprintf(&b, "%q: {\n  shape: sql_table\n  label: %q\n", exportID(e), label)
		if slices.ContainsFunc(rows[i], func(r row) bool { return r.shared }) {
			fmt.Fprintf(&b, "  style.fill: %q\n", sharedFill)
			sharedDrawn = true
		}
		for _, r := range rows[i] {
			fmt.Fprintf(&b, "  %q: %q {constraint: %q}\n", r.kind, r.resource, r.version)
		}
		b.WriteString("}\n")
	}

	// A claim on a resource that an export in the tree serves points at that resource's row.
	// Claims on anything else, kcp's own resources included, only count in the header.
	for _, e := range m.Exports {
		for _, c := range e.Claims {
			claimed := resourceName(Resource{Group: c.Group, Name: c.Resource})
			for i, target := range m.Exports {
				for _, r := range rows[i] {
					if r.resource == claimed {
						edge(claimsKind, fmt.Sprintf("%q -> %q.%q: %q",
							exportID(e), exportID(target), r.kind, "claims "+strings.Join(c.Verbs, ", ")))
					}
				}
			}
		}
	}

	if unserved := unservedSchemas(m); len(unserved) > 0 {
		b.WriteString("\"unserved\": {\n  shape: sql_table\n  label: \"APIResourceSchemas no export serves\"\n  style.fill: \"#868e96\"\n  style.stroke-dash: 3\n")
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

	for _, line := range edges {
		b.WriteString(line + "\n")
	}

	if len(drawn) > 0 || sharedDrawn {
		b.WriteString("\"legend\": {\n  label: \"Legend\"\n  near: bottom-left\n  style.stroke-dash: 2\n  style.fill: transparent\n  *.style.font-size: 12\n")
		for _, r := range relations {
			if !drawn[r] {
				continue
			}
			from, to := r.key+":from", r.key+":to"
			fmt.Fprintf(&b, "  %q: %q\n  %q: %q\n  %q -> %q: %q {%s}\n", from, r.from, to, r.to, from, to, r.label, r.style)
		}
		if sharedDrawn {
			fmt.Fprintf(&b, "  \"shared\": \"APIExport with a resource another export also serves\" {style.fill: %q}\n", sharedFill)
		}
		b.WriteString("}\n")
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

// row is one served resource. kind is also the row's key, so it must be unique in its table.
type row struct {
	kind     string
	resource string
	version  string
	shared   bool
}

func rowsOf(e Export, schemas []Schema, serving map[string][]string) []row {
	rows := make([]row, 0, len(e.Resources))
	rowsOfKind := map[string]int{}
	for _, r := range e.Resources {
		kind, found := kindOf(r, path.Dir(e.Path), schemas)
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
		rows = append(rows, row{kind: kind, resource: resourceName(r), version: version, shared: len(others) > 0})
	}
	for i, r := range e.Resources {
		if rowsOfKind[rows[i].kind] > 1 {
			rows[i].kind = fmt.Sprintf("%s (%s)", rows[i].kind, r.Group)
		}
	}
	return rows
}

// kindOf names a resource by the kind its schema defines, and says whether the schema was found.
// kcp requires an export and its schemas to share a workspace, so the schema in the export's own
// directory wins; otherwise the first schema of that name in the tree. Without one, the plural
// stands in.
func kindOf(r Resource, dir string, schemas []Schema) (string, bool) {
	var elsewhere *Schema
	for i := range schemas {
		s := &schemas[i]
		if s.Name != r.Schema {
			continue
		}
		if path.Dir(s.Path) == dir {
			return s.Kind, true
		}
		if elsewhere == nil {
			elsewhere = s
		}
	}
	if elsewhere == nil {
		return r.Name, false
	}
	return elsewhere.Kind, true
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

// resourcesByExport lists the resources each export serves, copies of an export together.
func resourcesByExport(exports []Export) map[string][]string {
	served := map[string][]string{}
	for _, e := range exports {
		for _, r := range e.Resources {
			if name := resourceName(r); !slices.Contains(served[e.Name], name) {
				served[e.Name] = append(served[e.Name], name)
			}
		}
	}
	return served
}

// conflictsAmong finds the bindings of one use case that kcp refuses together: exports with
// different names serving the same resource, which a workspace can hold from one binding only. It
// maps each such export to what it conflicts with. Exports outside the tree serve nothing known.
func conflictsAmong(bound []binding, resources map[string][]string) map[string][]string {
	conflicts := map[string][]string{}
	for _, bd := range bound {
		for _, res := range resources[bd.ref.Name] {
			for _, other := range bound {
				if other.ref.Name != bd.ref.Name && slices.Contains(resources[other.ref.Name], res) {
					conflicts[bd.ref.Name] = append(conflicts[bd.ref.Name], other.ref.Name+" on "+res)
				}
			}
		}
	}
	return conflicts
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
