# wideboi — notes for agents

A scrolling tiling terminal multiplexer for CLI coding agents. `README.md` covers
what it does and how to drive it; this file covers what is easy to get wrong.

**Read `docs/LESSONS.md` before changing anything.** It is the evergreen home for
things that have already cost someone a wasted round. Add to it when the project
teaches you something; session-scoped notes go under `docs/dev-sessions/` instead.

## GitHub Project

- **Owner:** `lmorchard`
- **Number:** `10`
- **Status field:** `Status`
- **Columns:**
  - `ready: Backlog`
  - `in_progress: In progress`
  - `in_review: In review`
  - `done: Done`

Two things about that mapping are deliberate:

- **`ready` points at `Backlog`, not `Ready`.** `ready` is the skill's key for
  "where a freshly filed spec lands". On this board `Ready` means *picked, and
  next up* — a human decision — so `/dev-session file` drops new issues into
  `Backlog` and Les promotes them.
- **Sentence case.** `In progress`, not `In Progress`. A name that does not
  match the board does not error, it silently no-ops every transition.

`Priority` (P0–P3) and `Size` (XS–XL) exist and the skill does not set them.
Curating those is a human job.

## The premise everything rests on

**A pane's width is independent of whether it is visible.** Open more panes and
the viewport scrolls; nothing already on screen gets squeezed. This is the whole
point of the project, and it is the invariant most likely to be broken by a
plausible-looking geometry change — see `TestReservedToggleVerbChangesNothing`
in `internal/server/status_test.go`, `internal/server/server.go:240`,
`internal/layout/layout.go:247`.

## Architecture, in four facts

- **There is a client/server seam, and it is enforced.** `internal/client` must
  not import `internal/server` or vice versa; they share `internal/layout` and
  `internal/protocol` only. `make seam-check` fails on a new crossing, with
  existing ones allowlisted in `scripts/seam-check.sh` so it catches the *next*
  one.
- **The server ships cell data, not PTY bytes** (`internal/protocol.CellData`).
  A wire format that cannot encode a styled cell passes every in-process test
  and dies on the first coloured prompt over a socket.
- **Placements and layout mode are client-side** (`internal/client`). The
  server broadcasts columns and focus and computes no placements (#47); each
  client picks and toggles its own layout (#92).
- **Layout is a pure core** (`internal/layout`) with `Strategy` implementations
  for the scrolling strip and the card fan. Geometry logic belongs there, not in
  the client or the server.

## Working here

    make quick    # the edit loop: fmt, vet, seam, go tests. ~5s
    make check    # the gate: adds race, exit contract, smoke, attach. ~50s

`make check` is parallel by default (`CHECK_JOBS=1` for serial). It is cheap now
— there is no reason to skip it before pushing.

Use the `dev-session` skill for anything beyond a small fix; artifacts land in
`docs/dev-sessions/<timestamp>-<slug>/`. Branch and PR rather than committing to
`main`.

## Testing conventions that will surprise you

- **The pty harness pins its environment on purpose.** `scripts/ptylib.py` forces
  `SHELL`, `TERM`, `PS1`, the layout (no `WIDEBOI_LAYOUT`, an empty config
  home) *and* the child's signal dispositions, each because an
  assertion depends on it and each with a comment saying which. If a test seems
  to care about the environment, that is why — do not relax the assertion, pin
  the thing.
- **Waits are ceilings, not durations.** Everything waits for observed state and
  uses the old fixed value as a timeout. Raising a ceiling costs nothing when
  things are fast; adding a `sleep` costs everyone, every run.
- **A timing or concurrency change is unverified until it has repeated.** Run it
  four times. One green run is how a flaky suite presents, and this project has
  been bitten three times.
- **Prove a new test fails for the reason you think** before trusting it green.

## Dependencies are pre-1.0 and do not behave as you would assume

`charmbracelet/x/vt` has no tagged release and is pinned to a pseudo-version;
`ultraviolet` is similar. Read the vendored source rather than assuming an API
does what its name suggests — several lessons in `docs/LESSONS.md` exist because
someone did not.
