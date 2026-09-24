package term

import "bytes"

// oscScanner sits between a pane's output and x/vt's parser and keeps a
// UTF-8 character inside an OSC string from being cut in half.
//
// x/ansi v0.11.8 treats byte 0x9C as the C1 String Terminator anywhere in
// an OSC, including as a UTF-8 continuation byte
// (ansi/parser/transition_table.go, OscStringState). U+2733 is E2 9C B3,
// so Claude Code's "✳ Claude Code" title became the lone byte "\xe2" and
// the rest of the sequence was printed as text. Protobuf then refused
// the invalid title, which closed the owner's connection and ended the
// session (#175).
//
// Outside an OSC the parser already decodes UTF-8, so bytes pass straight
// through. Inside one, a complete character containing 0x9C reaches the
// parser as U+FFFD instead. If the OSC set the title, the original title
// is then stored directly. Everything else is forwarded unchanged, as
// subslices of the input. The only thing held between writes is an
// unfinished character (at most three bytes).
//
// Not covered: the 8-bit OSC introducer 0x9D, and DCS/APC/SOS/PM strings,
// which share the same ST ambiguity but have never carried text we show.
type oscScanner struct {
	state    oscState
	char     [4]byte // UTF-8 character in progress inside an OSC
	charLen  int
	charWant int
	raw      []byte // the OSC payload as it arrived, for re-deriving a title
	rawOver  bool   // payload outgrew oscRawCap; leave the parser's version
	subst    bool   // a character in this OSC was replaced
}

type oscState uint8

const (
	oscGround oscState = iota
	oscEscape
	oscString
)

// oscRawCap bounds the copy kept for re-deriving a title. It is not a
// limit on forwarding: longer payloads still pass through in full.
const oscRawCap = 4096

var replacementChar = []byte("\uFFFD")

// write feeds p through the scanner. emit receives the bytes for the
// parser, in order. setTitle is called after emit has been given the
// OSC's terminator, so it overrides whatever the parser made of it.
func (s *oscScanner) write(p []byte, emit func([]byte), setTitle func(string)) {
	start := 0 // p[start:i] is pass-through not yet emitted
	flush := func(end int) {
		if end > start {
			emit(p[start:end])
		}
		start = end
	}

	for i := 0; i < len(p); i++ {
		b := p[i]
		switch s.state {
		case oscGround:
			j := bytes.IndexByte(p[i:], 0x1B)
			if j < 0 {
				i = len(p)
				continue
			}
			i += j
			s.state = oscEscape

		case oscEscape:
			switch b {
			case ']':
				s.state = oscString
				s.raw = s.raw[:0]
				s.rawOver = false
				s.subst = false
			case 0x1B:
				// ESC ESC: the parser stays in its escape state too.
			default:
				s.state = oscGround
			}

		case oscString:
			if s.charLen > 0 {
				if continues(s.char[0], s.charLen, b) {
					s.record(b)
					s.char[s.charLen] = b
					s.charLen++
					if s.charLen == s.charWant {
						s.completeChar(emit)
					}
					start = i + 1
					continue
				}
				// Malformed: release what was held as it came, then
				// treat b as if no character had been in progress.
				held := s.char[:s.charLen]
				s.charLen = 0
				emit(held)
				if bytes.IndexByte(held, 0x9C) >= 0 {
					// The parser read that 0x9C as ST, so the OSC is
					// over and b belongs to ground. No title repair:
					// the payload ended somewhere the copy cannot say.
					s.state = oscGround
					i--
					continue
				}
			}

			switch {
			case b >= 0xC2 && b <= 0xF4:
				flush(i)
				s.record(b)
				s.char[0] = b
				s.charLen = 1
				s.charWant = utf8Len(b)
				start = i + 1
			case b == 0x07 || b == 0x9C:
				flush(i + 1)
				s.finalize(setTitle)
				s.state = oscGround
			case b == 0x1B:
				// The parser dispatches the OSC on ESC; ESC \ is its ST.
				flush(i + 1)
				s.finalize(setTitle)
				s.state = oscEscape
			case b == 0x18 || b == 0x1A:
				s.state = oscGround
			default:
				s.record(b)
			}
		}
	}
	flush(len(p))
}

func (s *oscScanner) record(b byte) {
	if s.rawOver {
		return
	}
	if len(s.raw) >= oscRawCap {
		s.rawOver = true
		return
	}
	s.raw = append(s.raw, b)
}

func (s *oscScanner) completeChar(emit func([]byte)) {
	c := s.char[:s.charLen]
	s.charLen = 0
	if bytes.IndexByte(c, 0x9C) >= 0 {
		emit(replacementChar)
		s.subst = true
		return
	}
	emit(c)
}

// finalize restores a title the parser could only see with U+FFFD in it.
// It splits at the first ';', where x/vt's handleTitle requires exactly
// one, so a title containing ';' is accepted here and ignored there.
func (s *oscScanner) finalize(setTitle func(string)) {
	if !s.subst || s.rawOver {
		return
	}
	cmd, title, ok := bytes.Cut(s.raw, []byte{';'})
	if !ok {
		return
	}
	if c := string(cmd); c == "0" || c == "2" {
		setTitle(string(title))
	}
}

// continues reports whether b can be byte n (1-based: n bytes already
// held) of a well-formed character starting with lead. The second byte
// is narrower after E0, ED, F0 and F4 (no overlongs, surrogates, or
// code points past U+10FFFF), and E0 9C or F4 9C is therefore a lead
// byte followed by a real ST.
func continues(lead byte, n int, b byte) bool {
	lo, hi := byte(0x80), byte(0xBF)
	if n == 1 {
		switch lead {
		case 0xE0:
			lo = 0xA0
		case 0xED:
			hi = 0x9F
		case 0xF0:
			lo = 0x90
		case 0xF4:
			hi = 0x8F
		}
	}
	return b >= lo && b <= hi
}

// utf8Len is the encoded length announced by a lead byte in C2..F4.
func utf8Len(lead byte) int {
	switch {
	case lead >= 0xF0:
		return 4
	case lead >= 0xE0:
		return 3
	default:
		return 2
	}
}
