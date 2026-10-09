package pie

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// Check warns about the exports a use case binds that kcp refuses together, of which a workspace
// gets only one; RenderD2 marks the same conflicts in red. It then notes what is worth a look.
func Check(m Model) []Diagnostic {
	resources := servedByExport(m.Exports, m.Schemas)
	var diags []Diagnostic
	for _, t := range m.WorkspaceTypes {
		for _, c := range pairsOf(bindingsOf(t, m.WorkspaceTypes), resources) {
			diags = append(diags, Diagnostic{Level: Warning, Path: t.Path, Doc: t.Doc, Reason: c.reason("WorkspaceType " + t.Name + " binds")})
		}
	}
	for _, dir := range bindingsByDir(m.APIBindings) {
		for _, c := range pairsOf(dir.bound, resources) {
			// A directory is no single document, so the warning points at the later binding of the two.
			later := firstBindingOf(c.other, dir.dir, m.APIBindings)
			diags = append(diags, Diagnostic{Level: Warning, Path: later.Path, Doc: later.Doc, Reason: c.reason("APIBindings in " + dir.dir + " bind")})
		}
	}
	return append(diags, notices(m)...)
}

// notices points out what is worth a look about how the APIs are cut and where their claims reach.
// Each is a fact about the manifests; whether it is wrong is the designer's call.
func notices(m Model) []Diagnostic {
	// Copies of an export share its name and count once, from the first.
	var exports []Export
	for _, e := range m.Exports {
		if len(exportsNamed(e.Name, exports)) == 0 {
			exports = append(exports, e)
		}
	}
	return append(cohesionNotices(m, exports), reachNotices(m, exports)...)
}

func noticeAt(path string, doc int, format string, args ...any) Diagnostic {
	return Diagnostic{Level: Notice, Path: path, Doc: doc, Reason: fmt.Sprintf(format, args...)}
}

// cohesionNotices points out an export serving several API groups, a group split across exports, a
// WorkspaceType that binds nothing of the tree, and an export nothing binds.
func cohesionNotices(m Model, exports []Export) []Diagnostic {
	var diags []Diagnostic
	notice := func(path string, doc int, format string, args ...any) {
		diags = append(diags, noticeAt(path, doc, format, args...))
	}

	var groups []string
	servingGroup := map[string][]Export{}
	for _, e := range exports {
		var own []string
		for _, r := range e.Resources {
			if !slices.Contains(own, r.Group) {
				own = append(own, r.Group)
			}
		}
		if len(own) > 1 {
			notice(e.Path, e.Doc, "APIExport %s serves %d API groups: %s", e.Name, len(own), strings.Join(own, ", "))
		}
		for _, g := range own {
			if len(servingGroup[g]) == 0 {
				groups = append(groups, g)
			}
			servingGroup[g] = append(servingGroup[g], e)
		}
	}
	for _, g := range groups {
		if serving := servingGroup[g]; len(serving) > 1 {
			names := make([]string, 0, len(serving))
			for _, e := range serving {
				names = append(names, e.Name)
			}
			notice(serving[1].Path, serving[1].Doc, "API group %s is served by %d APIExports: %s", g, len(serving), strings.Join(names, ", "))
		}
	}

	bound := map[string]bool{}
	for _, t := range m.WorkspaceTypes {
		bindings := bindingsOf(t, m.WorkspaceTypes)
		var outside []string
		inside := false
		for _, bd := range bindings {
			bound[bd.ref.Name] = true
			switch {
			case len(exportsNamed(bd.ref.Name, exports)) > 0:
				inside = true
			case !slices.Contains(outside, bd.ref.Name):
				outside = append(outside, bd.ref.Name)
			}
		}
		switch {
		case len(bindings) == 0:
			notice(t.Path, t.Doc, "WorkspaceType %s binds no APIExport", t.Name)
		case !inside:
			notice(t.Path, t.Doc, "WorkspaceType %s binds only APIExports from outside the tree: %s", t.Name, strings.Join(outside, ", "))
		}
	}
	for _, ab := range m.APIBindings {
		bound[ab.Export.Name] = true
	}
	// A tree that binds nothing provides APIs for others; that its exports go unbound is no news.
	if len(m.WorkspaceTypes)+len(m.APIBindings) == 0 {
		return diags
	}
	for _, e := range exports {
		if !bound[e.Name] {
			notice(e.Path, e.Doc, "APIExport %s is bound by no WorkspaceType or APIBinding in the tree", e.Name)
		}
	}
	return diags
}

// pairsOf lists the conflicts among one use case's bindings once each, from the side of the export
// bound first.
func pairsOf(bound []binding, resources map[string][]served) []conflict {
	at := func(name string) int {
		return slices.IndexFunc(bound, func(bd binding) bool { return bd.ref.Name == name })
	}
	var pairs []conflict
	for _, c := range conflictsAmong(bound, resources) {
		if at(c.export) < at(c.other) {
			pairs = append(pairs, c)
		}
	}
	return pairs
}

func firstBindingOf(export, dir string, bindings []APIBinding) APIBinding {
	for _, b := range bindings {
		if b.Export.Name == export && path.Dir(b.Path) == dir {
			return b
		}
	}
	return APIBinding{}
}

func (c conflict) reason(subject string) string {
	if c.on == "" {
		return fmt.Sprintf("%s %s and %s, which both serve %s; kcp binds only one of them", subject, c.export, c.other, c.mine)
	}
	return fmt.Sprintf("%s %s and %s, whose %s and %s both use %s; kcp binds only one of them", subject, c.export, c.other, c.mine, c.theirs, c.on)
}
