package pie

import (
	"bytes"
	"fmt"
	"path"
	"slices"
	"strings"
)

// The overview's columns, by d2 key.
const (
	resourcesColumn = "resources"
	exportsColumn   = "apiexports"
	boundAtColumn   = "bound-at"
)

// dagreLayout makes dagre the layout engine of the overview. Between plain boxes its curves read
// better than ELK's right angles, and the columns stay close: ELK gives every edge a lane of its own
// between them. D2_LAYOUT and --layout still win.
const dagreLayout = "vars: {\n  d2-config: {\n    layout-engine: dagre\n  }\n}\n"

// overviewClasses are the looks of the overview's boxes. A use case takes the colour of the edges
// it draws: blue for a WorkspaceType, green for APIBinding files.
const overviewClasses = `classes: {
  column: {style: {fill: transparent; stroke: transparent; font-size: 22; bold: true; font-color: "#868e96"}}
  group: {style: {fill: "#f1f3f5"; stroke: "#ced4da"; font-size: 15; border-radius: 6}}
  kind: {style: {fill: "#ffffff"; stroke: "#adb5bd"; font-size: 14; border-radius: 4}}
  export: {style: {fill: "#ffffff"; stroke: "#364fc7"; stroke-width: 2; font-size: 16; bold: true; border-radius: 6}}
  story: {style: {fill: transparent; stroke: transparent; font-size: 13; bold: false; font-color: "#343a40"}}
  workspace: {style: {fill: "#f8f9fa"; stroke: "#868e96"; font-size: 15; border-radius: 8}}
  outside: {style: {fill: transparent; stroke: "#868e96"; stroke-dash: 3; font-size: 14}}
  outside-export: {style: {fill: "#f8f9fa"; stroke: "#868e96"; stroke-dash: 3; font-size: 14; border-radius: 6}}
  workspacetype: {style: {fill: "#e7f5ff"; stroke: "#1971c2"; font-size: 15; border-radius: 6}}
  apibindings: {style: {fill: "#ebfbee"; stroke: "#2f9e44"; font-size: 15; border-radius: 6}}
  claimed: {style: {fill: transparent; stroke: "#adb5bd"; stroke-dash: 3; font-size: 15; border-radius: 6}}
  claimed-resource: {style: {fill: "#f8f9fa"; stroke: "#adb5bd"; stroke-dash: 3; font-size: 14; border-radius: 4; font-color: "#495057"}}
}
`

// apiGroupSample is the legend's sample of a box in RESOURCES: an API group holding a kind.
const apiGroupSample = "  \"api-group\": {\n    label: \"API group\"\n    class: group\n    \"kind\": \"kind\" {class: kind}\n  }\n"

// kindGroup is the box of an API group in RESOURCES.
type kindGroup struct {
	group string
	kinds []kindBox
	in    presence
}

// kindBox is a kind an export in the tree serves or, when claimed is set, a resource exports claim
// and none in the tree serves. shared says more than one export serves it. name is the kind's, or
// its plural when the tree has no schema for it.
type kindBox struct {
	resource, name, label string
	shared, claimed       bool
	in                    presence
}

// resourceArea is a part of RESOURCES: the groups the tree serves or, under a category, the claimed
// groups it does not, by where a consumer's workspace gets them.
type resourceArea struct {
	category string
	groups   []kindGroup
}

func (a resourceArea) kinds() []kindBox {
	var kinds []kindBox
	for _, g := range a.groups {
		kinds = append(kinds, g.kinds...)
	}
	return kinds
}

// The categories of what claims reach and no export in the tree serves, by d2 key. A claim naming an
// export's identity is on a resource of that export, which a consumer gets by binding it; any other
// claim is on a resource built into every workspace, Kubernetes' and kcp's own system APIs alike.
const (
	exportedCategory = "exported"
	builtInCategory  = "builtin"
)

var categoryLabels = map[string]string{
	exportedCategory: "from an APIExport outside the tree",
	builtInCategory:  "built-in · in every workspace",
}

// RenderOverviewD2 draws the model as d2 source in columns, from left to right: RESOURCES, each kind
// once in a box of its API group; APIEXPORTS, in the box of the workspace they live in, pointing at
// the kinds they serve and, dashed, at what their claims reach, the heavier the more verbs; and
// BOUND AT, the WorkspaceTypes and directories of APIBinding files, pointing at the exports they
// bind. A kind more than one export serves is orange, and what is claimed but served by no export in
// the tree is dashed. Edges mean what they mean in RenderD2, and a legend explains them.
func RenderOverviewD2(m Model) []byte {
	return overviewOf(m, false).d2()
}

