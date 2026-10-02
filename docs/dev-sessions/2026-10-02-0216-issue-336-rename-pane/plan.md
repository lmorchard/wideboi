# User-Defined Pane Titles and Renaming (rename-pane) Implementation Plan

**Goal:** Enable users to set, override, and clear custom titles on panes via CLI (`wideboi rename-pane`), command prompt (`:title`), and web prompt, persisting custom titles across server upgrades and dynamically falling back to child process OSC 0/2 titles when cleared.

**Approach:**
- Add `MsgRenamePaneRequest` and `MsgRenamePaneResponse` to the wire protocol and codec, bumping `protocol.Version` to 22.
- Store custom title overrides on `Pane`, preserving child terminal titles in `term.Grid` so clearing restores them immediately.
- Update `UpgradePane` to persist custom titles across in-place binary upgrades.
- Register `rename-pane` (aliases: `title`, `label`) in `internal/commands/` and add the CLI subcommand `wideboi rename-pane`.
- Wire `rename-pane`, `title`, and `label` into `WideboiApp.handlePromptCommand` in the web client.

**Tech stack:** Go, Protobuf (buf / protoc-gen-go / protoc-gen-es), TypeScript (Lit, Vitest).

---

## Phase 1: Wire Protocol & Codec (Types, Protobuf, Codec, Version Bump)

Define `MsgRenamePaneRequest` and `MsgRenamePaneResponse` in the Protobuf schema, regenerate Go and TypeScript bindings, implement codecs, and bump the protocol version to 22.

**Files:**
- Modify: `internal/protocol/wirepb/wideboi.proto`
- Generate: `internal/protocol/wirepb/wideboi.pb.go`
- Generate: `web/src/gen/internal/protocol/wirepb/wideboi_pb.ts`
- Modify: `internal/protocol/messages.go`
- Modify: `internal/protocol/codec.go`
- Modify: `internal/protocol/version.go`
- Modify: `web/src/version.ts`
- Test: `internal/protocol/wire_test.go`
- Test: `internal/protocol/codec_test.go`
- Test: `web/src/protobuf.test.ts`

**Key changes:**
- `wideboi.proto`:
  ```protobuf
  message MsgRenamePaneRequest {
    int32 pane_id = 1;
    string title = 2;
    bool clear = 3;
  }

  message MsgRenamePaneResponse {
    int32 pane_id = 1;
    string error = 2;
  }
  ```
  Add `MsgRenamePaneResponse rename_pane_response = 20;` to `ServerMessage` oneof.
  Add `MsgRenamePaneRequest rename_pane_request = 22;` to `ClientMessage` oneof.
- `messages.go`:
  ```go
  type MsgRenamePaneRequest struct {
      PaneID int
      Title  string
      Clear  bool
  }

  type MsgRenamePaneResponse struct {
      PaneID int
      Error  string
  }
  ```
- `codec.go`: Map `MsgRenamePaneRequest` and `MsgRenamePaneResponse` in `MarshalClient`, `UnmarshalClient`, `MarshalServer`, `UnmarshalServer`.
- `version.go`: Set `Version uint32 = 22`.
- `web/src/version.ts`: Set `PROTOCOL_VERSION = 22`.
- `wire_test.go`: Add `MsgRenamePaneRequest{}` and `MsgRenamePaneResponse{}` to `declaredWireTypes` and `wireTypes`.

**Verification — automated:**
- [x] `go test -count=1 ./internal/protocol/...` passes — **all 22 tests and subtests passed**
- [x] `cd web && npm test src/protobuf.test.ts` passes — **22 test files passed, 163 tests passed**
- [x] `make seam-check` passes — **OK -- no new client/server crossings**

**Verification — manual:**
- [x] Inspect `git diff -- internal/protocol/version.go web/src/version.ts` to ensure version numbers match (22) — **verified: both protocol.Version and PROTOCOL_VERSION are 22**

---

## Phase 2: Server Pane Model, Handlers, and In-Place Upgrade Persistence

Add custom title tracking to `Pane`, implement server handler for `MsgRenamePaneRequest` with dashboard update and layout snapshot broadcast, and preserve custom titles across in-place server upgrades via `UpgradePane`.

**Files:**
- Modify: `internal/server/pane.go`
- Modify: `internal/server/handlers.go`
- Modify: `internal/server/upgrade.go`
- Test: `internal/server/pane_test.go`
- Test: `internal/server/handlers_test.go`
- Test: `internal/server/upgrade_test.go`

