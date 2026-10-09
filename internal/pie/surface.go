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

// surface is the table both renderings draw: a column per use case, and a block of rows per export.
type surface struct {
	columns [][]string
	blocks  []surfaceBlock
}

// surfaceBlock is an export: its name, then a row per kind it serves and a row per set of claims.
type surfaceBlock struct {
	export string
	rows   []surfaceRow
}

// surfaceRow is a kind or a set of claims, in one or more lines, with a cell per use case: bound or
// empty, and on a claims row a line saying which of them a binding file does not grant. writes marks
// claims with a verb beyond reading.
type surfaceRow struct {
	lines  []string
	cells  [][]string
	writes bool
}

// surfaceOf lays out which use case gets which kinds and grants which claims. A use case that binds
// an export gets all of it, so each export reads as one block, the bundle a workspace takes or leaves.
func surfaceOf(m Model) surface {
	// useCase is a column. withheld says, per export, the claims a binding file does not accept;
	// kcp accepts every claim of a WorkspaceType's bindings, so those columns have none.
	type useCase struct {
		exports  []string
		withheld map[string][]GroupResource
	}
	var s surface
	var cases []useCase
	exportsOf := func(bindings []binding) []string {
		var names []string
		for _, bd := range bindings {
			names = append(names, bd.ref.Name)
		}
		return names
	}
	for _, t := range m.WorkspaceTypes {
		s.columns = append(s.columns, []string{t.Name})
		cases = append(cases, useCase{exports: exportsOf(bindingsOf(t, m.WorkspaceTypes))})
	}
	for _, dir := range bindingsByDir(m.APIBindings) {
		withheld := map[string][]GroupResource{}
		for _, bd := range dir.bound {
			named := exportsNamed(bd.ref.Name, m.Exports)
			if len(named) == 0 {
				continue
			}
			accepts := firstBindingOf(bd.ref.Name, dir.dir, m.APIBindings).Accepts
			for _, c := range named[0].Claims {
				if gr := (GroupResource{Group: c.Group, Resource: c.Resource}); !slices.Contains(accepts, gr) {
					withheld[bd.ref.Name] = append(withheld[bd.ref.Name], gr)
				}
			}
		}
		s.columns = append(s.columns, append([]string{"APIBindings in"}, strings.SplitAfter(dir.dir, "/")...))
		cases = append(cases, useCase{exports: exportsOf(dir.bound), withheld: withheld})
	}

	cellsOf := func(export string, claims []Claim) [][]string {
		cells := make([][]string, 0, len(cases))
		for _, c := range cases {
			var cell, refused []string
			if slices.Contains(c.exports, export) {
				cell = append(cell, bound)
			}
			for _, claim := range claims {
				if slices.Contains(c.withheld[export], GroupResource{Group: claim.Group, Resource: claim.Resource}) {
					refused = append(refused, claim.Resource)
				}
			}
			if len(refused) > 0 {
				cell = append(cell, "not "+strings.Join(refused, ", "))
			}
			cells = append(cells, cell)
		}
		return cells
	}
	serving := exportsServing(m.Exports)
	block := func(export string, kinds []string, claims []Claim) {
		bl := surfaceBlock{export: export}
		for _, kind := range kinds {
			bl.rows = append(bl.rows, surfaceRow{lines: []string{kind}, cells: cellsOf(export, nil)})
		}
		for _, set := range claimSets(claims) {
			bl.rows = append(bl.rows, surfaceRow{lines: set.grants(serving), cells: cellsOf(export, set.claims), writes: set.writes()})
		}
		s.blocks = append(s.blocks, bl)
	}

	// Copies of an export share its name and draw once, from the first. Exports are commonly named
	// after their API group, so a kind names its group only when it is another one.
	var drawn []string
	for _, e := range m.Exports {
		if slices.Contains(drawn, e.Name) {
			continue
		}
		drawn = append(drawn, e.Name)
		var kinds []string
		for _, r := range rowsOf(e, m.Schemas, serving) {
			kind := r.kind
			if r.group != e.Name {
				kind += " · " + r.group
			}
			if !r.inTree {
				kind += " · not in tree"
			}
			kinds = append(kinds, kind)
		}
		if len(kinds) == 0 {
			kinds = []string{"(no kinds)"}
		}
		block(e.Name, kinds, e.Claims)
	}
	for _, c := range cases {
		for _, name := range c.exports {
			if !slices.Contains(drawn, name) {
				drawn = append(drawn, name)
				block(name, []string{"(outside the tree)"}, nil)
			}
		}
	}
	return s
}

// RenderSurface draws which use case gets which kinds as a markdown table for docs: a row per kind
// and per set of claims, grouped by the export, and a column per use case.
func RenderSurface(m Model) []byte {
	s := surfaceOf(m)
	var b bytes.Buffer
	header := []string{"APIExport", "kind"}
	rule := []string{"---", "---"}
	for _, lines := range s.columns {
		header = append(header, strings.Join(lines, "<br/>"))
		rule = append(rule, ":-:")
	}
	b.WriteString("| " + strings.Join(header, " | ") + " |\n")
	b.WriteString("|" + strings.Join(rule, "|") + "|\n")
	for _, bl := range s.blocks {
		for i, r := range bl.rows {
			cells := []string{"", strings.Join(r.lines, "<br/>")}
			if i == 0 {
				cells[0] = "**" + bl.export + "**"
			}
			for _, lines := range r.cells {
				cells = append(cells, strings.Join(lines, "<br/>"))
			}
			b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		}
	}
	return b.Bytes()
}

// claimSet is the claims of an export with the same verbs, one row of the surface.
type claimSet struct {
	verbs  string
	claims []Claim
}

