# ProxyDock / Traefik Fleet Manager — top-level Makefile.
#
# All paths are relative to repo root. Targets are grouped:
#   - build / test  (Go + web)
#   - demo          (docker compose lifecycle)
#   - smoke         (quick curl checks against a running demo)

SHELL              := bash
.SHELLFLAGS        := -eu -o pipefail -c
.DEFAULT_GOAL      := help

API_DIR            := apps/api
WEB_DIR            := apps/web
WEBUI_EMBED_DIR    := $(API_DIR)/internal/webui/dist
COMPOSE_DIR        := deploy/docker-compose
PROVIDER_DIR       := providers/traefik-fleet

MANAGER_BIN        := $(API_DIR)/manager-api
ADMIN_TOKEN        ?= demo-admin
MANAGER_URL        ?= http://localhost:8090
TRAEFIK_1          ?= http://localhost:8081
TRAEFIK_2          ?= http://localhost:8082

# ---------------------------------------------------------------------------
# Help
# ---------------------------------------------------------------------------

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"; printf "Targets:\n"} \
	      /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

# ---------------------------------------------------------------------------
# Web (Vite SPA — embedded into the manager-api binary)
# ---------------------------------------------------------------------------

.PHONY: web-deps
web-deps: ## npm ci in apps/web (run once after package.json changes).
	cd $(WEB_DIR) && npm ci

.PHONY: web
web: ## Build the SPA into apps/web/dist.
	cd $(WEB_DIR) && npm run build

.PHONY: web-dev
web-dev: ## Run the Vite dev server (hot reload, talks to manager-api on :8090).
	cd $(WEB_DIR) && npm run dev

.PHONY: web-embed
web-embed: web ## Build SPA and copy into the Go embed dir (for `go build` outside Docker).
	rm -rf $(WEBUI_EMBED_DIR)
	mkdir -p $(WEBUI_EMBED_DIR)
	cp -r $(WEB_DIR)/dist/. $(WEBUI_EMBED_DIR)/

# ---------------------------------------------------------------------------
# Go (manager-api + provider plugin)
# ---------------------------------------------------------------------------

.PHONY: build
build: web-embed ## Build the manager-api binary with the SPA embedded.
	cd $(API_DIR) && go build -o manager-api ./cmd/manager-api

.PHONY: tidy
tidy: ## go mod tidy across all Go modules.
	cd $(API_DIR) && go mod tidy
	cd $(PROVIDER_DIR) && go mod tidy

.PHONY: vet
vet: ## go vet across all Go modules.
	cd $(API_DIR) && go vet ./...
	cd $(PROVIDER_DIR) && go vet ./...

.PHONY: fmt
fmt: ## gofmt -w across all Go sources.
	gofmt -w $(API_DIR) $(PROVIDER_DIR)

.PHONY: test
test: ## Run unit tests (fast — Postgres integration tests are skipped).
	cd $(API_DIR) && go test ./...
	cd $(PROVIDER_DIR) && go test ./...

.PHONY: test-integration
test-integration: ## Run Postgres integration tests (testcontainers — requires Docker).
	cd $(API_DIR) && go test -tags integration ./internal/store/postgres/...

.PHONY: test-all
test-all: test test-integration ## Unit + integration tests.

# ---------------------------------------------------------------------------
# Docker compose demo
# ---------------------------------------------------------------------------

.PHONY: demo-up
demo-up: ## Bring up the full demo stack and add proxy host domains to /etc/hosts (sudo, NO_HOSTS=1 to skip).
	cd $(COMPOSE_DIR) && docker compose up -d --build
	@$(MAKE) -s _wait-manager
	@$(if $(filter 1 true yes,$(NO_HOSTS)),echo "skipping /etc/hosts sync (NO_HOSTS set)",$(MAKE) -s hosts-sync)

.PHONY: demo-down
demo-down: ## Stop the demo (preserves volumes) and remove the /etc/hosts managed block.
	@$(if $(filter 1 true yes,$(NO_HOSTS)),echo "skipping /etc/hosts clear (NO_HOSTS set)",$(MAKE) -s hosts-clear)
	cd $(COMPOSE_DIR) && docker compose down

.PHONY: demo-clean
demo-clean: ## Stop demo, wipe volumes, and remove the /etc/hosts managed block.
	@$(if $(filter 1 true yes,$(NO_HOSTS)),echo "skipping /etc/hosts clear (NO_HOSTS set)",$(MAKE) -s hosts-clear)
	cd $(COMPOSE_DIR) && docker compose down -v

