#!/bin/sh
# Real-binary check for #299: a pane's scroll region survives
# `upgrade-server`. The pane sets rows 3-5 as its region and echoes each
# line it reads (tty echo off). After the upgrade, 10 more lines must
# scroll inside rows 3-5 only: TOP stays on row 1 and nothing lands
# below row 5. Without the region restored they would scroll the whole
# screen, and TOP would scroll away.
set -eu
cd "$(git rev-parse --show-toplevel)"
tmp=$(mktemp -d /tmp/wb299.XXXXXX)
bin=$tmp/wideboi
sock=$tmp/s.sock
go build -o "$bin" ./cmd/wideboi
"$bin" -s "$sock" server >"$tmp/server.log" 2>&1 &
trap '"$bin" -s "$sock" kill-session >/dev/null 2>&1 || true; rm -rf "$tmp"' EXIT
i=0; until "$bin" -s "$sock" status >/dev/null 2>&1; do i=$((i+1)); [ $i -lt 100 ] || { echo "server did not start"; exit 1; }; sleep 0.05; done

child='stty -echo; printf "\033[2J\033[1;1HTOP\033[3;5r\033[5;1H"; while read l; do printf "%s\n" "$l"; done'
pane=$("$bin" -s "$sock" split --keep sh -c "$child")
sleep 0.5
"$bin" -s "$sock" send -e "$pane" "before"
"$bin" -s "$sock" upgrade-server "$bin"
i=0; until "$bin" -s "$sock" status >/dev/null 2>&1; do i=$((i+1)); [ $i -lt 600 ] || { echo "server did not come back"; exit 1; }; sleep 0.1; done
for n in 1 2 3 4 5 6 7 8 9 10; do "$bin" -s "$sock" send -e "$pane" "after$n"; done
sleep 0.5
cap=$("$bin" -s "$sock" capture "$pane")
printf '%s\n' "$cap" | head -8 | nl -ba
row1=$(printf '%s\n' "$cap" | sed -n 1p)
below=$(printf '%s\n' "$cap" | sed -n '6,$p' | tr -d ' \n')
[ "$row1" = "TOP" ] || { echo "FAIL: row 1 is '$row1', want TOP"; exit 1; }
[ -z "$below" ] || { echo "FAIL: output below the region: $below"; exit 1; }
# Each line ends in a newline, so the last one has scrolled up to row 4.
printf '%s\n' "$cap" | sed -n 4p | grep -q after10 || { echo "FAIL: row 4 lacks after10"; exit 1; }
echo "PASS: scroll region survived upgrade-server"
