package client

import (
	"testing"

	"github.com/lmorchard/wideboi/internal/transport"
)

func TestPaletteInputNavigationAndFiltering(t *testing.T) {
	tp := transport.NewInProcChannel(32)
	c := NewClient(tp, 80, 24, "C-b")

	if c.InPalette() {
		t.Fatal("expected not InPalette initially")
	}

	c.StartPalette()
	if !c.InPalette() {
		t.Fatal("expected InPalette after StartPalette")
	}

	c.PaletteEdit("split", false)
	c.mu.Lock()
	matches := c.palette.matches
	c.mu.Unlock()

	found := false
	for _, m := range matches {
		if m.Name == "split" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected 'split' command in matches")
	}

	c.PaletteNavigate(1)
	c.PaletteNavigate(-1)

	c.PaletteCancel()
	if c.InPalette() {
		t.Fatal("expected not InPalette after PaletteCancel")
	}
}
