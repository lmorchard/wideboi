# Issue #70 implementation notes

- Protocol v11 adds a per-pane width map to size claims and an exact pane-width request.
- The server applies a width request only from its current size owner; a claim transfers ownership and applies the claimant's widths.
- Terminal and browser clients keep local widths and horizontal pan independently of PTYs and other clients. Follow PTY is client-wide and defaults off.
- Terminal `H`/`L` pan by `pan_step` (default 10), and `f` toggles Follow PTY. Typing and paste reveal the cursor without moving pan on background output.
- The browser's card-only global width selector became a per-pane width control available in both layouts.
- Verification: focused Go and browser tests plus `make check` outside the sandbox, where local socket and PTY binds are available.
