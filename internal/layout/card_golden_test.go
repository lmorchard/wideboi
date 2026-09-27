package layout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type GoldenColumn struct {
	PaneID int `json:"paneId"`
	Width  int `json:"width"`
}

type GoldenPlacement struct {
	PaneID  int  `json:"paneId"`
	Left    int  `json:"left"`
	Visible bool `json:"visible"`
	Z       int  `json:"z"`
}

type GoldenCase struct {
	Name          string            `json:"name"`
	Columns       []GoldenColumn    `json:"columns"`
	FocusedPaneID int               `json:"focusedPaneId"`
	ViewportWidth int               `json:"viewportWidth"`
	PreviousFirst int               `json:"previousFirst"`
	First         int               `json:"first"`
	HiddenLeft    int               `json:"hiddenLeft"`
	HiddenRight   int               `json:"hiddenRight"`
	Placements    []GoldenPlacement `json:"placements"`
}

func generateGoldenCases() []GoldenCase {
	makeCols := func(widths ...int) []GoldenColumn {
		cols := make([]GoldenColumn, len(widths))
		for i, w := range widths {
			cols[i] = GoldenColumn{PaneID: i + 1, Width: w}
		}
		return cols
	}

	scenarios := []struct {
		name          string
		cols          []GoldenColumn
		focusID       int
		viewportWidth int
		prevFirst     int
	}{
		{
			name:          "single column fits viewport",
			cols:          makeCols(40),
			focusID:       1,
			viewportWidth: 80,
			prevFirst:     0,
		},
		{
			name:          "single column clamped by viewport",
			cols:          makeCols(60),
			focusID:       1,
			viewportWidth: 30,
			prevFirst:     0,
		},
		{
			name:          "two columns wide viewport",
			cols:          makeCols(40, 40),
			focusID:       1,
			viewportWidth: 100,
			prevFirst:     0,
		},
		{
			name:          "two columns focused second",
			cols:          makeCols(40, 40),
			focusID:       2,
			viewportWidth: 100,
			prevFirst:     0,
		},
		{
			name:          "two columns narrow viewport",
			cols:          makeCols(40, 40),
			focusID:       1,
			viewportWidth: 42,
			prevFirst:     0,
		},
		{
			name:          "three columns center focus",
			cols:          makeCols(40, 40, 40),
			focusID:       2,
			viewportWidth: 80,
			prevFirst:     0,
		},
		{
			name:          "three columns right focus",
			cols:          makeCols(40, 40, 40),
			focusID:       3,
			viewportWidth: 80,
			prevFirst:     0,
		},
		{
			name:          "six columns focus 3 in 42 cols",
			cols:          makeCols(30, 30, 30, 30, 30, 30),
			focusID:       3,
			viewportWidth: 42,
			prevFirst:     0,
		},
		{
			name:          "six columns focus 6 window scroll",
			cols:          makeCols(30, 30, 30, 30, 30, 30),
			focusID:       6,
			viewportWidth: 42,
			prevFirst:     0,
		},
		{
			name:          "six columns focus 5 narrow viewport fills card",
			cols:          makeCols(30, 30, 30, 30, 30, 30),
			focusID:       5,
			viewportWidth: 25,
			prevFirst:     0,
		},
		{
			name:          "six columns focus 4 with previousFirst 2",
			cols:          makeCols(30, 30, 30, 30, 30, 30),
			focusID:       4,
			viewportWidth: 42,
			prevFirst:     2,
		},
		{
			name:          "ten columns wide viewport with large window",
			cols:          makeCols(50, 60, 40, 80, 50, 60, 70, 40, 50, 60),
			focusID:       7,
			viewportWidth: 150,
			prevFirst:     3,
		},
		{
			name:          "ten columns focus at right edge keeps window stable",
			cols:          makeCols(30, 30, 30, 30, 30, 30, 30, 30, 30, 30),
			focusID:       10,
			viewportWidth: 60,
			prevFirst:     5,
		},
		{
			name:          "ten columns focus at left edge scrolls back",
			cols:          makeCols(30, 30, 30, 30, 30, 30, 30, 30, 30, 30),
			focusID:       1,
			viewportWidth: 60,
			prevFirst:     5,
		},
		{
			name:          "eight columns uneven remaining width shares",
			cols:          makeCols(35, 45, 55, 65, 35, 45, 55, 65),
			focusID:       4,
			viewportWidth: 95,
			prevFirst:     1,
		},
	}

	cases := make([]GoldenCase, len(scenarios))
	for i, sc := range scenarios {
		gc := computeCardLayout(sc.cols, sc.focusID, sc.viewportWidth, sc.prevFirst)
		gc.Name = sc.name
		cases[i] = gc
	}
	return cases
}

