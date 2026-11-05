.PHONY: build run test test-race vet up down seed

ROWS ?= 1000000

build:
	go build -o bin/api ./cmd/api

run:
	@set -a; \
	. ./.env; \
	set +a; \
	go run ./cmd/api

test:
	go test ./...

test-unit:
	go test -v ./test/unit

test-integration:
	go test -v ./test/integration

test-race:
	go test -race ./...

vet:
	go vet ./...

up:
	docker compose up --build

down:
	docker compose down

seed:
	@case "$(ROWS)" in ''|*[!0-9]*) echo "ROWS must be a positive integer" >&2; exit 1;; esac; \
	if [ "$(ROWS)" -le 0 ]; then echo "ROWS must be a positive integer" >&2; exit 1; fi
	docker compose up -d --build postgres migrate
	@status="$$(docker wait migrate)"; \
	if [ "$$status" -ne 0 ]; then docker logs migrate; exit "$$status"; fi
	docker exec -i postgres psql -U blog -d blog \
		-v ON_ERROR_STOP=1 -v rows="$(ROWS)" -q < loadtest/seed/seed_blogs.sql
