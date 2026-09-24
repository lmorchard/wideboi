# notes

- Added a `web/dist` target to the `Makefile` which automatically runs `npm install && npm run build` inside `web/` to produce the static files.
- Added `web/embed.go` with `//go:embed dist` which exposes the static assets through `DistFS()`.
- Updated the HTTP multiplexer in `cmd/wideboi/main.go` to serve these files using `http.FileServer` on any route not matching `/ws` (by catching `/`).
- Updated Makefile targets (build, test, lint, race) to depend on `web/dist` to avoid `go:embed` errors on a fresh clone.
- Confirmed that starting `wideboi server --websocket :8080` serves the web client correctly without requiring `vite` to be running separately.
