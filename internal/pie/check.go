package pie

import (
	"fmt"
	"path"
	"slices"
)

// Check warns about the exports a use case binds that kcp refuses together, of which a workspace
// gets only one. RenderD2 marks the same conflicts in red.
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
