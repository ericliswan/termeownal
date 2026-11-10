.PHONY: build static test race lint fmt check fuzz demo snapshot

build:
	go build ./...

static:
	CGO_ENABLED=0 go build -o bin/meow ./cmd/meow

test:
	go test ./...

race:
	go test -race ./...

lint:
	golangci-lint run

fmt:
	gofmt -w . && goimports -w .

# Everything AGENTS.md §3 requires before finishing a task.
check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	go test -race ./...
	golangci-lint run

# Fuzzes the cookie paste parser, as CI does when login.go changes.
fuzz:
	go test -run '^$$' -fuzz=FuzzSubmitPaste -fuzztime=60s ./internal/backend/instagram

# Records the README demo against the fake backend (needs vhs).
demo: static
	vhs docs/demo.tape

# Builds every release target into dist/ without publishing (needs goreleaser).
snapshot:
	goreleaser release --snapshot --clean
