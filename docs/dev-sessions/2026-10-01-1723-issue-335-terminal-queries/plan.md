# Plan: Issue 335 Terminal Environment Queries

## Proposed Changes

### Phase 1: Implement `queryScanner` in `internal/server/queries.go`
1. Define palette colors (standard 256-color palette RGB strings).
2. Define `QueryTheme` (`FgColor`, `BgColor`, `IsDark`) with sensible dark defaults (`1e1e/1e1e/1e1e` bg, `d4d4/d4d4/d4d4` fg).
3. Implement `queryScanner`:
   - `process(chunk []byte, cols, rows int) (cleaned []byte, responses []byte)`
   - `matchQuery(b []byte, cols, rows int, theme QueryTheme) (consumed int, reply []byte, partial bool)`
   - Support `OSC 10;?`, `OSC 11;?`, `OSC 4;N;?`, `CSI 14t`, `CSI 16t`, `CSI 18t`, and `CSI ? 996 n`.
   - Preserve `\x07` vs `\x1b\\` terminators.
   - Buffer in-flight queries across chunks with bounds checks.

### Phase 2: Integrate into `internal/server/pane.go`
1. In `Pane.Start()` reader pump:
   - Initialize `qs := newQueryScanner()`.
   - On `p.pty.Master.Read`:
     - Run `cleaned, replies := qs.process(buf[:n], cols, rows)`.
     - If `len(replies) > 0`, write to `p.pty.WriteBounded(replies, ptyWriteTimeout)`.
     - If `len(cleaned) > 0`, write to `p.grid.Write(cleaned)`.

### Phase 3: Tests & Verification
1. Add `internal/server/queries_test.go`:
   - `TestQueryScannerSynthesizesOSC10And11`
   - `TestQueryScannerSynthesizesOSC4Palette`
   - `TestQueryScannerSynthesizesCSISizes`
   - `TestQueryScannerPassesNonQueriesThrough`
   - `TestQueryScannerHandlesSplitPackets`
2. Run full test suite:
   - `go test ./...`
   - `npm test` and `npm run test:browser`
   - `smoke.py`, `attachcheck.py`, `golden.py`, `seam-check`
