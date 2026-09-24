package protocol

import (
	"image"
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// This file holds the concrete mirrors of the two upstream types a cell
// update needs: uv.Style and uv.Key. They are the in-process model;
// codec.go maps them to the protobuf wire schema.
//
// Neither could travel as-is. uv.Style's Fg, Bg and UnderlineColor are
// color.Color *interfaces*, and uv.KeyEvent is an interface too. The
// socket used to carry gob, which refuses to encode an interface value
// whose concrete type has not been registered, and the refusal is an
// error on a write pump, not a panic: before this existed, the first
// coloured cell a child emitted killed the socket and the attached client
// exited silently. A schema-driven codec has the same need -- it can
// only map declared, concrete fields.
//
// Registering the implementations instead of mirroring them was the
// obvious alternative and the wrong one. The set is open-ended --
// ansi.ReadStyleColor alone yields ansi.IndexedColor, color.RGBA,
// color.CMYK and color.Transparent, uv.ReadStyle adds ansi.BasicColor,
// and any future upstream branch adds more -- and a missing entry fails
// exactly the way this one did: silently, at runtime, on someone's
// prompt. protocol_test.go's TestWireTypesCarryNoInterfaces enforces the
// mirroring instead, because "contains no interface" is a property of
// the declared type that a test can actually check.
//
// The package still imports uv, but only here and only for the
// converters. The wire structs themselves are scalars.

// ColorKind tags which arm of ColorData carries the value.
type ColorKind uint8

const (
	// ColorNone is an absent colour: the terminal's default.
	ColorNone ColorKind = iota
	// ColorBasic is one of the 16 palette colours (ansi.BasicColor).
	ColorBasic
	// ColorIndexed is one of the 256 palette colours (ansi.IndexedColor).
	ColorIndexed
	// ColorRGBA is a direct colour, already reduced to 8 bits per channel.
	ColorRGBA
)

// ColorData is a color.Color flattened into scalars.
//
// The two palette kinds are preserved exactly rather than resolved to
// RGB, because a palette index means "whatever the user's terminal has
// bound to slot n" and resolving it here would freeze someone else's
// theme into the wire. Everything else -- including CMYK, which no
// terminal renders natively anyway -- collapses to RGBA via the
// color.Color interface's own RGBA method, which is exactly the
// conversion the renderer would do later.
type ColorData struct {
	Kind       ColorKind
	Index      uint8
	R, G, B, A uint8
}

// EncodeColor flattens c for transport. A nil colour stays absent.
func EncodeColor(c color.Color) ColorData {
	switch v := c.(type) {
	case nil:
		return ColorData{Kind: ColorNone}
	case ansi.BasicColor:
		return ColorData{Kind: ColorBasic, Index: uint8(v)}
	case ansi.IndexedColor:
		return ColorData{Kind: ColorIndexed, Index: uint8(v)}
	}

	// RGBA returns alpha-premultiplied 16-bit channels; the wire carries
	// 8. Shifting rather than dividing matches how ansi's own toRGBA
	// widens 8-bit values, so an 8-bit colour survives the round trip
	// unchanged.
	r, g, b, a := c.RGBA()
	return ColorData{
		Kind: ColorRGBA,
		R:    uint8(r >> 8),
		G:    uint8(g >> 8),
		B:    uint8(b >> 8),
		A:    uint8(a >> 8),
	}
}

// Decode rebuilds a color.Color. ColorNone decodes to a nil interface,
// which is what uv.Style means by "use the terminal's default".
func (c ColorData) Decode() color.Color {
	switch c.Kind {
	case ColorBasic:
		return ansi.BasicColor(c.Index)
	case ColorIndexed:
		return ansi.IndexedColor(c.Index)
	case ColorRGBA:
		return color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A}
	default:
		return nil
	}
}

// StyleData is uv.Style with its three colours flattened.
//
// Underline and Attrs are already concrete scalars upstream
// (ansi.Underline is an alias for byte, Attrs is a uint8 bitfield), but
// they are restated with their own types here so the wire format does
// not change under us if upstream widens either one -- the converters
// below would stop compiling, which is the point.
type StyleData struct {
	Fg             ColorData
	Bg             ColorData
	UnderlineColor ColorData
	Underline      uint8
	Attrs          uint8
}

