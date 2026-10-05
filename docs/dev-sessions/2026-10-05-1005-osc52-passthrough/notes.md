# Notes

## Phase 1 (server capture + show-clipboard)

- **Plan gap: CLI dispatch.** `wideboi <subcommand>` doesn't go through the registry. `cmd/wideboi/main.go` has a hand-kept subcommand list (`parseCLI`) plus a `switch`. Added `show-clipboard` to both. `runShowClipboard` in `control.go` calls the registry command so CLI and prompt share one implementation. Added a `parseCLI` test case.
- **Existing bug found, not fixed (out of scope):** `wait-output`, `wait-status` and `set-pane-status` have `case`s in the switch but are missing from the `parseCLI` list. `wideboi wait-output …` therefore falls through to the default `run`, which starts a session or trips the nesting guard. Their tests call `runWaitOutput` etc. directly, so nothing caught it. Proven with a throwaway `parseCLI` probe (subcommand == ""). Saved to project memory; worth an issue.
- `make proto-check` diffs against the git index, so it fails until regenerated bindings are staged. Not a bug, just a trap.
- `make quick` stops at `test` because of the three known macOS config failures, so `web-test` never runs inside it. Run `make web-test` separately until that's fixed.
- Session flags must come before the subcommand (`wideboi -L name show-clipboard`). The registry's flagset doesn't know `-L`/`-s`. Other control subcommands accept them after; this one doesn't.

## Phase 2 (deliver to TUI + setting)

- **Test trap:** an `InProcChannel` drops `SendServer` messages when its buffer is full, and pane updates fill it. Tests tap each client's channel in a goroutine. A tap that discards non-clipboard messages also eats responses, so `clipTap` forwards show-clipboard responses too, and `barrier()` waits on those. The first version raced `showClipboard` against the tap and timed out.
- `lastInput` is lazily initialised in `noteInputLocked`, because many tests build `&Server{...}` literals with nil maps.
- Added beyond the plan: `TestPaneClipboardMouseCountsAsInput`, and a smoke case proving `--disable-clipboard` emits no OSC 52.
- `make check` hit one smoke flake ("reclaimed control keys pass through") in 1 of 4 full smoke runs, under parallel load only. It passed 6/6 isolated, and the case doesn't touch this change. This matches the parallel-load flake already in project memory.

## Phase 3 (web toast)

- Playwright runs against the Vite dev server, so source edits show up without `make build`.
- The opt-out spec passes trivially before the toast exists. A mutation check (pref check removed → spec fails) proved it guards something.
- **Smoke flakiness under `make check` is not specific to this branch,** which corrects my Phase 2 note. Alternating runs: branch 4× gave ✘ reclaimed, ✘ reclaimed, ✘ host resize, clean. origin/main 3× gave clean, ✘ card layout toggles, clean. The failing case differs from run to run and each passes alone. The project memory note says "~1-in-6"; on this machine it looks more like 1 in 2 to 3 under `make check`. Worth updating that memory and possibly an issue. Smoke shares the box with race tests at `--jobs 8`.

## Phase 4 (docs)

