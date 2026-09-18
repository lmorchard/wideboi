.PHONY: check test lint fmt build run tidy verify-exit

check: fmt lint test

test:
	go test ./...

lint:
	go vet ./...

fmt:
	gofmt -l -w .

build:
	go build -o bin/wideboi ./cmd/wideboi

run: build
	./bin/wideboi

tidy:
	go mod tidy

# verify-exit runs scripts/ptycheck.py against the built binary across a
# size matrix, including the no-winsize (0x0) case that used to panic
# before main.go clamped width/height. Each size must both exit by the
# sent signal (not normally) and leave no stray wideboi process behind;
# ptycheck.py itself never hangs (bounded waits, SIGKILL-and-reap on
# timeout), so this target fails loudly rather than wedging CI or a dev
# machine.
verify-exit: build
	for size in 80x24 4x2 1x1 0x0; do \
		python3 scripts/ptycheck.py --size $$size --signal SIGTERM || exit 1; \
	done
