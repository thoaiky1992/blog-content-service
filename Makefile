.PHONY: build run test test-race vet up down

build:
	go build -o bin/api ./cmd/api

run:
	@set -a; \
	. ./.env; \
	set +a; \
	go run ./cmd/api

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

up:
	docker compose up --build

down:
	docker compose down