- MANUAL: added a "Copying from Programs in a Pane" subsection to §5 and a `show-clipboard` subcommand entry to §7. Added `clipboard` to the trust-boundary list of keys untrusted project files *do* load (it's loaded before the trust gate) and to the example TOML.
- README unchanged: its "OSC 52 clipboard sharing" line already covers this.
- LESSONS: two entries under "Probe the pinned terminal dependencies": the 4 MB silent OSC truncation, and the silently dropped unhandled OSCs.

## For the PR / follow-ups

- Filed #396: `parseCLI` misses `wait-output`, `wait-status`, `set-pane-status`.
- Filed #397: macOS `/tmp` symlink rejection (user-facing, plus 3 local test failures).
- Filed #398: smoke flakiness under parallel `make check` (~1 in 2-3, main included).
- Filed #399: URL clicks inside mouse-grabbing agents open on the remote host (first step: Les's iTerm2 Cmd/Option-click test).
- Not done: pruning `lastInput` entries when a pane closes. Harmless if pane IDs aren't reused; unverified.
- Les's manual checks for Phases 1–3 are still pending, batched for the end.

## Copilot review (PR #400), all six fixed

1. **Untrusted project could re-enable clipboard** over a global `clipboard = false`. This was a wrong premise in the spec's open question, now revised. `applyFile` honours `true` only from trusted files. Test: `TestUntrustedProjectCannotEnableClipboard`.
2. **Unbounded goroutines,** one per write, each holding up to 1 MiB. My first burst test passed on the old code because in-process sends never block. Adding `stallingTransport`, a client whose clipboard sends run out their 100 ms timeout, made it fail: 1000 goroutines, and the last copy never arrived. Replaced with a single worker and a coalescing slot, which removed the sequence number and `clipboardMu` entirely.
3. **Mouse motion, wheel and release counted as input.** Now only a press counts. Test: `TestPaneClipboardIgnoresMouseMotionAndWheel`.
4. **The size cap was checked after stripping whitespace,** so padding could beat x/vt's 4 MB truncation. Added a 2 MiB raw cap, with tests for padding and for a maximal payload wrapped at 76 columns.
5. **The negative smoke check could pass without the command having run** (the #398 failure mode). Both OSC 52 cases now use an `osc52-done-$((40+2))` sentinel. The positive case waits for the OSC 52 itself; the negative case waits for the sentinel, then settles. Mutation-checked.
6. **A pending web toast survived turning the setting off.** The settings dialog now emits `pane-clipboard-change` and the app drops the pending copy. The Copy handler also re-checks the pref. New Playwright case.
- Post-fix `make check`: only the 3 config failures, plus two load flakes in layout cases (smoke `partly clipped pane keeps full width`, attach-check `layout toggle affects only its own client`). Both passed 3/3 in isolation. So #398's pattern reaches attach-check as well.

## Retrospective

### Recap
- Pane OSC 52 clipboard writes now pass through to the client that last typed, pasted or clicked in the pane, falling back to every attached client.
- The TUI writes them to the host terminal. The web client asks first, with a Copy/Dismiss toast.
- `wideboi show-clipboard [--json]` exposes the latest copy.
- Protocol v30. PR #400 is open, CI green on Linux. Les's manual ssh/iTerm2 and web checks are still pending.
- Filed along the way: #396 (CLI subcommands unreachable), #397 (macOS `/tmp` socket rejection), #398 (load flakes), #399 (URL clicks).

### Scope drift
- **`show-clipboard` added mid-brainstorm** at Les's suggestion. It shared the handler and the version bump, so it cost one request/response pair.
- **CLI routing:** the plan assumed registry commands are reachable from the CLI. They aren't (`parseCLI` keeps its own list), which is also how #396 turned up.
- **Copilot review added six fixes:** trust gate, bounded worker, press-only input, raw size cap, sentinel smoke waits, toast cleared on opt-out. Three of them overturned spec decisions, now marked revised.

### Surprises
- x/vt has no OSC 52 support and silently drops unhandled OSCs. Its parser truncates OSC data at 4 MB and still calls the handler.
- tmux's default (`set-clipboard external`) deliberately blocks pane clipboard writes. That forced the clipboard-poisoning trade-off into the open.
- `InProcChannel.SendServer` never blocks; it drops when full. So in-process tests can't show backpressure, and my first burst test couldn't fail.
- `make proto-check` diffs against the git index. `make quick` stops at `test` on macOS, so `web-test` never ran inside it.
- Load flakes under `make check` are far more frequent than the 1 in 6 in memory, and hit attach-check too.

### Workflow friction
- **The documentarian research pass paid off.** The bell path it traced became the delivery model almost directly, and it surfaced the 4 MB buffer.
- **Plan code snippets went stale the moment review changed the design.** I kept them, marked "revised", rather than rewriting history. That's fine, but it means the plan's code reads as wrong to anyone skimming.
- **The skill's "don't open a PR with red checks" rule can't be met on macOS** until #397 lands, and is rarely met under #398. I had to stop and ask Les for an exception. Fixing #397 and #398 makes the rule usable again.
- **Manual verification was batched to the end at Les's choice,** so the PR went up with the real-world ssh/iTerm2 path never exercised. All automated evidence comes from pty harnesses, not iTerm2.

### Misses
- **My self-review found nothing; Copilot found six real issues.** All six had the same shape: the spec stated an invariant, and the code enforced a nearby but weaker one.
  - "Untrusted can only turn it off" didn't consider an existing global `false`.
  - "Stay under the 4 MB truncation" was checked after normalising.
  - "Typed, pasted or clicked" was implemented as any mouse event.
  - "Async like the bell" wasn't bounded.
  - "The negative check proves the flag works" didn't prove the command ran.
  - "The opt-out stops copies" missed a toast that was already pending.

  Self-review read the diff for correctness. It never tried to break each stated invariant.
- **I diagnosed the settle-as-proxy flaw in #398 in this very session,** then had shipped a negative smoke test with exactly that flaw. Knowing a failure mode doesn't mean checking for it in your own fresh code.
- **What went right:** the first burst test passed on old code, which I noticed and treated as "proves nothing" rather than as green. The same instinct applied earlier would have caught #5.

### Memory candidates
- `make proto-check` fails until regenerated bindings are staged (saved to memory).
- Backpressure in server tests needs a transport whose sends block (a `stallingTransport`), because `InProcChannel` drops instead (added to LESSONS).

### Skill candidates
- **dev-session `pr` self-review: add an adversarial invariant pass.** For each invariant the spec states (who may, what bounds, what never happens), try to construct an input or sequence that violates it in the diff: a preexisting state, an unnormalised input, an unbounded rate, an already-pending item. All six review findings would have fallen to that question.
- **Negative tests must prove their precondition happened.** A test asserting "X does not occur" needs evidence that the action which would cause X actually ran: a sentinel, a counter, a mutation check. It fits the existing "prove a new test fails" convention.
