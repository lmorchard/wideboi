Web Client: Embed UI assets in Go binary and serve statically

Currently, the Web Client (#29) is a separate Vite project in the `web/` directory that requires running `npm run dev` to serve the HTML/JS assets.

To make this a seamless, zero-dependency experience for users, we should ship the web client inside the `wideboi` executable:

1. Create a "production" build pipeline for the Vite project (compiling down to minified static HTML/JS/CSS).
2. Use Go's `//go:embed` directive to bundle the `web/dist/` static assets directly into the Go binary.
3. Add a built-in static HTTP file server to the Go backend, running on the same `http.ServeMux` as the WebSocket server.

This will allow users to simply run `wideboi server --websocket :8080`, navigate their browser to `http://localhost:8080`, and instantly access the web client without needing Node.js or a separate dev server.
