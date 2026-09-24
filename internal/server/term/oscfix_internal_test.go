package term

import (
	"bytes"
	"strings"
	"testing"
)

// feed runs input through a fresh scanner in chunks split at cut, and
// returns what reached the parser plus any titles set.
func feed(input []byte, cut int) (out []byte, titles []string) {
	var s oscScanner
	emit := func(b []byte) { out = append(out, b...) }
	set := func(t string) { titles = append(titles, t) }
	s.write(input[:cut], emit, set)
	s.write(input[cut:], emit, set)
	return out, titles
}

// Without a 0x9C continuation inside an OSC -- one that really continues
// a well-formed character -- the scanner must be the identity, byte for byte, wherever the reads split: it sits in front of
// every byte every pane writes.
func TestOSCScannerIsTheIdentityOtherwise(t *testing.T) {
	inputs := []string{
		"plain text\r\n",
		"\x1b[31mRED\x1b[m \x1b[38;2;1;2;3mRGB\x1b[m",
		"\x1b]0;héllo ⠂ — 中文\x07after", // no 0x9C in any of these
		"\x1b]133;A\x07$ \x1b]133;B\x07",
		"\x1b]0;abc\x9cafter",      // a real 8-bit ST
		"\x1b]0;\xe2\x07tail",      // truncated character, then BEL
		"\x1b]0;\x9c\xb3stray\x07", // continuation bytes with no lead
		"\x1b\x1b]2;x\x1b\\done",   // ESC ESC ]
		"\x1b]0;cancelled\x18text", // CAN
		"✳ outside any OSC ✳",
		// After E0 the next byte must be A0..BF, and after F4 it must be
		// 80..8F, so 0x9C cannot continue these: it is a real ST.
		"\x1b]0;a\xe0\x9ctail ✳",
		"\x1b]0;a\xf4\x9ctail ✳",
		"\x1b]0;a\xe0\x9c\xb3 ✳", // overlong: E0 9C never starts a character
		// A character cut short after a 0x9C: the parser ended the OSC at
		// it, so the scanner must be back in ground for the ✳ as well.
		"\x1b]0;a\xe2\x9cAtail ✳",
	}
	for _, in := range inputs {
		for cut := 0; cut <= len(in); cut++ {
			out, titles := feed([]byte(in), cut)
			if !bytes.Equal(out, []byte(in)) {
				t.Errorf("%q split at %d: forwarded %q", in, cut, out)
			}
			if len(titles) != 0 {
				t.Errorf("%q split at %d: set titles %q", in, cut, titles)
			}
		}
	}
}

func TestOSCScannerReplacesTheCharacterAndRestoresTheTitle(t *testing.T) {
	out, titles := feed([]byte("\x1b]0;✳ x\x07"), 0)
	if want := "\x1b]0;\uFFFD x\x07"; string(out) != want {
		t.Errorf("forwarded %q, want %q", out, want)
	}
	if len(titles) != 1 || titles[0] != "✳ x" {
		t.Errorf("titles = %q, want [✳ x]", titles)
	}
}

// Past oscRawCap the scanner stops keeping a copy and leaves the
// parser's version of the title alone, but still forwards every byte.
func TestOSCScannerGivesUpOnOversizedPayloads(t *testing.T) {
	in := "\x1b]0;✳" + strings.Repeat("a", oscRawCap) + "\x07"
	out, titles := feed([]byte(in), 0)
	if want := strings.Replace(in, "✳", "\uFFFD", 1); string(out) != want {
		t.Errorf("forwarded %d bytes, want %d", len(out), len(want))
	}
	if len(titles) != 0 {
		t.Errorf("set a title from a truncated copy: %d bytes", len(titles[0]))
	}
}
