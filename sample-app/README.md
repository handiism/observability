# Sample App - SigNoz Observability Demo

Sample application yang mendemonstrasikan semua kemampuan SigNoz observability.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Sample App                             │
│  ┌──────────────┐  ┌──────────────┐                     │
│  │ api-gateway  │──│backend-service│                     │
│  │   :8080      │  │   :8081      │                     │
│  └──────────────┘  └──────────────┘                     │
│         │                │                               │
│         └────────┬───────┘                               │
│                  │                                       │
│         ┌────────┴────────┐                             │
│         │   OpenTelemetry │                             │
│         │   (OTLP gRPC)   │                             │
│         └────────┬────────┘                             │
└──────────────────┼───────────────────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────────────────────────┐
│                    SigNoz (VPS)                              │
│  - Distributed Traces                                       │
│  - Metrics                                                  │
│  - Structured Logs                                          │
└─────────────────────────────────────────────────────────────┘
```

## Services

### api-gateway
- HTTP API endpoint
- Receives order requests
- Calls backend-service for processing
- Sends traces and metrics to SigNoz

### backend-service
- Internal processing service
- Stores orders in SQLite
- Records processing duration metrics
- Sends traces to SigNoz

## Quick Start

### 1. Run with Docker Compose

```bash
cd sample-app
docker compose up --build
```

### 2. Test the API

```bash
# Create an order
curl -X POST http://localhost:8080/api/orders

# Check health
curl http://localhost:8080/api/health
```

### 3. View in SigNoz

- Open SigNoz UI: http://localhost:3301
- View traces: Traces → Search
- View metrics: Metrics → Explorer
- View logs: Logs → Search

## OpenTelemetry Instrumentation

### Traces
- Each HTTP request creates a span
- Backend service creates child spans
- Spans include attributes (order.id, duration, status)

### Metrics
- `http.requests.total` - Request counter
- `http.errors.total` - Error counter
- `backend.orders.processed` - Processed orders
- `backend.orders.duration` - Processing duration histogram

### Logs
- Structured JSON logs
- Includes trace_id for correlation
- Log levels: INFO, ERROR

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | 8080 | API Gateway port |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | http://localhost:4317 | SigNoz OTLP endpoint |
| `OTEL_SERVICE_NAME` | api-gateway | Service name in SigNoz |
| `BACKEND_SERVICE_URL` | http://backend-service:8081 | Backend service URL |
