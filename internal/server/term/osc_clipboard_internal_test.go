package term

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseOSC52(t *testing.T) {
	oversize := strings.Repeat("A", base64.StdEncoding.EncodedLen(maxClipboardBytes)+4)
	// Valid base64 at both ends with whitespace between that strips to
	// almost nothing: the raw OSC is what x/vt truncates, so the raw
	// length is what has to be capped.
	padded := "YWJj" + strings.Repeat(" ", 3<<20) + "ZGVm"
	// base64(1)-style wrapping of a maximal payload must still pass.
	full := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", maxClipboardBytes)))
	var wrapped strings.Builder
	for i := 0; i < len(full); i += 76 {
		wrapped.WriteString(full[i:min(i+76, len(full))])
		wrapped.WriteString("\n")
	}
	cases := []struct {
		name    string
		payload string
		want    string
		ok      bool
	}{
		{"system clipboard", "52;c;aGVsbG8=", "hello", true},
		{"empty selection", "52;;aGVsbG8=", "hello", true},
		{"unpadded", "52;p;aGVsbG8", "hello", true},
		{"wrapped like base64(1)", "52;c;aGVs\nbG8=", "hello", true},
		{"read query refused", "52;c;?", "", false},
		{"clear refused", "52;c;", "", false},
		{"invalid base64", "52;c;!!!", "", false},
		{"invalid utf-8", "52;c;/w==", "", false},
		{"oversize", "52;c;" + oversize, "", false},
		{"whitespace padding past the raw cap", "52;c;" + padded, "", false},
		{"maximal payload wrapped at 76", "52;c;" + wrapped.String(), strings.Repeat("x", maxClipboardBytes), true},
		{"no payload field", "52;c", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseOSC52([]byte(tc.payload))
			if ok != tc.ok || got != tc.want {
				t.Errorf("parseOSC52(%q) = %q, %v; want %q, %v", tc.payload, got, ok, tc.want, tc.ok)
			}
		})
	}
}
