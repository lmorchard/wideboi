// Package compose turns per-pane cell surfaces into one screen buffer.
//
// Compositing is painter's algorithm: callers Blit back to front, and
// later draws cover earlier ones. That is what will let overlapping
// card layouts work later without changing this package.
package compose

import (
	"image"

	uv "github.com/charmbracelet/ultraviolet"
)

// Surface is an off-screen cell buffer that satisfies uv.Screen.
//
// Note: *uv.Buffer does NOT satisfy uv.Screen — it lacks WidthMethod().
// uv.ScreenBuffer is the type that does.
type Surface = uv.ScreenBuffer

// NewSurface returns a blank surface of the given size.
func NewSurface(cols, rows int) Surface {
	return uv.NewScreenBuffer(cols, rows)
}

// Blit draws src into dst at dest, clipping anything outside dest.
func Blit(dst uv.Screen, src Surface, dest image.Rectangle) {
	src.Draw(dst, dest)
}

// WriteString writes plain unstyled text into s starting at (x, y).
// Delegates to WriteStyled with a zero style.
//
// Caveat: one rune per cell, so combining marks get a cell of their
// own instead of joining their base rune. Width, however, is handled:
// WriteStyled advances by each cell's measured width, so a
// double-width glyph occupies two columns and what follows it lands
// where it should. See TestWriteStyledAdvancesByMeasuredWidth.
//
// An earlier version of this comment claimed width was ignored too.
// It was wrong, and docs/BEYOND-V1.md carried a defect row resting on
// it. What genuinely does not consult width is Text, below, and
// rune-counting truncation -- use TruncateWidth for any text a child
// process supplied.
func WriteString(s uv.Screen, x, y int, text string) {
	WriteStyled(s, x, y, text, uv.Style{})
}

// WriteStyled writes text into s at (x, y) with every cell carrying
// style. A zero style is exactly what an unstyled write produces, which
// is why WriteString is one of these.
//
// Advances by each cell's measured width, so wide glyphs do not push
// the rest of the line left. Shares WriteString's combining-mark
// caveat; see there.
//
// Kept as a separate function rather than a variadic option on
// WriteString because every existing call site wants the unstyled form
// and should not have to say so.
func WriteStyled(s uv.Screen, x, y int, text string, style uv.Style) {
	currX := x
	for _, r := range []rune(text) {
		// NewCell returns a fresh cell in every case -- for " " it
		// clones the package-level EmptyCell rather than handing it
		// back -- so assigning Style here is safe.
		cell := uv.NewCell(s.WidthMethod(), string(r))
		cell.Style = style
		s.SetCell(currX, y, cell)
		w := 1
		if cell.Width > 1 {
			w = cell.Width
		}
		currX += w
	}
}

// TruncateWidth returns the longest prefix of text whose total display
// width fits budget, measured with s's width method.
//
// Distinct from counting runes: a double-width glyph occupies two
// cells, so an N-rune prefix can be up to 2N cells and overflow
// whatever it was budgeted against. Chrome that renders a title its
// child process chose cannot assume single-width input.
//
// A glyph that would straddle the budget is dropped rather than half
// drawn -- there is no half cell, and its continuation would land
// under the next thing drawn.
func TruncateWidth(s uv.Screen, text string, budget int) string {
	if budget <= 0 || text == "" {
		return ""
	}
	used := 0
	for i, r := range text {
		w := uv.NewCell(s.WidthMethod(), string(r)).Width
		if w < 0 {
			w = 0
		}
		if used+w > budget {
			return text[:i]
		}
		used += w
	}
	return text
}

// Text renders a screen region to plain strings, for tests and snapshots.
//
// Caveat: the mirror image of WriteString's. It emits exactly one rune
// per cell — the first rune of each cell's content — so a wide glyph
// reads as its base rune followed by whatever the continuation cell
// holds, and combining marks are dropped. Adequate for the ASCII
// snapshots it exists to serve; not a faithful rendering.
func Text(s uv.Screen, area image.Rectangle) []string {
	lines := make([]string, 0, area.Dy())
	for y := area.Min.Y; y < area.Max.Y; y++ {
		row := make([]rune, 0, area.Dx())
		for x := area.Min.X; x < area.Max.X; x++ {
			c := s.CellAt(x, y)
			if c == nil || c.Content == "" {
				row = append(row, ' ')
				continue
			}
			row = append(row, []rune(c.Content)[0])
		}
		lines = append(lines, string(row))
	}
	return lines
}
