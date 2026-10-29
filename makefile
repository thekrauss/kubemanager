BIN_DIR = bin
ENUMGEN_BINARY = $(BIN_DIR)/enumgen

.PHONY: build-tools
build-tools:
	@echo "🛠  Building internal tools..."
	@mkdir -p $(BIN_DIR)
	@go build -o $(ENUMGEN_BINARY) ./tools/enumgen

.PHONY: gen-enums
gen-enums: build-tools
	@echo "Generating Enums..."
	@# the tool at the root (.)
	@$(ENUMGEN_BINARY)
	@echo "Enums generated successfully!"

#  Dev stack 

KEYCLOAK_BASE_URL ?= http://127.0.0.1:18081
KEYCLOAK_ADMIN_USER ?= admin
KEYCLOAK_ADMIN_PASSWORD ?= admin

.PHONY: dev-up
dev-up: ## Lance l'infra dev (postgres, redis, temporal, keycloak, mailhog, centrifugo, jaeger, seaweedfs, static-edge, caddy)
	@docker compose up -d k8sdb redis temporal temporal-ui keycloak mailhog centrifugo jaeger seaweedfs static-edge caddy
	@echo "✓ Infra up. Attends ~30s que Keycloak démarre, puis : make dev-bootstrap"

.PHONY: dev-down
dev-down: ## Arrête la stack dev (garde les volumes)
	@docker compose down

.PHONY: dev-reset
dev-reset: ## Arrête la stack ET purge les volumes (wipe Postgres, Keycloak, Redis)
	@docker compose down -v
	@echo "✓ Volumes purgés. Prochain 'make dev-up' ré-initialisera la DB Keycloak."

.PHONY: dev-bootstrap
dev-bootstrap: ## Bootstrap Keycloak (realm, clients, SMTP)
	@KEYCLOAK_BASE_URL=$(KEYCLOAK_BASE_URL) \
	 KEYCLOAK_ADMIN_USER=$(KEYCLOAK_ADMIN_USER) \
	 KEYCLOAK_ADMIN_PASSWORD=$(KEYCLOAK_ADMIN_PASSWORD) \
	 bash scripts/keycloak/bootstrap_platform.sh

.PHONY: dev-test-user
dev-test-user: ## Crée un user de test (override via TEST_USER_EMAIL, TEST_USER_PASSWORD)
	@KEYCLOAK_BASE_URL=$(KEYCLOAK_BASE_URL) \
	 KEYCLOAK_ADMIN_USER=$(KEYCLOAK_ADMIN_USER) \
	 KEYCLOAK_ADMIN_PASSWORD=$(KEYCLOAK_ADMIN_PASSWORD) \
	 bash scripts/keycloak/create_test_user.sh

.PHONY: dev-token
dev-token: ## Obtient un access token (nécessite TEST_USER_PASSWORD env var)
	@KEYCLOAK_BASE_URL=$(KEYCLOAK_BASE_URL) \
	 KEYCLOAK_ADMIN_USER=$(KEYCLOAK_ADMIN_USER) \
	 KEYCLOAK_ADMIN_PASSWORD=$(KEYCLOAK_ADMIN_PASSWORD) \
	 bash scripts/keycloak/get_test_token.sh

.PHONY: dev-decode
dev-decode: ## Décode un JWT (ACCESS_TOKEN=... ou auto-fetch)
	@bash scripts/keycloak/decode_test_token.sh

.PHONY: dev-test-realtime
dev-test-realtime: ## Test e2e Centrifugo (publish → WS subscriber → réception)
	@bash scripts/centrifugo/test-realtime-e2e.sh

.PHONY: dev-status
dev-status: ## Statut des services + URLs utiles
	@docker compose ps
	@echo ""
	@echo "Keycloak admin  : $(KEYCLOAK_BASE_URL)/admin (admin/admin)"
	@echo "MailHog UI      : http://localhost:8025"
	@echo "Centrifugo UI   : http://localhost:8000 (admin / \$$CENTRIFUGO_ADMIN_PASSWORD)"
	@echo "Temporal UI     : http://localhost:8233"
	@echo "Jaeger          : http://localhost:16686"
	@echo "Grafana         : http://localhost:3000"

.PHONY: help
help: ## Affiche cette aide
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
