# Implementation Plan: #301 Shrink the in-place upgrade snapshot

## Goal
Dramatically reduce the upgrade snapshot size (from 2,041 MB to < 1 MB gzipped / ~8 MB uncompressed), reduce encode time under `s.mu` (from ~6.3s to < 100ms), and reduce `RestoreState` time (from ~18.6s to < 100ms), while maintaining 100% fidelity and backward compatibility for reading legacy snapshots.

## Phases

### Phase 1: Compact Terminal Line Representation in `internal/server/term`
- Define `CellRun`, `LineSnapshot`, and update `GridSnapshot` in `internal/server/term/grid.go`.
- Implement `encodeLine` and `decodeLine` functions in `internal/server/term/compact.go`.
- Update `vtGrid.ExportSnapshot()`:
  - Extract `Styles []protocol.StyleData` palette.
  - Export `ScrollbackLines []LineSnapshot` directly from `g.em.Scrollback().Lines()` (already trimmed).
  - Export `ScreenLines []LineSnapshot` from visible screen rows, trimming trailing empty cells.
- Update `vtGrid.RestoreSnapshot()`:
  - If `len(snap.ScrollbackLines) > 0 || len(snap.ScreenLines) > 0`, restore using compact lines.
  - If legacy `snap.Scrollback` or `snap.Screen` is populated, restore using legacy line data (preserving backward compatibility).
- Unit tests in `internal/server/term/snapshot_test.go`:
  - Verify compact round-trip with:
    - plain ASCII
    - multiple styles & colors
    - repeated characters & styled spaces
    - wide glyphs (CJK, emoji) and continuation cells
    - empty lines & trailing spaces
    - legacy snapshot backward compatibility test

### Phase 2: Transparent Gzip Compression in `internal/server/upgrade.go`
- Update `PrepareUpgrade` to write gzipped JSON.
- Update `RestoreState` to auto-detect gzip magic bytes (`0x1f, 0x8b`) and wrap in `gzip.NewReader`, falling back to uncompressed JSON.
- Unit tests in `internal/server/upgrade_test.go`:
  - Test round-trip with gzip-compressed state.
  - Test backward compatibility: restore an uncompressed legacy state file.

### Phase 3: Benchmarks & Verification
- Update `BenchmarkRestoreStateHeavy` and `BenchmarkEncodeStateHeavy` in `internal/server/upgrade_bench_test.go`.
- Run benchmarks to verify:
  - Size reduction (> 99%)
  - Encode speedup (> 50x)
  - Restore speedup (> 50x)
- Run full test suite (`make check`) to ensure no regressions.
