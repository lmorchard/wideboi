package protocol

import (
	"image"
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// EncodeColor flattens c for transport. A nil colour stays absent.
func EncodeColor(c color.Color) *ColorData {
	switch v := c.(type) {
	case nil:
		return &ColorData{Kind: ColorKind_COLOR_NONE}
	case ansi.BasicColor:
		return &ColorData{Kind: ColorKind_COLOR_BASIC, Index: uint32(v)}
	case ansi.IndexedColor:
		return &ColorData{Kind: ColorKind_COLOR_INDEXED, Index: uint32(v)}
	}

	r, g, b, a := c.RGBA()
	return &ColorData{
		Kind: ColorKind_COLOR_RGBA,
		R:    uint32(r >> 8),
		G:    uint32(g >> 8),
		B:    uint32(b >> 8),
		A:    uint32(a >> 8),
	}
}

// Decode rebuilds a color.Color. ColorNone decodes to a nil interface.
func (c *ColorData) Decode() color.Color {
	if c == nil {
		return nil
	}
	switch c.Kind {
	case ColorKind_COLOR_BASIC:
		return ansi.BasicColor(c.Index)
	case ColorKind_COLOR_INDEXED:
		return ansi.IndexedColor(c.Index)
	case ColorKind_COLOR_RGBA:
		return color.RGBA{R: uint8(c.R), G: uint8(c.G), B: uint8(c.B), A: uint8(c.A)}
	default:
		return nil
	}
}

// EncodeStyle flattens s for transport.
func EncodeStyle(s uv.Style) *StyleData {
	return &StyleData{
		Fg:             EncodeColor(s.Fg),
		Bg:             EncodeColor(s.Bg),
		UnderlineColor: EncodeColor(s.UnderlineColor),
		Underline:      uint32(s.Underline),
		Attrs:          uint32(s.Attrs),
	}
}

// Decode rebuilds a uv.Style suitable for handing to a renderer.
func (s *StyleData) Decode() uv.Style {
	if s == nil {
		return uv.Style{}
	}
	return uv.Style{
		Fg:             s.Fg.Decode(),
		Bg:             s.Bg.Decode(),
		UnderlineColor: s.UnderlineColor.Decode(),
		Underline:      ansi.Underline(s.Underline),
		Attrs:          uint8(s.Attrs),
	}
}

// EncodeKey flattens k for transport.
func EncodeKey(k uv.KeyEvent) *KeyData {
	if k == nil {
		return &KeyData{}
	}
	key := k.Key()
	return &KeyData{
		Text:        key.Text,
		Mod:         int32(key.Mod),
		Code:        key.Code,
		ShiftedCode: key.ShiftedCode,
		BaseCode:    key.BaseCode,
		IsRepeat:    key.IsRepeat,
	}
}

// Decode rebuilds a uv.KeyEvent for the server to feed its emulator.
func (k *KeyData) Decode() uv.KeyEvent {
	if k == nil {
		return nil
	}
	return uv.KeyPressEvent(uv.Key{
		Text:        k.Text,
		Mod:         uv.KeyMod(k.Mod),
		Code:        k.Code,
		ShiftedCode: k.ShiftedCode,
		BaseCode:    k.BaseCode,
		IsRepeat:    k.IsRepeat,
	})
}

// IsZero reports whether k carries no key at all.
func (k *KeyData) IsZero() bool {
	return k == nil || (k.Text == "" && k.Mod == 0 && k.Code == 0 && k.ShiftedCode == 0 && k.BaseCode == 0 && !k.IsRepeat)
}

// EncodeMouse flattens ev for transport to paneID, replacing its screen
// coordinates with local, which is where the event lands in the pane.
func EncodeMouse(paneID int, ev uv.MouseEvent, local image.Point) *MsgMouse {
	var kind MouseKind
	switch ev.(type) {
	case uv.MouseReleaseEvent:
		kind = MouseKind_MOUSE_RELEASE
	case uv.MouseMotionEvent:
		kind = MouseKind_MOUSE_MOTION
	case uv.MouseWheelEvent:
		kind = MouseKind_MOUSE_WHEEL
	default:
		kind = MouseKind_MOUSE_PRESS
	}
	m := ev.Mouse()
	return &MsgMouse{
		PaneId: int32(paneID),
		Kind:   kind,
		X:      int32(local.X),
		Y:      int32(local.Y),
		Button: int32(m.Button),
		Mod:    int32(m.Mod),
	}
}

// Decode rebuilds the uv.MouseEvent for the server to feed its emulator.
func (m *MsgMouse) Decode() uv.MouseEvent {
	if m == nil {
		return nil
	}
	mouse := uv.Mouse{X: int(m.X), Y: int(m.Y), Button: uv.MouseButton(m.Button), Mod: uv.KeyMod(m.Mod)}
	switch m.Kind {
	case MouseKind_MOUSE_RELEASE:
		return uv.MouseReleaseEvent(mouse)
	case MouseKind_MOUSE_MOTION:
		return uv.MouseMotionEvent(mouse)
	case MouseKind_MOUSE_WHEEL:
		return uv.MouseWheelEvent(mouse)
	default:
		return uv.MouseClickEvent(mouse)
	}
}

func EncodeRectangle(r image.Rectangle) *Rectangle {
	return &Rectangle{
		MinX: int32(r.Min.X),
		MinY: int32(r.Min.Y),
		MaxX: int32(r.Max.X),
		MaxY: int32(r.Max.Y),
	}
}

func (r *Rectangle) Decode() image.Rectangle {
	if r == nil {
		return image.Rectangle{}
	}
	return image.Rect(int(r.MinX), int(r.MinY), int(r.MaxX), int(r.MaxY))
}
