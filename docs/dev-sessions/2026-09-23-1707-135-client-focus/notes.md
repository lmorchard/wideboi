## Progress
- Refactored `layout.Strip` mutating methods to take a `PaneID` instead of relying on an internal `focusIndex`.
- Updated `Server` to drop `MsgFocusPane` and instead take explicit `PaneID`s in `MsgVerb` for structural mutations.
- Updated `Client` to manage focus locally, and handle `MsgVerb`s for focus changes locally without a roundtrip.
- Moved `PaneStatus` definitions to `protocol` package so that `MsgLayoutSnapshot` carries semantically meaningful statuses to clients (as requested).
- The codebase now compiles correctly (`go build ./...` succeeds).

## Blockers / Next steps
- Several tests are failing in `internal/client/` and `internal/layout/` because they still assert the old behavior (e.g. `TestMoveLeftAndRightSwapWithNeighbourAndKeepFocus`, and mouse tests which try to assert `MsgFocusPane` transport messages were sent).
- We need to go through the failed test outputs from `make check` and adjust the test fixtures and assertions to match the new client-side focus architecture.