# Internal: block until manager-api responds 200 on /healthz.
.PHONY: _wait-manager
_wait-manager:
	@printf "waiting for manager-api at $(MANAGER_URL)/healthz "
	@for i in $$(seq 1 60); do \
	  if curl -fsS $(MANAGER_URL)/healthz >/dev/null 2>&1; then echo "ok"; exit 0; fi; \
	  printf "."; sleep 1; \
	done; \
	echo "timeout"; exit 1

.PHONY: demo-logs
demo-logs: ## Tail logs from all demo services.
	cd $(COMPOSE_DIR) && docker compose logs -f

.PHONY: demo-rebuild
demo-rebuild: ## Rebuild + restart only the manager-api container (after Go/web changes).
	cd $(COMPOSE_DIR) && docker compose up -d --build manager-api

.PHONY: demo-ps
demo-ps: ## Show the demo's container status.
	cd $(COMPOSE_DIR) && docker compose ps

.PHONY: info
info: ## Summarise a running demo: containers, URLs, fleets, revisions, agents.
	@echo "== containers =="
	@cd $(COMPOSE_DIR) && docker compose ps --format 'table {{.Service}}\t{{.Status}}\t{{.Ports}}'
	@echo
	@echo "== endpoints =="
	@printf "  manager UI / API   %s\n" "$(MANAGER_URL)"
	@printf "  metrics            %s/metrics\n" "$(MANAGER_URL)"
	@printf "  Jaeger UI          http://localhost:16686\n"
	@printf "  traefik-1 http     %s   (https :8443, tcp :6900)\n" "$(TRAEFIK_1)"
	@printf "  traefik-2 http     %s   (https :8444, tcp :6901)\n" "$(TRAEFIK_2)"
	@echo
	@echo "== fleets / revisions / agents =="
	@curl -fsS -H "Authorization: Bearer $(ADMIN_TOKEN)" $(MANAGER_URL)/api/v1/fleets 2>/dev/null \
	  | python3 -c 'import json,sys; d=json.load(sys.stdin); [print("  fleet  {:<12} published_rev={}".format(f["id"], f.get("published_revision_id"))) for f in d.get("fleets",[])]' \
	  || echo "  (manager not reachable or admin token mismatch)"
	@curl -fsS -H "Authorization: Bearer $(ADMIN_TOKEN)" $(MANAGER_URL)/api/v1/fleets/homelab/agents 2>/dev/null \
	  | python3 -c 'import json,sys; d=json.load(sys.stdin); [print("  agent  {:<12} last_heartbeat={} last_rev={}".format(a["id"], a.get("last_heartbeat_at"), a.get("last_revision_seen"))) for a in d.get("agents",[])]' \
	  || true
	@curl -fsS -H "Authorization: Bearer $(ADMIN_TOKEN)" $(MANAGER_URL)/api/v1/fleets/homelab/proxy_hosts 2>/dev/null \
	  | python3 -c 'import json,sys; d=json.load(sys.stdin); [print("  host   {:<14} {:<4} {:<28} -> {} (mw={})".format(h["name"], h["protocol"], h.get("domain",""), h["upstream_url"], len(h.get("middlewares",[])))) for h in d.get("proxy_hosts",[])]' \
	  || true

# ---------------------------------------------------------------------------
# Smoke tests against a running demo
# ---------------------------------------------------------------------------

.PHONY: smoke
smoke: ## Curl the HTTP routes through both Traefik agents.
	@echo "== manager healthz =="
	@curl -fsS $(MANAGER_URL)/healthz && echo
	@echo "== whoami via traefik-1 =="
	@curl -s -o /dev/null -w "  http  %{http_code}\n" -H "Host: whoami.localhost"     $(TRAEFIK_1)
	@curl -sk -o /dev/null -w "  https %{http_code}\n" -H "Host: secure.localhost"    https://localhost:8443
	@curl -sk -o /dev/null -w "  acme  %{http_code}\n" -H "Host: acme.localhost"      https://localhost:8443
	@echo "== forwardAuth (allow / deny) via traefik-1 =="
	@curl -s -o /dev/null -w "  allow %{http_code}\n" -H "Host: auth-allow.localhost" $(TRAEFIK_1)
	@curl -s -o /dev/null -w "  deny  %{http_code}\n" -H "Host: auth-deny.localhost"  $(TRAEFIK_1)
	@echo "== whoami via traefik-2 =="
	@curl -s -o /dev/null -w "  http  %{http_code}\n" -H "Host: whoami.localhost"     $(TRAEFIK_2)

.PHONY: fleets
fleets: ## List fleets (requires ADMIN_TOKEN).
	@curl -fsS -H "Authorization: Bearer $(ADMIN_TOKEN)" $(MANAGER_URL)/api/v1/fleets | python3 -m json.tool

