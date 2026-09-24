package protocol

import "slices"

// BuildPanePatch compares complete rendered rows, independent of the
// emulator's Touched flags. A shift is used only when every retained row
// matches exactly and the shift is unambiguous.
func BuildPanePatch(base, next MsgPaneUpdate) (MsgPanePatch, bool) {
	if base.PaneID != next.PaneID || base.Cols != next.Cols || base.Rows != next.Rows ||
		len(base.Lines) != base.Rows || len(next.Lines) != next.Rows || next.Generation <= base.Generation {
		return MsgPanePatch{}, false
	}
	patch := MsgPanePatch{
		PaneID: next.PaneID, Cols: next.Cols, Rows: next.Rows,
		BaseGeneration: base.Generation, Generation: next.Generation,
		CursorX: next.CursorX, CursorY: next.CursorY,
		CursorVisible: next.CursorVisible, MouseTracking: next.MouseTracking,
		ScrollOffset: next.ScrollOffset, ScrollbackLen: next.ScrollbackLen,
		UnreadOutput: next.UnreadOutput,
	}
	for y := range next.Lines {
		if len(base.Lines[y]) != base.Cols || len(next.Lines[y]) != next.Cols {
			return MsgPanePatch{}, false
		}
		if !slices.Equal(base.Lines[y], next.Lines[y]) {
			patch.ChangedRows = append(patch.ChangedRows, PaneRow{Y: y, Cells: next.Lines[y]})
		}
	}
	if len(patch.ChangedRows)*2 < next.Rows {
		return patch, true
	}
	// Terminal output commonly fills the bottom row after the screen moves
	// up, so the replacement band may be wider than the shift itself. The
	// unaffected interior must match exactly. Pick the smallest replacement
	// band; equal-cost matches are ambiguous (often a blank screen).
	var shift, replaced int
	ambiguous := false
	for magnitude := 1; magnitude <= next.Rows/2; magnitude++ {
		for _, candidate := range []int{-magnitude, magnitude} {
			for band := magnitude; band <= next.Rows/2; band++ {
				matches := true
				for y := 0; y < next.Rows; y++ {
					if candidate < 0 && y >= next.Rows-band || candidate > 0 && y < band {
						continue
					}
					if !slices.Equal(base.Lines[y-candidate], next.Lines[y]) {
						matches = false
						break
					}
				}
				if matches {
					if replaced == 0 || band < replaced {
						shift, replaced = candidate, band
						ambiguous = false
					} else if band == replaced && shift != candidate {
						ambiguous = true
					}
					break
				}
			}
		}
	}
	if shift == 0 || ambiguous {
		return MsgPanePatch{}, false
	}
	patch.ShiftRows = shift
	patch.ChangedRows = nil
	for y := 0; y < next.Rows; y++ {
		if shift < 0 && y >= next.Rows-replaced || shift > 0 && y < replaced {
			patch.ChangedRows = append(patch.ChangedRows, PaneRow{Y: y, Cells: next.Lines[y]})
		}
	}
	return patch, true
}

// ApplyPanePatch returns a new immutable full snapshot when the baseline
// and every replacement row are valid. On mismatch the caller must ask
// for a full snapshot rather than displaying a partly patched pane.
func ApplyPanePatch(base MsgPaneUpdate, patch MsgPanePatch) (MsgPaneUpdate, bool) {
	if base.PaneID != patch.PaneID || base.Generation != patch.BaseGeneration ||
		base.Cols != patch.Cols || base.Rows != patch.Rows ||
		len(base.Lines) != base.Rows || patch.Generation <= patch.BaseGeneration ||
		patch.ShiftRows <= -base.Rows || patch.ShiftRows >= base.Rows {
		return MsgPaneUpdate{}, false
	}
	if patch.ShiftRows != 0 {
		band := len(patch.ChangedRows)
		magnitude := patch.ShiftRows
		if magnitude < 0 {
			magnitude = -magnitude
		}
		if band < magnitude || band > base.Rows/2 {
			return MsgPaneUpdate{}, false
		}
		seen := make([]bool, band)
		for _, row := range patch.ChangedRows {
			index := row.Y
			if patch.ShiftRows < 0 {
				index -= base.Rows - band
			}
			if index < 0 || index >= band || seen[index] {
				return MsgPaneUpdate{}, false
			}
			seen[index] = true
		}
	}
	next := base
	next.Lines = make([]LineData, base.Rows)
	for y := range next.Lines {
		source := y - patch.ShiftRows
		if source >= 0 && source < base.Rows {
			next.Lines[y] = base.Lines[source]
		}
	}
	seen := make([]bool, base.Rows)
	for _, row := range patch.ChangedRows {
		if row.Y < 0 || row.Y >= base.Rows || seen[row.Y] || len(row.Cells) != base.Cols {
			return MsgPaneUpdate{}, false
		}
		seen[row.Y] = true
		next.Lines[row.Y] = row.Cells
	}
	for _, row := range next.Lines {
		if len(row) != base.Cols {
			return MsgPaneUpdate{}, false
		}
	}
	next.Generation = patch.Generation
	next.CursorX, next.CursorY = patch.CursorX, patch.CursorY
	next.CursorVisible, next.MouseTracking = patch.CursorVisible, patch.MouseTracking
	next.ScrollOffset = patch.ScrollOffset
	next.ScrollbackLen = patch.ScrollbackLen
	next.UnreadOutput = patch.UnreadOutput
	return next, true
}
