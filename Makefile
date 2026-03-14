.PHONY: pki server enrol enroll up down clean demo check help ui-install ui-build ui-dev

CERTS_DIR := certs
SERVER_URL ?= http://localhost:8080
UI_DIR := ui

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

ui-install: ## Install frontend dependencies
	cd $(UI_DIR) && npm install

ui-build: ## Build the React dashboard for Go static serving
	cd $(UI_DIR) && if [ ! -d node_modules ]; then npm install; fi && npm run build

ui-dev: ## Start the Vite frontend dev server
	cd $(UI_DIR) && if [ ! -d node_modules ]; then npm install; fi && npm run dev

pki: ## Bootstrap PKI (generate all certificates)
	bash ./scripts/bootstrap-pki.sh $(CERTS_DIR)

server: ## Build and run the enrollment server
	@$(MAKE) ui-build
	CERTS_DIR=$(CERTS_DIR) LISTEN_ADDR=:8080 go run ./cmd/server

enrol: ## Run the edge enrollment agent
	SERVER_URL=$(SERVER_URL) bash ./scripts/enroll-device.sh

enroll: enrol ## Alias for US spelling

up: ## Start the full stack (docker-compose)
	docker compose up -d

down: ## Stop the stack
	docker compose down

check: ## Run basic Go checks
	go test ./...

clean: ## Remove generated certificates and temp files
	rm -rf $(CERTS_DIR)/* /tmp/edgeflux-*

demo: pki server ## Bootstrap PKI then run server (Ctrl+C to stop, then 'make enrol' in another terminal)
