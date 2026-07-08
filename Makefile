.PHONY: help deploy status logs restart stop clean

# ============================================
# Configuration
# ============================================

# Environment variables for local development
# Set these in your shell or .env file for local development
# For CI/CD, these are set by GitHub Actions secrets
VPS_HOST ?= $(shell echo $$VPS_HOST)
SIGNOZ_DOMAIN ?= $(shell echo $$SIGNOZ_DOMAIN)

# ============================================
# Targets
# ============================================

help: ## Show this help
	@echo "Usage: make [target]"
	@echo ""
	@echo "Deployment Options:"
	@echo "  make deploy           Deploy full stack"
	@echo "  make deploy-local     Deploy with local variables"
	@echo "  make deploy-ci        Deploy with CI/CD variables"
	@echo ""
	@echo "Components:"
	@echo "  make deploy-common    Deploy only common role"
	@echo "  make deploy-signoz    Deploy only SigNoz stack"
	@echo ""
	@echo "Operations:"
	@echo "  make status           Check container status"
	@echo "  make logs-traefik     View Traefik logs"
	@echo "  make logs-signoz      View SigNoz logs"
	@echo "  make logs-clickhouse  View ClickHouse logs"
	@echo "  make logs-uptime      View Uptime Kuma logs"
	@echo "  make restart          Restart all services"
	@echo "  make stop             Stop all services"
	@echo "  make clean            Remove everything (WARNING: deletes data)"
	@echo ""
	@echo "SSH:"
	@echo "  make ssh-root         SSH to VPS as root"
	@echo "  make ssh-ops          SSH to VPS as ops user"
	@echo ""
	@echo "Development:"
	@echo "  make ansible-lint     Lint Ansible playbooks"
	@echo "  make syntax-check     Check Ansible syntax"

deploy: ## Deploy full stack (common + signoz + traefik + uptime-kuma)
	cd ansible && ansible-playbook -i inventory/production.yml site.yml

deploy-local: ## Deploy with local variables (requires .env or shell exports)
	@if [ -z "$(VPS_HOST)" ]; then echo "Error: VPS_HOST not set"; exit 1; fi
	@if [ -z "$(SIGNOZ_DOMAIN)" ]; then echo "Error: SIGNOZ_DOMAIN not set"; exit 1; fi
	cd ansible && VPS_HOST=$(VPS_HOST) SIGNOZ_DOMAIN=$(SIGNOZ_DOMAIN) ansible-playbook -i inventory/production.yml site.yml

deploy-ci: ## Deploy with CI/CD variables (GitHub Actions)
	cd ansible && ansible-playbook -i inventory/production.yml site.yml -v

deploy-common: ## Deploy only common role (user, SSH, Docker)
	cd ansible && ansible-playbook -i inventory/production.yml site.yml --tags common

deploy-signoz: ## Deploy only SigNoz stack (includes Traefik, Uptime Kuma)
	cd ansible && ansible-playbook -i inventory/production.yml site.yml --tags signoz

status: ## Check status on remote VPS
	@if [ -n "$(VPS_HOST)" ]; then \
		ssh root@$(VPS_HOST) "docker ps --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'"; \
	else \
		echo "Error: VPS_HOST not set"; exit 1; \
	fi

logs-traefik: ## View Traefik logs on remote VPS
	@if [ -n "$(VPS_HOST)" ]; then \
		ssh ops@$(VPS_HOST) "cd /opt/signoz && docker compose logs -f traefik"; \
	else \
		echo "Error: VPS_HOST not set"; exit 1; \
	fi

logs-signoz: ## View SigNoz logs on remote VPS
	@if [ -n "$(VPS_HOST)" ]; then \
		ssh ops@$(VPS_HOST) "cd /opt/signoz && docker compose logs -f signoz"; \
	else \
		echo "Error: VPS_HOST not set"; exit 1; \
	fi

logs-clickhouse: ## View ClickHouse logs on remote VPS
	@if [ -n "$(VPS_HOST)" ]; then \
		ssh ops@$(VPS_HOST) "cd /opt/signoz && docker compose logs -f clickhouse"; \
	else \
		echo "Error: VPS_HOST not set"; exit 1; \
	fi

logs-uptime: ## View Uptime Kuma logs on remote VPS
	@if [ -n "$(VPS_HOST)" ]; then \
		ssh ops@$(VPS_HOST) "cd /opt/signoz && docker compose logs -f uptime-kuma"; \
	else \
		echo "Error: VPS_HOST not set"; exit 1; \
	fi

restart: ## Restart all services on remote VPS
	@if [ -n "$(VPS_HOST)" ]; then \
		ssh ops@$(VPS_HOST) "cd /opt/signoz && docker compose restart"; \
	else \
		echo "Error: VPS_HOST not set"; exit 1; \
	fi

stop: ## Stop all services on remote VPS
	@if [ -n "$(VPS_HOST)" ]; then \
		ssh ops@$(VPS_HOST) "cd /opt/signoz && docker compose stop"; \
	else \
		echo "Error: VPS_HOST not set"; exit 1; \
	fi

clean: ## Remove all containers and volumes on remote VPS
	@echo "WARNING: This will delete all data!"
	@read -p "Are you sure? (y/N): " confirm && [ "$$confirm" = "y" ] || exit 1
	@if [ -n "$(VPS_HOST)" ]; then \
		ssh ops@$(VPS_HOST) "cd /opt/signoz && docker compose down -v"; \
	else \
		echo "Error: VPS_HOST not set"; exit 1; \
	fi

ssh-root: ## SSH to VPS as root
	@if [ -n "$(VPS_HOST)" ]; then \
		ssh root@$(VPS_HOST); \
	else \
		echo "Error: VPS_HOST not set"; exit 1; \
	fi

ssh-ops: ## SSH to VPS as ops user
	@if [ -n "$(VPS_HOST)" ]; then \
		ssh ops@$(VPS_HOST); \
	else \
		echo "Error: VPS_HOST not set"; exit 1; \
	fi

ansible-lint: ## Lint Ansible playbooks
	ansible-lint ansible/

syntax-check: ## Check Ansible syntax
	cd ansible && ansible-playbook -i inventory/production.yml site.yml --syntax-check

# ============================================
# Local Development Setup
# ============================================

env-example: ## Create .env.example file
	@echo "# VPS Configuration" > .env.example
	@echo "VPS_HOST=your-vps-ip" >> .env.example
	@echo "SIGNOZ_DOMAIN=signoz.yourdomain.com" >> .env.example
	@echo "" >> .env.example
	@echo "# SSH Configuration" >> .env.example
	@echo "OPS_SSH_PUBKEY=ssh-ed25519 AAAA..." >> .env.example
	@echo "" >> .env.example
	@echo "# SSL Configuration" >> .env.example
	@echo "LETSENCRYPT_EMAIL=admin@yourdomain.com" >> .env.example
	@echo "Created .env.example"

setup-local: ## Setup local development environment
	@if [ ! -f .env ]; then \
		echo "Creating .env from .env.example..."; \
		cp .env.example .env; \
		echo "Please edit .env with your values"; \
	else \
		echo ".env already exists"; \
	fi
