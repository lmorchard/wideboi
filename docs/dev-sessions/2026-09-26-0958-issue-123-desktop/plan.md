# Plan

1. Add a loopback WebSocket to Unix transport bridge with a per-app token.
2. Build a Wails v3 manager and independent session windows around the
   existing web client.
3. Reuse the owner socketpair for new sessions; implement keep, stop, and quit
   behavior.
4. Add project matching, documentation, a macOS local bundle target, and
   browser and transport acceptance coverage.
5. Run the repository gate and manually verify the native macOS workflow.

## Validation

- `make check` passed on macOS, including Go race tests, browser acceptance,
  socket lifecycle, and PTY smoke checks.
- Desktop-tagged Go tests passed. The macOS `.app` built and its plist passed
  `plutil -lint`.
- A macOS native smoke run covered creation, one session window per session,
  project matching, closing a client window, keeping a session, and the quit
  dialog's stop action while unrelated CLI sessions remained running.
- A Linux desktop binary built in a Debian 13 container with GTK4 and
  WebKitGTK 6.0 development packages. Native Linux window behavior still
  needs verification on a graphical Linux desktop before a release artifact.
