package protocol

import (
	"reflect"
	"strings"
	"testing"
)

// wireTypes is every type that crosses the transport boundary. Anything
// added to messages.go belongs here.
var wireTypes = []any{
	MsgAttach{},
	MsgStatusRequest{},
	MsgVerb{},
	MsgMouse{},
	MsgInput{},
	MsgResize{},
	MsgScroll{},
	MsgHistoryRequest{},
	MsgHistorySnapshot{},
	MsgShutdown{},
	MsgDetach{},
	MsgLayoutSnapshot{},
	MsgPaneCreated{},
	MsgPaneUpdate{},
	MsgPanePatch{},
	MsgPaneResync{},
	MsgPaneClosed{},
	MsgPaneMetadata{},
	MsgTrafficRequest{},
	MsgTrafficStats{},
	MsgFocusPane{},
	MsgSplitRequest{},
	MsgSplitResponse{},
	MsgSendInputRequest{},
	MsgSendInputResponse{},
	MsgCaptureRequest{},
	MsgCaptureResponse{},
	MsgClosePaneRequest{},
	MsgClosePaneResponse{},
	MsgWaitRequest{},
	MsgWaitResponse{},
}

// TestWireTypesCarryNoInterfaces is the structural guard for the defect
// that broke attach: protocol.CellData.Style was a uv.Style, whose Fg,
// Bg and UnderlineColor are color.Color *interfaces*, and MsgInput.Key
// was a uv.KeyEvent, itself an interface. gob refuses to encode an
// interface value whose concrete type is not registered, and the set of
// concrete color.Color implementations an upstream SGR parser can
// produce is open-ended -- ansi.BasicColor, ansi.IndexedColor,
// color.RGBA, color.CMYK and color.Transparent all appear in
// ansi.ReadStyleColor alone. Registering them one by one is whack-a-mole
// that fails closed on the *next* one, silently, by killing the socket.
//
// So the invariant is not "register the types we know about" but "no
// wire type contains an interface at all." That is checkable, which is
// the whole point: a table of registered names cannot tell you what it
// is missing, and a roundtrip test only covers the values it happens to
// build. This walks the declared shape instead.
func TestWireTypesCarryNoInterfaces(t *testing.T) {
	for _, wt := range wireTypes {
		typ := reflect.TypeOf(wt)
		t.Run(typ.Name(), func(t *testing.T) {
			seen := make(map[reflect.Type]bool)
			if path := findInterface(typ, typ.Name(), seen); path != "" {
				t.Errorf("wire type %s reaches an interface at %s; gob cannot encode it "+
					"unless every concrete implementation is registered, so give it a "+
					"concrete codec-neutral representation instead", typ.Name(), path)
			}
		})
	}
}

// findInterface returns the field path to the first interface-typed
// field reachable from typ, or "" if there is none.
func findInterface(typ reflect.Type, path string, seen map[reflect.Type]bool) string {
	if seen[typ] {
		return ""
	}
	seen[typ] = true

	switch typ.Kind() {
	case reflect.Interface:
		return path + " (" + typ.String() + ")"
	case reflect.Ptr, reflect.Slice, reflect.Array:
		return findInterface(typ.Elem(), path+"[]", seen)
	case reflect.Map:
		if p := findInterface(typ.Key(), path+"{key}", seen); p != "" {
			return p
		}
		return findInterface(typ.Elem(), path+"{val}", seen)
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if p := findInterface(f.Type, path+"."+f.Name, seen); p != "" {
				return p
			}
		}
	}
	return ""
}

// TestWireTypesExportEveryField guards the other half of gob's contract:
// it ignores unexported fields entirely, so a wire type carrying state
// in one transmits a zero value with no error at all. Silent corruption
// is worse than the refusal above.
func TestWireTypesExportEveryField(t *testing.T) {
	for _, wt := range wireTypes {
		typ := reflect.TypeOf(wt)
		t.Run(typ.Name(), func(t *testing.T) {
			for _, path := range unexportedFields(typ, typ.Name(), map[reflect.Type]bool{}) {
				t.Errorf("wire type %s has unexported field %s; gob silently drops it", typ.Name(), path)
			}
		})
	}
}

func unexportedFields(typ reflect.Type, path string, seen map[reflect.Type]bool) []string {
	if seen[typ] {
		return nil
	}
	seen[typ] = true

	var out []string
	switch typ.Kind() {
	case reflect.Ptr, reflect.Slice, reflect.Array:
		out = append(out, unexportedFields(typ.Elem(), path+"[]", seen)...)
	case reflect.Map:
		out = append(out, unexportedFields(typ.Elem(), path+"{val}", seen)...)
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.PkgPath != "" {
				out = append(out, path+"."+f.Name)
				continue
			}
			out = append(out, unexportedFields(f.Type, path+"."+f.Name, seen)...)
		}
	}
	return out
}

// Keep the failure message honest about where to look.
func TestWireTypesListedHere(t *testing.T) {
	if len(wireTypes) == 0 || !strings.HasPrefix(reflect.TypeOf(wireTypes[0]).Name(), "Msg") {
		t.Fatal("wireTypes must list the Msg* types from messages.go")
	}
}
