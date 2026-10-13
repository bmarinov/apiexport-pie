package pie

import (
	"bytes"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
)

// RenderDiff lists what changes from a to b, a line per change, in the order of each tree. Exports
// are told apart by name, so one that moved to another file is no change and a renamed one is
// removed and added. Kinds are told apart by resource and follow it from export to export.
func RenderDiff(a, b Model) []byte {
	var out bytes.Buffer
	before, after := exportNames(a.Exports), exportNames(b.Exports)
	for _, name := range before {
		if !slices.Contains(after, name) {
			fmt.Fprintf(&out, "removed: APIExport %s\n", name)
		}
	}
	for _, name := range after {
		if !slices.Contains(before, name) {
			fmt.Fprintf(&out, "added: APIExport %s\n", name)
		}
	}

	servedBefore, servedAfter := exportsServing(a.Exports), exportsServing(b.Exports)
	for _, resource := range resourcesOf(a.Exports, b.Exports) {
		left := without(servedBefore[resource], servedAfter[resource])
		came := without(servedAfter[resource], servedBefore[resource])
		kind := kindNamed(resource, b)
		if len(came) == 0 {
			kind = kindNamed(resource, a)
		}
		// kcp never takes a resource out of a binding (kcp v0.32.3), so an export of both trees keeps
		// serving what it stops serving to the workspaces that bind it already.
		kept := func(exports ...string) string {
			if slices.ContainsFunc(exports, func(e string) bool { return slices.Contains(after, e) }) {
				return ", existing workspaces keep it"
			}
			return ""
		}
		if len(left) > 0 && len(came) > 0 {
			fmt.Fprintf(&out, "moved: kind %s from %s to %s%s\n", kind, strings.Join(left, " and "), strings.Join(came, " and "), kept(left...))
			continue
		}
		for _, e := range left {
			fmt.Fprintf(&out, "removed: kind %s from %s%s\n", kind, e, kept(e))
		}
		for _, e := range came {
			fmt.Fprintf(&out, "added: kind %s in %s\n", kind, e)
		}
	}
	diffClaims(&out, a, b)
	diffConsumers(&out, a, b)
	diffFindings(&out, a, b)
	return out.Bytes()
}

