.PHONY: help dev dev-up dev-down dev-logs dev-shell prod prod-up prod-down prod-logs build clean test

# Default target
help:
	@echo "JFShare Backend - Development & Deployment Commands"
	@echo ""
	@echo "Development (in Docker):"
	@echo "  make dev-up      - Start development environment"
	@echo "  make dev-down    - Stop development environment"
	@echo "  make dev-logs    - View development logs"
	@echo "  make dev-shell   - Open shell in dev container"
	@echo "  make dev-rebuild - Rebuild dev container"
	@echo ""
	@echo "Production:"
	@echo "  make prod-up     - Build and start production"
	@echo "  make prod-down   - Stop production"
	@echo "  make prod-logs   - View production logs"
	@echo "  make prod-rebuild- Rebuild production image"
	@echo ""
	@echo "Utilities:"
	@echo "  make generate-keys - Generate API keys"
	@echo "  make clean         - Remove all containers and volumes"

# =============================================================================
# DEVELOPMENT (Docker-based, no local Go/Node needed)
# =============================================================================

dev-up:
	docker-compose -f docker-compose.dev.yml up -d
	@echo ""
	@echo "Development environment starting..."
	@echo "  Backend:  http://localhost:8097"
	@echo "  Frontend: http://localhost:5173"
	@echo "  pgAdmin:  http://localhost:5050 (run with: make dev-pgadmin)"
	@echo ""
	@echo "Run 'make dev-logs' to see logs"

dev-down:
	docker-compose -f docker-compose.dev.yml down

dev-logs:
	docker-compose -f docker-compose.dev.yml logs -f dev

dev-shell:
	docker-compose -f docker-compose.dev.yml exec dev bash

dev-rebuild:
	docker-compose -f docker-compose.dev.yml build --no-cache dev
	docker-compose -f docker-compose.dev.yml up -d dev

dev-pgadmin:
	docker-compose -f docker-compose.dev.yml --profile tools up -d pgadmin
	@echo "pgAdmin: http://localhost:5050"
	@echo "Login: admin@local.dev / admin"

# =============================================================================
# PRODUCTION
# =============================================================================

prod-up:
	@if [ ! -f .env ]; then \
		echo "ERROR: .env file not found. Copy .env.example and configure it."; \
		exit 1; \
	fi
	docker-compose up -d --build
	@echo ""
	@echo "Production environment running at http://localhost:8097"

prod-down:
	docker-compose down

prod-logs:
	docker-compose logs -f jfshare

prod-rebuild:
	docker-compose build --no-cache jfshare
	docker-compose up -d jfshare

# =============================================================================
# UTILITIES
# =============================================================================

generate-keys:
	@echo "Backend API Key:"
	@openssl rand -hex 32
	@echo ""
	@echo "Postgres Password:"
	@openssl rand -hex 16

clean:
	docker-compose -f docker-compose.dev.yml down -v --remove-orphans 2>/dev/null || true
	docker-compose down -v --remove-orphans 2>/dev/null || true
	docker rmi jfshare:latest 2>/dev/null || true
	@echo "Cleaned up containers and volumes"

# =============================================================================
# LOCAL DEVELOPMENT (requires Go and Node installed)
# =============================================================================

local-backend:
	go run ./cmd/server

local-frontend:
	cd web && npm install && npm run dev

local-build:
	cd web && npm install && npm run build
	go build -o jfshare ./cmd/server

test:
	go test -v ./...
