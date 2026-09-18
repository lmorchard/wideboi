.PHONY: check test lint fmt build run tidy

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
