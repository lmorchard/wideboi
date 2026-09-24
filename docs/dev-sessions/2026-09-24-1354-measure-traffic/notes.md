# Notes: Measure live pane traffic and rendering costs (#179)

## Phase 4 benchmark (machine: arm64, Apple M5 Max)

`go test ./internal/client -run '^$' -bench BenchmarkClientApplyAndDraw -benchmem -benchtime=200x`

```
BenchmarkClientApplyAndDraw/full_80x24-18         	     200	    165114 ns/op	  239529 B/op	    2332 allocs/op
BenchmarkClientApplyAndDraw/row_80x24-18          	     200	    176349 ns/op	  240265 B/op	    2333 allocs/op
BenchmarkClientApplyAndDraw/shift_80x24-18        	     200	    178336 ns/op	  240290 B/op	    2333 allocs/op
BenchmarkClientApplyAndDraw/full_160x48-18        	     200	    580716 ns/op	  906908 B/op	    8461 allocs/op
BenchmarkClientApplyAndDraw/row_160x48-18         	     200	    603364 ns/op	  908655 B/op	    8463 allocs/op
BenchmarkClientApplyAndDraw/shift_160x48-18       	     200	    611007 ns/op	  908661 B/op	    8464 allocs/op
```

Patch kind barely moves the terminal client's cost: a row or shift patch
costs the same as (slightly more than) a full update, because
`applyPaneUpdateLocked` rewrites the whole mirror either way and `Draw`
recomposes the whole screen. Cost scales with pane area (4x cells, ~3.5x
time and ~3.8x bytes). Patches save wire bytes, not terminal-client CPU.

Messages are prebuilt in a 128-entry ring before the timer starts; the loop
only restamps generations. Mutation checks: a wrong `BaseGeneration` fails the
benchmark on the `MsgPaneResync` guard, and a "row" frame that changes too
many rows fails on the kind guard.

## Phase 6 first full run (superseded)

Superseded by "Phase 6 run 2" below: this run's UPD/S counted the settle's
trailing quiet as workload time (understating bursts; `scroll` read 21.4/s,
really ~31/s), and its B/UPD columns blended forced fulls with patches.

`make traffic TRAFFIC_ARGS="--profile"` (default `--seconds 10`), 2026-09-24
15:02 PDT, at `ef4589d` plus the uncommitted Phase 6 tree. Machine: Apple M5
Max, 18 cores, macOS 27.0 (arm64), load average ~4 (other agents running).
Raw JSON: `tmp/traffic/traffic-20260924-150246.json` (not committed), with
22 pprof files beside it (`tmp/traffic/<scenario>.<server|client>.<cpu|mem>.<pid>.pprof`).