// overview is what the overview of a model draws, before it is written: the boxes of each column in
// the order they are written, and the edges between them.
type overview struct {
	areas      []resourceArea
	workspaces []string
	exports    []exportBox
	outside    []outsideBox
	places     []placeBox
	edges      []overviewEdge
}

// exportBox is an export in APIEXPORTS: its keys inside the column, its name, the places binding
// it and what it needs of other exports.
type exportBox struct {
	keys  []string
	label string
	bound []placeRef
	needs []string
	in    presence
}

// placeRef is a place binding an export, by its id in BOUND AT and its short name.
type placeRef struct{ id, name string }

// story is what the box says under the name: where the export is bound and what it needs of other
// exports, so the box reads without tracing arrows.
func (e exportBox) story() []string {
	var story []string
	if len(e.bound) > 0 {
		names := make([]string, 0, len(e.bound))
		for _, p := range e.bound {
			names = append(names, p.name)
		}
		story = append(story, "bound at "+strings.Join(names, ", "))
	}
	return append(story, e.needs...)
}

// outsideBox is an export bound from outside the tree, named with the workspace it is bound from.
type outsideBox struct {
	name string
	in   presence
}

// placeBox is a use case in BOUND AT. changes, in a diff, says which kinds a place both trees have
// loses and gets.
type placeBox struct {
	id, label, class, changes string
	in                        presence
}

// overviewEdge is an arrow between d2 paths, drawn in the style of its relation.
type overviewEdge struct {
	r               relation
	from, to, label string
	in              presence
}

// overviewOf works out what the overview of m draws. byName keys exports and WorkspaceTypes by name
// rather than by where they are written, as a diff tells them apart; copies of one then share a box.
func overviewOf(m Model, byName bool) overview {
	var o overview
	edge := func(r relation, from, to, label string) {
		o.edges = append(o.edges, overviewEdge{r: r, from: from, to: to, label: label})
	}

	// An export stands in the box of its workspace when the references to it agree on one, as in
	// RenderD2.
	paths := exportPaths(m)
	exportKeys := func(e Export) []string {
		id := exportID(e)
		if byName {
			id = "apiexport:" + e.Name
		}
		if p := paths[e.Name]; len(p) == 1 {
			return []string{workspaceID(p[0]), id}
		}
		return []string{id}
	}
	exportAt := func(e Export) string { return d2Path(append([]string{exportsColumn}, exportKeys(e)...)...) }

	for _, e := range m.Exports {
		for _, r := range e.Resources {
			edge(servesKind, exportAt(e), d2Path(resourcesColumn, "group:"+r.Group, resourceName(r)), "")
		}
	}
	o.areas = resourceAreasOf(m)
	for _, e := range m.Exports {
		var reaches []string
		covered := map[string]map[string]bool{}
		weight := map[string]relation{}
		for _, c := range e.Claims {
			r, reach := reachOf(c)
			if covered[reach] == nil {
				reaches = append(reaches, reach)
				covered[reach] = map[string]bool{}
				weight[reach] = r
			}
			covered[reach][resourceName(Resource{Group: c.Group, Name: c.Resource})] = true
		}
		for _, reach := range reaches {
			for _, target := range claimTargets(o.areas, covered[reach]) {
				edge(weight[reach], exportAt(e), target, reach)
			}
		}
	}

	resources := servedByExport(m.Exports, m.Schemas)
	// boundBy lists, by export name, the places binding it, for its box.
	boundBy := map[string][]placeRef{}
	boundAt := func(id, label, name, class string, bound []binding) {
		o.places = append(o.places, placeBox{id: id, label: label, class: class})
		conflicts := conflictsAmong(bound, resources)
		for _, bd := range bound {
			if !slices.ContainsFunc(boundBy[bd.ref.Name], func(p placeRef) bool { return p.name == name }) {
				boundBy[bd.ref.Name] = append(boundBy[bd.ref.Name], placeRef{id: id, name: name})
			}
			// The other exports alone keep the label short; the warning on stderr names the resources.
			r, label := bd.relation, ""
			var with []string
			for _, c := range conflicts {
				if c.export == bd.ref.Name && !slices.Contains(with, c.other) {
					with = append(with, c.other)
				}
			}
			if len(with) > 0 {
				r, label = bindsRefused, "conflicts with "+strings.Join(with, ", ")
			}
			var targets []string
			for _, e := range exportsNamed(bd.ref.Name, m.Exports) {
				targets = append(targets, exportAt(e))
			}
			if len(targets) == 0 {
				box := bd.ref.Name + " @ " + workspaceOf(bd.ref)
				if !slices.Contains(o.outside, outsideBox{name: box}) {
					o.outside = append(o.outside, outsideBox{name: box})
				}
				targets = append(targets, d2Path(exportsColumn, outsideID, box))
			}
			for _, target := range targets {
				edge(r, d2Path(boundAtColumn, id), target, label)
			}
		}
	}
	for _, p := range placesOf(m) {
		id := "workspacetype:" + location(p.t.Path, p.t.Doc)
		if byName {
			id = "workspacetype:" + p.t.Name
		}
		boundAt(id, p.label, p.t.Name, "workspacetype", bindingsOf(p.t, m.WorkspaceTypes))
	}
	dirs := bindingsByDir(m.APIBindings)
	names := shortDirNames(dirs)
	for _, dir := range dirs {
		boundAt("apibindings:"+dir.dir, "APIBindings in "+dir.dir, names[dir.dir], "apibindings", dir.bound)
	}

	for _, e := range m.Exports {
		if p := paths[e.Name]; len(p) == 1 && !slices.Contains(o.workspaces, p[0]) {
			o.workspaces = append(o.workspaces, p[0])
		}
	}
	exportsCalled := countNames(m.Exports, func(e Export) string { return e.Name })
	serving := exportsServing(m.Exports)
	for _, e := range m.Exports {
		label := e.Name
		if exportsCalled[e.Name] > 1 && !byName {
			label += " · " + location(e.Path, e.Doc)
		}
		o.exports = append(o.exports, exportBox{keys: exportKeys(e), label: label, bound: boundBy[e.Name], needs: needsOf(e, serving)})
	}
	return o
}

