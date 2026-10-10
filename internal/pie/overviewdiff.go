package pie

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// presence says which of the two trees of a diff a box or arrow is in. The zero value is both, which
// is also every box of an overview that is not a diff.
type presence int

const (
	inBoth presence = iota
	onlyInA
	onlyInB
)

// The colours of a diff, as the surface diff has them: cyan for what only B has, grape for what only
// A has. Neither is red, orange or violet, which say what kcp makes of a binding or a claim.
var (
	presenceStroke = map[presence]string{onlyInA: "#ae3ec9", onlyInB: "#1098ad"}
	presenceFill   = map[presence]string{onlyInA: "#f8f0fc", onlyInB: "#e3fafc"}
	presenceKeys   = map[presence]string{onlyInA: "only-a", onlyInB: "only-b"}
	presenceLabels = map[presence]string{onlyInA: "only in A", onlyInB: "only in B"}
	// presenceSigns mark what only one tree has inside a box both have.
	presenceSigns = map[presence]string{onlyInA: "−", onlyInB: "+"}
)

// changedLook frames a box both trees have whose contents change: thick, in the cyan of B, over the
// box's own fill.
var changedLook = fmt.Sprintf("; style.stroke: %q; style.stroke-width: 3", presenceStroke[onlyInB])

// look is the style of a box only one tree has, to follow its class inside braces.
func (p presence) look() string {
	if p == inBoth {
		return ""
	}
	return fmt.Sprintf("; style.stroke: %q; style.fill: %q", presenceStroke[p], presenceFill[p])
}

// stroke is look without the fill, for a box whose fill already says something.
func (p presence) stroke() string {
	if p == inBoth {
		return ""
	}
	return fmt.Sprintf("; style.stroke: %q", presenceStroke[p])
}

// lines is look as lines of a container at indent.
func (p presence) lines(indent string) string {
	if p == inBoth {
		return ""
	}
	return fmt.Sprintf("%sstyle.stroke: %q\n%sstyle.fill: %q\n", indent, presenceStroke[p], indent, presenceFill[p])
}

var leadingStroke = regexp.MustCompile(`^style\.stroke: "#[0-9a-f]{6}"`)

// restroke is the style of an arrow of relation r: its own colour when both trees have it or when it
// is red, since kcp refusing a binding says more than which tree it is in.
func (p presence) restroke(r relation) string {
	if p == inBoth || r == bindsRefused {
		return r.style
	}
	return leadingStroke.ReplaceAllString(r.style, fmt.Sprintf("style.stroke: %q", presenceStroke[p]))
}

// draws says whether the overview has a box or arrow of presence p.
func (o overview) draws(p presence) bool {
	for _, a := range o.areas {
		for _, g := range a.groups {
			if g.in == p || slices.ContainsFunc(g.kinds, func(k kindBox) bool { return k.in == p }) {
				return true
			}
		}
	}
	return slices.ContainsFunc(o.exports, func(e exportBox) bool { return e.in == p }) ||
		slices.ContainsFunc(o.outside, func(x outsideBox) bool { return x.in == p }) ||
		slices.ContainsFunc(o.places, func(x placeBox) bool { return x.in == p }) ||
		slices.ContainsFunc(o.edges, func(e overviewEdge) bool { return e.in == p })
}

// RenderOverviewDiffD2 draws trees a and b as one overview: what both have as b has it, what only b
// has in cyan, what only a has in grape. Exports and WorkspaceTypes are told apart by name, so one
// moved to another file or workspace is no change; directories of APIBinding files by path, kinds by
// resource.
func RenderOverviewDiffD2(a, b Model) []byte {
	return diffOverviews(overviewOf(a, true), overviewOf(b, true)).d2()
}

// diffOverviews merges the overviews of two trees, keyed by name: b's boxes and arrows in b's order,
// then those only a has, in a's.
func diffOverviews(a, b overview) overview {
	u := overview{areas: diffAreas(a.areas, b.areas)}

	// An export whose workspace changed has another d2 path in a; its arrows in a follow it to b's.
	name := func(e exportBox) string { return e.keys[len(e.keys)-1] }
	at := func(e exportBox) string { return d2Path(append([]string{exportsColumn}, e.keys...)...) }
	moved := map[string]string{}
	for _, e := range b.exports {
		e.in = onlyInB
		if i := slices.IndexFunc(a.exports, func(x exportBox) bool { return name(x) == name(e) }); i >= 0 {
			e.in = inBoth
			moved[at(a.exports[i])] = at(e)
			// Places are told apart by id: a directory's short name changes with the other directories.
			e.bound = merge(a.exports[i].bound, e.bound, func(p placeRef) string { return p.id },
				func(p *placeRef, in presence) { p.name = presenceSigns[in] + p.name })
			e.needs = merge(a.exports[i].needs, e.needs, func(line string) string { return line },
				func(line *string, in presence) { *line = presenceSigns[in] + *line })
		}
		if !slices.ContainsFunc(u.exports, func(x exportBox) bool { return name(x) == name(e) }) {
			u.exports = append(u.exports, e)
		}
	}
	for _, e := range a.exports {
		if !slices.ContainsFunc(u.exports, func(x exportBox) bool { return name(x) == name(e) }) {
			e.in = onlyInA
			u.exports = append(u.exports, e)
		}
	}
	for _, e := range u.exports {
		if ws := strings.TrimPrefix(e.keys[0], "workspace:"); len(e.keys) == 2 && !slices.Contains(u.workspaces, ws) {
			u.workspaces = append(u.workspaces, ws)
		}
	}

	u.outside = merge(a.outside, b.outside, func(x outsideBox) string { return x.name }, func(x *outsideBox, p presence) { x.in = p })
	u.places = merge(a.places, b.places, func(x placeBox) string { return x.id }, func(x *placeBox, p presence) { x.in = p })
	kindsInA, kindsInB, names := a.kindsAt(), b.kindsAt(), kindNames(u.areas)
	for i, p := range u.places {
		if p.in == inBoth {
			u.places[i].changes = changeOf(names, kindsInA[p.id], kindsInB[p.id])
		}
	}

	edgesOfA := make([]overviewEdge, 0, len(a.edges))
	for _, e := range a.edges {
		if to, ok := moved[e.to]; ok {
			e.to = to
		}
		if from, ok := moved[e.from]; ok {
			e.from = from
		}
		edgesOfA = append(edgesOfA, e)
	}
	u.edges = merge(edgesOfA, b.edges, func(e overviewEdge) overviewEdge { return e }, func(e *overviewEdge, p presence) { e.in = p })
	return u
}

