package protocol

import "slices"

// BuildPanePatch compares complete rendered rows, independent of the
// emulator's Touched flags. It uses a patch only when fewer than half
// the rows changed; scrolling and resize fall back to a full snapshot.
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
	}
	for y := range next.Lines {
		if len(base.Lines[y]) != base.Cols || len(next.Lines[y]) != next.Cols {
			return MsgPanePatch{}, false
		}
		if !slices.Equal(base.Lines[y], next.Lines[y]) {
			patch.ChangedRows = append(patch.ChangedRows, PaneRow{Y: y, Cells: next.Lines[y]})
			if len(patch.ChangedRows)*2 >= next.Rows {
				return MsgPanePatch{}, false
			}
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
		len(base.Lines) != base.Rows || patch.Generation <= patch.BaseGeneration {
		return MsgPaneUpdate{}, false
	}
	next := base
	next.Lines = slices.Clone(base.Lines)
	seen := make([]bool, base.Rows)
	for _, row := range patch.ChangedRows {
		if row.Y < 0 || row.Y >= base.Rows || seen[row.Y] || len(row.Cells) != base.Cols {
			return MsgPaneUpdate{}, false
		}
		seen[row.Y] = true
		next.Lines[row.Y] = row.Cells
	}
	next.Generation = patch.Generation
	next.CursorX, next.CursorY = patch.CursorX, patch.CursorY
	next.CursorVisible, next.MouseTracking = patch.CursorVisible, patch.MouseTracking
	return next, true
}
