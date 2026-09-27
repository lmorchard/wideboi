package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lmorchard/wideboi/internal/server/term"
)

// BenchmarkRestoreStateHeavy times RestoreState for a heavy session: 8
// panes of 120x40, each with a full 10,000-line styled scrollback. The
// upgrade reconnect's handshake ceiling (reconnectHandshakeCeiling in
// cmd/wideboi) rests on this number. Run it on its own:
//
//	go test ./internal/server -run '^$' -bench RestoreStateHeavy -benchtime 1x
func BenchmarkRestoreStateHeavy(b *testing.B) {
	const panes, cols, rows = 8, 120, 40
	g := term.NewVT(cols, rows)
	defer g.Close()
	for i := 0; i < 10000+rows; i++ {
		fmt.Fprintf(g, "\x1b[3%dmline %05d the quick brown fox jumps over the lazy dog 0123456789abcdefghijklmnopqrstuvwxyz\x1b[0m\r\n", i%8, i)
	}
	snap := g.(term.Snapshotter).ExportSnapshot()
	state := UpgradeState{Cols: cols, Rows: rows, Panes: map[int]UpgradePane{}}
	for id := 1; id <= panes; id++ {
		state.Panes[id] = UpgradePane{ID: id, Cols: cols, Rows: rows, GridSnap: snap}
	}
	data, err := json.Marshal(state)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(len(data))/(1<<20), "MB")

	path := filepath.Join(b.TempDir(), "state.json")
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if err := os.WriteFile(path, data, 0o600); err != nil {
			b.Fatal(err)
		}
		b.Setenv("WIDEBOI_RESTORE_STATE", path)
		s := newBareServer()
		b.StartTimer()
		if err := RestoreState(s); err != nil {
			b.Fatal(err)
		}
	}
}
