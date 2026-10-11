package pie

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// Check warns about what is wrong with an APIExport as written, then about the exports a consumer
// binds that kcp refuses together, of which a workspace gets only one; RenderD2 marks the same
// conflicts in red. It then notes what is worth a look.
func Check(m Model) []Diagnostic {
	var diags []Diagnostic
	for _, f := range findings(m) {
		diags = append(diags, f.Diagnostic)
	}
	return diags
}

// findings are what Check reports, each with what it is about: the warnings, then the notices.
func findings(m Model) []finding {
	var fs []finding
	warnings := exportWarnings(m)
	for _, e := range m.Exports {
		for _, w := range warnings[location(e.Path, e.Doc)] {
			fs = append(fs, finding{about: w.about, Diagnostic: Diagnostic{Level: Warning, Path: e.Path, Doc: e.Doc, Reason: "APIExport " + e.Name + ": " + w.reason}})
		}
	}
	// A refusal is about the very pair and resources its reason names, so the reason tells it apart.
	refusal := func(path string, doc int, reason string) finding {
		return finding{about: "refusal: " + reason, Diagnostic: Diagnostic{Level: Warning, Path: path, Doc: doc, Reason: reason}}
	}
	resources := servedByExport(m.Exports, m.Schemas)
	for _, t := range m.WorkspaceTypes {
		for _, c := range pairsOf(bindingsOf(t, m.WorkspaceTypes), resources) {
			fs = append(fs, refusal(t.Path, t.Doc, c.reason("WorkspaceType "+t.Name+" binds")))
		}
	}
	for _, dir := range bindingsByDir(m.APIBindings) {
		for _, c := range pairsOf(dir.bound, resources) {
			// A directory is no single document, so the warning points at the later binding of the two.
			later := firstBindingOf(c.other, dir.dir, m.APIBindings)
			fs = append(fs, refusal(later.Path, later.Doc, c.reason("APIBindings in "+dir.dir+" bind")))
		}
	}
	return append(fs, notices(m)...)
}

// exportWarning is something wrong with an export as written: what it is about, by which a diff
// tells warnings apart, and the reason, without the export's name, as its box in the overview says it.
type exportWarning struct{ about, reason string }

// exportWarnings says what is wrong with each export of m as written, by the document it is in.
func exportWarnings(m Model) map[string][]exportWarning {
	serving := exportsServing(m.Exports)
	warnings := map[string][]exportWarning{}
	for _, e := range m.Exports {
		at := location(e.Path, e.Doc)
		// kcp v0.32.3 still serves v1alpha1 but deprecates it (pkg/admission/apiexport); a v1alpha1
		// claim has no verbs, and kcp converts it to all of them.
		if e.V1alpha1 {
			reason := "apis.kcp.io/v1alpha1 is deprecated"
			if len(e.Claims) > 0 {
				reason += "; claims get verbs *"
			}
			warnings[at] = append(warnings[at], exportWarning{about: "v1alpha1 of " + e.Name, reason: reason})
		}
		// A hash written out matches one identity; one kcp generates differs per installation.
		var servers []string
		literal := map[string][]string{}
		for _, c := range e.Claims {
			if !c.LiteralHash {
				continue
			}
			for _, server := range serving[resourceName(Resource{Group: c.Group, Name: c.Resource})] {
				if server == e.Name || exportsNamed(server, m.Exports)[0].IdentitySecret {
					continue
				}
				if len(literal[server]) == 0 {
					servers = append(servers, server)
				}
				literal[server] = append(literal[server], c.Resource)
			}
		}
		for _, server := range servers {
			warnings[at] = append(warnings[at], exportWarning{about: "literal hashes of " + e.Name + " for " + server,
				reason: fmt.Sprintf("identityHash for %s is a literal; %s's identity differs per installation", strings.Join(literal[server], ", "), server)})
		}
	}
	return warnings
}

// notices points out what is worth a look about how the APIs are cut and where their claims reach.
// Each is a fact about the manifests; whether it is wrong is the designer's call.
func notices(m Model) []finding {
	// Copies of an export share its name and count once, from the first.
	var exports []Export
	for _, e := range m.Exports {
		if len(exportsNamed(e.Name, exports)) == 0 {
			exports = append(exports, e)
		}
	}
	return append(cohesionNotices(m, exports), reachNotices(m, exports)...)
}

// finding is a warning or a notice and what it is about, by which a diff tells findings apart: a
// finding about the same thing may read differently in another tree.
type finding struct {
	about string
	Diagnostic
}

func noticeAbout(about, path string, doc int, format string, args ...any) finding {
	return finding{about: about, Diagnostic: Diagnostic{Level: Notice, Path: path, Doc: doc, Reason: fmt.Sprintf(format, args...)}}
}

// cohesionNotices points out an export serving several API groups, a group split across exports, a
// WorkspaceType that binds nothing of the tree, and an export nothing binds.
func cohesionNotices(m Model, exports []Export) []finding {
	var diags []finding
	note := func(about, path string, doc int, format string, args ...any) {
		diags = append(diags, noticeAbout(about, path, doc, format, args...))
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
			note("groups of "+e.Name, e.Path, e.Doc, "APIExport %s serves %d API groups: %s", e.Name, len(own), strings.Join(own, ", "))
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
			note("exports of "+g, serving[1].Path, serving[1].Doc, "API group %s is served by %d APIExports: %s", g, len(serving), strings.Join(names, ", "))
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
			note("bindings of "+t.Name, t.Path, t.Doc, "WorkspaceType %s binds no APIExport", t.Name)
		case !inside:
			note("bindings of "+t.Name, t.Path, t.Doc, "WorkspaceType %s binds only APIExports from outside the tree: %s", t.Name, strings.Join(outside, ", "))
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
			note("binders of "+e.Name, e.Path, e.Doc, "APIExport %s is bound by no WorkspaceType or APIBinding in the tree", e.Name)
		}
	}
	return diags
}

// pairsOf lists the conflicts among one consumer's bindings once each, from the side of the export
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