// d2 writes the overview as d2 source.
func (o overview) d2() []byte {
	var b bytes.Buffer
	b.WriteString(dagreLayout)
	// Edges point from BOUND AT to APIEXPORTS to RESOURCES; laid out to the left, the columns read
	// in that order from right to left.
	b.WriteString("direction: left\n")
	b.WriteString(overviewClasses)

	shared := ""
	if len(o.areas) > 0 {
		openColumn(&b, resourcesColumn, "RESOURCES")
		for _, a := range o.areas {
			indent, top := "  ", []string{resourcesColumn}
			if a.category != "" {
				fmt.Fprintf(&b, "  %q: {\n    label: %q\n    class: claimed\n", a.category, categoryLabels[a.category])
				indent, top = "    ", append(top, a.category)
			}
			for _, g := range a.groups {
				box := d2Path(append(slices.Clone(top), "group:"+g.group)...)
				into := slices.ContainsFunc(o.edges, func(e overviewEdge) bool { return strings.HasPrefix(e.to, box+".") })
				if writeGroup(&b, indent, g, into) {
					shared = "kind more than one APIExport serves"
				}
			}
			if a.category != "" {
				b.WriteString("  }\n")
			}
		}
		b.WriteString("}\n")
	}
	if len(o.exports) > 0 || len(o.outside) > 0 {
		openColumn(&b, exportsColumn, "APIEXPORTS")
		for _, ws := range o.workspaces {
			fmt.Fprintf(&b, "  %q: %q {class: workspace}\n", workspaceID(ws), "Workspace "+ws)
		}
		for _, e := range o.exports {
			story := e.story()
			if len(story) == 0 {
				fmt.Fprintf(&b, "  %s: %q {class: export%s}\n", d2Path(e.keys...), e.label, e.in.look())
				continue
			}
			// As a grid the box is laid out as one node; as a plain container dagre stretches it
			// across the ranks of its edges.
			fmt.Fprintf(&b, "  %s: {\n    label: %q\n    class: export\n%s    grid-columns: 1\n    \"story\": %q {class: story}\n  }\n",
				d2Path(e.keys...), e.label, e.in.lines("    "), strings.Join(story, "\n"))
		}
		if len(o.outside) > 0 {
			fmt.Fprintf(&b, "  %q: {\n    label: \"outside the tree\"\n    class: outside\n", outsideID)
			for _, box := range o.outside {
				fmt.Fprintf(&b, "    %q: {class: outside-export%s}\n", box.name, box.in.look())
			}
			b.WriteString("  }\n")
		}
		b.WriteString("}\n")
	}
	if len(o.places) > 0 {
		openColumn(&b, boundAtColumn, "BOUND AT")
		for _, p := range o.places {
			label, look := p.label, p.in.look()
			if p.changes != "" {
				label, look = label+"\n"+p.changes, changedLook
			}
			fmt.Fprintf(&b, "  %q: %q {class: %s%s}\n", p.id, label, p.class, look)
		}
		b.WriteString("}\n")
	}
	drawn := map[relation]bool{}
	for _, e := range o.edges {
		drawn[e.r] = true
		label := ""
		if e.label != "" {
			label = fmt.Sprintf(": %q", e.label)
		}
		fmt.Fprintf(&b, "%s -> %s%s {%s}\n", e.from, e.to, label, e.in.restroke(e.r))
	}
	// A sample box says once what every box in RESOURCES is, rather than each group's label.
	samples := ""
	if len(o.areas) > 0 {
		samples = apiGroupSample
	}
	for _, p := range []presence{onlyInB, onlyInA} {
		if o.draws(p) {
			samples += fmt.Sprintf("  %q: %q {%s}\n", presenceKeys[p], presenceLabels[p], strings.TrimPrefix(p.look(), "; "))
		}
	}
	if slices.ContainsFunc(o.places, func(p placeBox) bool { return p.changes != "" }) {
		samples += fmt.Sprintf("  \"kinds-change\": \"a place whose kinds change\" {%s}\n", strings.TrimPrefix(changedLook, "; "))
	}
	// Under the middle, the legend adds no width; at a corner it widens the diagram by its own.
	writeLegend(&b, "bottom-center", drawn, samples, shared)
	return b.Bytes()
}

