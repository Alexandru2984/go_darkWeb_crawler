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

##@ Misc

.PHONY: clean
clean: ## Remove local build outputs (tmp/, coverage files).
	rm -rf tmp $(BACKEND_DIR)/$(COVER_FILE) $(BACKEND_DIR)/coverage.html

.PHONY: env-docker
env-docker: ## Create .env.docker from the template if it doesn't exist.
	@test -f .env.docker || cp .env.docker.example .env.docker
	@echo "→ .env.docker ready (edit it before `make up`)"
