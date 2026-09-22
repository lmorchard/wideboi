package server

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// Keys and mouse events share one queue so the child sees them in the
// order the user produced them. Two channels drained by one select
// would pick at random whenever both were ready.
func TestKeysAndMouseShareOneOrderedQueue(t *testing.T) {
	p := &Pane{input: make(chan uv.Event, 8)}

	p.SendKey(uv.KeyPressEvent{Code: 'a', Text: "a"})
	p.SendMouse(uv.MouseClickEvent{X: 1, Y: 1, Button: uv.MouseLeft})
	p.SendKey(uv.KeyPressEvent{Code: 'b', Text: "b"})

	var got []string
	for len(p.input) > 0 {
		switch ev := (<-p.input).(type) {
		case uv.KeyPressEvent:
			got = append(got, ev.Text)
		case uv.MouseClickEvent:
			got = append(got, "click")
		}
	}
	if want := []string{"a", "click", "b"}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("queue order = %v, want %v", got, want)
	}
}

// A fast drag produces a flood of motion. Motion is the one event that
// is safe to lose -- the next one supersedes it -- so it stops being
// queued at half capacity, leaving room for the release that ends the
// drag. Losing that would leave the child believing the button is
// still held.
func TestMotionFloodLeavesRoomForRelease(t *testing.T) {
	p := &Pane{input: make(chan uv.Event, 8)}

	for i := 0; i < 20; i++ {
		p.SendMouse(uv.MouseMotionEvent{X: i, Y: 1, Button: uv.MouseLeft})
	}
	p.SendMouse(uv.MouseReleaseEvent{X: 19, Y: 1, Button: uv.MouseLeft})

	var sawRelease bool
	for len(p.input) > 0 {
		if _, ok := (<-p.input).(uv.MouseReleaseEvent); ok {
			sawRelease = true
		}
	}
	if !sawRelease {
		t.Error("a motion flood crowded the release out of the queue")
	}
}