// resourceAreasOf lists what RESOURCES holds: the kinds the exports serve, each once, by API group,
// in the order they first appear, then what their claims reach that no export serves, by category.
// A kind is named by the schema of the first export serving it; a claimed resource by its plural.
func resourceAreasOf(m Model) []resourceArea {
	var areas []resourceArea
	// add puts a resource in the box of its group, made in the area it belongs to when there is none.
	add := func(category, group string, k kindBox) {
		a := slices.IndexFunc(areas, func(a resourceArea) bool { return a.category == category })
		if a < 0 {
			a = len(areas)
			areas = append(areas, resourceArea{category: category})
		}
		g := slices.IndexFunc(areas[a].groups, func(g kindGroup) bool { return g.group == group })
		if g < 0 {
			g = len(areas[a].groups)
			areas[a].groups = append(areas[a].groups, kindGroup{group: group})
		}
		if !slices.ContainsFunc(areas[a].groups[g].kinds, func(b kindBox) bool { return b.resource == k.resource }) {
			areas[a].groups[g].kinds = append(areas[a].groups[g].kinds, k)
		}
	}
	serving := exportsServing(m.Exports)
	for _, e := range m.Exports {
		for _, r := range e.Resources {
			name := resourceName(r)
			kind, label := r.Name, r.Name+" · not in tree"
			if s, ok := schemaOf(r, path.Dir(e.Path), m.Schemas); ok {
				kind, label = s.Kind, s.Kind
			}
			add("", r.Group, kindBox{resource: name, name: kind, label: label, shared: len(serving[name]) > 1})
		}
	}
	for _, e := range m.Exports {
		for _, c := range e.Claims {
			// A group the tree serves keeps its claimed resources in its own box, where a claim on a
			// kind it serves finds the kind already.
			category := builtInCategory
			if c.FromExport {
				category = exportedCategory
			}
			if len(areas) > 0 && areas[0].category == "" && slices.ContainsFunc(areas[0].groups, func(g kindGroup) bool { return g.group == c.Group }) {
				category = ""
			}
			add(category, c.Group, kindBox{resource: resourceName(Resource{Group: c.Group, Name: c.Resource}), label: c.Resource, claimed: true})
		}
	}
	return areas
}

// writeGroup writes the box of an API group at indent, and says whether it holds an orange kind.
// A box of claimed resources alone is dashed, and holds them in a row unless an arrow points into
// it, at one of them alone.
func writeGroup(b *bytes.Buffer, indent string, g kindGroup, into bool) bool {
	label, class := g.group, "claimed"
	if label == "" {
		label = "core"
	}
	if slices.ContainsFunc(g.kinds, func(k kindBox) bool { return !k.claimed }) {
		class = "group"
	}
	fmt.Fprintf(b, "%s%q: {\n%s  label: %q\n%s  class: %s\n%s", indent, "group:"+g.group, indent, label, indent, class, g.in.lines(indent+"  "))
	// Claimed resources stand in a row: stacked, they made the diagram half again as tall. d2 draws
	// an arrow into a grid straight, across what is in the way, so a box an arrow points into
	// stacks them and the arrow is routed.
	if class == "claimed" && !into {
		fmt.Fprintf(b, "%s  grid-rows: 1\n%s  grid-gap: 12\n", indent, indent)
	}
	shared := false
	for _, k := range g.kinds {
		look := "class: kind"
		switch {
		case k.claimed:
			look = "class: claimed-resource"
		case k.shared:
			look += fmt.Sprintf("; style.fill: %q", sharedFill)
			shared = true
		}
		// Orange says more than the fill of a diff; the stroke still tells which tree has the kind.
		if k.shared && !k.claimed {
			look += k.in.stroke()
		} else {
			look += k.in.look()
		}
		fmt.Fprintf(b, "%s  %q: %q {%s}\n", indent, k.resource, k.label, look)
	}
	fmt.Fprintf(b, "%s}\n", indent)
	return shared
}

