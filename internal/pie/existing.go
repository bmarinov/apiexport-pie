package pie

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// keep is a kind the existing workspaces of a consumer both trees have keep in their binding of
// export: an export of both trees that served the kind in A and no longer does, while B gives the
// consumer the kind through the exports in to. kcp releases no resource a binding has bound for as
// long as the binding exists, so that binding goes on serving the kind, and a binding of another
// export that serves it is left without it, with the reason NamingConflicts (kcp v0.32.3). refused
// lists the exports of to whose binding existing workspaces have: one they had already, one a
// directory of APIBinding files applies, or a default binding kcp maintains.
type keep struct {
	consumer consumerBindings
	resource string
	export   string
	to       []string
	refused  []string
}

// keptInExisting lists what existing workspaces keep, by consumer in b's order, then by the export
// that served the kinds and the kinds in a's order.
func keptInExisting(a, b Model) []keep {
	servedBefore, servedAfter := exportsServing(a.Exports), exportsServing(b.Exports)
	inB := exportNames(b.Exports)
	before := existingBindingsOf(a)
	var keeps []keep
	for _, c := range existingBindingsOf(b) {
		i := slices.IndexFunc(before, func(x existingBindings) bool { return x.class == c.class && x.name == c.name })
		if i < 0 {
			continue
		}
		had := before[i].binds
		for _, export := range had {
			if !slices.Contains(inB, export) {
				continue
			}
			for _, resource := range resourcesOf(a.Exports) {
				if !slices.Contains(servedBefore[resource], export) || slices.Contains(servedAfter[resource], export) {
					continue
				}
				// Of two bindings serving a kind, kcp locks it for the one it reconciles first, so the
				// trees do not tell which one existing workspaces keep it in.
				if len(had)-len(without(had, servedBefore[resource])) > 1 {
					continue
				}
				k := keep{consumer: c.consumerBindings, resource: resource, export: export}
				for _, x := range c.binds {
					if !slices.Contains(servedAfter[resource], x) {
						continue
					}
					k.to = append(k.to, x)
					if c.reaches[x] || slices.Contains(had, x) {
						k.refused = append(k.refused, x)
					}
				}
				if len(k.to) > 0 {
					keeps = append(keeps, k)
				}
			}
		}
	}
	return keeps
}

// existingBindings is a consumer with every export its workspaces bind, those of the types a
// WorkspaceType extends included, and those whose binding kcp creates in workspaces that exist already.
type existingBindings struct {
	consumerBindings
	reaches map[string]bool
}

// existingBindingsOf lists the WorkspaceTypes of m by name, copies once, then its directories of
// APIBinding files, which reach existing workspaces with every binding.
func existingBindingsOf(m Model) []existingBindings {
	var consumers []existingBindings
	for _, t := range m.WorkspaceTypes {
		if slices.ContainsFunc(consumers, func(c existingBindings) bool { return c.name == t.Name }) {
			continue
		}
		c := existingBindings{consumerBindings: consumerBindings{class: "workspacetype", name: t.Name}, reaches: map[string]bool{}}
		for _, bd := range bindingsOf(t, m.WorkspaceTypes) {
			c.binds = append(c.binds, bd.ref.Name)
			declarer, _ := typeNamed(cmp.Or(bd.via, t.Name), m.WorkspaceTypes)
			c.reaches[bd.ref.Name] = declarer.Maintains
		}
		consumers = append(consumers, c)
	}
	for _, d := range bindingsByDir(m.APIBindings) {
		c := existingBindings{consumerBindings: consumerBindings{class: "apibindings", name: d.dir}, reaches: map[string]bool{}}
		for _, bd := range d.bound {
			if !slices.Contains(c.binds, bd.ref.Name) {
				c.binds = append(c.binds, bd.ref.Name)
			}
			c.reaches[bd.ref.Name] = true
		}
		consumers = append(consumers, c)
	}
	return consumers
}

// diffExisting lists what existing workspaces keep, a line per consumer, export kept and the
// exports whose binding gets none of the kinds.
func diffExisting(out *bytes.Buffer, a, b Model) {
	type line struct {
		consumer       consumerBindings
		export, refuse string
		kinds          []string
	}
	var lines []line
	for _, k := range keptInExisting(a, b) {
		refuse := strings.Join(k.refused, " and ")
		i := slices.IndexFunc(lines, func(l line) bool {
			return l.consumer.class == k.consumer.class && l.consumer.name == k.consumer.name && l.export == k.export && l.refuse == refuse
		})
		if i < 0 {
			lines = append(lines, line{consumer: k.consumer, export: k.export, refuse: refuse})
			i = len(lines) - 1
		}
		lines[i].kinds = append(lines[i].kinds, kindNamed(k.resource, a))
	}
	for _, l := range lines {
		subject, keeps, its := "workspaces of "+l.consumer.String(), "keep", "their"
		if l.consumer.class == "apibindings" {
			subject, keeps, its = l.consumer.String(), "keeps", "its"
		}
		fmt.Fprintf(out, "existing: %s %s %s in %s binding of %s", subject, keeps, strings.Join(l.kinds, ", "), its, l.export)
		if l.refuse != "" {
			fmt.Fprintf(out, "; %s binding of %s does not bind them (NamingConflicts)", its, l.refuse)
		}
		out.WriteString("\n")
	}
}
