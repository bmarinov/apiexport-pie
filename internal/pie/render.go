package pie

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

// elkLayout makes ELK the layout engine of the file itself, so every way of rendering it, d2 --watch
// included, routes edges between consumers and exports at right angles. D2_LAYOUT and --layout
// still win.
const elkLayout = "vars: {\n  d2-config: {\n    layout-engine: elk\n  }\n}\n"

// outsideID is the box holding exports that consumers bind but the tree does not define.
const outsideID = "outside"

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
	bindsRefused       = relation{"refused", "consumer", "APIExport", "conflicts", fmt.Sprintf("style.stroke: %q; style.stroke-width: 3", refusedFill)}
	servesKind         = relation{"serves", "APIExport", "kind", "serves", `style.stroke: "#868e96"`}
	claimsKind         = relation{"claims", "APIExport", "claimed kind", "claims", `style.stroke: "#7048e8"; style.stroke-dash: 4`}
	// The overview weighs what a claim lets the provider do: the more verbs, the heavier the arrow.
	claimsRead  = relation{"reads", "APIExport", "what it claims", "claims to read, through its virtual workspace", `style.stroke: "#7048e8"; style.stroke-dash: 4`}
	claimsWrite = relation{"writes", "APIExport", "what it claims", "claims to write, through its virtual workspace", `style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 4`}
	claimsAll   = relation{"all-verbs", "APIExport", "what it claims", "claims all verbs, through its virtual workspace", `style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 6`}
)

// relations is the order the legend lists them in.
var relations = []relation{bindsByDefault, bindsThroughExtend, bindsByAPIBinding, bindsRefused, servesKind, claimsKind, claimsRead, claimsWrite, claimsAll}

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

	// consumerTable draws the table of exports a consumer binds. Bindings kcp refuses together turn the
	// table red, and their rows say what they conflict with.
	consumerTable := func(id, label string, bound []binding) {
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
		consumerTable("workspacetype:"+location(t.Path, t.Doc), typeLabel(t, typesNamed[t.Name] > 1, m.WorkspaceTypes, m.Workspaces), bindingsOf(t, m.WorkspaceTypes))
	}
	for _, dir := range bindingsByDir(m.APIBindings) {
		consumerTable("apibindings:"+dir.dir, "APIBindings in "+dir.dir, dir.bound)
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

	shared := ""
	if sharedDrawn {
		shared = "APIExport with a resource another export also serves"
	}
	writeLegend(&b, "bottom-left", drawn, "", shared)
	return b.Bytes()
}

// writeLegend explains by example the relations drawn, in the order relations lists them, then
// samples, d2 lines of boxes to explain, and what orange means, unless shared is empty because
// nothing orange is drawn. Nothing drawn, no legend. near is where the legend goes, outside the
// diagram.
func writeLegend(b *bytes.Buffer, near string, drawn map[relation]bool, samples, shared string) {
	if len(drawn) == 0 && shared == "" {
		return
	}
	fmt.Fprintf(b, "\"legend\": {\n  label: \"Legend\"\n  near: %s\n  style.stroke-dash: 2\n  style.fill: transparent\n  *.style.font-size: 12\n", near)
	for _, r := range relations {
		if !drawn[r] {
			continue
		}
		from, to := r.key+":from", r.key+":to"
		fmt.Fprintf(b, "  %q: %q\n  %q: %q\n  %q -> %q: %q {%s}\n", from, r.from, to, r.to, from, to, r.label, r.style)
	}
	b.WriteString(samples)
	if shared != "" {
		fmt.Fprintf(b, "  \"shared\": %q {style.fill: %q}\n", shared, sharedFill)
	}
	b.WriteString("}\n")
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
// which are how readers know a consumer: root:orgs is the orgs workspace, of type orgs.
func typeLabel(t WorkspaceType, repeated bool, types []WorkspaceType, workspaces []Workspace) string {
	label := "WorkspaceType " + t.Name
	if repeated {
		label += " · " + location(t.Path, t.Doc)
	}
	if len(t.Extends) > 0 {
		label += " · extends " + baseNames(t, types)
	}
	if named := workspacesOf(t, workspaces); named != "" {
		label += " · " + named
	}
	return label
}

// baseNames names the types t extends, those not in the tree marked so.
func baseNames(t WorkspaceType, types []WorkspaceType) string {
	bases := make([]string, 0, len(t.Extends))
	for _, base := range t.Extends {
		name := base.Name
		if _, ok := typeNamed(base.Name, types); !ok {
			name += " (not in tree)"
		}
		bases = append(bases, name)
	}
	return strings.Join(bases, ", ")
}

// workspacesOf names the workspaces in the tree of t's type: "workspace orgs", "workspaces a, b", or
// nothing.
func workspacesOf(t WorkspaceType, workspaces []Workspace) string {
	var named []string
	for _, w := range workspaces {
		if w.Type.Name == t.Name {
			named = append(named, w.Name)
		}
	}
	switch len(named) {
	case 0:
		return ""
	case 1:
		return "workspace " + named[0]
	}
	return "workspaces " + strings.Join(named, ", ")
}