// diffClaims lists, per export by name, the claims only a has, those whose verbs or selector change,
// and those only b has. Claims are told apart by group and resource.
func diffClaims(out *bytes.Buffer, a, b Model) {
	names := exportNames(a.Exports)
	for _, name := range exportNames(b.Exports) {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	on := func(c Claim) string { return resourceName(Resource{Group: c.Group, Name: c.Resource}) }
	for _, name := range names {
		before, after := claimsOf(name, a), claimsOf(name, b)
		for _, c := range before {
			i := slices.IndexFunc(after, onResource(c))
			if i < 0 {
				fmt.Fprintf(out, "removed: claim by %s on %s: %s\n", name, on(c), reachWords(c))
				continue
			}
			if !sameReach(c, after[i]) {
				fmt.Fprintf(out, "changed: claim by %s on %s: from %s to %s\n", name, on(c), reachWords(c), reachWords(after[i]))
			}
		}
		for _, c := range after {
			if !slices.ContainsFunc(before, onResource(c)) {
				fmt.Fprintf(out, "added: claim by %s on %s: %s\n", name, on(c), reachWords(c))
			}
		}
	}
}

// claimsOf lists the claims of the first export of m named name, each resource once.
func claimsOf(name string, m Model) []Claim {
	named := exportsNamed(name, m.Exports)
	if len(named) == 0 {
		return nil
	}
	var claims []Claim
	for _, c := range named[0].Claims {
		if !slices.ContainsFunc(claims, onResource(c)) {
			claims = append(claims, c)
		}
	}
	return claims
}

// onResource matches the claims on the resource c claims.
func onResource(c Claim) func(Claim) bool {
	return func(x Claim) bool { return x.Group == c.Group && x.Resource == c.Resource }
}

// reachWords say what a claim lets the provider do: its verbs as written, and whether a
// defaultSelector narrows it.
func reachWords(c Claim) string {
	words := strings.Join(c.Verbs, ", ")
	if c.Selected {
		words += " (defaultSelector)"
	}
	return words
}

// sameReach says two claims allow the same verbs, in any order, on the same objects.
func sameReach(a, b Claim) bool {
	va, vb := slices.Sorted(slices.Values(a.Verbs)), slices.Sorted(slices.Values(b.Verbs))
	return slices.Equal(va, vb) && a.Selected == b.Selected
}

// consumerBindings is a consumer as the diff compares it: a WorkspaceType or a directory of
// APIBinding files, with the exports it binds itself, by name.
// maintains says kcp keeps a type's default bindings in its existing workspaces too.
type consumerBindings struct {
	class, name string
	binds       []string
	maintains   bool
}

// consumerTitles name the classes of consumers, as the overview's boxes have them.
var consumerTitles = map[string]string{"workspacetype": "WorkspaceType", "apibindings": "APIBinding directory"}

func (c consumerBindings) String() string { return consumerTitles[c.class] + " " + c.name }

// consumerBindingsOf lists the WorkspaceTypes of m by name, copies once, then its directories of
// APIBinding files by path.
func consumerBindingsOf(m Model) []consumerBindings {
	var consumers []consumerBindings
	for _, t := range m.WorkspaceTypes {
		if slices.ContainsFunc(consumers, func(c consumerBindings) bool { return c.name == t.Name }) {
			continue
		}
		c := consumerBindings{class: "workspacetype", name: t.Name, maintains: t.Maintains}
		for _, ref := range t.Binds {
			c.binds = append(c.binds, ref.Name)
		}
		consumers = append(consumers, c)
	}
	for _, d := range bindingsByDir(m.APIBindings) {
		c := consumerBindings{class: "apibindings", name: d.dir}
		for _, bd := range d.bound {
			if !slices.Contains(c.binds, bd.ref.Name) {
				c.binds = append(c.binds, bd.ref.Name)
			}
		}
		consumers = append(consumers, c)
	}
	return consumers
}

// diffConsumers lists the consumers removed and added, the exports a consumer of both trees stops
// and starts binding, and then the consumers of both trees whose kinds change, as the overview of
// the diff marks them.
func diffConsumers(out *bytes.Buffer, a, b Model) {
	before, after := consumerBindingsOf(a), consumerBindingsOf(b)
	in := func(consumers []consumerBindings, c consumerBindings) int {
		return slices.IndexFunc(consumers, func(x consumerBindings) bool { return x.class == c.class && x.name == c.name })
	}
	for _, c := range before {
		if in(after, c) < 0 {
			fmt.Fprintf(out, "removed: %s\n", c)
		}
	}
	for _, c := range after {
		if in(before, c) >= 0 {
			continue
		}
		if len(c.binds) == 0 {
			fmt.Fprintf(out, "added: %s\n", c)
			continue
		}
		fmt.Fprintf(out, "added: %s, binding %s\n", c, strings.Join(c.binds, ", "))
	}
	for _, c := range after {
		i := in(before, c)
		if i < 0 {
			continue
		}
		// kcp neither deletes a default binding nor, unless it maintains them, creates one in a
		// workspace that already exists (kcp v0.32.3).
		where, lost, got := "in "+c.name, "", ""
		if c.class == "workspacetype" {
			where, lost, got = "by "+c.String(), ", existing workspaces keep it", ", for new workspaces only"
			if c.maintains {
				got = ", for existing workspaces too"
			}
		}
		for _, e := range without(before[i].binds, c.binds) {
			fmt.Fprintf(out, "removed: binding of %s %s%s\n", e, where, lost)
		}
		for _, e := range without(c.binds, before[i].binds) {
			fmt.Fprintf(out, "added: binding of %s %s%s\n", e, where, got)
		}
	}

	oa, ob := overviewOf(a, true), overviewOf(b, true)
	names := kindNames(oa.areas, func(k kindBox) string { return kindNamed(k.resource, a) })
	maps.Copy(names, kindNames(ob.areas, func(k kindBox) string { return kindNamed(k.resource, b) }))
	kindsInA, kindsInB := oa.kindsAt(), ob.kindsAt()
	for _, p := range ob.consumers {
		if !slices.ContainsFunc(oa.consumers, func(x consumerBox) bool { return x.id == p.id }) {
			continue
		}
		if change := lossesAndGains(kindsChange(names, kindsInA[p.id], kindsInB[p.id])); change != "" {
			name := strings.TrimPrefix(p.id, p.class+":")
			fmt.Fprintf(out, "changed: %s %s\n", consumerBindings{class: p.class, name: name}, change)
		}
	}
}

// diffFindings lists the warnings and notices only a has as removed, then in b's order those a has in
// other words as changed and those a has not as added, by their level. Findings are told apart by
// what they are about; of copies of an export or a WorkspaceType, each with its finding, the first
// counts.
func diffFindings(out *bytes.Buffer, a, b Model) {
	about := func(n finding) func(finding) bool { return func(x finding) bool { return x.about == n.about } }
	firsts := func(all []finding) []finding {
		var ns []finding
		for _, n := range all {
			if !slices.ContainsFunc(ns, about(n)) {
				ns = append(ns, n)
			}
		}
		return ns
	}
	before, after := firsts(findings(a)), firsts(findings(b))
	for _, n := range before {
		if !slices.ContainsFunc(after, about(n)) {
			fmt.Fprintf(out, "removed: %s: %s\n", n.Level, n.Reason)
		}
	}
	for _, n := range after {
		i := slices.IndexFunc(before, about(n))
		if i < 0 {
			fmt.Fprintf(out, "added: %s: %s\n", n.Level, n.Reason)
			continue
		}
		if before[i].Reason != n.Reason {
			fmt.Fprintf(out, "changed: %s: %s\n", n.Level, n.Reason)
		}
	}
}

// resourcesOf lists the resources the exports of both trees serve, those of the first tree first.
func resourcesOf(trees ...[]Export) []string {
	var resources []string
	for _, exports := range trees {
		for _, e := range exports {
			for _, r := range e.Resources {
				if name := resourceName(r); !slices.Contains(resources, name) {
					resources = append(resources, name)
				}
			}
		}
	}
	return resources
}

// kindNamed names a resource of a tree as Kind.group, the way Kubernetes writes Deployment.apps,
// or by the resource itself when its schema is not in the tree.
func kindNamed(resource string, m Model) string {
	for _, e := range m.Exports {
		for _, r := range e.Resources {
			if resourceName(r) != resource {
				continue
			}
			if s, ok := schemaOf(r, path.Dir(e.Path), m.Schemas); ok {
				return s.Kind + "." + r.Group
			}
			return resource
		}
	}
	return resource
}

func without(names, others []string) []string {
	var rest []string
	for _, name := range names {
		if !slices.Contains(others, name) {
			rest = append(rest, name)
		}
	}
	return rest
}

// exportNames lists the names of exports once each, copies of an export sharing its name.
func exportNames(exports []Export) []string {
	var names []string
	for _, e := range exports {
		if !slices.Contains(names, e.Name) {
			names = append(names, e.Name)
		}
	}
	return names
}

// lossesAndGains says what a consumer loses and gets: "loses A, B; gets C", or nothing.
func lossesAndGains(lost, got []string) string {
	var change []string
	if len(lost) > 0 {
		change = append(change, "loses "+strings.Join(lost, ", "))
	}
	if len(got) > 0 {
		change = append(change, "gets "+strings.Join(got, ", "))
	}
	return strings.Join(change, "; ")
}
