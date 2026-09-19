package term

import uv "github.com/charmbracelet/ultraviolet"

// Row is one row of screen cells. A nil entry is an empty cell.
type Row []*uv.Cell

// usedWidth returns one past the last non-empty cell, or 0 if the row is
// wholly empty. It is what distinguishes real content from the blank
// remainder of the screen, and getting it wrong evicts live content.
func usedWidth(r Row) int {
	for x := len(r) - 1; x >= 0; x-- {
		if c := r[x]; c != nil && c.Content != "" && c.Content != " " {
			return x + 1
		}
	}
	return 0
}

// Reflow rejoins soft-wrapped runs and re-splits them at newWidth.
//
// Soft wraps are not recorded anywhere in the cell data, so this uses the
// standard heuristic: a row that filled its full width, and is followed by
// a row with content, probably wrapped. A hard line break landing at
// exactly oldWidth is therefore joined with the line below it. That is
// wrong, accepted, and pinned by a test.
//
// The result always has len(rows) rows of newWidth each. Content that no
// longer fits is dropped from the TOP, as a terminal scrolls.
func Reflow(rows []Row, oldWidth, newWidth int) []Row {
	height := len(rows)
	if height == 0 || newWidth <= 0 {
		return rows
	}

	// Drop trailing blank rows: they are the unused remainder of the
	// screen, not content, and treating them as content is what pushed
	// live text off the top in the spike.
	content := height
	for content > 0 && usedWidth(rows[content-1]) == 0 {
		content--
	}

	// Rejoin wrapped runs into logical lines.
	var logical []Row
	var cur Row
	for y := 0; y < content; y++ {
		u := usedWidth(rows[y])
		cur = append(cur, rows[y][:u]...)
		continues := u == oldWidth && y+1 < content && usedWidth(rows[y+1]) > 0
		if !continues {
			logical = append(logical, cur)
			cur = nil
		}
	}
	if cur != nil {
		logical = append(logical, cur)
	}

	// Re-split at the new width, advancing by each cell's own Width so a
	// wide glyph is never severed from its placeholder slot.
	var out []Row
	for _, line := range logical {
		if len(line) == 0 {
			out = append(out, make(Row, newWidth))
			continue
		}
		row := make(Row, newWidth)
		x := 0
		for i := 0; i < len(line); {
			c := line[i]
			w := 1
			if c != nil && c.Width > 1 {
				w = c.Width
			}
			if x+w > newWidth {
				out = append(out, row)
				row = make(Row, newWidth)
				x = 0
			}
			row[x] = c
			x += w
			i += w
		}
		out = append(out, row)
	}

	// Pad or trim to the original height, dropping from the top.
	for len(out) < height {
		out = append(out, make(Row, newWidth))
	}
	if len(out) > height {
		out = out[len(out)-height:]
	}
	return out
}
