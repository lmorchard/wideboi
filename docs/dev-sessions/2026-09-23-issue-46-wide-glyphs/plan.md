# Plan

- Update `compose.Blit` to take both `srcRect` and `dstRect` so it accurately knows both the crop region of `src` and where it lands in `dst`.
- Add logic in `Blit` loop to properly handle wide glyphs overlapping with the clipping borders (`dstClip.Min.X` and `dstClip.Max.X`).
- Skip drawing a wide glyph if its start cell or continuation cell is clipped.
- Render empty spaces with the matching background styling where these wide glyphs were clipped to avoid visual holes.
- Make `internal/client/client.go` pass `p.Src` and `p.Dst` when blitting pane content.
- Add tests `TestBlitClipsWideGlyphs` to ensure wide glyphs are properly omitted at boundaries.