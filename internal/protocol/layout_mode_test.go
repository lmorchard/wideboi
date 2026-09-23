package protocol

import "testing"

func TestLayoutModeString(t *testing.T) {
	for mode, want := range map[LayoutMode]string{
		LayoutScroll:   "scroll",
		LayoutCards:    "cards",
		LayoutMode(7):  "LayoutMode(7)",
		LayoutMode(-1): "LayoutMode(-1)",
	} {
		if got := mode.String(); got != want {
			t.Errorf("LayoutMode(%d).String() = %q, want %q", int(mode), got, want)
		}
	}
}
