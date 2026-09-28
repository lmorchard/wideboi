# Dev Session Plan: Deduplicate shellQuote and shellJoin

## Overview
Deduplicate `shellQuote` and `shellJoin` across `internal/commands` and `cmd/wideboi`. Align behavior so single-argument pass-through is consistent.

## Plan
1. Inspect implementations in `cmd/wideboi/control.go`, `internal/commands/registry.go`, `cmd/wideboi/main.go`.
2. Determine aligned behavior for single-argument pass-through vs unconditional quoting.
3. Export `ShellQuote` and `ShellJoin` in `internal/commands`.
4. Add comprehensive unit tests in `internal/commands/registry_test.go` (or `internal/commands/shell_test.go`).
5. Update callers in `internal/commands/registry.go`, `cmd/wideboi/control.go`, and `cmd/wideboi/main.go`.
6. Run `make check` / `go test ./...`.
