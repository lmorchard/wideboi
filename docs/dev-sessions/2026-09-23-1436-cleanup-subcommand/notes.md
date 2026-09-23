# Notes

- Created `wideboi cleanup` subcommand to clean up dead session artifacts (sockets and logs).
- Left `*.lock` files alone as requested, to avoid reopening ownership races.
- Added tests for `cleanup` using `net.Listen("unix", ...)` to simulate active sockets vs dead sockets.
- Checked `make check` - it passes cleanly.
- Filed issue #119 for later consideration of an auto-cleanup hook, so we keep the immediate scope restricted and safe.
