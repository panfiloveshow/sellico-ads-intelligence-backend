.PHONY: build test test-integration test-cover migrate-up migrate-down sqlc-generate lint real-data-only docker-up docker-down docker-monitoring pack-extension backup-db

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

# Fails when the checked-in typed layer differs from what sqlc produces — i.e.
# when someone hand-edited a *.sql.go, or a *_manual.go started duplicating a
# generated declaration. Both used to happen silently and left `sqlc generate`
# unable to run at all.
#
# Compares against a snapshot rather than `git diff`, so it is meaningful with
# uncommitted work in the tree.
sqlc-check:
	@rm -rf .sqlc-check && cp -r internal/repository/sqlc .sqlc-check
	@sqlc generate
	@if diff -r .sqlc-check internal/repository/sqlc > /dev/null; then \
		rm -rf .sqlc-check; \
		go build ./... && echo "sqlc layer is in sync"; \
	else \
		rm -rf .sqlc-check; \
		echo "sqlc output differed — the tree now holds the regenerated files, review and commit them"; \
		exit 1; \
	fi

# --- Linting & Security ---
lint:
	golangci-lint run ./...

real-data-only:
	bash scripts/check-real-data-only.sh

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
