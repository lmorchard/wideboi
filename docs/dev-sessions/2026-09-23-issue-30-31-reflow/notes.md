# Notes on Issues 30 & 31 Fix

*   **Choice made:** Incremental Reflow (dynamically reflow scrollback on resize) instead of vendoring x/vt.
*   **Reasoning:** Vendoring 6000 lines of x/vt would add significant maintenance burden for a single feature. The heuristic reflow works well enough for scrollback where perfect soft-wrap fidelity is less critical, and is fast enough to do off the main thread.
*   **Implementation:**
    *   Added `ReflowLines` which does the same logical wrapping as `Reflow` but without padding or trimming to the screen height.
    *   In `Resize()`, after doing the synchronous reflow of the main screen, scheduled a timer via `scheduleScrollbackReflow(oldCols, newCols)`.
    *   `doReflowScrollback()` executes 100ms later (debounced). It reads the raw lines from the scrollback, reflows them via `ReflowLines`, clears the scrollback, and pushes the new lines back.
    *   Bumps `generation` so clients re-render the scrollback content cleanly.
    *   Locks added (`sbReflowMu`) for concurrent scheduling safety.
*   **Tests:**
    *   Unit tests and `smoke.py` pass.
    *   Benchmarking scrollback reflow locally showed ~25ms for 10,000 lines, so debouncing to 100ms is perfectly safe and won't lock the UI on rapid resizes.
