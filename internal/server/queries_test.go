package server

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQueryScannerSynthesizesOSC10And11(t *testing.T) {
	qs := newQueryScanner()

	// OSC 11 with BEL terminator
	cleaned, resp := qs.process([]byte("\x1b]11;?\x07"), 80, 24)
	if len(cleaned) != 0 {
		t.Errorf("cleaned = %q, want empty", cleaned)
	}
	wantResp := "\x1b]11;rgb:1e1e/1e1e/1e1e\x07"
	if string(resp) != wantResp {
		t.Errorf("resp = %q, want %q", resp, wantResp)
	}

	// OSC 10 with ST terminator
	cleaned, resp = qs.process([]byte("\x1b]10;?\x1b\\"), 80, 24)
	if len(cleaned) != 0 {
		t.Errorf("cleaned = %q, want empty", cleaned)
	}
	wantResp = "\x1b]10;rgb:d4d4/d4d4/d4d4\x1b\\"
	if string(resp) != wantResp {
		t.Errorf("resp = %q, want %q", resp, wantResp)
	}
}

func TestQueryScannerSynthesizesOSC4Palette(t *testing.T) {
	qs := newQueryScanner()

	// Color 1 (red) with BEL
	cleaned, resp := qs.process([]byte("\x1b]4;1;?\x07"), 80, 24)
	if len(cleaned) != 0 {
		t.Errorf("cleaned = %q, want empty", cleaned)
	}
	wantResp := "\x1b]4;1;rgb:cdcd/0000/0000\x07"
	if string(resp) != wantResp {
		t.Errorf("resp = %q, want %q", resp, wantResp)
	}

	// Color 240 (grayscale) with ST
	cleaned, resp = qs.process([]byte("\x1b]4;240;?\x1b\\"), 80, 24)
	if len(cleaned) != 0 {
		t.Errorf("cleaned = %q, want empty", cleaned)
	}
	if !bytes.HasPrefix(resp, []byte("\x1b]4;240;rgb:")) || !bytes.HasSuffix(resp, []byte("\x1b\\")) {
		t.Errorf("resp = %q, want OSC 4 palette reply with ST", resp)
	}
}

func TestQueryScannerSynthesizesCSISizes(t *testing.T) {
	qs := newQueryScanner()

	// CSI 14t (window pixel size) for 80x24: 24*16 = 384, 80*8 = 640
	cleaned, resp := qs.process([]byte("\x1b[14t"), 80, 24)
	if len(cleaned) != 0 {
		t.Errorf("cleaned = %q, want empty", cleaned)
	}
	wantResp := "\x1b[4;384;640t"
	if string(resp) != wantResp {
		t.Errorf("resp = %q, want %q", resp, wantResp)
	}

	// CSI 16t (cell pixel size)
	cleaned, resp = qs.process([]byte("\x1b[16t"), 80, 24)
	if len(cleaned) != 0 {
		t.Errorf("cleaned = %q, want empty", cleaned)
	}
	wantResp = "\x1b[6;16;8t"
	if string(resp) != wantResp {
		t.Errorf("resp = %q, want %q", resp, wantResp)
	}

	// CSI 18t (text area char size)
	cleaned, resp = qs.process([]byte("\x1b[18t"), 80, 24)
	if len(cleaned) != 0 {
		t.Errorf("cleaned = %q, want empty", cleaned)
	}
	wantResp = "\x1b[8;24;80t"
	if string(resp) != wantResp {
		t.Errorf("resp = %q, want %q", resp, wantResp)
	}

	// CSI ? 996 n (dark mode query -> responds with DSR 997;1n for dark)
	cleaned, resp = qs.process([]byte("\x1b[?996n"), 80, 24)
	if len(cleaned) != 0 {
		t.Errorf("cleaned = %q, want empty", cleaned)
	}
	wantResp = "\x1b[?997;1n"
	if string(resp) != wantResp {
		t.Errorf("resp = %q, want %q", resp, wantResp)
	}

	// Light theme query -> responds with DSR 997;2n for light
	qs.SetTheme(QueryTheme{FgColor: "0000/0000/0000", BgColor: "ffff/ffff/ffff", IsDark: false})
	cleaned, resp = qs.process([]byte("\x1b[?996n"), 80, 24)
	if string(resp) != "\x1b[?997;2n" {
		t.Errorf("light theme resp = %q, want \\x1b[?997;2n", resp)
	}
	_, resp = qs.process([]byte("\x1b]11;?\x07"), 80, 24)
	if string(resp) != "\x1b]11;rgb:ffff/ffff/ffff\x07" {
		t.Errorf("light theme bg resp = %q, want \\x1b]11;rgb:ffff/ffff/ffff\\x07", resp)
	}
}

func TestQueryScannerPassesNonQueriesThrough(t *testing.T) {
	qs := newQueryScanner()

	input := []byte("Hello, \x1b[31mworld\x1b[0m!\r\n")
	cleaned, resp := qs.process(input, 80, 24)
	if string(cleaned) != string(input) {
		t.Errorf("cleaned = %q, want %q", cleaned, input)
	}
	if len(resp) != 0 {
		t.Errorf("resp = %q, want empty", resp)
	}
}

func TestQueryScannerHandlesSplitPackets(t *testing.T) {
	qs := newQueryScanner()

	// Chunk 1: prefix of query
	cleaned1, resp1 := qs.process([]byte("prefix \x1b]11"), 80, 24)
	if string(cleaned1) != "prefix " {
		t.Errorf("cleaned1 = %q, want 'prefix '", cleaned1)
	}
	if len(resp1) != 0 {
		t.Errorf("resp1 = %q, want empty", resp1)
	}

	// Chunk 2: remainder of query followed by text
	cleaned2, resp2 := qs.process([]byte(";?\x07suffix"), 80, 24)
	if string(cleaned2) != "suffix" {
		t.Errorf("cleaned2 = %q, want 'suffix'", cleaned2)
	}
	wantResp := "\x1b]11;rgb:1e1e/1e1e/1e1e\x07"
	if string(resp2) != wantResp {
		t.Errorf("resp2 = %q, want %q", resp2, wantResp)
	}
}

func TestPaneRealPTYQueryInterception(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "response.out")
	script := fmt.Sprintf("stty raw -echo 2>/dev/null; printf '\x1b]11;?\x07'; dd bs=1 count=24 2>/dev/null > %q", outPath)
	p, err := NewPane(1, []string{"/bin/sh", "-c", script}, 40, 10, t.TempDir())
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	defer func() {
		_ = p.Close()
	}()

	done := make(chan struct{})
	p.Start(func() { close(done) })

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		data, _ := os.ReadFile(outPath)
		t.Fatalf("timed out waiting for child to complete query roundtrip; outPath has %q", string(data))
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading response file: %v", err)
	}
	want := "\x1b]11;rgb:1e1e/1e1e/1e1e\x07"
	if string(data) != want {
		t.Errorf("got %q, want %q", string(data), want)
	}

	// Verify that the query was stripped and not written to the grid as text
	if text := p.grid.CaptureText(false, 10); strings.Contains(text, "]11;") {
		t.Errorf("grid contains query text: %q", text)
	}
}