```
== typing: pane 80x24 (client pty 82x26), 10.5s workload + 6.5s quiet tail
CLIENT  TRANSPORT  UPD/S  FULL  ROW  SHIFT  ROWS  RESYNC  PANE PAYLOAD B/UPD  PAYLOAD B  WIRE B  WIRE B/UPD  WIRE OVH%  ENC us
1          socket   14.4     2  149      0   119       0                 641      96906   97518         646        0.6    13.8
2       websocket   14.4     2  149      0   119       0                 641      96906   97454         645        0.6    13.0
server: render avg 100.7 us (n=151), build patch avg 4.8 us (n=298)
WSSINK  UPD  RESYNC  PAYLOAD B  DEFLATE AS-IS B  SAVED%  us/MSG  DEFLATE CTX B  SAVED%  us/MSG  DECODE+APPLY us/MSG
0       151       0      96906            15016    84.5    13.1           6846    92.9     4.3                 23.6

traffic: running scroll...
== scroll: pane 80x24 (client pty 82x26), 3.3s workload + 6.1s quiet tail
CLIENT  TRANSPORT  UPD/S  FULL  ROW  SHIFT  ROWS  RESYNC  PANE PAYLOAD B/UPD  PAYLOAD B  WIRE B  WIRE B/UPD  WIRE OVH%  ENC us
1          socket   21.4    70    0      0     0       0               13528     947036  947324       13533        0.0   121.0
2       websocket   21.4    70    0      0     0       0               13528     947036  947320       13533        0.0   128.0
server: render avg 4572.7 us (n=70), build patch avg - us (n=0)
WSSINK  UPD  RESYNC  PAYLOAD B  DEFLATE AS-IS B  SAVED%  us/MSG  DEFLATE CTX B  SAVED%  us/MSG  DECODE+APPLY us/MSG
0        70       0     947036            18841    98.0    22.0          17273    98.2    15.3                166.1

traffic: running scroll-paced...
== scroll-paced: pane 80x24 (client pty 82x26), 12.4s workload + 6.0s quiet tail
CLIENT  TRANSPORT  UPD/S  FULL  ROW  SHIFT  ROWS  RESYNC  PANE PAYLOAD B/UPD  PAYLOAD B  WIRE B  WIRE B/UPD  WIRE OVH%  ENC us
1          socket   12.3     2   22    129   280       0                1237     189346  189966        1242        0.3    19.3
2       websocket   12.3     2   22    129   280       0                1237     189346  189962        1242        0.3    16.5
server: render avg 111.3 us (n=153), build patch avg 16.7 us (n=302)
WSSINK  UPD  RESYNC  PAYLOAD B  DEFLATE AS-IS B  SAVED%  us/MSG  DEFLATE CTX B  SAVED%  us/MSG  DECODE+APPLY us/MSG
0       153       0     189346            15013    92.1    14.1           4938    97.4     4.9                 36.8

traffic: running tui...
== tui: pane 80x24 (client pty 82x26), 12.1s workload + 6.5s quiet tail
CLIENT  TRANSPORT  UPD/S  FULL  ROW  SHIFT  ROWS  RESYNC  PANE PAYLOAD B/UPD  PAYLOAD B  WIRE B  WIRE B/UPD  WIRE OVH%  ENC us
1          socket   14.6    39   22    116   238       0                3764     666211  666927        3768        0.1    39.6
2       websocket   14.6    39   22    116   238       0                3764     666211  666879        3768        0.1    35.7
server: render avg 99.6 us (n=177), build patch avg 11.0 us (n=340)
WSSINK  UPD  RESYNC  PAYLOAD B  DEFLATE AS-IS B  SAVED%  us/MSG  DEFLATE CTX B  SAVED%  us/MSG  DECODE+APPLY us/MSG
0       177       0     666211            38947    94.2    16.9          14093    97.9     5.8                 52.5

traffic: running large...
== large: pane 160x48 (client pty 162x50), 10.5s workload + 6.5s quiet tail
CLIENT  TRANSPORT  UPD/S  FULL  ROW  SHIFT  ROWS  RESYNC  PANE PAYLOAD B/UPD  PAYLOAD B  WIRE B  WIRE B/UPD  WIRE OVH%  ENC us
1          socket   14.4     2  149      0   119       0                1617     244251  244863        1622        0.3    25.7
2          socket   14.4     2  149      0   119       0                1617     244251  244863        1622        0.3    24.7
3       websocket   14.4     2  149      0   119       0                1617     244251  244799        1621        0.2    27.0
4       websocket   14.4     2  149      0   119       0                1617     244251  244799        1621        0.2    18.3
server: render avg 329.8 us (n=151), build patch avg 17.2 us (n=596)
WSSINK  UPD  RESYNC  PAYLOAD B  DEFLATE AS-IS B  SAVED%  us/MSG  DEFLATE CTX B  SAVED%  us/MSG  DECODE+APPLY us/MSG
0       151       0     244251            17598    92.8    13.3           8470    96.5     4.9                 33.5
1       151       0     244251            17598    92.8    14.8           8470    96.5     4.7                 38.6

PANE PAYLOAD B/UPD: protobuf bytes of pane updates and patches per update.
PAYLOAD B: protobuf envelope bytes of every message (frame headers excluded).
WIRE B: bytes written to the connection (socket length prefix or WebSocket
  frame headers included). WIRE OVH% = (wire - payload) / payload.
DEFLATE AS-IS: estimate of permessage-deflate as gorilla/websocket v1.5.3
  would send it with EnableCompression (no context takeover, level 1).
DEFLATE CTX: estimate with context takeover (a shared window, level 1).
  Both are estimates on payload bytes; frame headers excluded.
UPD/S is over the workload; counts include the quiet tail after it, which
  holds the pane's idle flip (a forced full per client, see IDLE_QUIET).
All counts are after the baseline taken once every client had attached
and the session had gone quiet.

raw results: /Users/lmorchard/devel/mine/wideboi/.worktrees/issue-179-measure-traffic/tmp/traffic/traffic-20260924-150246.json
```

