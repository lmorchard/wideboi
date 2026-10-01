package server

import (
	"bytes"
	"fmt"
	"strconv"
)

// Default cell metrics used for pixel size query synthesis.
const (
	defaultCellWidth  = 8
	defaultCellHeight = 16
)

// Default colors in 16-bit hex format (rgb:rrrr/gggg/bbbb).
const (
	defaultFgColor = "d4d4/d4d4/d4d4"
	defaultBgColor = "1e1e/1e1e/1e1e"
)

var ansiPalette16 = [16]string{
	"0000/0000/0000", // 0 black
	"cdcd/0000/0000", // 1 red
	"0000/cdcd/0000", // 2 green
	"cdcd/cdcd/0000", // 3 yellow
	"0000/0000/eeee", // 4 blue
	"cdcd/0000/cdcd", // 5 magenta
	"0000/cdcd/cdcd", // 6 cyan
	"e5e5/e5e5/e5e5", // 7 white
	"7f7f/7f7f/7f7f", // 8 bright black
	"ffff/0000/0000", // 9 bright red
	"0000/ffff/0000", // 10 bright green
	"ffff/ffff/0000", // 11 bright yellow
	"5c5c/5c5c/ffff", // 12 bright blue
	"ffff/0000/ffff", // 13 bright magenta
	"0000/ffff/ffff", // 14 bright cyan
	"ffff/ffff/ffff", // 15 bright white
}

func paletteColor(index int) string {
	if index >= 0 && index < 16 {
		return ansiPalette16[index]
	}
	if index >= 16 && index <= 231 {
		n := index - 16
		r := (n / 36) % 6
		g := (n / 6) % 6
		b := n % 6
		val := func(v int) int {
			if v == 0 {
				return 0
			}
			return 55 + v*40
		}
		rv, gv, bv := val(r), val(g), val(b)
		return fmt.Sprintf("%02x%02x/%02x%02x/%02x%02x", rv, rv, gv, gv, bv, bv)
	}
	if index >= 232 && index <= 255 {
		v := 8 + (index-232)*10
		return fmt.Sprintf("%02x%02x/%02x%02x/%02x%02x", v, v, v, v, v, v)
	}
	return "0000/0000/0000"
}

// queryScanner tracks state across byte chunks to detect terminal queries
// and synthesize responses.
type queryScanner struct {
	buf []byte
}

func newQueryScanner() *queryScanner {
	return &queryScanner{}
}

// process scans chunk for terminal queries, returns:
// - cleaned: the bytes that should be passed to the VT emulator (queries stripped)
// - responses: synthesized response bytes to write back to the child PTY
func (qs *queryScanner) process(chunk []byte, cols, rows int) (cleaned []byte, responses []byte) {
	if len(qs.buf) > 0 {
		qs.buf = append(qs.buf, chunk...)
	} else {
		qs.buf = append(qs.buf, chunk...)
	}

	data := qs.buf
	var outClean bytes.Buffer
	var outResp bytes.Buffer

	i := 0
	for i < len(data) {
		if data[i] != 0x1b {
			outClean.WriteByte(data[i])
			i++
			continue
		}

		// Potential escape sequence starting at index i
		consumed, reply, partial := matchQuery(data[i:], cols, rows)
		if partial {
			// Sequence is in-flight; keep from i to end in qs.buf
			qs.buf = append([]byte(nil), data[i:]...)
			return outClean.Bytes(), outResp.Bytes()
		}
		if consumed > 0 {
			if len(reply) > 0 {
				outResp.Write(reply)
			}
			i += consumed
			continue
		}

		// Not a matched query, advance 1 byte
		outClean.WriteByte(data[i])
		i++
	}

	qs.buf = nil
	return outClean.Bytes(), outResp.Bytes()
}

