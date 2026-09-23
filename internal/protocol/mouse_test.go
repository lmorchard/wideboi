package protocol

import (
	"image"
	"reflect"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// Every kind of mouse event must come back as the same concrete type
// it left as: vt's SendMouse tells press from release from motion by
// type assertion, so a release decoded as a press would leave a child
// believing the button is still held.
func TestMouseRoundtripsEveryKind(t *testing.T) {
	mouse := uv.Mouse{X: 7, Y: 9, Button: uv.MouseLeft, Mod: uv.ModShift}
	wheel := uv.Mouse{X: 7, Y: 9, Button: uv.MouseWheelDown}
	cases := []uv.MouseEvent{
		uv.MouseClickEvent(mouse),
		uv.MouseReleaseEvent(mouse),
		uv.MouseMotionEvent(mouse),
		uv.MouseWheelEvent(wheel),
	}
	for _, ev := range cases {
		// Screen coordinates are replaced by pane-local ones.
		local := image.Pt(2, 3)
		msg := EncodeMouse(5, ev, local)
		if int(msg.PaneId) != 5 {
			t.Errorf("%T: PaneID = %d, want 5", ev, int(msg.PaneId))
		}
		got := msg.Decode()
		if reflect.TypeOf(got) != reflect.TypeOf(ev) {
			t.Errorf("%T decoded as %T", ev, got)
			continue
		}
		want := ev.Mouse()
		want.X, want.Y = local.X, local.Y
		if got.Mouse() != want {
			t.Errorf("%T: decoded %+v, want %+v", ev, got.Mouse(), want)
		}
	}
}
