# Notes — issue-127-protobuf

## Phase 1

- **Spec deviation: generation counters are `bigint` in the browser, not
  `number`.** protobuf-es 2.15 honours only `jstype = JS_STRING`
  (`web/node_modules/@bufbuild/protobuf/dist/esm/registry.js:653` sets
  `longAsString`); `JS_NUMBER` is silently ignored. Dropped the option.
  bigint compares by value with `===`/`<=`, and TypeScript rejects mixing
  bigint with number at compile time, so the renderer's generation checks stay
  safe as long as every generation value is typed bigint. Rejected: JS_STRING
  (string ordering is wrong for `<=`) and uint32 on the wire (changes the
  counter's range). Phase 3 renderer/tests use `1n`-style literals.
- `buf.gen.yaml` is v2 and runs protoc-gen-go via `go run` from go.mod, so no
  install is needed and the generator version tracks the module.
- `isCleanClose` on main already includes `io.ErrUnexpectedEOF`; phase 2
  only carries over the tests that pin it.

## Phase 2

- `internal/transport/wire_test.go`'s `roundtrip` goes through codec *and*
  framing, so it exercises the pumps' exact path.
- The connErr doc comment still mentions gob — it is the history of why
  connErr exists, and still accurate.

## Phase 3

- **Vitest does not typecheck.** A test fixture with a type error passed
  `npm test` and failed `make check`, whose `web/dist` build runs `tsc` over
  the tests too. `make check` is the gate for web type errors; `web-test`
  alone is not.
- protobuf-es `create()` typing: spreading a complete message
  (`{ ...pane, lines: [...] }`) makes nested fields require complete messages,
  not init objects. Write fixtures out in full or build nested values with
  `create()`.
- Mutation checks: dropping the cursor overwrite in `handlePanePatch` (the
  "absent means unchanged" bug) fails both patch tests.
- Web fixtures in `protobuf.test.ts` come from Go's codec (throwaway test
  printed hex; deleted).
- The lifecycle test now also has the live socket deliver a binary frame, so
  it covers `client.ts` decoding, not just stale-socket filtering.

## PR self-review

Fresh-eyes review (separate agent, opus) found no bugs. Acted on:
- `fill` produced only small positive ints, so a sign/width conversion bug
  could round-trip by luck. Ints are now ±(2^30+n); uint8 has the high bit
  set. Mutation: decoding `MsgScroll.Delta` via uint32 now fails (old fill
  passed it).
- Browser hex fixtures had no Go-side check. `TestBrowserFixturesMatchGo`
  pins the same bytes.

Skipped, with reasons:
- `readFrame` allocates the declared size (≤64 MiB) up front: local,
  owner-only socket.
- A text WebSocket frame would decode in the browser as an empty message:
  the server only sends binary.
- `TestWireSchemaCoversEveryWireType` compares counts only: the round-trip
  test catches a type the codec lacks.
- `input.test.ts` asserts send() args without encoding: tsc (in make check)
  guards the shape.

Manual: Les smoke-tested the worktree build in the CLI and the web UI from
another machine (2026-09-24).

## Follow-ups (not in this PR)

- Nothing checks that committed generated code matches the .proto; CI has
  no buf.

## Copilot review (PR #166)

Overview only, no inline findings. Two notes:
- `go.mod` had protobuf as `// indirect` (from `go get`): fixed with
  `go mod tidy`.
- `@bufbuild/protoc-gen-es` declares `node >= 22`: it is a devDependency used
  only by `make proto`, so the README's regeneration steps now say so. Did not
  add `engines` to package.json, which would claim the whole web build needs
  Node 22. CI (runner default Node, no setup step) passes with no engine
  warning.

Deferred: web UI blank-after-idle bug from Les's smoke test → #167 (not yet
known whether it predates this PR). PR #144 closed by Les.