// matchQuery inspects a slice starting with ESC (0x1b).
// Returns (consumedLength, replyBytes, isPartial).
func matchQuery(b []byte, cols, rows int) (int, []byte, bool) {
	if len(b) < 2 {
		return 0, nil, true
	}

	// CSI queries: ESC [ ...
	if b[1] == '[' {
		// CSI 14 t -> window pixel size
		if bytes.HasPrefix(b, []byte("\x1b[14t")) {
			height := max(rows, 1) * defaultCellHeight
			width := max(cols, 1) * defaultCellWidth
			reply := []byte(fmt.Sprintf("\x1b[4;%d;%dt", height, width))
			return len("\x1b[14t"), reply, false
		}
		// CSI 16 t -> cell pixel size
		if bytes.HasPrefix(b, []byte("\x1b[16t")) {
			reply := []byte(fmt.Sprintf("\x1b[6;%d;%dt", defaultCellHeight, defaultCellWidth))
			return len("\x1b[16t"), reply, false
		}
		// CSI 18 t -> text area character size
		if bytes.HasPrefix(b, []byte("\x1b[18t")) {
			reply := []byte(fmt.Sprintf("\x1b[8;%d;%dt", max(rows, 1), max(cols, 1)))
			return len("\x1b[18t"), reply, false
		}
		// CSI ? 996 n -> light/dark mode query (1=dark, 2=light)
		if bytes.HasPrefix(b, []byte("\x1b[?996n")) {
			reply := []byte("\x1b[?996;1n")
			return len("\x1b[?996n"), reply, false
		}

		// Check for potential partial matches of the fixed CSI queries
		csiPrefixes := []string{"\x1b[14t", "\x1b[16t", "\x1b[18t", "\x1b[?996n"}
		for _, p := range csiPrefixes {
			if len(b) < len(p) && p[:len(b)] == string(b) {
				return 0, nil, true
			}
		}
		return 0, nil, false
	}

	// OSC queries: ESC ] ...
	if b[1] == ']' {
		// Look for terminator: BEL (0x07) or ST (ESC \)
		termIdx := -1
		var termSeq string
		termLen := 0

		for j := 2; j < len(b); j++ {
			if b[j] == 0x07 {
				termIdx = j
				termSeq = "\x07"
				termLen = 1
				break
			}
			if b[j] == 0x1b && j+1 < len(b) && b[j+1] == '\\' {
				termIdx = j
				termSeq = "\x1b\\"
				termLen = 2
				break
			}
		}

		if termIdx == -1 {
			// Might be an in-progress OSC query up to 64 bytes
			if len(b) < 64 {
				// Only keep as partial if it looks like OSC 4, 10, or 11 query
				s := string(b)
				if s == "\x1b]" || s == "\x1b]1" || s == "\x1b]10" || s == "\x1b]11" || s == "\x1b]4" ||
					bytes.HasPrefix(b, []byte("\x1b]10;")) ||
					bytes.HasPrefix(b, []byte("\x1b]11;")) ||
					bytes.HasPrefix(b, []byte("\x1b]4;")) {
					return 0, nil, true
				}
			}
			return 0, nil, false
		}

		payload := string(b[2:termIdx])
		totalLen := termIdx + termLen

		// OSC 10;? -> query foreground
		if payload == "10;?" {
			reply := []byte(fmt.Sprintf("\x1b]10;rgb:%s%s", defaultFgColor, termSeq))
			return totalLen, reply, false
		}
		// OSC 11;? -> query background
		if payload == "11;?" {
			reply := []byte(fmt.Sprintf("\x1b]11;rgb:%s%s", defaultBgColor, termSeq))
			return totalLen, reply, false
		}
		// OSC 4;index;? -> query palette color
		if bytes.HasPrefix([]byte(payload), []byte("4;")) && bytes.HasSuffix([]byte(payload), []byte(";?")) {
			idxStr := payload[2 : len(payload)-2]
			if idx, err := strconv.Atoi(idxStr); err == nil && idx >= 0 && idx <= 255 {
				color := paletteColor(idx)
				reply := []byte(fmt.Sprintf("\x1b]4;%d;rgb:%s%s", idx, color, termSeq))
				return totalLen, reply, false
			}
		}

		return 0, nil, false
	}

	return 0, nil, false
}
