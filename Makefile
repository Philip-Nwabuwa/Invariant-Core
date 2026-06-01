.DEFAULT_GOAL := help
SHELL := /bin/bash

-include .env
export

DB_URL ?= postgres://invariantcore:invariantcore@localhost:5432/invariantcore?sslmode=disable
MIGRATIONS_DIR := migrations

.PHONY: help tools dev down logs migrate-up migrate-down sqlc proto seed gen-settlement \
        build test test-integration lint run-ledger run-switchd run-mockrail reconcile

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

tools: ## Install Go-based dev tools (migrate, sqlc, buf, golangci-lint, protoc plugins)
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

dev: ## Start postgres, redis, jaeger
	docker compose up -d

down: ## Stop the local stack
	docker compose down

logs: ## Tail the local stack logs
	docker compose logs -f

migrate-up: ## Apply database migrations
	migrate -path $(MIGRATIONS_DIR) -database "$(DB_URL)" up

migrate-down: ## Roll back the last migration
	migrate -path $(MIGRATIONS_DIR) -database "$(DB_URL)" down 1

sqlc: ## Regenerate type-safe DB code from SQL
	sqlc generate

proto: ## Generate gRPC code from protobuf (buf)
	buf generate

seed: ## Create system + demo accounts
	go run ./scripts/seed

gen-settlement: ## Generate a fixture pair into ./out. Override with GEN_ARGS="--count N --discrepancies K --seed S"
	go run ./scripts/gen_settlement --internal-out out/internal.jsonl --external-out out/settlement.csv $(GEN_ARGS)

build: ## Build all binaries into ./bin
	go build -o ./bin/ ./cmd/...

test: ## Run unit + property tests with the race detector
	go test ./... -race -count=1

test-integration: ## Run integration tests (testcontainers; needs Docker)
	go test ./test/integration/... -race -count=1 -tags=integration

lint: ## Run golangci-lint
	golangci-lint run ./...

run-ledger: ## Run the ledger service
	go run ./cmd/ledger

run-switchd: ## Run the switch (transfer engine)
	go run ./cmd/switchd

run-mockrail: ## Run the mock NIP rail
	go run ./cmd/mockrail

reconcile: ## Run reconciliation. Usage: make reconcile INTERNAL=path EXTERNAL=path [RECON_ARGS="--format json --no-persist"]
	go run ./cmd/reconcile run --internal "$(INTERNAL)" --external "$(EXTERNAL)" $(RECON_ARGS)
