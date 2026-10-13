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
	consumersColumn = "consumers"
)

// dagreLayout makes dagre the layout engine of the overview. Between plain boxes its curves read
// better than ELK's right angles, and the columns stay close: ELK gives every edge a lane of its own
// between them. D2_LAYOUT and --layout still win.
const dagreLayout = "vars: {\n  d2-config: {\n    layout-engine: dagre\n  }\n}\n"

// overviewClasses are the looks of the overview's boxes. A consumer takes the colour of the edges
// it draws: blue for a WorkspaceType, green for APIBinding files.
const overviewClasses = `classes: {
  column: {style: {fill: transparent; stroke: transparent; font-size: 22; bold: true; font-color: "#868e96"}}
  group: {style: {fill: "#f1f3f5"; stroke: "#ced4da"; font-size: 15; border-radius: 6}}
  kind: {style: {fill: "#ffffff"; stroke: "#adb5bd"; font-size: 14; border-radius: 4}}
  export: {style: {fill: "#ffffff"; stroke: "#364fc7"; stroke-width: 2; font-size: 16; bold: true; border-radius: 6}}
  story: {style: {fill: transparent; stroke: transparent; font-size: 13; bold: false; font-color: "#343a40"}}
  warning: {style: {fill: transparent; stroke: transparent; font-size: 13; bold: true; font-color: "#e8590c"}}
  workspace: {style: {fill: "#f8f9fa"; stroke: "#868e96"; font-size: 15; border-radius: 8}}
  workspacetype: {style: {fill: "#e7f5ff"; stroke: "#1971c2"; font-size: 15; border-radius: 6}}
  apibindings: {style: {fill: "#ebfbee"; stroke: "#2f9e44"; font-size: 15; border-radius: 6}}
  claimed: {style: {fill: transparent; stroke: "#adb5bd"; stroke-dash: 3; font-size: 15; border-radius: 6}}
  claimed-resource: {style: {fill: "#f8f9fa"; stroke: "#adb5bd"; stroke-dash: 3; font-size: 14; border-radius: 4; font-color: "#495057"}}
}
`

// apiGroupSample is the legend's sample of a box in RESOURCES: an API group holding a kind.
const apiGroupSample = "  \"api-group\": {\n    label: \"API group\"\n    class: group\n    \"kind\": \"kind\" {class: kind}\n  }\n"

// warningSample is the legend's sample of an export box with a warning.
const warningSample = "  \"warning\": {\n    label: \"APIExport\"\n    class: export\n    grid-columns: 1\n    grid-gap: 0\n" +
	"    \"warnings\": \"! a warning, also printed on stderr\" {class: warning; height: 17}\n  }\n"

// lineHeight is the height d2 0.6.9 gives a line of the 13 px text in an export box, with room
// to breathe. A grid cell as tall as its lines keeps d2 from spreading a box's cells over its
// height, which put the warnings at the bottom, far from the story.
const lineHeight = 17

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
// CONSUMERS, the WorkspaceTypes and directories of APIBinding files, pointing at the exports they
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
	consumers  []consumerBox
	edges      []overviewEdge
}

// exportBox is an export in APIEXPORTS: its keys inside the column, its name, the consumers
// binding it and what it needs of other exports. kept, in a diff, names the kinds an export of both
// trees serves only in A, which the workspaces binding it already keep.
type exportBox struct {
	keys  []string
	label string
	bound []consumerRef
	kept  []string
	needs []string
	open  []string
	// warnings say what is wrong with the export as written, a line each, in orange under the story.
	warnings []string
	in       presence
}

// consumerRef is a consumer binding an export, by its id in CONSUMERS and its short name. note, in a
// diff, says what kcp makes of a change of the binding in the workspaces the consumer already has.
type consumerRef struct{ id, name, note string }

// story is what the box says under the name: where the export is bound, what it needs of other
// exports, so the box reads without tracing arrows, and what it leaves open.
func (e exportBox) story() []string {
	var story []string
	if len(e.bound) > 0 {
		names := make([]string, 0, len(e.bound))
		for _, p := range e.bound {
			if p.note != "" {
				p.name += " (" + p.note + ")"
			}
			names = append(names, p.name)
		}
		story = append(story, "bound at "+strings.Join(names, ", "))
	}
	if len(e.kept) > 0 {
		story = append(story, "existing workspaces keep "+strings.Join(e.kept, ", "))
	}
	story = append(story, e.needs...)
	return append(story, e.open...)
}