What the numbers say (for Phase 8 to weigh, not a decision yet):

- **Forced fulls are a large share of bytes in quiet sessions.** Every
  layout broadcast force-resends every pane in full to every client
  (`broadcastLayout` ends with `broadcastPaneUpdates(ctx, true)`). Two
  everyday triggers: a pane's working/idle flip (`term.DefaultIdleTimeout`,
  3 s), and any handshaked connection closing (`dropClient`), which includes
  `wideboi status`, `status --traffic` and `kill-session` even though they
  never attach. In `typing` the two status-flip fulls are ~27 KB of 97 KB
  per client; in `large` roughly 2 × 50 KB of 244 KB (a 160×48 full, estimated
  from the row-patch size).
- **Sustained output never patches.** `scroll`: 70 fulls, 0 patches, BuildPatch
  never runs. When a pane changes during its ~4.5 ms render, the client's
  baseline is dropped ("leave this client behind"), so the next tick has no
  baseline. The client *did* receive the frame it was sent, so keeping it
  as the baseline looks possible; that is the #180 shift work's blind spot.
- **Paced scrolling does patch**: `scroll-paced` 129 shift / 22 row / 2 full,
  ~1.2 KB per update against 13.5 KB per full.
- **Wire overhead is noise** (0.0-0.6 %): framing is not where bytes go.
- **Deflate would be large, even as gorilla does it.** No context takeover
  saves 84-98 % of payload; with context takeover 93-98 %. The gap matters
  most for small patches (typing: 15.0 KB vs 6.8 KB). Cost ~13-22 µs per
  message without context, 4-15 µs with (sink-side estimate at BestSpeed).
- **Server render dominates server CPU per update** in the burst (4.5 ms per
  80×24 render under sustained output against ~100 µs when quiet); patch
  build is 5-17 µs, encode 13-128 µs.
- `ROWS` < `ROW` in typing (119 changed rows over 149 row patches): about a
  fifth of row patches carry no changed row, presumably cursor-only moves.

## Phase 6 run 2 (after review fixes)

