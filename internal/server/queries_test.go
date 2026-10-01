package server

import (
	"bytes"
	"testing"
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
