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
//
// Caveat: one rune per cell. It does not consult Cell.Width, so it is
// correct only for single-width glyphs. A double-width rune is written
// into one cell and everything after it on the line lands one column
// left of where it should. Combining marks get a cell of their own
// instead of joining the base rune.
//
// This matters sooner than it looks: WriteString is what a reader will
// reach for when the spec's status glyphs (`»` working, `!` needs
// input, `✓` done, `✗` failed) land. Widen this to grapheme clusters
// with their measured width — uv.Cell already carries Width, and
// Surface exposes WidthMethod — before using it for anything but ASCII
// chrome.
func WriteString(s uv.Screen, x, y int, text string) {
	currX := x
	for _, r := range []rune(text) {
		cell := uv.NewCell(s.WidthMethod(), string(r))
		s.SetCell(currX, y, cell)
		w := 1
		if cell.Width > 1 {
			w = cell.Width
		}
		currX += w
	}
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
