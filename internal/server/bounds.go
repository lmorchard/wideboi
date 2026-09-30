package server

import (
	"github.com/lmorchard/wideboi/internal/protocol"
)

const (
	maxClientCols  = 4096
	maxClientRows  = 2048
	maxClientCells = 4096 * 2048 // 8,388,608 cells

	maxWaitersPerPane = 64
	maxTotalWaiters   = 256

	maxMacrosCount   = 128
	maxMacroNameLen  = 128
	maxMacroStepLen  = 4096
	maxMacroTotalLen = 64 << 10 // 64 KiB
	maxMacroQueue    = 64
)

// validGeometry validates client-provided rows and columns against maximum bounds
// and allocation budgets, guarding against arithmetic overflow.
func validGeometry(cols, rows int) bool {
	if cols <= 0 || rows <= 0 {
		return false
	}
	if cols > maxClientCols || rows > maxClientRows {
		return false
	}
	cells := int64(cols) * int64(rows)
	if cells <= 0 || cells > maxClientCells {
		return false
	}
	return true
}

// validMacros validates macro count and payload lengths before persistence.
func validMacros(macros []protocol.Macro) bool {
	if len(macros) > maxMacrosCount {
		return false
	}
	totalLen := 0
	for _, m := range macros {
		if len(m.Name) > maxMacroNameLen {
			return false
		}
		totalLen += len(m.Name)
		for _, step := range m.Steps {
			stepLen := len(step.Text) + len(step.Key) + len(step.Code)
			if stepLen > maxMacroStepLen {
				return false
			}
			totalLen += stepLen
		}
		if totalLen > maxMacroTotalLen {
			return false
		}
	}
	return true
}