// reachOf sums up what a claim lets the provider do: the relation its arrow is drawn in, heavier
// for more verbs, and its label: read or write (a verb other than get, list, watch), and selected
// when a selector narrows it to some objects. The heaviest arrow, all verbs, needs no word for them.
func reachOf(c Claim) (relation, string) {
	r, words := claimsRead, []string{"read"}
	switch {
	case slices.Contains(c.Verbs, "*"):
		r, words = claimsAll, nil
	case canWrite(c.Verbs):
		r, words = claimsWrite, []string{"write"}
	}
	if c.Selected {
		words = append(words, "selected")
	}
	return r, strings.Join(words, " · ")
}

// needsOf says what an export claims of the kinds other exports in the tree serve, a line per
// serving export and reach, in the order of the claims: "reads accountinfos from core".
func needsOf(e Export, serving map[string][]string) []string {
	type need struct {
		server string
		r      relation
	}
	var needs []need
	claimed := map[need][]string{}
	for _, c := range e.Claims {
		r, _ := reachOf(c)
		for _, server := range serving[resourceName(Resource{Group: c.Group, Name: c.Resource})] {
			if server == e.Name {
				continue
			}
			n := need{server, r}
			if claimed[n] == nil {
				needs = append(needs, n)
			}
			claimed[n] = append(claimed[n], c.Resource)
		}
	}
	lines := make([]string, 0, len(needs))
	for _, n := range needs {
		lines = append(lines, fmt.Sprintf("%s %s from %s", needWords[n.r], strings.Join(claimed[n], ", "), n.server))
	}
	return lines
}

// needWords say what a claim lets the provider do, by the relation its arrow is drawn in.
var needWords = map[relation]string{claimsRead: "reads", claimsWrite: "writes", claimsAll: "all verbs on"}

// shortDirNames names each directory of APIBinding files by the shortest end of its path that no
// other such directory shares, so a name means the same on every box: a/manifests, b/manifests.
func shortDirNames(dirs []bindingDir) map[string]string {
	names := map[string]string{}
	for _, d := range dirs {
		parts := strings.Split(d.dir, "/")
		names[d.dir] = d.dir
		for n := 1; n < len(parts); n++ {
			end := strings.Join(parts[len(parts)-n:], "/")
			if !slices.ContainsFunc(dirs, func(o bindingDir) bool { return o.dir != d.dir && (o.dir == end || strings.HasSuffix(o.dir, "/"+end)) }) {
				names[d.dir] = end
				break
			}
		}
	}
	return names
}

// claimTargets are what the arrows of claims on the resources in covered point at, in the order of
// RESOURCES. Claims taking every resource of a box point at the box, the highest such, even one of a
// single resource: d2 draws arrows into the row of a claimed group straight, across what is in the
// way, and to its box routed. Others point at each resource.
func claimTargets(areas []resourceArea, covered map[string]bool) []string {
	takes := func(kinds []kindBox) bool {
		return !slices.ContainsFunc(kinds, func(k kindBox) bool { return !covered[k.resource] })
	}
	var targets []string
	for _, a := range areas {
		top := []string{resourcesColumn}
		if a.category != "" {
			top = append(top, a.category)
			if takes(a.kinds()) {
				targets = append(targets, d2Path(top...))
				continue
			}
		}
		for _, g := range a.groups {
			at := append(slices.Clone(top), "group:"+g.group)
			if takes(g.kinds) {
				targets = append(targets, d2Path(at...))
				continue
			}
			for _, k := range g.kinds {
				if covered[k.resource] {
					targets = append(targets, d2Path(append(slices.Clone(at), k.resource)...))
				}
			}
		}
	}
	return targets
}

// d2Path joins keys into the path of a nested d2 object.
func d2Path(keys ...string) string {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = fmt.Sprintf("%q", k)
	}
	return strings.Join(quoted, ".")
}

// openColumn starts a column of the overview: a heading over its boxes, with no frame of its own.
func openColumn(b *bytes.Buffer, key, heading string) {
	fmt.Fprintf(b, "%q: {\n  label: %q\n  class: column\n", key, heading)
}