.PHONY: revisions
revisions: ## List revisions for the homelab fleet.
	@curl -fsS -H "Authorization: Bearer $(ADMIN_TOKEN)" $(MANAGER_URL)/api/v1/fleets/homelab/revisions | python3 -m json.tool

.PHONY: metrics
metrics: ## Dump Prometheus metrics from the manager.
	@curl -fsS $(MANAGER_URL)/metrics

# ---------------------------------------------------------------------------
# Dynamic host management (talks to a running manager-api)
# ---------------------------------------------------------------------------
#
# All targets here use scripts/host.py and pick up MANAGER_URL,
# ADMIN_TOKEN, and FLEET (default homelab) from env / make vars.
#
# Examples:
#   make add-host NAME=api DOMAIN=api.localhost UPSTREAM=http://whoami:80
#   make add-host NAME=secure DOMAIN=secure.localhost UPSTREAM=http://whoami:80 TLS=1 ENTRY_POINTS=websecure
#   make add-host NAME=oidc-app DOMAIN=oidc.localhost UPSTREAM=http://whoami:80 \
#                 FORWARD_AUTH=http://httpbin:80/status/200
#   make add-host NAME=redis DOMAIN='*' UPSTREAM=redis:6379 PROTOCOL=tcp ENTRY_POINTS=tcpentry
#   make rm-host NAME=oidc-app
#   make hosts            # list
#   make publish          # publish a revision (e.g. after editing via UI)
#   make rm-host NAME=foo NO_PUBLISH=1   # delete without auto-publishing

FLEET              ?= homelab
HOST_ENV           := MANAGER_URL='$(MANAGER_URL)' ADMIN_TOKEN='$(ADMIN_TOKEN)' FLEET='$(FLEET)'

.PHONY: hosts
hosts: ## List proxy hosts in the fleet.
	@$(HOST_ENV) python3 scripts/host.py list

.PHONY: add-host
add-host: ## Create a proxy host (NAME, DOMAIN, UPSTREAM required; FORWARD_AUTH optional).
	@if [ -z "$(NAME)" ] || [ -z "$(DOMAIN)" ] || [ -z "$(UPSTREAM)" ]; then \
	  echo "Usage: make add-host NAME=<n> DOMAIN=<d> UPSTREAM=<u> [PROTOCOL=http|tcp|udp] [TLS=1] [ENTRY_POINTS=web,websecure] [FORWARD_AUTH=<url>] [NO_PUBLISH=1]"; \
	  exit 2; \
	fi
	@$(HOST_ENV) python3 scripts/host.py add '$(NAME)' \
	  --domain '$(DOMAIN)' --upstream '$(UPSTREAM)' \
	  --protocol '$(or $(PROTOCOL),http)' \
	  --entry-points '$(or $(ENTRY_POINTS),web)' \
	  $(if $(filter 1 true yes,$(TLS)),--tls,) \
	  $(if $(FORWARD_AUTH),--forward-auth '$(FORWARD_AUTH)',) \
	  $(if $(filter 1 true yes,$(NO_PUBLISH)),--no-publish,)

.PHONY: rm-host
rm-host: ## Delete a proxy host by NAME.
	@if [ -z "$(NAME)" ]; then \
	  echo "Usage: make rm-host NAME=<n> [NO_PUBLISH=1]"; \
	  exit 2; \
	fi
	@$(HOST_ENV) python3 scripts/host.py rm '$(NAME)' \
	  $(if $(filter 1 true yes,$(NO_PUBLISH)),--no-publish,)

.PHONY: publish
publish: ## Publish a new revision for the fleet.
	@$(HOST_ENV) python3 scripts/host.py publish

.PHONY: hosts-sync
hosts-sync: ## Write all current proxy-host domains to /etc/hosts (sudo).
	@sudo $(HOST_ENV) python3 scripts/host.py hosts-sync

.PHONY: hosts-clear
hosts-clear: ## Remove the managed block from /etc/hosts (sudo).
	@sudo python3 scripts/host.py hosts-clear

.PHONY: agent-config
agent-config: ## Show routers/services/middlewares an agent receives. AGENT=traefik-1 (default), RAW=1 for raw JSON.
	@$(HOST_ENV) python3 scripts/host.py agent-config '$(or $(AGENT),traefik-1)' \
	  $(if $(filter 1 true yes,$(RAW)),--raw,)

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------

.PHONY: clean
clean: ## Remove built artifacts (binary + web dist + embed dist).
	rm -f $(MANAGER_BIN)
	rm -rf $(WEB_DIR)/dist
	rm -rf $(WEBUI_EMBED_DIR)/assets
	rm -f $(WEBUI_EMBED_DIR)/index.html
	rm -f $(WEB_DIR)/tsconfig.tsbuildinfo
