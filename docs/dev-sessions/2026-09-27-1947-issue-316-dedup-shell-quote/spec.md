# Issue 316: Deduplicate shellQuote and shellJoin between cmd/wideboi and internal/commands

## Problem
`shellQuote` and `shellJoin` are independently implemented in:
- `cmd/wideboi/control.go:214-230`
- `internal/commands/registry.go:123-141`
- `cmd/wideboi/main.go:1152` (using `shellQuote`)

Additionally, `control.go` implements single-argument pass-through (`if len(args) == 1 { return args[0] }`) whereas `registry.go` quotes unconditionally, creating subtle behavioral divergence between internal commands (`:split`) and external CLI invocations (`wideboi split`).

## Proposed Fix
Export `ShellQuote` and `ShellJoin` from `internal/commands`, align behavior, and reuse them across `cmd/wideboi/control.go`, `cmd/wideboi/main.go`, and `internal/commands/registry.go`.
