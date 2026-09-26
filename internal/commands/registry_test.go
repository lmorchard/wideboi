package commands

import (
	"fmt"
	"testing"
)

func TestRegistryPointerStabilityAndDeduplication(t *testing.T) {
	r := NewRegistry()

	// Register initial command
	cmd0 := Command{
		Name:        "first",
		Aliases:     []string{"f", "1st"},
		Description: "First command",
	}
	r.Register(cmd0)

	ptrBefore, ok := r.Lookup("first")
	if !ok || ptrBefore.Description != "First command" {
		t.Fatalf("Lookup(first) failed or wrong description")
	}

	// Register many commands to trigger slice reallocation in r.commands
	for i := 0; i < 100; i++ {
		r.Register(Command{
			Name:        fmt.Sprintf("cmd-%d", i),
			Description: fmt.Sprintf("Description %d", i),
		})
	}

	// Verify original pointer in byName still points to valid data
	ptrAfter, ok := r.Lookup("first")
	if !ok {
		t.Fatalf("Lookup(first) returned false after reallocations")
	}
	if ptrAfter.Description != "First command" {
		t.Errorf("pointer corrupted: got description %q, want %q", ptrAfter.Description, "First command")
	}
	if ptrBefore != ptrAfter {
		t.Errorf("pointer changed after reallocations: %p vs %p", ptrBefore, ptrAfter)
	}

	// Re-register "first" with updated description and aliases
	r.Register(Command{
		Name:        "first",
		Aliases:     []string{"f_new"},
		Description: "Updated first command",
	})

	ptrUpdated, ok := r.Lookup("first")
	if !ok || ptrUpdated.Description != "Updated first command" {
		t.Fatalf("Lookup after update failed: %+v", ptrUpdated)
	}
	if ptrBefore.Description != "Updated first command" {
		t.Errorf("ptrBefore not updated in place: got %q, want %q", ptrBefore.Description, "Updated first command")
	}

	// Old alias should be gone
	if _, ok := r.Lookup("1st"); ok {
		t.Errorf("old alias '1st' should have been removed")
	}
	// New alias should exist
	if _, ok := r.Lookup("f_new"); !ok {
		t.Errorf("new alias 'f_new' should resolve")
	}

	// Test that alias cleanup does not delete an alias belonging to another command
	r.Register(Command{
		Name:    "cmd-a",
		Aliases: []string{"shared-alias"},
	})
	r.Register(Command{
		Name:    "cmd-b",
		Aliases: []string{"shared-alias"},
	})
	// Now shared-alias points to cmd-b
	if cmd, ok := r.Lookup("shared-alias"); !ok || cmd.Name != "cmd-b" {
		t.Fatalf("shared-alias should resolve to cmd-b, got %v", cmd)
	}
	// Re-register cmd-a without shared-alias; cmd-b should still own shared-alias
	r.Register(Command{
		Name:    "cmd-a",
		Aliases: []string{"a-only"},
	})
	if cmd, ok := r.Lookup("shared-alias"); !ok || cmd.Name != "cmd-b" {
		t.Errorf("re-registering cmd-a erroneously removed cmd-b's alias; got %v", cmd)
	}

	// Check for duplicates in All()
	var firstCount int
	for _, c := range r.All() {
		if c.Name == "first" {
			firstCount++
		}
	}
	if firstCount != 1 {
		t.Errorf("All() returned %d entries for 'first', want 1", firstCount)
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "''"},
		{"echo", "echo"},
		{"abc-123_xyz.txt", "abc-123_xyz.txt"},
		{"hello world", "'hello world'"},
		{"it's", `'it'\''s'`},
		{"'quoted'", `''\''quoted'\'''`},
		{"$VAR", "'$VAR'"},
		{"foo*bar", "'foo*bar'"},
		{"a;b", "'a;b'"},
		{"cmd1 && cmd2", "'cmd1 && cmd2'"},
		{"a|b", "'a|b'"},
		{"line1\nline2", "'line1\nline2'"},
		{"tab\there", "'tab\there'"},
		{`path\with\backslash`, `'path\with\backslash'`},
		{"(nested)", "'(nested)'"},
		{"<redirect>", "'<redirect>'"},
		{"[glob]", "'[glob]'"},
		{"{brace}", "'{brace}'"},
		{"#comment", "'#comment'"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := shellQuote(tt.input)
			if got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestShellJoin(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"ls"}, "ls"},
		{[]string{"ls", "-la"}, "ls -la"},
		{[]string{"echo", "hello world"}, "echo 'hello world'"},
		{[]string{"git", "commit", "-m", "hello 'friend'"}, "git commit -m 'hello '\\''friend'\\'''"},
		{[]string{"sh", "-c", "echo $FOO"}, "sh -c 'echo $FOO'"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := shellJoin(tt.args)
			if got != tt.want {
				t.Errorf("shellJoin(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}
