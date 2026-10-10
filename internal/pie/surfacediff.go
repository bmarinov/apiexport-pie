package pie

import (
	"slices"
	"strings"
)

// A diff of two surfaces marks what only one of them has: cyan for B, grape for A. The violet write
// tint of a single surface is left out of a diff, so purple means one thing there.
const (
	surfaceAddedFill   = "#c5f6fa"
	surfaceRemovedFill = "#eebefa"
	// lost marks a use case that no longer gets a row; a hollow dot tells it apart without colour.
	lost = "○"
)

// RenderSurfaceDiffD2 draws the surfaces of a and b as one d2 table: the columns, exports and rows
// of both, those only in b in cyan, those only in a in grape. A dot is where b binds the row's
// export, +● when the use case did not get the row before. ○ marks a use case that had the row and
// gets it from no export now. A kind is followed by its resource, so one that moved to another export
// shows in both and says where it went, and a use case that gets it through either export keeps its ●.
func RenderSurfaceDiffD2(a, b Model) []byte {
	return renderSurfaceD2(surfaceDiffOf(surfaceOf(a), surfaceOf(b)))
}

func surfaceDiffOf(before, after surface) surface {
	var d surface
	for _, c := range after.columns {
		if !slices.ContainsFunc(before.columns, func(o surfaceColumn) bool { return o.name == c.name }) {
			c.change = added
		}
		d.columns = append(d.columns, c)
	}
	for _, c := range before.columns {
		if !slices.ContainsFunc(after.columns, func(o surfaceColumn) bool { return o.name == c.name }) {
			c.change = removed
			d.columns = append(d.columns, c)
		}
	}

	// A row's dots are its own export's, so a row only a has has none; whether a use case had or still
	// has a kind counts any export.
	cellsFor := func(export string, r surfaceRow) ([][]string, []change) {
		cells := make([][]string, len(d.columns))
		changes := make([]change, len(d.columns))
		for i, c := range d.columns {
			is := cellOf(after, export, r, c.name, false)
			was := cellOf(before, export, r, c.name, r.kind && r.change != removed)
			gone := was != nil && cellOf(after, export, r, c.name, r.kind) == nil
			switch {
			case is != nil && was != nil:
				cells[i] = is
			case is != nil:
				cells[i], changes[i] = append([]string{"+" + bound}, is[1:]...), added
			case gone:
				cells[i], changes[i] = []string{lost}, removed
			}
		}
		return cells, changes
	}
	block := func(export string, now, then *surfaceBlock) surfaceBlock {
		bl := surfaceBlock{export: export}
		switch {
		case then == nil:
			bl.change = added
		case now == nil:
			bl.change = removed
		}
		var kinds, others []surfaceRow
		place := func(r surfaceRow) {
			r.writes = false
			if r.kind {
				kinds = append(kinds, r)
				return
			}
			others = append(others, r)
		}
		if now != nil {
			for _, r := range now.rows {
				if !hasRow(then, r) {
					r.change = added
					if from := without(servedBy(before, r.key), servedBy(after, r.key)); r.kind && len(from) > 0 {
						r.lines = append([]string{r.lines[0] + " · moved from " + strings.Join(from, " and ")}, r.lines[1:]...)
					}
				}
				r.cells, r.cellChanges = cellsFor(export, r)
				place(r)
			}
		}
		if then != nil {
			for _, r := range then.rows {
				if hasRow(now, r) {
					continue
				}
				r.change = removed
				r.cells, r.cellChanges = cellsFor(export, r)
				if to := without(servedBy(after, r.key), servedBy(before, r.key)); r.kind && len(to) > 0 {
					r.lines = append([]string{r.lines[0] + " · moved to " + strings.Join(to, " and ")}, r.lines[1:]...)
					r.cells, r.cellChanges = make([][]string, len(d.columns)), make([]change, len(d.columns))
				}
				place(r)
			}
		}
		bl.rows = append(kinds, others...)
		return bl
	}

	for i := range after.blocks {
		d.blocks = append(d.blocks, block(after.blocks[i].export, &after.blocks[i], blockOf(before, after.blocks[i].export)))
	}
	for i := range before.blocks {
		if blockOf(after, before.blocks[i].export) == nil {
			d.blocks = append(d.blocks, block(before.blocks[i].export, nil, &before.blocks[i]))
		}
	}
	return d
}

func blockOf(s surface, export string) *surfaceBlock {
	for i := range s.blocks {
		if s.blocks[i].export == export {
			return &s.blocks[i]
		}
	}
	return nil
}

func hasRow(bl *surfaceBlock, r surfaceRow) bool {
	return bl != nil && slices.ContainsFunc(bl.rows, func(o surfaceRow) bool { return o.key == r.key && o.kind == r.kind })
}

// servedBy lists the exports of a surface that have a kind's row.
func servedBy(s surface, key string) []string {
	var exports []string
	for _, bl := range s.blocks {
		if slices.ContainsFunc(bl.rows, func(r surfaceRow) bool { return r.kind && r.key == key }) {
			exports = append(exports, bl.export)
		}
	}
	return exports
}

// cellOf is what a use case shows for a row in a surface, nil unless it gets the row through the
// row's own export or, with anywhere, through any export that has the row.
func cellOf(s surface, export string, r surfaceRow, column string, anywhere bool) []string {
	col := slices.IndexFunc(s.columns, func(c surfaceColumn) bool { return c.name == column })
	if col < 0 {
		return nil
	}
	for _, bl := range s.blocks {
		if !anywhere && bl.export != export {
			continue
		}
		for _, o := range bl.rows {
			if cell := o.cells[col]; o.key == r.key && o.kind == r.kind && len(cell) > 0 && cell[0] == bound {
				return cell
			}
		}
	}
	return nil
}
