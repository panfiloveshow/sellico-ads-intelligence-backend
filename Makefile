.PHONY: build test test-integration test-cover migrate-up migrate-down sqlc-generate lint docker-up docker-down docker-monitoring pack-extension backup-db

# --- Build ---
build:
	go build -o bin/api ./cmd/api
	go build -o bin/worker ./cmd/worker

# --- Tests ---
test:
	go test ./... -v -race -count=1

test-integration:
	go test -tags=integration ./... -v -race -count=1 -timeout 120s

test-cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# --- Database ---
migrate-up:
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path migrations -database "$(DATABASE_URL)" down

backup-db:
	bash scripts/backup-db.sh

# --- Code generation ---
sqlc-generate:
	sqlc generate

# --- Linting & Security ---
lint:
	golangci-lint run ./...

gosec:
	gosec -exclude-generated ./...

# --- Docker ---
docker-up:
	docker compose up -d --build

docker-down:
	docker compose down -v

docker-monitoring:
	docker compose up -d prometheus grafana

# --- Extension ---
pack-extension:
	bash scripts/pack-extension.sh
