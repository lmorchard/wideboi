#!/bin/sh
# Run the Go test suite inside a pane of a throwaway wideboi session and
# report whether the hosting server survives it.
#
# Everything lives under a private TMPDIR, so the session dir the tests
# see (and may sweep) is this one, not the real $TMPDIR/wideboi-<uid>.
#
# usage: scripts/repro-gotest-in-pane.sh [go test packages...]
set -u

root=$(cd "$(dirname "$0")/.." && pwd)
bin="$root/bin/wideboi"
pkgs=${*:-"./internal/... ./cmd/..."}

# Short: socket paths under it must fit sun_path (104 bytes on macOS).
scratch=$(mktemp -d /tmp/wbr.XXXX)
export TMPDIR="$scratch/"
unset WIDEBOI_SOCK WIDEBOI_SESSION
echo "scratch: $scratch"

"$bin" -L repro server >"$scratch/server.stderr" 2>&1 &
srv=$!
sock="$scratch/wideboi-$(id -u)/repro.sock"
i=0
until [ -S "$sock" ]; do
	i=$((i + 1))
	[ $i -gt 100 ] && { echo "server never bound $sock"; exit 2; }
	sleep 0.05
done
echo "server pid $srv on $sock"

"$bin" -L repro split --keep sh -c \
	"cd '$root' && go test -count=1 -cover $pkgs >'$scratch/gotest.out' 2>&1; echo \$? >'$scratch/gotest.done'"

start=$(date +%s)
while :; do
	if ! kill -0 "$srv" 2>/dev/null; then
		wait "$srv"
		echo "SERVER DIED after $(($(date +%s) - start))s, wait status $?"
		break
	fi
	if [ -f "$scratch/gotest.done" ]; then
		echo "server survived; go test exit $(cat "$scratch/gotest.done") after $(($(date +%s) - start))s"
		"$bin" -L repro kill-session
		wait "$srv"
		break
	fi
	sleep 1
done

echo "--- go test tail"
tail -25 "$scratch/gotest.out" 2>/dev/null
echo "--- server log"
cat "$scratch/wideboi-$(id -u)/repro.server.log" "$scratch/server.stderr" 2>/dev/null | tail -20
echo "--- exits.log"
cat "$scratch/wideboi-$(id -u)/exits.log" 2>/dev/null
