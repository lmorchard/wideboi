package term

import (
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/lmorchard/wideboi/internal/protocol"
)

// CellRun represents a run of cells sharing the same style and width characteristics.
// For the vast majority of runs (width 1, single runes, default or uniform style),
// Width is 0 (omitted) and Count is 0 (omitted), meaning each rune in Text represents
// 1 cell of width 1.
// If Count > 0, Text represents the exact content of each cell (preserving multi-rune
// grapheme clusters like emoji sequences or combined characters without splitting them).
type CellRun struct {
	Text    string `json:"t,omitempty"`
	Width   int    `json:"w,omitempty"` // 0: standard width 1; >0: explicit width (e.g. 2 for wide glyph); <0 (-1): explicit zero width
	StyleID int    `json:"s,omitempty"` // index into GridSnapshot.Styles (0 is default/zero style)
	Count   int    `json:"c,omitempty"` // repeat count if Text is repeated or represents a multi-rune cell; 0 means each rune in Text
}

// LineSnapshot represents a single terminal line as a sequence of CellRuns.
type LineSnapshot []CellRun

type stylePalette struct {
	styleMap map[protocol.StyleData]int
	styles   []protocol.StyleData
}

func newStylePalette() *stylePalette {
	p := &stylePalette{
		styleMap: make(map[protocol.StyleData]int),
	}
	// Index 0 is always the zero/default style.
	zero := protocol.StyleData{}
	p.styles = append(p.styles, zero)
	p.styleMap[zero] = 0
	return p
}

func (p *stylePalette) idFor(s uv.Style) int {
	if s.IsZero() {
		return 0
	}
	sd := protocol.EncodeStyle(s)
	if id, ok := p.styleMap[sd]; ok {
		return id
	}
	id := len(p.styles)
	p.styles = append(p.styles, sd)
	p.styleMap[sd] = id
	return id
}

// encodeUVLine converts a slice of ultraviolet cells into a compact LineSnapshot,
// deduplicating styles via the provided stylePalette. Trailing empty cells are trimmed.
func encodeUVLine(line uv.Line, palette *stylePalette) LineSnapshot {
	lastNonEmpty := -1
	for i := len(line) - 1; i >= 0; i-- {
		c := &line[i]
		if !c.IsZero() && !c.Equal(&uv.EmptyCell) {
			lastNonEmpty = i
			break
		}
	}
	if lastNonEmpty < 0 {
		return nil
	}

	cells := line[:lastNonEmpty+1]
	var runs LineSnapshot
	var curRun CellRun
	var textBuf strings.Builder

	flush := func() {
		if curRun.Count > 0 || textBuf.Len() > 0 {
			if curRun.Count == 0 {
				curRun.Text = textBuf.String()
			}
			if curRun.Width == 1 {
				curRun.Width = 0 // omit default width 1
			}
			runs = append(runs, curRun)
			curRun = CellRun{}
			textBuf.Reset()
		}
	}

	continuationRemaining := 0

	for i := 0; i < len(cells); i++ {
		c := &cells[i]

		// Skip continuation cells of wide glyphs (Width == 0 && Content == "").
		// The decoder reconstructs them from the preceding cell's Width > 1.
		if continuationRemaining > 0 && c.Width == 0 && c.Content == "" {
			continuationRemaining--
			continue
		}
		continuationRemaining = 0

		sID := palette.idFor(c.Style)
		w := c.Width
		if w < 0 {
			w = 1
		}
		if w > 1 {
			continuationRemaining = w - 1
		}

		storedWidth := w
		if w == 0 {
			storedWidth = -1 // flag explicit zero width
		}

		runeCount := utf8.RuneCountInString(c.Content)

		// Multi-rune grapheme cluster or empty string: must preserve exact cell boundaries
		if runeCount != 1 {
			flush()
			curRun = CellRun{
				Text:    c.Content,
				Width:   storedWidth,
				StyleID: sID,
				Count:   1,
			}
			flush()
			continue
		}

		// Single-rune cell: can combine into text run if style and width match
		if curRun.Count == 0 && textBuf.Len() > 0 && curRun.StyleID == sID && curRun.Width == storedWidth {
			textBuf.WriteString(c.Content)
			continue
		}

		flush()
		curRun = CellRun{
			Width:   storedWidth,
			StyleID: sID,
		}
		textBuf.WriteString(c.Content)
	}
	flush()
	return runs
}

// decodeUVLine converts a LineSnapshot back into a uv.Line with full fidelity.
func decodeUVLine(runs LineSnapshot, styles []uv.Style) uv.Line {
	if len(runs) == 0 {
		return nil
	}
	var line uv.Line
	for _, r := range runs {
		var st uv.Style
		if r.StyleID >= 0 && r.StyleID < len(styles) {
			st = styles[r.StyleID]
		}
		cellW := r.Width
		if cellW == 0 {
			cellW = 1 // default width
		} else if cellW == -1 {
			cellW = 0 // explicit zero width
		}

		if r.Count > 0 {
			// Exact cell representation: r.Text is the content of each of r.Count cells
			content := r.Text
			for i := 0; i < r.Count; i++ {
				line = append(line, uv.Cell{Content: content, Width: cellW, Style: st})
				if cellW > 1 {
					for k := 1; k < cellW; k++ {
						line = append(line, uv.Cell{Content: "", Width: 0})
					}
				}
			}
		} else {
			// Run of single-rune cells
			for _, ch := range r.Text {
				line = append(line, uv.Cell{Content: string(ch), Width: cellW, Style: st})
				if cellW > 1 {
					for k := 1; k < cellW; k++ {
						line = append(line, uv.Cell{Content: "", Width: 0})
					}
				}
			}
		}
	}
	return line
}
