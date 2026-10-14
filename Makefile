.PHONY: all build test test-go test-py lint lint-go lint-py fmt proto proto-check fuzz cluster-up cluster-down py-sync clean

COMPOSE := docker compose -f deploy/docker-compose.yml

all: lint test build

build:
	go build ./...
	@if [ -d cmd ]; then go build -o bin/ ./cmd/...; fi

test: test-go test-py

test-go:
	go test -race ./...

test-py: py-sync
	uv run pytest -q

lint: lint-go lint-py

lint-go:
	go vet ./...
	golangci-lint run ./...
	buf lint

lint-py: py-sync
	uv run ruff check .
	uv run ruff format --check .

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './gen/*')
	uv run ruff format .

proto:
	buf generate

# Fails if generated code is stale relative to the .proto files.
proto-check: proto
	git diff --exit-code -- gen/

# Bounded fuzzing; FUZZTIME=5m make fuzz for a longer local run.
FUZZTIME ?= 20s
fuzz:
	go test -run '^$$' -fuzz FuzzFromBytes -fuzztime $(FUZZTIME) ./internal/segment

py-sync:
	uv sync --quiet

cluster-up:
	$(COMPOSE) up -d

cluster-down:
	$(COMPOSE) down

clean:
	rm -rf bin/
