# Notes

The macOS native smoke test created two sessions in a temporary project
folder. The first client window connected automatically, and closing it left
the owned session in the manager. The manager offered the existing/new choice.
Keeping the first session released ownership; the quit dialog appeared for
the second and stopped only that owned session. The detached first session
was then stopped through a confirmation that named it explicitly. Existing
user sessions were left untouched.

The first manager draft rebuilt its session rows on every poll, invalidating
accessibility targets. It now rerenders only when session data changes.
Generic Stop labels were ambiguous, so every action has a session-specific
accessible name. JavaScript `confirm()` did not display reliably in the
Wails webview and was replaced with inline confirmation.

The first `make quick` failed only under this API session's `NO_COLOR=1` and
`TERM=dumb`; the client theme tests expect color. It passed with
`NO_COLOR` unset and `TERM=xterm-256color`.
