package pie

import (
	"fmt"
	"slices"
	"strings"
)

// reachNotices points out where claims reach: two exports bound together that claim the same
// resources, an export that may write kcp's own resources, and a claim on what another export serves.
func reachNotices(m Model, exports []Export) []Diagnostic {
	var diags []Diagnostic
	notice := func(path string, doc int, format string, args ...any) {
		diags = append(diags, noticeAt(path, doc, format, args...))
	}
	cases := useCasesOf(m)
	binding := func(names ...string) []string {
		var where []string
		for _, c := range cases {
			if !slices.ContainsFunc(names, func(name string) bool { return !slices.Contains(c.exports, name) }) {
				where = append(where, c.name)
			}
		}
		return where
	}

	for i, a := range exports {
		for _, b := range exports[i+1:] {
			var both []string
			for _, ca := range a.Claims {
				if slices.ContainsFunc(b.Claims, func(cb Claim) bool { return cb.Group == ca.Group && cb.Resource == ca.Resource }) {
					both = append(both, ca.Resource)
				}
			}
			if where := binding(a.Name, b.Name); len(both) > 0 && len(where) > 0 {
				notice(b.Path, b.Doc, "APIExports %s and %s both claim %s where %s both", a.Name, b.Name, strings.Join(both, ", "), binds(where))
			}
		}
	}

	// kcp's own API groups end in .kcp.io: workspaces, their types, what they bind, their logical cluster.
	for _, e := range exports {
		var where []string
		granted := map[GroupResource]bool{}
		for _, c := range cases {
			if !slices.Contains(c.exports, e.Name) {
				continue
			}
			grants := false
			for _, cl := range e.Claims {
				gr := GroupResource{Group: cl.Group, Resource: cl.Resource}
				if strings.HasSuffix(cl.Group, ".kcp.io") && canWrite(cl.Verbs) && !slices.Contains(c.withheld[e.Name], gr) {
					granted[gr], grants = true, true
				}
			}
			if grants {
				where = append(where, c.name)
			}
		}
		if len(where) == 0 {
			continue
		}
		var writes []string
		for _, set := range claimSets(slices.DeleteFunc(slices.Clone(e.Claims), func(cl Claim) bool {
			return !granted[GroupResource{Group: cl.Group, Resource: cl.Resource}]
		})) {
			var resources []string
			for _, cl := range set.claims {
				resources = append(resources, cl.Resource)
			}
			writes = append(writes, set.verbs+" on "+strings.Join(resources, ", "))
		}
		notice(e.Path, e.Doc, "APIExport %s may write kcp's own resources where %s it: %s", e.Name, binds(where), strings.Join(writes, "; "))
	}

	serving := exportsServing(exports)
	for _, e := range exports {
		var servers []string
		claimed := map[string][]string{}
		for _, c := range e.Claims {
			for _, server := range serving[resourceName(Resource{Group: c.Group, Name: c.Resource})] {
				if server == e.Name {
					continue
				}
				if len(claimed[server]) == 0 {
					servers = append(servers, server)
				}
				claimed[server] = append(claimed[server], c.Resource)
			}
		}
		for _, server := range servers {
			reason := fmt.Sprintf("APIExport %s claims %s from %s", e.Name, strings.Join(claimed[server], ", "), server)
			var with, without []string
			for _, c := range cases {
				switch {
				case !slices.Contains(c.exports, e.Name):
				case slices.Contains(c.exports, server):
					with = append(with, c.name)
				default:
					without = append(without, c.name)
				}
			}
			switch {
			case len(without) > 0:
				reason += fmt.Sprintf("; %s it without %s", binds(without), server)
			case len(with) > 0:
				reason += fmt.Sprintf("; %s both", binds(with))
			}
			notice(e.Path, e.Doc, "%s", reason)
		}
	}
	return diags
}

// binds says that use cases bind: "account binds", "account and org bind".
func binds(names []string) string {
	if len(names) == 1 {
		return names[0] + " binds"
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] + " bind"
}
