# Status Dashboard Implementation Plan

**Goal:** Add a `wideboi status` CLI command that outputs a snapshot of all active panes in the current session, showing their titles, sizes, and operational statuses for external dashboarding or scripting.

**Approach:** Implement as a one-shot CLI client relying exclusively on `MsgLayoutSnapshot`. Support both human-readable tab-aligned text and JSON output formats.

**Tech stack:** Go, `text/tabwriter`, `encoding/json`

---

## Phase 1: Status command and CLI wiring

Wire up the new subcommand `status` with a `--json` flag, and implement the command logic to connect, receive `MsgLayoutSnapshot`, and output the formatted table or JSON.

**Files:**
- Modify: `cmd/wideboi/main.go`
  - Update `parseCLI` to recognize the `status` subcommand and the `--json` flag.
  - Update `cliOptions` to include `jsonOut bool`.
  - Add `case "status":` to `main()` routing to `runStatus(cfg, opts.jsonOut, os.Stdout)`.
  - Add `status` to `printHelp()`.
- Modify: `cmd/wideboi/cli_test.go`
  - Add `status` and `--json` to `TestParseCLISubcommands`.
- Create: `cmd/wideboi/status.go`
  - Implement `runStatus(cfg config.Config, jsonOut bool, w io.Writer) error`.
- Create: `cmd/wideboi/status_test.go`
  - Implement tests that start a mock server, send a `protocol.MsgLayoutSnapshot`, and verify the output.

**Key changes:**

```go
// In cmd/wideboi/status.go
func runStatus(cfg config.Config, jsonOut bool, w io.Writer) error {
	conn, err := net.Dial("unix", cfg.Socket)
	if err != nil {
		return fmt.Errorf("no wideboi server running at %s: %w", cfg.Socket, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cc := transport.NewClientSocketConn(conn, 256)
	cc.RunPumps(ctx)

	select {
	case msg, ok := <-cc.ServerSendChan():
		if !ok {
			return fmt.Errorf("server closed connection before sending state")
		}
		snap, ok := msg.(protocol.MsgLayoutSnapshot)
		if !ok {
			return fmt.Errorf("expected MsgLayoutSnapshot, got %T", msg)
		}

		if jsonOut {
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			return enc.Encode(snap)
		}

		tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
		fmt.Fprintln(tw, "PANE ID\tWIDTH\tHEIGHT\tFOCUS\tSTATUS\tTITLE")
		for _, col := range snap.Columns {
			focus := ""
			if col.PaneID == snap.FocusPaneID {
				focus = "*"
			}
			status := snap.PaneStatuses[col.PaneID]
			title := snap.PaneTitles[col.PaneID]
			fmt.Fprintf(tw, "%d\t%d\t%d\t%s\t%s\t%s\n", col.PaneID, col.Width, col.Height, focus, status, title)
		}
		return tw.Flush()
	case <-time.After(2 * time.Second):
		return fmt.Errorf("timeout waiting for server state")
	}
}
```

**Verification — automated**:
- [x] `make lint` passes — **lint OK**
- [x] `make test` passes — **all tests passed**
- [x] `make check` passes — **all 36 + 23 checks passed**

**Verification — manual:**
- [x] Start a `wideboi server` in a terminal. Run `wideboi status` and confirm a table of panes is printed.
- [x] Run `wideboi status --json` and confirm structured JSON is printed.
- [x] Confirm `wideboi status` exits 1 with a clear error if the server is not running.