func computeCardLayout(columns []GoldenColumn, focusedPaneID, viewportWidth, previousFirst int) GoldenCase {
	if len(columns) == 0 || viewportWidth <= 0 {
		placements := make([]GoldenPlacement, len(columns))
		for i, c := range columns {
			placements[i] = GoldenPlacement{PaneID: c.PaneID, Left: 0, Visible: false, Z: 0}
		}
		return GoldenCase{
			Columns:       columns,
			FocusedPaneID: focusedPaneID,
			ViewportWidth: viewportWidth,
			PreviousFirst: previousFirst,
			First:         0,
			HiddenLeft:    0,
			HiddenRight:   0,
			Placements:    placements,
		}
	}

	s := NewStrip()
	for _, col := range columns {
		s.AddColumn(col.PaneID, col.Width, 20, 0)
	}
	s.FocusPaneID(focusedPaneID)
	s.cardFirst = previousFirst

	focusedIdx := s.focusIndex
	focusedW := min(s.columns[focusedIdx].Width, viewportWidth)
	remaining := max(viewportWidth-focusedW, 0)

	cs := CardStrategy{}
	showLeft, showRight := cs.visibleSides(s, remaining)
	first := s.cardFirst
	size := showLeft + showRight + 1
	last := first + size - 1
	slivers := size - 1
	sliverIdx := 0
	x := 0

	placements := make([]GoldenPlacement, len(columns))
	for i, col := range columns {
		if i < first || i > last {
			placements[i] = GoldenPlacement{PaneID: col.PaneID, Left: 0, Visible: false, Z: 0}
			continue
		}
		left := x
		if i == focusedIdx {
			x += focusedW
		} else {
			share := shareAt(remaining, slivers, sliverIdx)
			w := min(share, col.Width)
			x += w
			sliverIdx++
		}
		z := i + 1
		if col.PaneID == focusedPaneID {
			z = len(columns) + 1
		}
		placements[i] = GoldenPlacement{PaneID: col.PaneID, Left: left, Visible: true, Z: z}
	}

	return GoldenCase{
		Columns:       columns,
		FocusedPaneID: focusedPaneID,
		ViewportWidth: viewportWidth,
		PreviousFirst: previousFirst,
		First:         first,
		HiddenLeft:    first,
		HiddenRight:   len(columns) - last - 1,
		Placements:    placements,
	}
}

func TestCardLayoutGoldenFixtures(t *testing.T) {
	cases := generateGoldenCases()
	data, err := json.MarshalIndent(cases, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden cases: %v", err)
	}
	data = append(data, '\n')

	goldenPath := filepath.Join("..", "..", "web", "src", "fixtures", "card-layout-golden.json")
	existing, err := os.ReadFile(goldenPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.WriteFile(goldenPath, data, 0644); err != nil {
				t.Fatalf("write golden file: %v", err)
			}
			return
		}
		t.Fatalf("read golden file: %v", err)
	}

	if string(existing) != string(data) {
		t.Fatalf("golden fixture %s does not match Go CardStrategy layout; regenerate with go test ./internal/layout", goldenPath)
	}
}
