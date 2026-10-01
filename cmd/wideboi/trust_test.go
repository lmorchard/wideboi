package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPromptTrustInput(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"Y\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"no\n", false},
		{"\n", false},
		{"random\n", false},
	}

	for _, tc := range cases {
		var stderr bytes.Buffer
		stdin := strings.NewReader(tc.input)
		prompt := defaultPromptTrust(stdin, &stderr)
		got := prompt(".wideboi.toml", true)
		if got != tc.want {
			t.Errorf("prompt(%q) = %v, want %v", tc.input, got, tc.want)
		}
		if !strings.Contains(stderr.String(), "contains sensitive settings") {
			t.Errorf("expected prompt message in stderr, got %q", stderr.String())
		}
	}

	// When hasSensitive is false, prompt returns false immediately without reading stdin
	var stderr bytes.Buffer
	stdin := strings.NewReader("y\n")
	prompt := defaultPromptTrust(stdin, &stderr)
	if prompt(".wideboi.toml", false) {
		t.Error("expected prompt to return false when hasSensitive is false")
	}
	if stderr.Len() > 0 {
		t.Error("expected no output when hasSensitive is false")
	}
}