// kindsAt lists, by place, the kinds it gets, by their d2 paths: those the exports it binds serve.
func (o overview) kindsAt() map[string]map[string]bool {
	served := map[string][]string{}
	for _, e := range o.edges {
		if e.r == servesKind {
			served[e.from] = append(served[e.from], e.to)
		}
	}
	kinds := map[string]map[string]bool{}
	for _, p := range o.places {
		kinds[p.id] = map[string]bool{}
		at := d2Path(boundAtColumn, p.id)
		for _, e := range o.edges {
			if e.from != at {
				continue
			}
			for _, k := range served[e.to] {
				kinds[p.id][k] = true
			}
		}
	}
	return kinds
}

// kindNames names the kinds of areas by their d2 paths.
func kindNames(areas []resourceArea) map[string]string {
	names := map[string]string{}
	for _, a := range areas {
		for _, g := range a.groups {
			for _, k := range g.kinds {
				names[d2Path(resourcesColumn, "group:"+g.group, k.resource)] = k.name
			}
		}
	}
	return names
}

// changeOf says which of the kinds in a a place loses and which of those in b it gets, by name and
// sorted, or nothing when they are the same.
func changeOf(names map[string]string, a, b map[string]bool) string {
	var lost, got []string
	for k := range a {
		if !b[k] {
			lost = append(lost, names[k])
		}
	}
	for k := range b {
		if !a[k] {
			got = append(got, names[k])
		}
	}
	slices.Sort(lost)
	slices.Sort(got)
	var change []string
	if len(lost) > 0 {
		change = append(change, "loses "+strings.Join(lost, ", "))
	}
	if len(got) > 0 {
		change = append(change, "gets "+strings.Join(got, ", "))
	}
	return strings.Join(change, "; ")
}

// merge lists b's items, each once, marked as in both trees when a has one of the same key and as only
// in b when not, then a's items b has none of, marked as only in a.
func merge[T any, K comparable](a, b []T, key func(T) K, mark func(*T, presence)) []T {
	var out []T
	seen := map[K]bool{}
	inA := map[K]bool{}
	for _, x := range a {
		inA[key(x)] = true
	}
	for _, x := range b {
		if k := key(x); !seen[k] {
			seen[k] = true
			p := onlyInB
			if inA[k] {
				p = inBoth
			}
			mark(&x, p)
			out = append(out, x)
		}
	}
	for _, x := range a {
		if k := key(x); !seen[k] {
			seen[k] = true
			mark(&x, onlyInA)
			out = append(out, x)
		}
	}
	return out
}

// diffAreas merges the RESOURCES of two trees: each area, group and kind by its name, b's first.
func diffAreas(a, b []resourceArea) []resourceArea {
	var out []resourceArea
	// areaOf is the area of a category, empty when the tree has none.
	areaOf := func(areas []resourceArea, category string) resourceArea {
		i := slices.IndexFunc(areas, func(x resourceArea) bool { return x.category == category })
		if i < 0 {
			return resourceArea{}
		}
		return areas[i]
	}
	for _, category := range merge(a, b, func(x resourceArea) string { return x.category }, func(*resourceArea, presence) {}) {
		inA, inB := areaOf(a, category.category), areaOf(b, category.category)
		groups := merge(inA.groups, inB.groups, func(g kindGroup) string { return g.group }, func(g *kindGroup, p presence) { g.in = p })
		for i, g := range groups {
			var kindsOfA, kindsOfB []kindBox
			for _, x := range inA.groups {
				if x.group == g.group {
					kindsOfA = x.kinds
				}
			}
			for _, x := range inB.groups {
				if x.group == g.group {
					kindsOfB = x.kinds
				}
			}
			groups[i].kinds = merge(kindsOfA, kindsOfB, func(k kindBox) string { return k.resource }, func(k *kindBox, p presence) { k.in = p })
		}
		out = append(out, resourceArea{category: category.category, groups: groups})
	}
	return out
}
