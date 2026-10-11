package pie

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
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

// A consumer both trees have whose kinds change keeps its look and holds a tag of the kinds only one
// tree gives it, keyed and headed by the tree.
var (
	tagKeys  = map[presence]string{onlyInA: "lost", onlyInB: "got"}
	tagHeads = map[presence]string{onlyInA: "had in A:", onlyInB: "got in B:"}
)

// tag is the style of such a tag: dashed and written in the tree's colour, over the box's own fill.
func (p presence) tag() string {
	return fmt.Sprintf("style.stroke: %q; style.stroke-dash: 3; style.fill: transparent; style.font-size: 11; style.bold: false; style.font-color: %q",
		presenceStroke[p], presenceStroke[p])
}

// tagWidth is the characters a consumer's tags wrap at, so the consumer keeps about the width it has
// without them: its title's longest line and half again, as the title's letters are bigger and bold,
// and no fewer than 24.
func tagWidth(title string) int {
	width := 24
	for _, line := range strings.Split(title, "\n") {
		width = max(width, utf8.RuneCountInString(line)*3/2)
	}
	return width
}

// wrapped lists kinds after head, a comma after each but the last, in lines of at most width
// characters, broken only between kinds.
func wrapped(head string, kinds []string, width int) string {
	var lines []string
	line := head
	for i, k := range kinds {
		if i < len(kinds)-1 {
			k += ","
		}
		if utf8.RuneCountInString(line)+1+len(k) > width {
			lines, line = append(lines, line), k
			continue
		}
		line += " " + k
	}
	return strings.Join(append(lines, line), "\n")
}

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
		slices.ContainsFunc(o.consumers, func(x consumerBox) bool { return x.in == p }) ||
		slices.ContainsFunc(o.edges, func(e overviewEdge) bool { return e.in == p })
}

// RenderOverviewDiffD2 draws trees a and b as one overview: what both have as b has it, what only b
// has in cyan, what only a has in grape. Exports and WorkspaceTypes are told apart by name, so one
// moved to another file or workspace is no change; directories of APIBinding files by path, kinds by
// resource.
func RenderOverviewDiffD2(a, b Model) []byte {
	u := diffOverviews(overviewOf(a, true), overviewOf(b, true))
	notes := lifecycleNotes(a, b)
	for i, e := range u.exports {
		bound := slices.Clone(e.bound)
		for j, p := range bound {
			bound[j].note = notes[bindingKey{consumer: p.id, export: e.keys[len(e.keys)-1]}]
		}
		u.exports[i].bound = bound
	}
	return u.d2()
}

// bindingKey is a binding of an export by a consumer, by their ids in the overview of a diff.
type bindingKey struct{ consumer, export string }

// lifecycleNotes says what kcp does in the workspaces a WorkspaceType of both trees already has
// about an export the type starts or stops binding, itself or through a type it extends. kcp creates
// default bindings with a workspace, and in existing workspaces only for a type declaring them with
// defaultAPIBindingLifecycle: Maintain; it deletes none (kcp v0.32.3).
func lifecycleNotes(a, b Model) map[bindingKey]string {
	// declarers lists, by export name, the type declaring the binding a workspace of t gets.
	declarers := func(t WorkspaceType, types []WorkspaceType) map[string]string {
		by := map[string]string{}
		for _, bd := range bindingsOf(t, types) {
			if _, ok := by[bd.ref.Name]; !ok {
				by[bd.ref.Name] = cmp.Or(bd.via, t.Name)
			}
		}
		return by
	}
	notes := map[bindingKey]string{}
	for _, t := range b.WorkspaceTypes {
		before, ok := typeNamed(t.Name, a.WorkspaceTypes)
		if !ok {
			continue
		}
		after, _ := typeNamed(t.Name, b.WorkspaceTypes)
		inA, inB := declarers(before, a.WorkspaceTypes), declarers(after, b.WorkspaceTypes)
		key := func(export string) bindingKey {
			return bindingKey{consumer: "workspacetype:" + t.Name, export: "apiexport:" + export}
		}
		for export, by := range inB {
			if _, ok := inA[export]; ok {
				continue
			}
			if declarer, _ := typeNamed(by, b.WorkspaceTypes); !declarer.Maintains {
				notes[key(export)] = "new workspaces only"
			}
		}
		for export := range inA {
			if _, ok := inB[export]; !ok {
				notes[key(export)] = "existing workspaces keep it"
			}
		}
	}
	return notes
}

