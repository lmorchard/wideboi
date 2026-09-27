# Research: #301 Shrink the in-place upgrade snapshot

## 1. Problem Analysis & Baseline Measurements

### The Heavy Case
- **Configuration**: 8 panes × 120 columns × 40 rows visible + 10,000 styled scrollback lines per pane.
- **Total lines in scrollback**: 80,000 lines.
- **Baseline file size**: **2,041 MB (2 GB)** JSON in `$TMPDIR`.
- **Baseline old-process encode time under `s.mu`**: **5.24s - 6.3s**.
- **Baseline new-process restore time**: **18.65s** in `BenchmarkRestoreStateHeavy`.

### Root Causes
1. **Per-cell JSON representation**:
   Every cell was serialized as:
   `{"Content":"...","Width":1,"Style":{"Fg":{"Kind":...,"Index":...,"R":0,"G":0,"B":0,"A":0},"Bg":...,"UnderlineColor":...,"Underline":0,"Attrs":0}}`
   This is ~182 to 212 bytes of ASCII JSON per cell.
   80,000 lines × 120 columns = 9,600,000 cells × ~212 bytes ≈ 2,041 MB.
2. **Padding scrollback lines to full terminal width**:
   `vtGrid.ExportSnapshot()` looped `x` from 0 to `cols` (120) and created dummy blank cells (`c == nil -> protocol.CellData{Content: " ", Width: 1}`) even though `vt.Scrollback` internally trims trailing blank cells!
3. **Allocation and GC overhead**:
   9.6 million `CellData` structs and map entries allocated and traversed by Go's reflection-based `encoding/json` encoder/decoder, taking ~5.2s to marshal and ~18.6s to unmarshal.

## 2. Tested Spike Findings

We prototyped a compact representation:
1. **Style Palette**:
   Extract unique styles into `Styles []protocol.StyleData`. In typical sessions, there are 5-30 unique styles total. Style references in cells become small integers (0 for default style).
2. **Run-Length Cell Runs**:
   Terminal lines consist of text runs sharing identical styling and width.
   `CellRun{ Text string, Width int, StyleID int, Count int }`
   - Consecutive width=1 cells sharing a style combine into a single `Text` string.
   - Repeated spaces or characters combine into `Count` repeats.
   - Wide glyphs (`Width: 2`) store the wide rune in `Text` and `Width: 2`. On decode, the continuation cell (`Width: 0, Content: ""`) is faithfully regenerated.
   - Trailing empty cells are not stored (scrollback lines are naturally trimmed; screen lines default to empty).
3. **Transparent Compression (Gzip)**:
   Wrapping the JSON stream in `gzip` with magic-byte auto-detection (`0x1f, 0x8b`).

### Measured Spike Results (8 panes × 10,000 lines)

| Metric | Baseline | Compact JSON | Compact JSON + Gzip | Improvement |
|---|---|---|---|---|
| **State File Size** | 2,041 MB | 8.25 MB | **0.23 MB (235 KB)** | **8,685x smaller (99.99%)** |
| **Marshal Time (8 panes)** | 5,239 ms | 13.4 ms | **24.2 ms** (incl. gzip) | **216x - 390x faster** |
| **Unmarshal Time (8 panes)** | 18,645 ms | 16.7 ms | **23.2 ms** (incl. gunzip)| **800x - 1,116x faster** |
| **Input Loss Window (`s.mu`)**| ~6,300 ms | ~70 ms | **~80 ms** | **98.7% reduction** |
| **Fidelity** | Baseline | 100% cell-by-cell match | 100% cell-by-cell match | Full fidelity |

## 3. Backward Compatibility & Upgrade Safety

- `RestoreState` must auto-detect whether the state file is gzipped (via magic bytes `0x1f, 0x8b`) or uncompressed JSON.
- `vtGrid.RestoreSnapshot` must support both:
  - New compact representation (`CompactScrollback`, `CompactScreen`, `Styles`)
  - Legacy representation (`Scrollback []protocol.LineData`, `Screen []protocol.LineData`)
- When an old server running the previous binary upgrades to the new binary:
  The old server writes legacy uncompressed JSON. The new server reads it correctly.
- When the new binary prepares an upgrade:
  It writes the compact gzipped state.
- If a future upgrade occurs, both ends use the compact format.
