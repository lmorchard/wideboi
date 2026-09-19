package client_test

import (
	"testing"

	"github.com/lmorchard/wideboi/internal/client"
	"github.com/lmorchard/wideboi/internal/client/compose"
)

func TestWipeTransitionSteppingAndDirection(t *testing.T) {
	fA := compose.NewSurface(20, 5)
	fB := compose.NewSurface(20, 5)
	compose.WriteString(fA, 0, 0, "AAAAAAAAAAAAAAAAAAAA")
	compose.WriteString(fB, 0, 0, "BBBBBBBBBBBBBBBBBBBB")

	wipe := client.NewWipeTransition(fA, fB, 20, 5, client.WipeLeftToRight, 4)

	if !wipe.Active() {
		t.Fatal("wipe should be active initially")
	}

	scr := compose.NewSurface(20, 5)

	// Step 1: 1/4 revealed (cols 0..5 = B, cols 5..20 = A)
	wipe.Step()
	wipe.Draw(scr)
	text := compose.Text(scr, scr.Bounds())[0]
	if text[:5] != "BBBBB" || text[5:] != "AAAAAAAAAAAAAAA" {
		t.Errorf("step 1 text = %q, want BBBBBAAAAAAAAAAAAAAA", text)
	}

	// Step 2: 2/4 revealed (cols 0..10 = B, cols 10..20 = A)
	wipe.Step()
	wipe.Draw(scr)
	text = compose.Text(scr, scr.Bounds())[0]
	if text[:10] != "BBBBBBBBBB" || text[10:] != "AAAAAAAAAA" {
		t.Errorf("step 2 text = %q, want BBBBBBBBBBAAAAAAAAAA", text)
	}

	wipe.Step()              // 3
	completed := wipe.Step() // 4

	if !completed || wipe.Active() {
		t.Fatal("wipe should be complete after 4 steps")
	}
}

func TestWipeRightToLeftStepping(t *testing.T) {
	fA := compose.NewSurface(20, 5)
	fB := compose.NewSurface(20, 5)
	compose.WriteString(fA, 0, 0, "AAAAAAAAAAAAAAAAAAAA")
	compose.WriteString(fB, 0, 0, "BBBBBBBBBBBBBBBBBBBB")

	wipe := client.NewWipeTransition(fA, fB, 20, 5, client.WipeRightToLeft, 4)

	scr := compose.NewSurface(20, 5)

	// Step 1: 1/4 revealed from right (cols 0..15 = A, cols 15..20 = B)
	wipe.Step()
	wipe.Draw(scr)
	text := compose.Text(scr, scr.Bounds())[0]
	if text[:15] != "AAAAAAAAAAAAAAA" || text[15:] != "BBBBB" {
		t.Errorf("step 1 R2L text = %q, want AAAAAAAAAAAAAAABBBBB", text)
	}
}
