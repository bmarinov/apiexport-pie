package pie

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// bound marks a use case that binds an export in the surface table.
const bound = "●"

// RenderSurface draws which use case gets which kinds, as a markdown table for docs: a row per kind,
// grouped by the export that serves it, and a column per use case. A use case that binds an export
// gets all its kinds, so each export reads as one block, the bundle a workspace takes or leaves.
func RenderSurface(m Model) []byte {
	// useCase is a column. withheld says, per export, the claims a binding file does not accept;
	// kcp accepts every claim of a WorkspaceType's bindings, so those columns have none.
	type useCase struct {
		name     string
		exports  []string
		withheld map[string][]string
	}
	var cases []useCase
	exportsOf := func(bindings []binding) []string {
		var names []string
		for _, bd := range bindings {
			names = append(names, bd.ref.Name)
		}
		return names
	}
	for _, t := range m.WorkspaceTypes {
		cases = append(cases, useCase{name: t.Name, exports: exportsOf(bindingsOf(t, m.WorkspaceTypes))})
	}
	for _, dir := range bindingsByDir(m.APIBindings) {
		withheld := map[string][]string{}
		for _, bd := range dir.bound {
			named := exportsNamed(bd.ref.Name, m.Exports)
			if len(named) == 0 {
				continue
			}
			accepts := firstBindingOf(bd.ref.Name, dir.dir, m.APIBindings).Accepts
			for _, c := range named[0].Claims {
				if !slices.Contains(accepts, GroupResource{Group: c.Group, Resource: c.Resource}) {
					withheld[bd.ref.Name] = append(withheld[bd.ref.Name], c.Resource)
				}
			}
		}
		cases = append(cases, useCase{name: "APIBindings in<br/>" + strings.ReplaceAll(dir.dir, "/", "/<br/>"), exports: exportsOf(dir.bound), withheld: withheld})
	}

	var b bytes.Buffer
	header := []string{"APIExport", "kind"}
	rule := []string{"---", "---"}
	for _, c := range cases {
		header = append(header, c.name)
		rule = append(rule, ":-:")
	}
	b.WriteString("| " + strings.Join(header, " | ") + " |\n")
	b.WriteString("|" + strings.Join(rule, "|") + "|\n")

	// row draws one kind. The first row of an export names it and says what binding it grants; a
	// binding file that grants less says so in its cell on that row.
	row := func(export, kind string, claims []Claim, first bool) {
		cells := []string{"", kind}
		if first {
			cells[0] = "**" + export + "**"
			if len(claims) > 0 {
				cells[0] += "<br/>" + grants(claims)
			}
		}
		for _, c := range cases {
			cell := ""
			if slices.Contains(c.exports, export) {
				cell = bound
			}
			if withheld := c.withheld[export]; first && len(withheld) > 0 {
				cell += "<br/>not " + strings.Join(withheld, ", ")
			}
			cells = append(cells, cell)
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}

	// Copies of an export share its name and draw once, from the first.
	var drawn []string
	serving := exportsServing(m.Exports)
	for _, e := range m.Exports {
		if slices.Contains(drawn, e.Name) {
			continue
		}
		drawn = append(drawn, e.Name)
		rows := rowsOf(e, m.Schemas, serving)
		if len(rows) == 0 {
			row(e.Name, "(no kinds)", e.Claims, true)
		}
		for i, r := range rows {
			row(e.Name, r.kind, e.Claims, i == 0)
		}
	}
	for _, c := range cases {
		for _, name := range c.exports {
			if !slices.Contains(drawn, name) {
				drawn = append(drawn, name)
				row(name, "(outside the tree)", nil, true)
			}
		}
	}
	return b.Bytes()
}

// grantsWidth keeps the grants under an export narrow enough for its column. Longer ones break at
// their verb groups, and a group still too long between its words.
const grantsWidth = 60

// grants tells what a binding lets the export's provider do, claims with the same verbs together:
// "grants: * on workspaces, apibindings; get, create on selected secrets".
func grants(claims []Claim) string {
	var verbSets []string
	on := map[string][]string{}
	for _, c := range claims {
		verbs := strings.Join(c.Verbs, ", ")
		if _, ok := on[verbs]; !ok {
			verbSets = append(verbSets, verbs)
		}
		resource := c.Resource
		if c.Selected {
			resource = "selected " + resource
		}
		on[verbs] = append(on[verbs], resource)
	}
	parts := make([]string, 0, len(verbSets))
	for _, verbs := range verbSets {
		parts = append(parts, verbs+" on "+strings.Join(on[verbs], ", "))
	}
	if whole := "grants: " + strings.Join(parts, "; "); utf8.RuneCountInString(whole) <= grantsWidth {
		return whole
	}
	var lines []string
	for i, part := range parts {
		if i == 0 {
			part = "grants: " + part
		}
		if i < len(parts)-1 {
			part += ";"
		}
		lines = append(lines, wrapWords(part, grantsWidth)...)
	}
	return strings.Join(lines, "<br/>")
}

func wrapWords(text string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) > width {
			lines = append(lines, line)
			line = word
			continue
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	return append(lines, line)
}

// d2 v0.6.9 measures markdown tables short and clips their last rows, so RenderSurfaceD2 sets the
// box's height from an overestimate: a line per table row, one more per <br/> and per
// surfaceWrapWidth characters. Too much only leaves blank space under the table.
const (
	surfaceLineHeight = 32
	surfaceMargin     = 40
	surfaceWrapWidth  = 150
)

// RenderSurfaceD2 draws the surface table as d2, for d2 to turn into SVG or PNG.
func RenderSurfaceD2(m Model) []byte {
	var b bytes.Buffer
	b.WriteString("\"surface\": |`md\n")
	lines := 0
	for _, line := range strings.Split(strings.TrimSuffix(string(RenderSurface(m)), "\n"), "\n") {
		b.WriteString("  " + line + "\n")
		if strings.HasPrefix(line, "|---") {
			continue
		}
		lines += 1 + strings.Count(line, "<br/>") + utf8.RuneCountInString(line)/surfaceWrapWidth
	}
	b.WriteString("`|\n")
	fmt.Fprintf(&b, "\"surface\".height: %d\n", surfaceMargin+surfaceLineHeight*lines)
	return b.Bytes()
}
