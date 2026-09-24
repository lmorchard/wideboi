# plan

## Phase 1: Build the Vite project in Makefile

**Files:**
- `Makefile`
- `web/embed.go` (new)

**Key changes:**
- Create `web/embed.go` to expose the embedded `dist` directory as an `http.FileSystem`.
- Add `//go:embed dist` to `web/embed.go`.
- Modify `Makefile` to add a `web/dist` target that runs `cd web && npm install && npm run build`.
- Make `build`, `test`, `lint`, `seam-check` depend on `web/dist` so that `go build` and `go test` succeed (since `//go:embed` requires the directory to exist).

**Verification — automated:**
- [x] `make web/dist` creates the directory.
- [x] `make check` passes.

**Verification — manual:**
- [x] Check `web/dist` contains the compiled HTML/JS/CSS assets.

## Phase 2: Serve static files on the HTTP server

**Files:**
- `cmd/wideboi/main.go`

**Key changes:**
- In `runServer`, after setting up the WebSocket handler on `mux`, get the filesystem from `web.DistFS()`.
- Add a fallback handler to `mux` (e.g. `mux.Handle("/", http.FileServer(distFS))`) so any non-websocket request serves the static assets.

**Verification — automated:**
- [x] `make check` passes.

**Verification — manual:**
- [x] Start the server with `wideboi server --websocket :8080`.
- [x] Navigate to `http://localhost:8080` in a browser.
- [x] Verify the web client loads and can connect to the websocket.
