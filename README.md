# SigNoz Observability Platform - Infrastructure as Code

Infrastructure as Code (IaC) untuk deployment SigNoz observability platform menggunakan Ansible, Docker Compose, dan GitHub Actions CI/CD.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        VPS                                  │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐   │
│  │   Traefik    │  │   SigNoz     │  │  Uptime Kuma     │   │
│  │    :443      │──│   :3301      │  │    :3001         │   │
│  │  (SSL/TLS)   │  │   (UI)       │  │  (Monitoring)    │   │
│  └──────────────┘  └──────────────┘  └──────────────────┘   │
│          │                │                    │            │
│          │          ┌─────┴─────┐              │            │
│          │          │ ClickHouse│              │            │
│          │          │  :9000    │              │            │
│          │          └───────────┘              │            │
│          │                │                    │            │
│          │          ┌─────┴─────┐              │            │
│          │          │ PostgreSQL│              │            │
│          │          │  :5432    │              │            │
│          │          └───────────┘              │            │
│          │                                     │            │
│  ┌───────┴─────────────────────────────────────┴────────┐   │
│  │                   OTLP Endpoints                     │   │
│  │              :4317 (gRPC)  :4318 (HTTP)              │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                          ▲
                          │ OTLP
┌─────────────────────────┴───────────────────────────────────┐
│                   Kubernetes Cluster                        │
│              (OTEL Collector → VPS)                         │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│                   GitHub Actions                            │
│              (CI/CD Pipeline)                               │
│  Push to main → Deploy to VPS                               │
└─────────────────────────────────────────────────────────────┘
```

## Prerequisites

### For Local Deployment
- Ansible installed locally (`brew install ansible` or `pip install ansible`)
- SSH key pair for root access to VPS
- Domain name pointing to VPS IP

### For CI/CD Deployment
- GitHub repository with GitHub Actions enabled
- SSH private key stored in GitHub Secrets
- Domain name pointing to VPS IP

## Quick Start

### Option 1: Local Deployment

#### 1. Setup Local Environment

```bash
# Create .env file from template
make setup-local

# Edit .env with your values
vim .env
```

#### 2. Deploy

```bash
# Full deployment
make deploy-local

# Or deploy individual components
make deploy-common    # User setup, SSH hardening, Docker
make deploy-signoz    # SigNoz stack + Traefik + Uptime Kuma
```

### Option 2: CI/CD Deployment (Recommended)

#### 1. Setup GitHub Secrets

Go to your GitHub repository → Settings → Secrets and variables → Actions → New repository secret

Add the following secrets:

| Secret | Description | Example |
|--------|-------------|---------|
| `SSH_PRIVATE_KEY` | Private key for SSH access | `-----BEGIN OPENSSH PRIVATE KEY-----...` |
| `VPS_HOST` | VPS IP address | `123.45.67.89` |
| `SIGNOZ_DOMAIN` | Domain for SigNoz UI | `signoz.example.com` |
| `UPTIME_KUMA_DOMAIN` | Domain for Uptime Kuma | `uptime.example.com` |
| `OPS_SSH_PUBKEY` | SSH public key for ops user | `ssh-ed25519 AAAA...` |
| `LETSENCRYPT_EMAIL` | Email for Let's Encrypt | `admin@example.com` |
| `POSTGRES_PASSWORD` | PostgreSQL password | `your-secure-password` |

#### 2. Deploy

Push to main branch or trigger manual deployment:

```bash
# Push to main (auto-deploy)
git add .
git commit -m "Initial deployment"
git push origin main

# Or trigger manual deployment from GitHub UI
# Actions → Deploy SigNoz → Run workflow
```

## Deployment Flow

### CI/CD Pipeline

```
Push to main
    ↓
GitHub Actions
    ↓
└── Deploy to VPS
    ├── Setup SSH connection
    ├── Run ansible-playbook
    └── Verify services running
```

### Ansible Deployment

```
ansible-playbook site.yml
    ↓
├── common role
│   ├── Create ops user (superuser)
│   ├── SSH hardening (disable root, password auth)
│   ├── Install Docker
│   └── Configure UFW firewall
    ↓
└── signoz role
    ├── Deploy Docker Compose
    ├── Configure Traefik (auto SSL)
    ├── Configure ClickHouse
    ├── Start SigNoz stack
    └── Start Uptime Kuma
```

## Post-Deployment Setup

### 1. DNS Configuration

Point your domains to VPS IP:
```
signoz.example.com  → YOUR_VPS_IP
uptime.example.com  → YOUR_VPS_IP
```

### 2. SSL Certificate

Traefik will automatically provision SSL certificate via Let's Encrypt once DNS is configured.

### 3. Access SigNoz

Open `https://signoz.yourdomain.com` and create admin account.

### 4. Access Uptime Kuma

Open `https://uptime.yourdomain.com` and setup:
- Create admin account
- Add monitors for:
  - SigNoz UI (`https://signoz.yourdomain.com`)
  - OTLP gRPC endpoint
  - OTLP HTTP endpoint
- Configure notification channels:
  - Telegram (built-in support)
  - Email
  - Other supported channels

## Operations

```bash
# Check status
make status

# View logs
make logs-traefik
make logs-signoz
make logs-clickhouse
make logs-uptime

# Restart services
make restart

# Stop services
make stop

# Remove everything (WARNING: deletes data)
make clean
```

## Retention

Default retention periods:
- **Logs**: 15 days
- **Traces**: 15 days
- **Metrics**: 30 days

To modify, update `ansible/group_vars/all.yml` and re-run:

```bash
cd ansible && ansible-playbook -i inventory/production.yml site.yml --tags signoz
```

## File Structure

```
observability/
├── .github/
│   └── workflows/
│       └── deploy.yml              # CI/CD deployment
├── ansible/
│   ├── inventory/production.yml    # VPS target
│   ├── group_vars/all.yml          # Configuration variables
│   ├── roles/
│   │   ├── common/                 # System setup
│   │   └── signoz/                 # SigNoz + Traefik + Uptime Kuma
│   └── site.yml                    # Master playbook
├── .env.example                    # Environment template
├── .gitignore                      # Git ignore rules
├── Makefile                        # Operational commands
└── README.md
```

## Troubleshooting

### Check container status
```bash
make ssh-ops
cd /opt/signoz && docker compose ps
```

### View logs
```bash
make logs-traefik
make logs-signoz
make logs-clickhouse
make logs-uptime
```

### Restart services
```bash
make restart
```

### Full reset (WARNING: deletes data)
```bash
make clean
make deploy
```

### Check CI/CD status
1. Go to GitHub repository → Actions
2. Click on the workflow run
3. Check logs for errors

## Security Notes

- Root SSH login is disabled
- Password authentication is disabled
- SSH key-only authentication is enforced
- User `ops` has sudo NOPASSWD access
- UFW firewall is configured with minimal open ports
- SSL/TLS is enforced for web traffic (auto-provisioned by Traefik)
- Sensitive data is stored in GitHub Secrets (not in code)
