.PHONY: all build test test-go test-py lint lint-go lint-py fmt proto proto-check fuzz cluster-up cluster-down py-sync clean demo

COMPOSE := docker compose -f deploy/docker-compose.yml

all: lint test build

build:
	go build ./...
	@if [ -d cmd ]; then go build -o bin/ ./cmd/...; fi

# The demo web page on http://127.0.0.1:8080 (cmd/demo): STATIONS=0 skips live monitoring.
STATIONS ?= 3
demo: build py-sync
	uv run python -m bench.scripts.demo --stations $(STATIONS)

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

# INDEX selects data/segments/<INDEX> (default medium/p2, matching deploy/cluster.p2.json).
cluster-up:
	$(COMPOSE) up -d --build

cluster-down:
	$(COMPOSE) down

clean:
	rm -rf bin/
