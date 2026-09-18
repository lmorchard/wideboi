#!/usr/bin/env bash
#
# seam-check enforces the spec's client/server boundary:
#
#   "internal/client must not import internal/server, and neither may
#    import the other's types. They share layout and protocol only. A
#    go list check in CI enforces this, because the boundary erodes
#    silently otherwise."
#
# It checks both directions, and it checks test imports too, because a
# test that reaches across the seam pulls the dependency into the module
# graph just as surely as production code does.
#
# Known violations are allowlisted below rather than ignored. The point
# of a tripwire with a known-good baseline is that it catches the NEXT
# one.

set -euo pipefail

MODULE="github.com/lmorchard/wideboi"

# Plan 1 temporaries, dated 2026-09-18. Every entry here is a boundary
# violation that exists because Plan 1 runs both halves in one process
# with no protocol or transport package yet. Plan 2 cuts the seam for
# real (spec milestone 6) and must empty this list; nothing new should
# ever be added to it.
#
#   internal/client -> internal/server/{ptyx,term}
#       client.Pane owns a PTY and an emulator directly. The spec puts
#       both server-side, behind protocol messages.
#   internal/server/term -> internal/client/compose
#       term's tests render into a compose.Surface to assert on emulator
#       output. Test-only, but still the seam.
ALLOW="
internal/client -> internal/server/ptyx
internal/client -> internal/server/term
internal/server/term -> internal/client/compose
"

cd "$(dirname "$0")/.."

# One line per package: importpath, then every import including those
# pulled in only by tests.
listing=$(go list -f \
  '{{.ImportPath}} {{join .Imports " "}} {{join .TestImports " "}} {{join .XTestImports " "}}' \
  ./internal/...)

# [[ ]] rather than case: macOS still ships bash 3.2, whose parser ends a
# $(...) at the first ')' it sees, including a case pattern's.
violations=$(
  echo "$listing" | while read -r pkg imports; do
    short_pkg=${pkg#"$MODULE"/}
    if [[ $short_pkg == internal/client* ]]; then
      other=internal/server
    elif [[ $short_pkg == internal/server* ]]; then
      other=internal/client
    else
      continue
    fi
    for imp in $imports; do
      if [[ $imp == "$MODULE/$other"* ]]; then
        echo "$short_pkg -> ${imp#"$MODULE"/}"
      fi
    done
  done | sort -u
)

allowed=$(echo "$ALLOW" | grep -v '^[[:space:]]*$' | sed 's/^[[:space:]]*//' | sort -u)

new=$(comm -23 <(echo "$violations") <(echo "$allowed") || true)
stale=$(comm -13 <(echo "$violations") <(echo "$allowed") || true)

status=0

if [ -n "$new" ]; then
  echo "seam-check: FAIL -- internal/client and internal/server must not import each other."
  echo "$new" | sed 's/^/  /'
  echo
  echo "Route this through internal/protocol and internal/transport instead,"
  echo "or, if it is genuinely another Plan 1 temporary, add it to ALLOW in"
  echo "$0 with a dated reason."
  status=1
fi

if [ -n "$stale" ]; then
  echo "seam-check: FAIL -- allowlisted violations that no longer exist:"
  echo "$stale" | sed 's/^/  /'
  echo
  echo "Good news; delete these entries from ALLOW so the tripwire tightens."
  status=1
fi

if [ "$status" -eq 0 ]; then
  count=$(echo "$allowed" | grep -c . || true)
  echo "seam-check: OK -- no new client/server crossings ($count Plan 1 temporaries allowlisted)."
fi

exit "$status"