// diffOverviews merges the overviews of two trees, keyed by name: b's boxes and arrows in b's order,
// then those only a has, in a's.
func diffOverviews(a, b overview) overview {
	u := overview{areas: diffAreas(a.areas, b.areas)}

	// An export whose workspace changed has another d2 path in a; its arrows in a follow it to b's.
	name := func(e exportBox) string { return e.keys[len(e.keys)-1] }
	at := func(e exportBox) string { return d2Path(append([]string{exportsColumn}, e.keys...)...) }
	moved := map[string]string{}
	names := kindNames(u.areas, func(k kindBox) string { return k.name })
	servedInA, servedInB := a.served(), b.served()
	for _, e := range b.exports {
		e.in = onlyInB
		if i := slices.IndexFunc(a.exports, func(x exportBox) bool { return name(x) == name(e) }); i >= 0 {
			e.in = inBoth
			moved[at(a.exports[i])] = at(e)
			// Consumers are told apart by id: a directory's short name changes with the other directories.
			e.bound = merge(a.exports[i].bound, e.bound, func(p consumerRef) string { return p.id },
				func(p *consumerRef, in presence) { p.name = presenceSigns[in] + p.name })
			e.needs = merge(a.exports[i].needs, e.needs, func(line string) string { return line },
				func(line *string, in presence) { *line = presenceSigns[in] + *line })
			e.open = merge(a.exports[i].open, e.open, func(line string) string { return line },
				func(line *string, in presence) { *line = presenceSigns[in] + *line })
			e.warnings = merge(a.exports[i].warnings, e.warnings, func(line string) string { return line },
				func(line *string, in presence) { *line = presenceSigns[in] + *line })
			e.kept, _ = kindsChange(names, servedInA[at(a.exports[i])], servedInB[at(e)])
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

	u.consumers = merge(a.consumers, b.consumers, func(x consumerBox) string { return x.id }, func(x *consumerBox, p presence) { x.in = p })
	kindsInA, kindsInB := a.kindsAt(), b.kindsAt()
	for i, p := range u.consumers {
		if p.in == inBoth {
			inA := a.consumers[slices.IndexFunc(a.consumers, func(x consumerBox) bool { return x.id == p.id })]
			u.consumers[i].outside = merge(inA.outside, p.outside, func(r outsideRef) outsideRef { return r },
				func(r *outsideRef, in presence) { r.name = presenceSigns[in] + r.name })
			if lost, got := kindsChange(names, kindsInA[p.id], kindsInB[p.id]); len(lost)+len(got) > 0 {
				u.consumers[i].changed = map[presence][]string{onlyInA: lost, onlyInB: got}
			}
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

// served lists, by export, the kinds it serves, both by their d2 paths.
func (o overview) served() map[string]map[string]bool {
	served := map[string]map[string]bool{}
	for _, e := range o.edges {
		if e.r != servesKind {
			continue
		}
		if served[e.from] == nil {
			served[e.from] = map[string]bool{}
		}
		served[e.from][e.to] = true
	}
	return served
}

// kindsAt lists, by consumer, the kinds it gets, by their d2 paths: those the exports it binds serve.
func (o overview) kindsAt() map[string]map[string]bool {
	served := o.served()
	kinds := map[string]map[string]bool{}
	for _, p := range o.consumers {
		kinds[p.id] = map[string]bool{}
		at := d2Path(consumersColumn, p.id)
		for _, e := range o.edges {
			if e.from != at {
				continue
			}
			for k := range served[e.to] {
				kinds[p.id][k] = true
			}
		}
	}
	return kinds
}

// kindNames names the kinds of areas by their d2 paths, each as name says.
func kindNames(areas []resourceArea, name func(kindBox) string) map[string]string {
	names := map[string]string{}
	for _, a := range areas {
		for _, g := range a.groups {
			for _, k := range g.kinds {
				names[d2Path(resourcesColumn, "group:"+g.group, k.resource)] = name(k)
			}
		}
	}
	return names
}

// kindsChange lists the kinds in a and not in b, and those in b and not in a, by name and sorted.
func kindsChange(names map[string]string, a, b map[string]bool) (lost, got []string) {
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
	return lost, got
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
