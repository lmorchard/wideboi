package client

import (
	"image"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/client/compose"
)

type Direction int

const (
	WipeLeftToRight Direction = iota // Focus moving right
	WipeRightToLeft                  // Focus moving left
)

// WipeTransition renders progressive directional column wipes between frame A and B.
type WipeTransition struct {
	frameA     compose.Surface
	frameB     compose.Surface
	cols       int
	rows       int
	dir        Direction
	step       int
	totalSteps int
}

// NewWipeTransition initializes a WipeTransition instance.
func NewWipeTransition(frameA, frameB compose.Surface, cols, rows int, dir Direction, totalSteps int) *WipeTransition {
	if totalSteps <= 0 {
		totalSteps = 8
	}
	return &WipeTransition{
		frameA:     frameA,
		frameB:     frameB,
		cols:       cols,
		rows:       rows,
		dir:        dir,
		step:       0,
		totalSteps: totalSteps,
	}
}

// Step advances the transition by one frame step. Returns true when complete.
func (wt *WipeTransition) Step() bool {
	if wt.step < wt.totalSteps {
		wt.step++
	}
	return wt.step >= wt.totalSteps
}

// Active reports whether the transition is currently in progress.
func (wt *WipeTransition) Active() bool {
	return wt.step < wt.totalSteps
}

// Fits reports whether this transition's frames were composed for the
// given viewport.
//
// A resize part-way through a transition leaves the frames sized for
// the old viewport while the client's cols/rows have already moved on.
// Blitting them anyway paints the previous layout for the rest of the
// transition, so the caller drops the wipe and snaps instead.
func (wt *WipeTransition) Fits(cols, rows int) bool {
	return wt.cols == cols && wt.rows == rows
}

// Draw renders the progressive wipe state onto host screen scr.
func (wt *WipeTransition) Draw(scr uv.Screen) {
	if !wt.Active() {
		compose.Blit(scr, wt.frameB, image.Rect(0, 0, wt.cols, wt.rows))
		return
	}

	revealCol := (wt.cols * wt.step) / wt.totalSteps

	if wt.dir == WipeLeftToRight {
		if revealCol > 0 {
			compose.Blit(scr, wt.frameB, image.Rect(0, 0, revealCol, wt.rows))
		}
		if revealCol < wt.cols {
			compose.Blit(scr, wt.frameA, image.Rect(revealCol, 0, wt.cols, wt.rows))
		}
	} else {
		splitCol := max(0, wt.cols-revealCol)
		if splitCol < wt.cols {
			compose.Blit(scr, wt.frameB, image.Rect(splitCol, 0, wt.cols, wt.rows))
		}
		if splitCol > 0 {
			compose.Blit(scr, wt.frameA, image.Rect(0, 0, splitCol, wt.rows))
		}
	}
}