// EncodeStyle flattens s for transport.
func EncodeStyle(s uv.Style) StyleData {
	return StyleData{
		Fg:             EncodeColor(s.Fg),
		Bg:             EncodeColor(s.Bg),
		UnderlineColor: EncodeColor(s.UnderlineColor),
		Underline:      uint8(s.Underline),
		Attrs:          s.Attrs,
	}
}

// Decode rebuilds a uv.Style suitable for handing to a renderer.
func (s StyleData) Decode() uv.Style {
	return uv.Style{
		Fg:             s.Fg.Decode(),
		Bg:             s.Bg.Decode(),
		UnderlineColor: s.UnderlineColor.Decode(),
		Underline:      ansi.Underline(s.Underline),
		Attrs:          s.Attrs,
	}
}

// KeyData is uv.Key's fields, restated so the wire carries a struct
// rather than the uv.KeyEvent interface uv.Key is delivered behind.
//
// Only the press form travels. wideboi never enables the Kitty
// keyboard protocol's release reporting, so a release event is
// something the client cannot currently produce; if that changes, this
// grows a flag rather than going back to carrying the interface.
type KeyData struct {
	Text        string
	Mod         int
	Code        rune
	ShiftedCode rune
	BaseCode    rune
	IsRepeat    bool
}

// EncodeKey flattens k for transport.
func EncodeKey(k uv.KeyEvent) KeyData {
	if k == nil {
		return KeyData{}
	}
	key := k.Key()
	return KeyData{
		Text:        key.Text,
		Mod:         int(key.Mod),
		Code:        key.Code,
		ShiftedCode: key.ShiftedCode,
		BaseCode:    key.BaseCode,
		IsRepeat:    key.IsRepeat,
	}
}

// Decode rebuilds a uv.KeyEvent for the server to feed its emulator.
func (k KeyData) Decode() uv.KeyEvent {
	return uv.KeyPressEvent(uv.Key{
		Text:        k.Text,
		Mod:         uv.KeyMod(k.Mod),
		Code:        k.Code,
		ShiftedCode: k.ShiftedCode,
		BaseCode:    k.BaseCode,
		IsRepeat:    k.IsRepeat,
	})
}

// IsZero reports whether k carries no key at all, which is how
// MsgInput distinguishes a raw-bytes send from a key send.
func (k KeyData) IsZero() bool { return k == KeyData{} }

// EncodeMouse flattens ev for transport to paneID, replacing its screen
// coordinates with local, which is where the event lands in the pane.
func EncodeMouse(paneID int, ev uv.MouseEvent, local image.Point) MsgMouse {
	var kind MouseKind
	switch ev.(type) {
	case uv.MouseReleaseEvent:
		kind = MouseRelease
	case uv.MouseMotionEvent:
		kind = MouseMotion
	case uv.MouseWheelEvent:
		kind = MouseWheel
	default:
		kind = MousePress
	}
	m := ev.Mouse()
	return MsgMouse{
		PaneID: paneID,
		Kind:   kind,
		X:      local.X,
		Y:      local.Y,
		Button: int(m.Button),
		Mod:    int(m.Mod),
	}
}

// Decode rebuilds the uv.MouseEvent for the server to feed its
// emulator. The concrete type matters: vt's SendMouse distinguishes
// press, release and motion by type assertion.
func (m MsgMouse) Decode() uv.MouseEvent {
	mouse := uv.Mouse{X: m.X, Y: m.Y, Button: uv.MouseButton(m.Button), Mod: uv.KeyMod(m.Mod)}
	switch m.Kind {
	case MouseRelease:
		return uv.MouseReleaseEvent(mouse)
	case MouseMotion:
		return uv.MouseMotionEvent(mouse)
	case MouseWheel:
		return uv.MouseWheelEvent(mouse)
	default:
		return uv.MouseClickEvent(mouse)
	}
}