`make traffic TRAFFIC_ARGS="--profile --outdir tmp/traffic-run2"` (default
`--seconds 10`), 2026-09-24 15:23 PDT, at `2f8f852` plus the uncommitted
review-fix tree (committed as "Phase 6 review: honest rates, per-kind bytes,
workload checks"). Machine: Apple M5 Max, 18 cores, macOS 27.0 (arm64), load
average ~3.2-3.8 (other agents running). rc=0, every self-check passed.
Raw JSON: `tmp/traffic-run2/traffic-20260924-152317.json` (not committed),
22 pprof files beside it; full console output in `tmp/traffic-run2/full-run.txt`.

What changed in the report: UPD/S is now over the *active* workload (start
to the first client's last pty output), per-kind counts and bytes come from
the wssink (FULL, B/FULL, PATCH = row + shift, B/PATCH), and the blended
PANE PAYLOAD B/UPD and WIRE B/UPD columns are gone in favour of totals.

```
== typing: pane 80x24 (client pty 82x26), 10.0s active workload + 7.0s quiet tail
CLIENT  TRANSPORT  UPD/S  FULL  ROW  SHIFT  ROWS  RESYNC  PANE PAYLOAD B  PAYLOAD B  WIRE B  WIRE OVH%  ENC us
1          socket   15.1     2  149      0   119       0           96862      96906   97518        0.6    11.7
2       websocket   15.1     2  149      0   119       0           96862      96906   97454        0.6    13.5
server: render avg 105.2 us (n=151), build patch avg 4.9 us (n=298)
WSSINK  UPD  RESYNC  FULL  B/FULL  PATCH  B/PATCH  PAYLOAD B  DEFLATE AS-IS B  SAVED%  us/MSG  DEFLATE CTX B  SAVED%  us/MSG  DECODE+APPLY us/MSG
0       151       0     2   13528    149      468      96906            15016    84.5    13.4           6846    92.9     4.7                 24.4

== scroll: pane 80x24 (client pty 82x26), 1.8s active workload + 7.1s quiet tail
CLIENT  TRANSPORT  UPD/S  FULL  ROW  SHIFT  ROWS  RESYNC  PANE PAYLOAD B  PAYLOAD B  WIRE B  WIRE OVH%  ENC us
1          socket   30.8    56    0      0     0       0          757644     757688  757920        0.0   118.4
2       websocket   30.8    56    0      0     0       0          757644     757688  757916        0.0   103.7
server: render avg 3901.1 us (n=56), build patch avg - us (n=0)
WSSINK  UPD  RESYNC  FULL  B/FULL  PATCH  B/PATCH  PAYLOAD B  DEFLATE AS-IS B  SAVED%  us/MSG  DEFLATE CTX B  SAVED%  us/MSG  DECODE+APPLY us/MSG
0        56       0    56   13529      0        -     757688            15283    98.0    19.0          14275    98.1    13.3                148.5

== scroll-paced: pane 80x24 (client pty 82x26), 11.4s active workload + 7.1s quiet tail
CLIENT  TRANSPORT  UPD/S  FULL  ROW  SHIFT  ROWS  RESYNC  PANE PAYLOAD B  PAYLOAD B  WIRE B  WIRE OVH%  ENC us
1          socket   13.4     2   22    129   280       0          189320     189364  189984        0.3    20.5
2       websocket   13.4     2   22    129   280       0          189320     189364  189980        0.3    14.9
server: render avg 101.3 us (n=153), build patch avg 17.2 us (n=302)
WSSINK  UPD  RESYNC  FULL  B/FULL  PATCH  B/PATCH  PAYLOAD B  DEFLATE AS-IS B  SAVED%  us/MSG  DEFLATE CTX B  SAVED%  us/MSG  DECODE+APPLY us/MSG
0       153       0     2   13528    151     1075     189364            15030    92.1    13.8           4950    97.4     4.8                 29.9

== tui: pane 80x24 (client pty 82x26), 11.6s active workload + 7.2s quiet tail
CLIENT  TRANSPORT  UPD/S  FULL  ROW  SHIFT  ROWS  RESYNC  PANE PAYLOAD B  PAYLOAD B  WIRE B  WIRE OVH%  ENC us
1          socket   15.5    38   18    123   248       0          658440     658484  659208        0.1    35.9
2       websocket   15.5    38   18    123   248       0          658440     658484  659168        0.1    32.0
server: render avg 101.7 us (n=179), build patch avg 11.3 us (n=344)
WSSINK  UPD  RESYNC  FULL  B/FULL  PATCH  B/PATCH  PAYLOAD B  DEFLATE AS-IS B  SAVED%  us/MSG  DEFLATE CTX B  SAVED%  us/MSG  DECODE+APPLY us/MSG
0       179       0    38   13528    141     1024     658484            40204    93.9    17.5          14115    97.9     5.8                 51.4

== large: pane 160x48 (client pty 162x50), 10.0s active workload + 7.0s quiet tail
CLIENT  TRANSPORT  UPD/S  FULL  ROW  SHIFT  ROWS  RESYNC  PANE PAYLOAD B  PAYLOAD B  WIRE B  WIRE OVH%  ENC us
1          socket   15.2     2  149      0   119       0          244205     244251  244863        0.3    28.2
2          socket   15.2     2  149      0   119       0          244205     244251  244863        0.3    22.9
3       websocket   15.2     2  149      0   119       0          244205     244251  244799        0.2    25.1
4       websocket   15.2     2  149      0   119       0          244205     244251  244799        0.2    20.2
server: render avg 322.5 us (n=151), build patch avg 18.3 us (n=596)
WSSINK  UPD  RESYNC  FULL  B/FULL  PATCH  B/PATCH  PAYLOAD B  DEFLATE AS-IS B  SAVED%  us/MSG  DEFLATE CTX B  SAVED%  us/MSG  DECODE+APPLY us/MSG
0       151       0     2   53922    149      915     244251            17598    92.8    12.7           8470    96.5     4.8                 29.0
1       151       0     2   53922    149      915     244251            17598    92.8    12.5           8470    96.5     4.8                 36.0
```

(Legend as printed by `scripts/traffic.py`; see `LEGEND` there.)

What run 2 changes about the reading of run 1:

- **The burst runs at the frame cap.** `scroll` is 56 fulls in 1.8 s active,
  30.8/s: one full every 33 ms frame. Run 1's 21.4/s was the 1 s settle
  quiet counted as workload.
- **Forced fulls dominate quiet-session bytes, now measured, not
  estimated.** A full is 13.5 KB at 80x24 and 53.9 KB at 160x48; a patch is
  0.47 KB (typing), 0.92 KB (typing at 160x48), 1.0-1.1 KB (shift-heavy
  paced scroll and vim). In `typing` the 2 fulls are 27 KB of 97 KB (28 %);
  in `large` 108 KB of 244 KB (44 %) per client.
- **`tui` is mostly fulls by bytes**: 38 fulls (Ctrl-F pages change every
  row) are ~514 KB of 658 KB (78 %); its 141 patches average 1.0 KB.
- **A one-character row patch is ~470 B at 80 cols**: about one full row
  (13.5 KB / 24 rows = ~560 B). Row patches resend whole rows; typing
  changes one cell of one.
- Every other run-1 observation (deflate savings, wire overhead as noise,
  render dominating server CPU under the burst) stands with near-identical
  numbers.

## Phase 8: decision and follow-up drafts

Results and decision: `docs/partial-pane-updates.md` "Live measurements
(#179)". Profile analysis: `profile-analysis.md` in this directory.

Still open before merge:

- The manual browser `?stats=1` run. The doc has a placeholder.
- Follow-ups filed 2026-09-24 and set to Backlog: #206 (baseline), #202
  (forced resends), #203 (WebSocket deflate), #204 (Draw lock), #205 (scrollback).

Draft follow-up issues:

1. **Keep the sent frame as the patch baseline when a pane changes mid-send.**
   - `broadcastPaneUpdates` deletes `paneGens`/`paneFrames` when
     `r.pane.Generation() != r.gen` after a successful send, so sustained
     output goes out as fulls at 30/s. That is all 56 updates in the `seq`
     run and 38 of 179 in vim.
   - Proposal: store `r.gen` + `r.frame` on that path. The client holds
     exactly that frame. Safety analysis is in `profile-analysis.md`.
   - Done when: a paced-but-continuous output workload sends patches, and a
     `TestPaneTrafficWorkloads`-style test covers writes that land during a
     render.
2. **Don't force a full resend of every pane when an unattached connection
   closes or only a status changes.**
   - `dropClient` → `broadcastLayout` → `broadcastPaneUpdates(ctx, true)`
     runs for `status`, `kill-session` and `status --traffic`. The working
     and idle flip does the same.
   - Measured: 2 forced fulls per pane per client per activity burst, which
     is 28–44% of the typing bytes.
   - Proposal: force only when the snapshot can actually prune or replace a
     client mirror (column set or size changed). Skip the broadcast entirely
     when a never-attached connection leaves.
3. **Enable permessage-deflate for WebSocket clients.**
   - Set `EnableCompression: true` on the upgrader.
   - Estimated saving is 84–98% of WebSocket payload at about 15 µs per
     message, as gorilla sends it (no context takeover, level 1).
   - Verify with `make traffic` wire bytes after enabling it. The counting
     conn sees the compressed frames.
4. **`vtGrid.Draw` takes `writeResizeMu` on the fast path, contrary to its
   comments.**
   - A render waits behind each emulator `Write` chunk: about 3.9 ms per
     frame during bursts, of which about 0.14 ms is CPU. This stalls the Run
     loop's frame tick.
   - Either fix the comments (if the lock is required since 41627cd) or
     restore the lock-free fast path.
5. *(optional, upstream)* **`vt.Scrollback.Push` is O(scrollback) per line**
   once full (`slices.Delete(s.lines, 0, 1)`). It is about 30% of server CPU
   during bursts, and its `slices.Clone` is 63% of burst allocation.
   - This is a ring-buffer change in `charmbracelet/x/vt`, or a local
     wrapper.
