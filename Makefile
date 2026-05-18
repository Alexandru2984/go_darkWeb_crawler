# Onion Spider — developer Makefile.
# Run `make` (no args) for a list of targets.
#
# Prod is deployed via systemd on the host VPS; this Makefile does NOT touch
# the live binary at backend/onion-spider-api. Use `make build-prod` to
# produce a fresh binary in a separate location, then promote it manually.

SHELL          := /bin/bash
BACKEND_DIR    := backend
FRONTEND_DIR   := frontend
API_PKG        := ./cmd/api
CRAWLER_PKG    := ./cmd/crawler
GO_BUILD_FLAGS := -ldflags="-s -w" -trimpath
COVER_FILE     := coverage.out

.DEFAULT_GOAL := help

# ── Help ─────────────────────────────────────────────────────────────────────
.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage: make \033[36m<target>\033[0m\n\nTargets:\n"} \
	  /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } \
	  /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Go (backend)

.PHONY: build
build: ## Build the API binary to ./tmp/onion-spider-api (does NOT touch the live binary).
	@mkdir -p tmp
	cd $(BACKEND_DIR) && go build $(GO_BUILD_FLAGS) -o ../tmp/onion-spider-api $(API_PKG)
	@echo "→ built tmp/onion-spider-api"

.PHONY: build-crawler
build-crawler: ## Build the standalone crawler binary to ./tmp/onion-spider-crawler.
	@mkdir -p tmp
	cd $(BACKEND_DIR) && go build $(GO_BUILD_FLAGS) -o ../tmp/onion-spider-crawler $(CRAWLER_PKG)

.PHONY: test
test: ## Run all Go tests.
	cd $(BACKEND_DIR) && go test ./...

.PHONY: race
race: ## Run all Go tests with the race detector.
	cd $(BACKEND_DIR) && go test -race ./...

.PHONY: cover
cover: ## Run tests with coverage and print summary.
	cd $(BACKEND_DIR) && go test -coverprofile=$(COVER_FILE) ./... \
	  && go tool cover -func=$(COVER_FILE) | tail -1

.PHONY: cover-html
cover-html: ## Generate HTML coverage report at backend/coverage.html.
	cd $(BACKEND_DIR) && go test -coverprofile=$(COVER_FILE) ./... \
	  && go tool cover -html=$(COVER_FILE) -o coverage.html
	@echo "→ open $(BACKEND_DIR)/coverage.html"

.PHONY: vet
vet: ## go vet across all packages.
	cd $(BACKEND_DIR) && go vet ./...

.PHONY: lint
lint: ## Run golangci-lint (install: brew install golangci-lint OR go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest).
	cd $(BACKEND_DIR) && golangci-lint run ./...

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum.
	cd $(BACKEND_DIR) && go mod tidy

.PHONY: vulncheck
vulncheck: ## Run govulncheck against the API package (install: go install golang.org/x/vuln/cmd/govulncheck@latest).
	cd $(BACKEND_DIR) && govulncheck ./...

##@ Frontend

.PHONY: frontend-install
frontend-install: ## npm ci in the frontend directory.
	cd $(FRONTEND_DIR) && npm ci --no-audit --no-fund

.PHONY: frontend-build
frontend-build: ## Build the production frontend bundle into frontend/dist.
	cd $(FRONTEND_DIR) && npm run build

.PHONY: frontend-dev
frontend-dev: ## Run the Vite dev server (hot reload) on http://localhost:5173.
	cd $(FRONTEND_DIR) && npm run dev

##@ Docker (dev stack)

.PHONY: up
up: ## Bring up the dev stack (api + postgres + tor + frontend).
	docker compose --env-file .env.docker up --build -d
	@echo "→ frontend on http://localhost:8080"

.PHONY: up-fg
up-fg: ## Same as `up` but stays attached (Ctrl-C to stop).
	docker compose --env-file .env.docker up --build

.PHONY: down
down: ## Stop the dev stack (keeps volumes).
	docker compose down

.PHONY: nuke
nuke: ## Stop the dev stack AND wipe the postgres volume. Destructive.
	docker compose down -v

.PHONY: logs
logs: ## Tail logs for all services.
	docker compose logs -f --tail=200

.PHONY: ps
ps: ## Show running compose services.
	docker compose ps

##@ Database migrations

MIGRATIONS_DIR := $(BACKEND_DIR)/internal/database/migrations
# Reads DATABASE_URL from backend/.env if present so `make migrate-*` works
# without exporting variables. Override with `DB_URL=... make migrate-up`.
DB_URL ?= $(shell test -f $(BACKEND_DIR)/.env && grep -E '^DATABASE_URL=' $(BACKEND_DIR)/.env | head -1 | cut -d= -f2-)

# Embedded migrations always run at API startup, so these targets exist for
# manual control during dev (rolling back, checking state, creating new files).
# They use the external `migrate` CLI — install with:
#   go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
MIGRATE_BIN := migrate
MIGRATE_INSTALL_HINT := "Install: go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest"

.PHONY: migrate-up
migrate-up: ## Apply all pending migrations against $$DB_URL (defaults to backend/.env DATABASE_URL).
	@command -v $(MIGRATE_BIN) >/dev/null || (echo $(MIGRATE_INSTALL_HINT); exit 1)
	@test -n "$(DB_URL)" || (echo "DB_URL is empty — set DB_URL=... or add DATABASE_URL to backend/.env"; exit 1)
	$(MIGRATE_BIN) -path $(MIGRATIONS_DIR) -database "$(DB_URL)" up

.PHONY: migrate-down
migrate-down: ## Roll back the last migration. DESTRUCTIVE — wipes the schema it owns.
	@command -v $(MIGRATE_BIN) >/dev/null || (echo $(MIGRATE_INSTALL_HINT); exit 1)
	@test -n "$(DB_URL)" || (echo "DB_URL is empty — set DB_URL=... or add DATABASE_URL to backend/.env"; exit 1)
	$(MIGRATE_BIN) -path $(MIGRATIONS_DIR) -database "$(DB_URL)" down 1

.PHONY: migrate-version
migrate-version: ## Show the currently-applied migration version.
	@command -v $(MIGRATE_BIN) >/dev/null || (echo $(MIGRATE_INSTALL_HINT); exit 1)
	@test -n "$(DB_URL)" || (echo "DB_URL is empty — set DB_URL=... or add DATABASE_URL to backend/.env"; exit 1)
	$(MIGRATE_BIN) -path $(MIGRATIONS_DIR) -database "$(DB_URL)" version

.PHONY: migrate-new
migrate-new: ## Scaffold a new migration. Usage: make migrate-new NAME=add_users_email_index
	@command -v $(MIGRATE_BIN) >/dev/null || (echo $(MIGRATE_INSTALL_HINT); exit 1)
	@test -n "$(NAME)" || (echo "NAME is required: make migrate-new NAME=add_..."; exit 1)
	$(MIGRATE_BIN) create -ext sql -dir $(MIGRATIONS_DIR) -seq $(NAME)

.PHONY: migrate-force
migrate-force: ## Force-set version (recovers from dirty state). Usage: make migrate-force V=1
	@command -v $(MIGRATE_BIN) >/dev/null || (echo $(MIGRATE_INSTALL_HINT); exit 1)
	@test -n "$(DB_URL)" || (echo "DB_URL is empty"; exit 1)
	@test -n "$(V)" || (echo "V is required: make migrate-force V=1"; exit 1)
	$(MIGRATE_BIN) -path $(MIGRATIONS_DIR) -database "$(DB_URL)" force $(V)

##@ Misc

.PHONY: clean
clean: ## Remove local build outputs (tmp/, coverage files).
	rm -rf tmp $(BACKEND_DIR)/$(COVER_FILE) $(BACKEND_DIR)/coverage.html

.PHONY: env-docker
env-docker: ## Create .env.docker from the template if it doesn't exist.
	@test -f .env.docker || cp .env.docker.example .env.docker
	@echo "→ .env.docker ready (edit it before `make up`)"
