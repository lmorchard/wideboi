package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lmorchard/wideboi/internal/server/term"
)

// BenchmarkRestoreStateHeavy times RestoreState for a heavy session: 8
// panes of 120x40, each with a full 10,000-line styled scrollback, using
// the production gzipped compact state file produced by PrepareUpgrade.
//
//	go test ./internal/server -run '^$' -bench 'RestoreStateHeavy$' -benchtime 1x
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
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(gw).Encode(state); err != nil {
		b.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		b.Fatal(err)
	}
	data := buf.Bytes()
	b.ReportMetric(float64(len(data))/(1<<20), "MB")

	path := filepath.Join(b.TempDir(), "state.gz")
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

// BenchmarkRestoreStateHeavyUncompressed times RestoreState for a heavy session
// with an uncompressed state file.
func BenchmarkRestoreStateHeavyUncompressed(b *testing.B) {
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

// BenchmarkEncodeStateHeavy times ExportSnapshot for all 8 panes + serialization + gzip compression
// for a heavy session: 8 panes of 120x40 each with 10,000 styled scrollback lines.
// This measures the old process's total work while holding s.mu before exec in PrepareUpgrade.
func BenchmarkEncodeStateHeavy(b *testing.B) {
	const panes, cols, rows = 8, 120, 40
	grids := make([]term.Grid, panes)
	for id := 0; id < panes; id++ {
		g := term.NewVT(cols, rows)
		defer g.Close()
		for i := 0; i < 10000+rows; i++ {
			fmt.Fprintf(g, "\x1b[3%dmline %05d the quick brown fox jumps over the lazy dog 0123456789abcdefghijklmnopqrstuvwxyz\x1b[0m\r\n", i%8, i)
		}
		grids[id] = g
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		state := UpgradeState{Cols: cols, Rows: rows, Panes: map[int]UpgradePane{}}
		for id := 1; id <= panes; id++ {
			snap := grids[id-1].(term.Snapshotter).ExportSnapshot()
			state.Panes[id] = UpgradePane{ID: id, Cols: cols, Rows: rows, GridSnap: snap}
		}
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		if err := json.NewEncoder(gw).Encode(state); err != nil {
			b.Fatal(err)
		}
		if err := gw.Close(); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(buf.Len())/(1<<20), "MB")
	}
}
