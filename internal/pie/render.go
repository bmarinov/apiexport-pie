package pie

import (
	"bytes"
	"fmt"
	"path"
	"slices"
	"strings"
)

// elkLayout makes ELK the layout engine of the file itself, so every way of rendering it, d2 --watch
// included, routes edges between use cases and exports at right angles. D2_LAYOUT and --layout
// still win.
const elkLayout = "vars: {\n  d2-config: {\n    layout-engine: elk\n  }\n}\n"

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
	b.WriteString(elkLayout)
	var edges, outside []string
	drawn := map[relation]bool{}
	sharedDrawn := false

	paths := exportPaths(m)
	// key is an export's d2 key: inside the box of its workspace when the references to it agree.
	key := func(e Export) string {
		if p := paths[e.Name]; len(p) == 1 {
			return fmt.Sprintf("%q.%q", workspaceID(p[0]), exportID(e))
		}
		return fmt.Sprintf("%q", exportID(e))
	}

	edge := func(r relation, line string) {
		drawn[r] = true
		edges = append(edges, line+" {"+r.style+"}")
	}

	// link draws an edge from a binding row to every export of that name in the tree or, when the
	// tree has none, to the export's box outside it.
	link := func(r relation, from string, ref ExportRef) {
		targets := exportsNamed(ref.Name, m.Exports)
		for _, e := range targets {
			edge(r, fmt.Sprintf("%q.%q -> %s", from, ref.Name, key(e)))
		}
		if len(targets) == 0 {
			box := ref.Name + " @ " + workspaceOf(ref)
			if !slices.Contains(outside, box) {
				outside = append(outside, box)
			}
			edge(r, fmt.Sprintf("%q.%q -> %q.%q", from, ref.Name, outsideID, box))
		}
	}

	resources := servedByExport(m.Exports, m.Schemas)

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
			var with []string
			for _, c := range conflicts {
				if c.export == bd.ref.Name {
					with = append(with, c.String())
				}
			}
			if len(with) > 0 {
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
		useCase("workspacetype:"+location(t.Path, t.Doc), typeLabel(t, typesNamed[t.Name] > 1, m.WorkspaceTypes, m.Workspaces), bindingsOf(t, m.WorkspaceTypes))
	}
	for _, dir := range bindingsByDir(m.APIBindings) {
		useCase("apibindings:"+dir.dir, "APIBindings in "+dir.dir, dir.bound)
	}

	var workspaces []string
	for _, e := range m.Exports {
		if p := paths[e.Name]; len(p) == 1 && !slices.Contains(workspaces, p[0]) {
			workspaces = append(workspaces, p[0])
		}
	}
	for _, ws := range workspaces {
		fmt.Fprintf(&b, "%q: {\n  label: %q\n}\n", workspaceID(ws), "Workspace "+ws)
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
		if p := paths[e.Name]; len(p) > 1 {
			label += " · references disagree: " + strings.Join(p, ", ")
		}
		rows[i] = rowsOf(e, m.Schemas, serving)
		fmt.Fprintf(&b, "%s: {\n  shape: sql_table\n  label: %q\n", key(e), label)
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
						edge(claimsKind, fmt.Sprintf("%s -> %s.%q: %q",
							key(e), key(target), r.kind, "claims "+strings.Join(c.Verbs, ", ")))
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

func workspaceID(path string) string { return "workspace:" + path }

// exportPaths lists, by export name, the workspace paths WorkspaceTypes and APIBinding files bind
// it from. An empty path names the referring object's own workspace, which the tree does not tell,
// so it says nothing about where the export lives.
func exportPaths(m Model) map[string][]string {
	paths := map[string][]string{}
	add := func(ref ExportRef) {
		if ref.Path != "" && !slices.Contains(paths[ref.Name], ref.Path) {
			paths[ref.Name] = append(paths[ref.Name], ref.Path)
		}
	}
	for _, t := range m.WorkspaceTypes {
		for _, ref := range t.Binds {
			add(ref)
		}
	}
	for _, binding := range m.APIBindings {
		add(binding.Export)
	}
	return paths
}

func countNames[T any](items []T, name func(T) string) map[string]int {
	counts := map[string]int{}
	for _, item := range items {
		counts[name(item)]++
	}
	return counts
}

// typeLabel names a WorkspaceType, the types it extends, and the workspaces in the tree of its type,
// which are how readers know a use case: root:orgs is the orgs workspace, of type orgs.
func typeLabel(t WorkspaceType, repeated bool, types []WorkspaceType, workspaces []Workspace) string {
	label := "WorkspaceType " + t.Name
	if repeated {
		label += " · " + location(t.Path, t.Doc)
	}
	if len(t.Extends) > 0 {
		bases := make([]string, 0, len(t.Extends))
		for _, base := range t.Extends {
			name := base.Name
			if _, ok := typeNamed(base.Name, types); !ok {
				name += " (not in tree)"
			}
			bases = append(bases, name)
		}
		label += " · extends " + strings.Join(bases, ", ")
	}
	var named []string
	for _, w := range workspaces {
		if w.Type.Name == t.Name {
			named = append(named, w.Name)
		}
	}
	if len(named) == 1 {
		label += " · workspace " + named[0]
	} else if len(named) > 1 {
		label += " · workspaces " + strings.Join(named, ", ")
	}
	return label
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
		rows = append(rows, row{kind: kind, resource: resourceName(r), version: version, shared: len(others) > 0})
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

// conflictsAmong finds the bindings of one use case that kcp refuses together, from the side of
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
