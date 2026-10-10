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
  outside: {style: {fill: transparent; stroke: "#868e96"; stroke-dash: 3; font-size: 14}}
  outside-export: {style: {fill: "#f8f9fa"; stroke: "#868e96"; stroke-dash: 3; font-size: 14; border-radius: 6}}
  workspacetype: {style: {fill: "#e7f5ff"; stroke: "#1971c2"; font-size: 15; border-radius: 6}}
  apibindings: {style: {fill: "#ebfbee"; stroke: "#2f9e44"; font-size: 15; border-radius: 6}}
  claimed: {style: {fill: transparent; stroke: "#adb5bd"; stroke-dash: 3; font-size: 15; border-radius: 6}}
  claimed-resource: {style: {fill: "#f8f9fa"; stroke: "#adb5bd"; stroke-dash: 3; font-size: 14; border-radius: 4; font-color: "#495057"}}
}
`

// kindGroup is the box of an API group in RESOURCES.
type kindGroup struct {
	group string
	kinds []kindBox
}

// kindBox is a kind an export in the tree serves or, when claimed is set, a resource exports claim
// and none in the tree serves. shared says more than one export serves it.
type kindBox struct {
	resource, label string
	shared, claimed bool
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
// once in a box of its API group; APIEXPORTS, pointing at the kinds they serve and, dashed, at what
// their claims reach; and BOUND AT, the WorkspaceTypes and directories of APIBinding files, pointing
// at the exports they bind. A kind more than one export serves is orange, and what is claimed but
// served by no export in the tree is dashed. Edges mean what they mean in RenderD2, and a legend
// explains them.
func RenderOverviewD2(m Model) []byte {
	var b bytes.Buffer
	b.WriteString(dagreLayout)
	// Edges point from BOUND AT to APIEXPORTS to RESOURCES; laid out to the left, the columns read
	// in that order from right to left.
	b.WriteString("direction: left\n")
	b.WriteString(overviewClasses)
	var places, edges, outside []string
	drawn := map[relation]bool{}
	edge := func(r relation, from, to, label string) {
		drawn[r] = true
		edges = append(edges, fmt.Sprintf("%s -> %s%s {%s}", from, to, label, r.style))
	}

	for _, e := range m.Exports {
		for _, r := range e.Resources {
			edge(servesKind, d2Path(exportsColumn, exportID(e)), d2Path(resourcesColumn, "group:"+r.Group, resourceName(r)), "")
		}
	}
	areas := resourceAreasOf(m)
	for _, e := range m.Exports {
		var reaches []string
		covered := map[string]map[string]bool{}
		for _, c := range e.Claims {
			reach := reachOf(c)
			if covered[reach] == nil {
				reaches = append(reaches, reach)
				covered[reach] = map[string]bool{}
			}
			covered[reach][resourceName(Resource{Group: c.Group, Name: c.Resource})] = true
		}
		for _, reach := range reaches {
			for _, target := range claimTargets(areas, covered[reach]) {
				edge(claimsReach, d2Path(exportsColumn, exportID(e)), target, fmt.Sprintf(": %q", reach))
			}
		}
	}

	resources := servedByExport(m.Exports, m.Schemas)
	place := func(id, label, class string, bound []binding) {
		places = append(places, fmt.Sprintf("  %q: %q {class: %s}\n", id, label, class))
		conflicts := conflictsAmong(bound, resources)
		for _, bd := range bound {
			// The other exports alone keep the label short; the warning on stderr names the resources.
			r, label := bd.relation, ""
			var with []string
			for _, c := range conflicts {
				if c.export == bd.ref.Name && !slices.Contains(with, c.other) {
					with = append(with, c.other)
				}
			}
			if len(with) > 0 {
				r, label = bindsRefused, fmt.Sprintf(": %q", "conflicts with "+strings.Join(with, ", "))
			}
			var targets []string
			for _, e := range exportsNamed(bd.ref.Name, m.Exports) {
				targets = append(targets, d2Path(exportsColumn, exportID(e)))
			}
			if len(targets) == 0 {
				box := bd.ref.Name + " @ " + workspaceOf(bd.ref)
				if !slices.Contains(outside, box) {
					outside = append(outside, box)
				}
				targets = append(targets, d2Path(exportsColumn, outsideID, box))
			}
			for _, target := range targets {
				edge(r, d2Path(boundAtColumn, id), target, label)
			}
		}
	}
	typesNamed := countNames(m.WorkspaceTypes, func(t WorkspaceType) string { return t.Name })
	for _, t := range m.WorkspaceTypes {
		label := t.Name
		if typesNamed[t.Name] > 1 {
			label += " · " + location(t.Path, t.Doc)
		}
		place("workspacetype:"+location(t.Path, t.Doc), label, "workspacetype", bindingsOf(t, m.WorkspaceTypes))
	}
	for _, dir := range bindingsByDir(m.APIBindings) {
		place("apibindings:"+dir.dir, "APIBindings in "+dir.dir, "apibindings", dir.bound)
	}

	shared := ""
	if len(areas) > 0 {
		openColumn(&b, resourcesColumn, "RESOURCES")
		for _, a := range areas {
			indent := "  "
			if a.category != "" {
				fmt.Fprintf(&b, "  %q: {\n    label: %q\n    class: claimed\n", a.category, categoryLabels[a.category])
				indent = "    "
			}
			for _, g := range a.groups {
				if writeGroup(&b, indent, g) {
					shared = "kind more than one APIExport serves"
				}
			}
			if a.category != "" {
				b.WriteString("  }\n")
			}
		}
		b.WriteString("}\n")
	}
	if len(m.Exports) > 0 || len(outside) > 0 {
		openColumn(&b, exportsColumn, "APIEXPORTS")
		exportsCalled := countNames(m.Exports, func(e Export) string { return e.Name })
		for _, e := range m.Exports {
			label := e.Name
			if exportsCalled[e.Name] > 1 {
				label += " · " + location(e.Path, e.Doc)
			}
			fmt.Fprintf(&b, "  %q: %q {class: export}\n", exportID(e), label)
		}
		if len(outside) > 0 {
			fmt.Fprintf(&b, "  %q: {\n    label: \"outside the tree\"\n    class: outside\n", outsideID)
			for _, box := range outside {
				fmt.Fprintf(&b, "    %q: {class: outside-export}\n", box)
			}
			b.WriteString("  }\n")
		}
		b.WriteString("}\n")
	}
	if len(places) > 0 {
		openColumn(&b, boundAtColumn, "BOUND AT")
		b.WriteString(strings.Join(places, ""))
		b.WriteString("}\n")
	}
	for _, line := range edges {
		b.WriteString(line + "\n")
	}
	// Under the middle, the legend adds no width; at a corner it widens the diagram by its own.
	writeLegend(&b, "bottom-center", drawn, shared)
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
			label := r.Name + " · not in tree"
			if s, ok := schemaOf(r, path.Dir(e.Path), m.Schemas); ok {
				label = s.Kind
			}
			add("", r.Group, kindBox{resource: name, label: label, shared: len(serving[name]) > 1})
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
// A box of claimed resources alone is dashed, and holds them in a row.
func writeGroup(b *bytes.Buffer, indent string, g kindGroup) bool {
	label, class := g.group, "claimed"
	if label == "" {
		label = "core"
	}
	if slices.ContainsFunc(g.kinds, func(k kindBox) bool { return !k.claimed }) {
		class = "group"
	}
	fmt.Fprintf(b, "%s%q: {\n%s  label: %q\n%s  class: %s\n", indent, "group:"+g.group, indent, label, indent, class)
	// Claimed resources stand in a row: stacked, they made the diagram half again as tall. d2 draws
	// arrows into a grid straight, which only the few to one claimed resource get.
	if class == "claimed" {
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
		fmt.Fprintf(b, "%s  %q: %q {%s}\n", indent, k.resource, k.label, look)
	}
	fmt.Fprintf(b, "%s}\n", indent)
	return shared
}

// reachOf sums up what a claim lets the provider do, as its arrow says it: all verbs, write (a verb
// other than get, list, watch) or read, and whether a selector narrows it to some objects.
func reachOf(c Claim) string {
	reach := "read"
	switch {
	case slices.Contains(c.Verbs, "*"):
		reach = "all verbs"
	case canWrite(c.Verbs):
		reach = "write"
	}
	if c.Selected {
		reach += " · selected"
	}
	return reach
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