func claimSets(claims []Claim) []claimSet {
	var sets []claimSet
	for _, c := range claims {
		verbs := strings.Join(c.Verbs, ", ")
		i := slices.IndexFunc(sets, func(s claimSet) bool { return s.verbs == verbs })
		if i < 0 {
			sets = append(sets, claimSet{verbs: verbs})
			i = len(sets) - 1
		}
		sets[i].claims = append(sets[i].claims, c)
	}
	return sets
}

// writes says whether the claims let the provider change anything: a verb other than get, list, watch.
func (s claimSet) writes() bool {
	for _, verb := range strings.Split(s.verbs, ", ") {
		if !slices.Contains([]string{"get", "list", "watch"}, verb) {
			return true
		}
	}
	return false
}

// grantsWidth keeps a claims row narrow enough to sit beside the kinds. Longer ones break between
// their words.
const grantsWidth = 60

// grants tells what a binding lets the export's provider do: "grants: get, create on selected secrets".
// A resource that exports in the tree serve names them, since kcp ties such a claim to one of them.
func (s claimSet) grants(serving map[string][]string) []string {
	resources := make([]string, 0, len(s.claims))
	for _, c := range s.claims {
		resource := c.Resource
		if c.Selected {
			resource = "selected " + resource
		}
		if from := serving[resourceName(Resource{Group: c.Group, Name: c.Resource})]; len(from) > 0 {
			resource += " from " + strings.Join(from, " or ")
		}
		resources = append(resources, resource)
	}
	return wrapWords("grants: "+s.verbs+" on "+strings.Join(resources, ", "), grantsWidth)
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

// The surface in d2 is a grid of cells, which d2 sizes to their text. A cell of one line gets a fixed
// height, since d2 would draw it about twice as tall; a row still grows to its tallest cell. Every
// other export is shaded so each reads as one block.
const (
	surfaceFontSize  = 14
	surfaceRowHeight = 34
	surfaceHeadFill  = "#e7ebf7"
	surfaceShadeFill = "#f4f6fb"
	// Claims that can write are violet: red and orange are kcp's verdicts, the blues are layout.
	surfaceWriteFill = "#e5dbff"
	// A claims row that wraps is a cell of one-line children, since d2 centres every line of a label.
	surfaceLineHeight = 20
)

// RenderSurfaceD2 draws the surface table as d2, for d2 to turn into SVG or PNG.
func RenderSurfaceD2(m Model) []byte {
	s := surfaceOf(m)
	var b bytes.Buffer
	rows := 1
	for _, bl := range s.blocks {
		rows += len(bl.rows)
	}
	fmt.Fprintf(&b, "grid-rows: %d\ngrid-columns: %d\ngrid-gap: 0\n", rows, 2+len(s.columns))
	fmt.Fprintf(&b, "*.style.font-size: %d\n*.style.bold: false\n*.style.fill: \"#ffffff\"\n", surfaceFontSize)

	const left = "label.near: center-left"
	r := 0
	cell := func(c int, lines []string, style ...string) {
		if len(lines) <= 1 {
			style = append(style, fmt.Sprintf("height: %d", surfaceRowHeight))
		}
		fmt.Fprintf(&b, "%q: %q", fmt.Sprintf("r%dc%d", r, c), strings.Join(lines, "\n"))
		if len(style) > 0 {
			b.WriteString(" {" + strings.Join(style, "; ") + "}")
		}
		b.WriteString("\n")
	}

	head := []string{"style.bold: true", fmt.Sprintf("style.fill: %q", surfaceHeadFill)}
	cell(0, []string{"APIExport"}, slices.Concat(head, []string{left})...)
	cell(1, []string{"kind"}, slices.Concat(head, []string{left})...)
	for c, lines := range s.columns {
		cell(2+c, lines, head...)
	}
	tinted := false
	for i, bl := range s.blocks {
		var shade []string
		if i%2 == 1 {
			shade = []string{fmt.Sprintf("style.fill: %q", surfaceShadeFill)}
		}
		for j, row := range bl.rows {
			r++
			fill := shade
			if row.writes {
				fill = []string{fmt.Sprintf("style.fill: %q", surfaceWriteFill)}
				tinted = true
			}
			// d2's PNG export draws bold text wider than d2 measured it, so a name on the left would
			// lose its first letter; centred, it spills evenly into the cell's padding.
			if j == 0 {
				cell(0, []string{bl.export}, slices.Concat(shade, []string{"style.bold: true"})...)
			} else {
				cell(0, nil, shade...)
			}
			if len(row.lines) <= 1 {
				cell(1, row.lines, slices.Concat(fill, []string{left})...)
			} else {
				fmt.Fprintf(&b, "%q: \"\" {\n", fmt.Sprintf("r%dc1", r))
				for _, style := range fill {
					b.WriteString("  " + style + "\n")
				}
				b.WriteString("  grid-columns: 1\n  grid-gap: 0\n")
				for k, line := range row.lines {
					fmt.Fprintf(&b, "  %q: %q {style.stroke: transparent; style.fill: transparent; style.font-size: %d; style.bold: false; %s; height: %d}\n",
						fmt.Sprintf("l%d", k), line, surfaceFontSize, left, surfaceLineHeight)
				}
				b.WriteString("}\n")
			}
			for c, lines := range row.cells {
				cell(2+c, lines, shade...)
			}
		}
	}
	if tinted {
		fmt.Fprintf(&b, "\"legend\": \"grants that can write: a verb other than get, list, watch\" {near: bottom-center; style.fill: %q; height: %d}\n",
			surfaceWriteFill, surfaceRowHeight)
	}
	return b.Bytes()
}