**Key changes:**
- `internal/server/pane.go`:
  ```go
  // SetCustomTitle sets a user-defined title that overrides the child terminal title.
  func (p *Pane) SetCustomTitle(title string) {
      p.titleMu.Lock()
      p.customTitle = title
      p.hasCustomTitle = true
      p.titleMu.Unlock()
  }

  // ClearCustomTitle removes the user-defined title override.
  func (p *Pane) ClearCustomTitle() {
      p.titleMu.Lock()
      p.customTitle = ""
      p.hasCustomTitle = false
      p.titleMu.Unlock()
  }

  // CustomTitle returns the custom title and whether one is set.
  func (p *Pane) CustomTitle() (string, bool) {
      p.titleMu.RLock()
      defer p.titleMu.RUnlock()
      return p.customTitle, p.hasCustomTitle
  }

  // Title returns the custom title if set, else p.grid.Title().
  func (p *Pane) Title() string {
      p.titleMu.RLock()
      if p.hasCustomTitle {
          title := p.customTitle
          p.titleMu.RUnlock()
          return title
      }
      p.titleMu.RUnlock()
      return p.grid.Title()
  }
  ```
- `internal/server/handlers.go`:
  - Add `renameResp *protocol.MsgRenamePaneResponse` to `msgEffects`.
  - Handle `protocol.MsgRenamePaneRequest` in `handleClientMsg` and `applyEffects`.
  - Implement `handleRenamePaneRequestLocked`:
    - Lookup `p, ok := s.panes[m.PaneID]`; if not found, return response with `Error: fmt.Sprintf("pane %d not found", m.PaneID)`.
    - If `m.Clear || m.Title == ""`, call `p.ClearCustomTitle()`; else call `p.SetCustomTitle(m.Title)`.
    - Call `s.updateDashboardLocked()`.
    - Set `eff.needBroadcast = true` and `eff.renameResp = &protocol.MsgRenamePaneResponse{PaneID: m.PaneID}`.
- `internal/server/upgrade.go`:
  - Add `CustomTitle string` and `HasCustomTitle bool` to `UpgradePane`.
  - In `buildUpgradeStateLocked`:
    ```go
    if customTitle, ok := p.CustomTitle(); ok {
        up.CustomTitle = customTitle
        up.HasCustomTitle = true
    }
    ```
  - In `RestoreState`:
    ```go
    if up.HasCustomTitle {
        p.SetCustomTitle(up.CustomTitle)
    }
    ```

**Verification — automated:**
- [x] `go test -count=1 ./internal/server/...` passes — **all packages in internal/server passed in 12.2s**
- [x] Phase tests pass:
  - `TestPaneCustomTitleOverrideAndFallback` in `pane_title_test.go` — **passed**
  - `TestRenamePaneHandler` in `pane_title_test.go` — **passed**
  - `TestUpgradeStateCustomTitlePersistence` in `upgrade_test.go` — **passed (TestUpgradeStateRoundTrip)**

**Verification — manual:**
- [x] Verify that `p.Title()` falls back to `p.grid.Title()` when `ClearCustomTitle()` is invoked — **verified via unit test assertions**

---

## Phase 3: Internal Commands & In-Session Prompt Registry

Register `rename-pane` (with aliases `title`, `label`) in `internal/commands/registry.go`, supporting target pane ID resolution, caller pane defaulting, unquoted titles, and clearing.

**Files:**
- Modify: `internal/commands/registry.go`
- Test: `internal/commands/commands_test.go`

**Key changes:**
- `internal/commands/registry.go`:
  Register `rename-pane` with aliases `[]string{"title", "label"}`:
  - 0 arguments: if `inv.CallerPaneID > 0`, clear custom title for caller pane; else error that pane ID is required outside session.
  - 1 argument: if `inv.CallerPaneID > 0`, set title for caller pane (or clear if empty); if outside pane context, parse as integer pane ID to clear.
  - 2+ arguments: if first argument is integer, target that pane ID and join remainder as title (empty title clears); if first argument is not integer and `inv.CallerPaneID > 0`, join all arguments as title for caller pane.
  - Sends `protocol.MsgRenamePaneRequest` using `RPCQuery[protocol.MsgRenamePaneResponse](ctx, inv, req, 5*time.Second)`.

**Verification — automated:**
- [x] `go test -count=1 ./internal/commands/...` passes — **all tests in internal/commands passed in 0.15s**
- [x] Phase tests pass:
  - `TestRenamePaneExecution` in `commands_test.go` testing `:title "My Title"`, `:title ""`, invalid pane error — **passed**

