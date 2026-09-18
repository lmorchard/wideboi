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
func WriteString(s uv.Screen, x, y int, text string) {
	for i, r := range []rune(text) {
		s.SetCell(x+i, y, uv.NewCell(s.WidthMethod(), string(r)))
	}
}

// Text renders a screen region to plain strings, for tests and snapshots.
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
