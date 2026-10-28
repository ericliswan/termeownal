.PHONY: build static test race lint fmt check

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
