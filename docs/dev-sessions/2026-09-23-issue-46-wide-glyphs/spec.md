# Wide glyphs can be split at a moving pane boundary

A wide glyph can be split when a pane boundary lands mid-glyph.

The old directional wipe cut at a hard column index, which was the obvious version of this hazard; Plan 18 deleted it. **Placement interpolation clips whole pane rects instead, so the hazard moved rather than went away** — it is now wherever a pane's `Dst` boundary can land mid-glyph during a transition, and that is harder to reason about than a single seam was, because every pane edge moves.

Untested either way. This codebase has been bitten by the wide-glyph class twice already — see `docs/LESSONS.md`.