// consumerBox is a consumer in CONSUMERS. outside lists the exports it binds that the tree does not
// define, which its title names rather than an arrow across the picture. changed, in a diff, lists
// the kinds a consumer both trees have had only in A and got only in B, by tree.
type consumerBox struct {
	id, label, class string
	outside          []outsideRef
	in               presence
	changed          map[presence][]string
}

// outsideRef is an export outside the tree a consumer binds: its name, the workspace it is bound
// from, the type in the tree the consumer binds it through, if not itself, and whether it is kcp's own.
type outsideRef struct {
	name, at, via string
	kcp           bool
}

// title is a consumer's label, then a line per type it binds exports outside the tree through,
// itself first, kcp's own apart from the others: "binds tenancy.kcp.io, topology.kcp.io @ root (kcp)".
func (p consumerBox) title() string {
	lines := []string{p.label}
	type source struct {
		via string
		kcp bool
	}
	var sources []source
	for _, r := range p.outside {
		if s := (source{via: r.via, kcp: r.kcp}); !slices.Contains(sources, s) {
			sources = append(sources, s)
		}
	}
	for _, s := range sources {
		var ats []string
		names := map[string][]string{}
		for _, r := range p.outside {
			if r.via != s.via || r.kcp != s.kcp {
				continue
			}
			if len(names[r.at]) == 0 {
				ats = append(ats, r.at)
			}
			names[r.at] = append(names[r.at], r.name)
		}
		groups := make([]string, 0, len(ats))
		for _, at := range ats {
			groups = append(groups, strings.Join(names[at], ", ")+" @ "+at)
		}
		note := "not in tree"
		if s.kcp {
			note = "kcp"
		}
		if s.via != "" {
			note += ", through " + s.via
		}
		lines = append(lines, "binds "+strings.Join(groups, "; ")+" ("+note+")")
	}
	return strings.Join(lines, "\n")
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
	// boundBy lists, by export name, the consumers binding it, for its box.
	boundBy := map[string][]consumerRef{}
	boundAt := func(id, label, name, class string, bound []binding) {
		var outside []outsideRef
		conflicts := conflictsAmong(bound, resources)
		for _, bd := range bound {
			if !slices.ContainsFunc(boundBy[bd.ref.Name], func(p consumerRef) bool { return p.name == name }) {
				boundBy[bd.ref.Name] = append(boundBy[bd.ref.Name], consumerRef{id: id, name: name})
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
			targets := exportsNamed(bd.ref.Name, m.Exports)
			if len(targets) == 0 {
				outside = append(outside, outsideRef{name: bd.ref.Name, at: workspaceOf(bd.ref), via: bd.via, kcp: isKcpExport(bd.ref)})
			}
			for _, target := range targets {
				edge(r, d2Path(consumersColumn, id), exportAt(target), label)
			}
		}
		o.consumers = append(o.consumers, consumerBox{id: id, label: label, class: class, outside: outside})
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
		// The short name, as the boxes of the exports it binds say it: a tree scanned from a repository's
		// root otherwise has its longest titles here.
		boundAt("apibindings:"+dir.dir, "APIBindings in "+names[dir.dir], names[dir.dir], "apibindings", dir.bound)
	}

	for _, e := range m.Exports {
		if p := paths[e.Name]; len(p) == 1 && !slices.Contains(o.workspaces, p[0]) {
			o.workspaces = append(o.workspaces, p[0])
		}
	}
	exportsCalled := countNames(m.Exports, func(e Export) string { return e.Name })
	serving, warnings := exportsServing(m.Exports), exportWarnings(m)
	for _, e := range m.Exports {
		label := e.Name
		if exportsCalled[e.Name] > 1 && !byName {
			label += " · " + location(e.Path, e.Doc)
		}
		o.exports = append(o.exports, exportBox{keys: exportKeys(e), label: label, bound: boundBy[e.Name], needs: needsOf(e, serving), open: openOf(e),
			warnings: warningLines(warnings[location(e.Path, e.Doc)])})
	}
	return o
}

// d2 writes the overview as d2 source.
func (o overview) d2() []byte {
	var b bytes.Buffer
	b.WriteString(dagreLayout)
	// Edges point from CONSUMERS to APIEXPORTS to RESOURCES; laid out to the left, the columns read
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
	if len(o.exports) > 0 {
		openColumn(&b, exportsColumn, "APIEXPORTS")
		for _, ws := range o.workspaces {
			fmt.Fprintf(&b, "  %q: %q {class: workspace}\n", workspaceID(ws), "Workspace "+ws)
		}
		for _, e := range o.exports {
			// Every box has a story, as it says whether the export sets a maximal permission policy.
			// As a grid the box is laid out as one node; as a plain container dagre stretches it
			// across the ranks of its edges.
			fmt.Fprintf(&b, "  %s: {\n    label: %q\n    class: export\n%s    grid-columns: 1\n    grid-gap: 0\n",
				d2Path(e.keys...), e.label, e.in.lines("    "))
			writeCell(&b, "story", "story", e.story())
			if len(e.warnings) > 0 {
				writeCell(&b, "warnings", "warning", e.warnings)
			}
			b.WriteString("  }\n")
		}
		b.WriteString("}\n")
	}
	if len(o.consumers) > 0 {
		openColumn(&b, consumersColumn, "CONSUMERS")
		for _, p := range o.consumers {
			if p.changed == nil {
				fmt.Fprintf(&b, "  %q: %q {class: %s%s}\n", p.id, p.title(), p.class, p.in.look())
				continue
			}
			// A container's label is not bold, as a box's is; its tags stack under it.
			fmt.Fprintf(&b, "  %q: {\n    label: %q\n    class: %s\n    style.bold: true\n    grid-columns: 1\n    grid-gap: 4\n", p.id, p.title(), p.class)
			for _, in := range []presence{onlyInA, onlyInB} {
				if kinds := p.changed[in]; len(kinds) > 0 {
					fmt.Fprintf(&b, "    %q: %q {%s}\n", tagKeys[in], wrapped(tagHeads[in], kinds, tagWidth(p.title())), in.tag())
				}
			}
			b.WriteString("  }\n")
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
	if slices.ContainsFunc(o.exports, func(e exportBox) bool { return len(e.warnings) > 0 }) {
		samples += warningSample
	}
	for _, p := range []presence{onlyInB, onlyInA} {
		if o.draws(p) {
			samples += fmt.Sprintf("  %q: %q {%s}\n", presenceKeys[p], presenceLabels[p], strings.TrimPrefix(p.look(), "; "))
		}
	}
	for _, in := range []presence{onlyInB, onlyInA} {
		if slices.ContainsFunc(o.consumers, func(p consumerBox) bool { return len(p.changed[in]) > 0 }) {
			samples += fmt.Sprintf("  %q: %q {%s}\n", tagKeys[in], tagHeads[in]+" kinds", in.tag())
		}
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
// for more verbs, and its label: read or write (a verb other than get, list, watch), and
// defaultSelector when one narrows it to some objects. The heaviest arrow, all verbs, needs no word
// for them.
func reachOf(c Claim) (relation, string) {
	r, words := claimsRead, []string{"read"}
	switch {
	case slices.Contains(c.Verbs, "*"):
		r, words = claimsAll, nil
	case canWrite(c.Verbs):
		r, words = claimsWrite, []string{"write"}
	}
	if c.Selected {
		words = append(words, "defaultSelector")
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

// openOf says what an export leaves open: whether a maximal permission policy caps what consumers
// may do with what it serves. It reads as the field does, with "none" for an export that sets none,
// which kcp allows: a mark on that line would read as a fault.
func openOf(e Export) []string {
	policy := e.MaximalPermissionPolicy
	if policy == "" {
		policy = "none"
	}
	return []string{"maximalPermissionPolicy: " + policy}
}

// warningLines are what is wrong with an export as written, as its box says it: a warning's first
// row says what is wrong, and what follows from it goes on a second behind an arrow, as the whole
// reason is wider than the box d2 measures for bold text. A warning stays one entry, so a diff marks
// it once.
func warningLines(warnings []exportWarning) []string {
	var lines []string
	for _, w := range warnings {
		line := "! " + w.reason
		if what, follows, found := strings.Cut(w.reason, "; "); found {
			line = "! " + what + "\n→ " + follows
		}
		lines = append(lines, line)
	}
	return lines
}

// writeCell writes lines as a cell of an export box's grid, as tall as its rows.
func writeCell(b *bytes.Buffer, key, class string, lines []string) {
	text := strings.Join(lines, "\n")
	fmt.Fprintf(b, "    %q: %q {class: %s; height: %d}\n", key, text, class, lineHeight*(strings.Count(text, "\n")+1))
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
