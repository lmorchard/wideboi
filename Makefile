.PHONY: check test lint fmt fmt-check seam-check build run tidy verify-exit

# check is the pre-merge / CI gate, so every target it depends on must be
# read-only. fmt is deliberately NOT one of them: it rewrites files, and a
# gate that mutates the tree it is judging cannot be trusted by either CI
# or a reviewer. Run `make fmt` yourself; `make check` only tells you.
#
# verify-exit is included even though it costs ~18s (against a fraction of
# a second for the rest of check combined): it's the only target in this
# file that exercises the real binary in a real pty, and it's what proves
# the exit-status/teardown contract that the other tests only exercise in
# isolation. build's output (bin/wideboi) is gitignored, so verify-exit is
# still read-only with respect to the tree check is judging.
check: fmt-check lint seam-check test verify-exit

test:
	go test ./...

lint:
	go vet ./...

# fmt rewrites. fmt-check reports and fails. Keep them separate.
fmt:
	gofmt -l -w .

fmt-check:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then \
		echo "gofmt needed for:"; echo "$$out" | sed 's/^/  /'; \
		echo "run: make fmt"; \
		exit 1; \
	fi

# seam-check enforces the spec's "internal/client must not import
# internal/server" invariant in both directions, with Plan 1's existing
# crossings allowlisted in the script so it catches the next one.
seam-check:
	./scripts/seam-check.sh

build:
	go build -o bin/wideboi ./cmd/wideboi

run: build
	./bin/wideboi

tidy:
	go mod tidy

# verify-exit runs scripts/ptycheck.py against the built binary in a real
# pty. For each case it asserts three things:
#
#   1. The process dies BY the signal sent rather than exiting normally.
#      On its own this is weak -- the kernel's default disposition
#      satisfies it too, so an unarmed binary passes.
#   2. The alt-screen exit sequence reached the pty before that death.
#      This is what distinguishes wideboi's restore-then-re-raise from
#      a bare default kill, and what gives assertion 1 its meaning.
#   3. Nothing wideboi spawned outlived it -- including a nohup'd
#      background job the harness types into a pane first, because the
#      pane shells exit on their own when the master closes and so
#      cannot detect a skipped teardown by themselves.
#
# The size matrix includes the no-winsize (0x0) case that used to panic
# before main.go clamped width/height. The signal matrix covers the armed
# set that can be delivered without a core dump; SIGQUIT is armed too but
# left out here because its default disposition dumps core.
#
# ptycheck.py itself never hangs (bounded waits, SIGKILL-and-reap on
# timeout), so this target fails loudly rather than wedging CI or a dev
# machine.
verify-exit: build
	@for size in 80x24 4x2 1x1 0x0; do \
		python3 scripts/ptycheck.py --size $$size --signal SIGTERM || exit 1; \
	done; \
	for sig in SIGINT SIGHUP; do \
		python3 scripts/ptycheck.py --size 80x24 --signal $$sig || exit 1; \
	done
