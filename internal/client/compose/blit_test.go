package compose

import (
	"image"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestBlitClipsWideGlyphs(t *testing.T) {
	dst := NewSurface(10, 1)
	src := NewSurface(10, 1)

	// Seed destination with sentinel characters to ensure they are clobbered or preserved correctly.
	WriteStyled(dst, 0, 0, "ZZZZZZZZZZ", uv.Style{Attrs: uv.AttrBold})

	bgStyle := uv.Style{Attrs: uv.AttrReverse}
	// "日" is wide. Let's put one at x=2 and one at x=6
	WriteStyled(src, 2, 0, "日", bgStyle)
	WriteStyled(src, 6, 0, "日", bgStyle)
	// src has wide glyphs at [2,3] and [6,7]

	// We blit src[3:7] to dst[0:4]
	// src: 0 1 [2 3] 4 5 [6 7] 8 9
	// The crop is [3:7].
	// src x=3 is the second half of "日". It should be omitted (blanked).
	// src x=6 is the first half of "日". Since x=7 is outside the crop (max=7, so x=6 is the last cell), it should be omitted.

	Blit(dst, src, image.Rect(3, 0, 7, 1), image.Rect(0, 0, 4, 1))

	got := strings.Join(Text(dst, image.Rect(0, 0, 10, 1)), "")
	// Expected: 0-3 should be from src (with clipped ones as spaces), 4-9 remain 'Z'
	want := "    ZZZZZZ"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}

	// Also verify styles were preserved where blanks were substituted (dst[0] maps to src[3], which is the second half of 日; dst[3] maps to src[6], which is the first half of 日)
	for _, x := range []int{0, 3} {
		c := dst.CellAt(x, 0)
		if c.Style.Attrs != uv.AttrReverse {
			t.Errorf("cell at %d lost background style, got %v", x, c.Style)
		}
	}

	// Ensure the middle blank cells didn't pick up the style unexpectedly.
	for _, x := range []int{1, 2} {
		c := dst.CellAt(x, 0)
		if c.Style.Attrs == uv.AttrReverse {
			t.Errorf("cell at %d unexpectedly has reverse style", x)
		}
	}
}
