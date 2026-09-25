package protocol

import (
	"encoding/json"
	"testing"
)

// TestPaneStatusText pins the names status --json emits: every status
// round-trips through its text form, and an unknown name is an error
// rather than a silent idle.
func TestPaneStatusText(t *testing.T) {
	want := map[PaneStatus]string{
		StatusIdle:       "idle",
		StatusWorking:    "working",
		StatusNeedsInput: "needs_input",
		StatusDone:       "done",
		StatusFailed:     "failed",
	}
	for s, name := range want {
		b, err := s.MarshalText()
		if err != nil || string(b) != name {
			t.Errorf("%d.MarshalText() = %q, %v; want %q", int(s), b, err, name)
		}
		var back PaneStatus
		if err := back.UnmarshalText([]byte(name)); err != nil || back != s {
			t.Errorf("UnmarshalText(%q) = %d, %v; want %d", name, int(back), err, int(s))
		}
	}

	out, err := json.Marshal(map[int]PaneStatus{1: StatusNeedsInput})
	if err != nil || string(out) != `{"1":"needs_input"}` {
		t.Errorf("json.Marshal(map) = %s, %v; want {\"1\":\"needs_input\"}", out, err)
	}

	var bad PaneStatus
	if err := bad.UnmarshalText([]byte("sleepy")); err == nil {
		t.Error(`UnmarshalText("sleepy") = nil error, want an error`)
	}
}

// TestColumnDataJSONKeys pins snake_case keys, matching the rest of
// status --json.
func TestColumnDataJSONKeys(t *testing.T) {
	out, err := json.Marshal(ColumnData{PaneID: 1, Width: 80, Height: 24})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"pane_id":1,"width":80,"height":24}`; string(out) != want {
		t.Errorf("json.Marshal(ColumnData) = %s, want %s", out, want)
	}
}
