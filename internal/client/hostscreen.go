package client

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
)

// HostScreen is everything Draw needs from the host terminal: a cell
// surface, plus cursor control.
type HostScreen interface {
	uv.Screen
	HideCursor()
	ShowCursor()
	SetCursorPosition(x, y int)
}

type offscreenHostScreen struct {
	compose.Surface
	cursorShown bool
	cursorX     int
	cursorY     int
}

func newOffscreenHostScreen(cols, rows int) *offscreenHostScreen {
	return &offscreenHostScreen{
		Surface: compose.NewSurface(cols, rows),
	}
}

func (s *offscreenHostScreen) HideCursor()                { s.cursorShown = false }
func (s *offscreenHostScreen) ShowCursor()                { s.cursorShown = true }
func (s *offscreenHostScreen) SetCursorPosition(x, y int) { s.cursorX, s.cursorY = x, y }

func (s *offscreenHostScreen) clear() {
	s.Surface.Clear()
	s.cursorShown = false
	s.cursorX = 0
	s.cursorY = 0
}

func (s *offscreenHostScreen) equal(other *offscreenHostScreen) bool {
	if s == nil || other == nil {
		return false
	}
	if s.cursorShown != other.cursorShown {
		return false
	}
	if s.cursorShown && (s.cursorX != other.cursorX || s.cursorY != other.cursorY) {
		return false
	}
	b1 := s.Bounds()
	b2 := other.Bounds()
	if b1 != b2 {
		return false
	}
	w, h := b1.Dx(), b1.Dy()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c1 := s.CellAt(x, y)
			c2 := other.CellAt(x, y)
			if c1 == c2 {
				continue
			}
			if c1 == nil || c2 == nil {
				return false
			}
			if !c1.Equal(c2) {
				return false
			}
		}
	}
	return true
}

func copyToHostScreen(src *offscreenHostScreen, dst HostScreen) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	for y := 0; y < h; y++ {
		for x := 0; x < w; {
			c := src.CellAt(x, y)
			if c == nil {
				x++
				continue
			}
			dst.SetCell(x, y, c)
			width := c.Width
			if width <= 0 {
				width = 1
			}
			x += width
		}
	}
	if src.cursorShown {
		dst.SetCursorPosition(src.cursorX, src.cursorY)
		dst.ShowCursor()
	} else {
		dst.HideCursor()
	}
}

// screenRowText returns the character contents of row y on scr.
func screenRowText(scr uv.Screen, y, width int) string {
	var b strings.Builder
	for x := 0; x < width; x++ {
		cell := scr.CellAt(x, y)
		if cell == nil || cell.Content == "" {
			b.WriteString(" ")
		} else {
			b.WriteString(cell.Content)
		}
	}
	return b.String()
}
