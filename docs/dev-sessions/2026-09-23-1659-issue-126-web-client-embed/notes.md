# notes

- Added a `web/dist` target to the `Makefile` which automatically runs `npm install && npm run build` inside `web/` to produce the static files.
- Added `web/embed.go` with `//go:embed dist` which exposes the static assets through `DistFS()`.
- Updated the HTTP multiplexer in `cmd/wideboi/main.go` to serve these files using `http.FileServer` on any route not matching `/ws` (by catching `/`).
- Updated Makefile targets (build, test, lint, race) to depend on `web/dist` to avoid `go:embed` errors on a fresh clone.
- Confirmed that starting `wideboi server --websocket :8080` serves the web client correctly without requiring `vite` to be running separately.

## Retrospective
- **Brief recap:** Built a robust automated integration of a Vite-built static web app into the Go HTTP server. Implemented Make targets to compile assets before embedding.
- **Scope drift:** None. Remained tightly aligned to the issue requirements.
- **Surprises:**  requires the targeted directory to exist. I had to ensure make: Nothing to be done for `web/dist'. runs and correctly links into , ,  and  so that commands don't fail mysteriously on fresh checkouts.
- **Workflow friction:** Smooth end-to-end. Discovered one stray issue: since  isn't sticky on commands, running tests without moving to the worktree accidentally picked up unrelated files in the root. Fixed by specifying  explicitly.
- **Misses:** Should have remembered how  resolves relative embedding paths.


## Retrospective
- **Brief recap:** Built a robust automated integration of a Vite-built static web app into the Go HTTP server. Implemented Make targets to compile assets before embedding.
- **Scope drift:** None. Remained tightly aligned to the issue requirements.
- **Surprises:** `//go:embed` requires the targeted directory to exist. I had to ensure `make web/dist` runs and correctly links into `build`, `test`, `race` and `lint` so that commands don't fail mysteriously on fresh checkouts.
- **Workflow friction:** Smooth end-to-end. Discovered one stray issue: since `workdir` isn't sticky on commands, running tests without moving to the worktree accidentally picked up unrelated files in the root. Fixed by specifying `workdir` explicitly.
- **Misses:** None significant.
