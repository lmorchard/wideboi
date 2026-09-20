package client

import (
	"fmt"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
	"github.com/lmorchard/wideboi/internal/keys"
)

// helpLines builds the overlay's contents from the binding table, so it
// cannot drift from the bindings it documents.
//
// The prefix is passed in rather than hardcoded: WIDEBOI_PREFIX exists,
// and someone who changed it is exactly the person most likely to open
// this.
func helpLines(prefixLabel string, detachable bool) []string {
	lines := []string{"control mode", ""}

	for _, b := range keys.Bindings {
		if b.NeedsDetach && !detachable {
			continue
		}
		lines = append(lines, fmt.Sprintf("%-4s  %s", b.Key, b.Long))
	}

	lines = append(lines,
		"",
		"hold ctrl to stay in control mode:",
		fmt.Sprintf("%s ctrl+k ctrl+k k  scrolls up three times", prefixLabel),
		"",
		"any key closes this",
	)
	return lines
}

// drawHelpOverlay paints a bordered box centred on scr.
//
// Every dimension is clamped before it is used. make verify-exit runs
// wideboi at 4x2, 1x1 and 0x0, and a centred-box origin is exactly the
// arithmetic that goes negative there -- a panic in this function would
// take out the teardown contract scripts/ptycheck.py asserts, in a code
// path nobody was even looking at.
//
// The box never touches the last column: ultraviolet brackets a write
// there with autowrap toggles, which splits the text across escape
// sequences on the wire. See docs/LESSONS.md.
func drawHelpOverlay(scr uv.Screen, cols, rows int, prefixLabel string, detachable bool) {
	if cols <= 0 || rows <= 0 {
		return
	}

	lines := helpLines(prefixLabel, detachable)

	inner := 0
	for _, l := range lines {
		if n := runeLen(l); n > inner {
			inner = n
		}
	}

	// Two cells of border plus one of padding on each side.
	boxW := inner + 4
	boxH := len(lines) + 2

	if maxW := cols - 1; boxW > maxW {
		boxW = maxW
	}
	if boxH > rows {
		boxH = rows
	}
	// Below this there is no room for a border and any content at all,
	// and a partial box reads as corruption rather than as help.
	if boxW < 4 || boxH < 3 {
		return
	}

	x0 := (cols - boxW) / 2
	y0 := (rows - boxH) / 2
	// The clamps above (boxW <= cols-1, boxH <= rows, and the boxW<4 /
	// boxH<3 return) already guarantee x0 and y0 land non-negative here,
	// so these two floors are not currently reachable. They stay anyway:
	// this is exactly the arithmetic that goes wrong at 1x1 the moment
	// someone loosens one of those clamps, and two reviewers have now
	// had to re-derive that before trusting it is dead code.
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}

	style := uv.Style{Attrs: uv.AttrReverse}
	contentW := boxW - 4

	top := "┌" + strings.Repeat("─", boxW-2) + "┐"
	bottom := "└" + strings.Repeat("─", boxW-2) + "┘"
	compose.WriteStyled(scr, x0, y0, top, style)
	compose.WriteStyled(scr, x0, y0+boxH-1, bottom, style)

	for i := 0; i < boxH-2; i++ {
		text := ""
		if i < len(lines) {
			text = truncateRunes(lines[i], contentW)
		}
		if pad := contentW - runeLen(text); pad > 0 {
			text += strings.Repeat(" ", pad)
		}
		compose.WriteStyled(scr, x0, y0+1+i, "│ "+text+" │", style)
	}
}
