# Research: Issue 332 Review Fixes

- Review findings from PR #354:
  1. `cmd/wideboi/main.go:272`: `split` calls `connectOrSpawn`, which starts an owned server on missing socket without checking nesting.
  2. `cmd/wideboi/main.go:277`: `unset WIDEBOI` hint is misleading because `LC_WIDEBOI` and `WIDEBOI_PANE_ID` remain set.
  3. `internal/server/server.go:386`: `TERM_PROGRAM=wideboi` overwrites host terminal capability detection.
  4. `internal/server/server_test.go:530`: `strings.Contains("WIDEBOI=1\n")` matches suffix of `LC_WIDEBOI=1\n`.
  5. `internal/testenv/testenv.go`: `WIDEBOI_ALLOW_NESTED` should be cleared in `testenv.Run`.
  6. `docs/MANUAL.md`: SSH locale forwarding requirements should be qualified.