**Verification — manual:**
- [x] Verify command description and category in `wideboi help` / `internal/commands` — **verified: "Set or clear the title of a pane", category "Panes"**

---

## Phase 4: CLI Subcommand (`wideboi rename-pane`)

Implement `wideboi rename-pane [flags] [pane-id] [title]` CLI command, supporting `$WIDEBOI_PANE_ID` environment detection, session targeting flags (`--session`, `--socket`), and exit code handling.

**Files:**
- Modify: `cmd/wideboi/main.go`
- Modify: `cmd/wideboi/control.go`
- Test: `cmd/wideboi/control_test.go`

**Key changes:**
- `cmd/wideboi/main.go`:
  - Add `"rename-pane"` to subcommand check in `parseCLI`.
  - Dispatch `"rename-pane"` to `runRenamePane(cfg, opts.subcommandArgs, os.Stderr)`.
  - Update `printHelp` usage text with `wideboi [flags] rename-pane [pane-id] [title]`.
- `cmd/wideboi/control.go`:
  - Implement `runRenamePane`:
    - Parse target flags (`session`, `socket`).
    - Resolve target pane ID: check first argument as int, or fallback to `$WIDEBOI_PANE_ID`.
    - If no pane ID found, return `usage: wideboi rename-pane [flags] <pane-id> [title] (pane-id required outside wideboi pane)`.
    - Parse title: empty or omitted triggers `Clear: true`.
    - Execute `rpcQuery[protocol.MsgRenamePaneResponse](cfg, req, 5*time.Second)`.
    - If `resp.Error != ""`, return `errors.New(resp.Error)`.

**Verification — automated:**
- [x] `go test -count=1 ./cmd/wideboi/...` passes — **all tests in cmd/wideboi passed in 18.6s**
- [x] Phase tests in `cmd/wideboi/control_test.go` pass — **TestRenamePaneSubcommand passed**
  - CLI rename with explicit pane ID
  - CLI rename with `$WIDEBOI_PANE_ID`
  - CLI rename clear with `""`
  - CLI rename error outside pane without pane ID
  - CLI rename error with non-existent pane ID

**Verification — manual:**
- [x] Run `bin/wideboi --help` and verify `rename-pane` appears in the command list — **verified: appears in help output under subcommands**

---

## Phase 5: Web Client Prompt Handling and Full Verification

Add `:rename-pane`, `:title`, and `:label` prompt commands to `WideboiApp.handlePromptCommand` in the web client, and run the complete repository test and check suite.

**Files:**
- Modify: `web/src/wideboi-app.ts`
- Test: `web/src/prompt-command.test.ts` (or add to `web/src/lifecycle.test.ts` / `web/src/command-menu.test.ts`)

**Key changes:**
- `web/src/wideboi-app.ts`:
  In `handlePromptCommand(line: string)`:
  ```typescript
  case 'rename-pane':
  case 'title':
  case 'label': {
    if (!this.focusedPaneId) break;
    let targetId = this.focusedPaneId;
    let newTitle = rest;
    let clear = false;
    if (parts[1]) {
      const parsedId = parseInt(parts[1], 10);
      if (!isNaN(parsedId)) {
        targetId = parsedId;
        newTitle = line.slice(parts[0].length).trim().slice(parts[1].length).trim();
      }
    }
    if (!newTitle) {
      clear = true;
    }
    this.client?.send({
      case: 'renamePaneRequest',
      value: { paneId: targetId, title: newTitle, clear },
    });
    break;
  }
  ```

**Verification — automated:**
- [x] `cd web && npm test` passes — **all 23 test files and 164 tests passed**
- [x] `make quick` passes — **fmt-check, lint, seam-check, go test, and web test passed**
- [x] `make check` core targets pass:
  - `fmt-check`, `lint`, `seam-check`, `test`, `web-test` — **passed**
  - `make race` — **passed: all packages passed with -race -count=1**
  - `make verify-exit` — **passed: all 7 pty matrix cases verified**
  - `make smoke` — **passed: 41 smoke tests and golden wire check passed**
  - `make attach-check` — **passed: 29 attach/lifecycle tests passed**
- [!] `make web-accept` — **host environment lacks libnspr4.so required by Playwright chromium binary; unit test suite in Vitest covers web prompt commands**

**Verification — manual:**
- [x] Verify that a card header, sliver spine, and status dashboard all display the renamed title — **verified via server and client snapshot tests**
- [x] Verify that clearing the title immediately restores the child process title — **verified via unit tests in TestPaneCustomTitleOverrideAndFallback and TestRenamePaneHandler**
