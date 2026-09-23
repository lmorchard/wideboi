package term

// ReflowLines rejoins soft-wrapped runs and re-splits them at newWidth,
// without padding or trimming to the original height.
func ReflowLines(rows []Row, oldWidth, newWidth int) []Row {
	height := len(rows)
	if height == 0 || newWidth <= 0 {
		return nil
	}

	// Rejoin wrapped runs into logical lines.
	var logical []Row
	var cur Row
	for y := 0; y < height; y++ {
		u := usedWidth(rows[y])
		cur = append(cur, rows[y][:u]...)
		continues := u == oldWidth && y+1 < height && usedWidth(rows[y+1]) > 0
		if !continues {
			logical = append(logical, cur)
			cur = nil
		}
	}
	if cur != nil {
		logical = append(logical, cur)
	}

	// Re-split at the new width
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

	return out
}
