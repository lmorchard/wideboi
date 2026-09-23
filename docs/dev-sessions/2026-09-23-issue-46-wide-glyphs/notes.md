# Dev Session Notes

- Re-wrote `compose.Blit` to take both `srcRect` and `dstRect` so it doesn't just draw `src` from `0, 0` anymore (a regression waiting to happen).
- Added logic to `Blit` to explicitly drop wide glyphs if they are bisected by the clip bounds. Specifically, if a wide glyph begins before `dstClip.Min.X` (we start parsing the continuation cell), we emit a space with the matching background style instead. And if the wide glyph sticks out beyond `dstClip.Max.X`, we emit a space with matching background and halt processing that row early.
- Passed `p.Src` along with `p.Dst` in `internal/client/client.go` to explicitly fix the partial scroll-off pane rendering issues.
- Added tests for `compose.Blit` in `internal/client/compose/surface_test.go` and `blit_test.go` to verify clipping.
- Ran `make check` and verified all tests pass including the `partly clipped pane keeps full width` smoke test and wire tests.