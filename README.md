# HudSci Pipelines

Go-based pipeline service for HudSci EPCIS data processing. This project implements two core pipelines for pharmaceutical supply chain tracking:

1. **Inbound Pipeline** - Poll XML files from Directus, convert to JSON, extract shipping data, insert to database
2. **Outbound Pipeline** - Query approved shipments, build EPCIS documents, dispatch via TrustMed mTLS

## Architecture

Built on the `tv-pipelines-template` pattern with:
- Flow-based task orchestration
- Comprehensive logging with structured JSON (GCP-optimized)
- Secret management via Cloud Run mounted files
- mTLS certificate handling for TrustMed integration
- MySQL/TiDB database support

## Project Structure

```
tv-pipelines-hudsci/
├── main.go                          # HTTP server + pipeline registry
├── pipelines/
│   ├── flow.go                      # Task orchestration with logging
│   ├── inbound/pipeline.go          # Inbound shipments pipeline
│   └── outbound/pipeline.go         # Outbound shipments pipeline
├── tasks/
│   ├── directus_client.go           # Directus REST API client (base)
│   ├── directus_files.go            # File operations (poll, upload, download)
│   ├── directus_collections.go      # Collection operations (watermark, inbox)
│   ├── epcis_converter.go           # XML ↔ JSON conversion service client
│   ├── epcis_extractor.go           # Extract shipping data from XML
│   ├── epcis_builder.go             # Build EPCIS 2.0 JSON-LD documents
│   ├── epcis_enhancer.go            # Add SBDH, DSCSA, VocabularyList
│   ├── trustmed_client.go           # TrustMed Partner API (mTLS dispatch)
│   ├── trustmed_dashboard.go        # TrustMed Dashboard API (confirmation)
│   ├── tidb_queries.go              # TiDB CTE queries for events
│   ├── outbound_shipments.go        # Outbound pipeline helpers
│   └── *_test.go                    # Unit tests for each task
├── types/
│   └── types.go                     # Shared type definitions
├── configs/
│   ├── env.go                       # Configuration management
│   └── env_test.go                  # Configuration tests
├── certs/trustmed/                  # TrustMed mTLS certificates (gitignored)
├── tests/
│   ├── e2e_inbound_test.go          # E2E test for inbound pipeline
│   ├── e2e_outbound_test.go         # E2E test for outbound pipeline
│   └── fixtures/
│       └── DSCSAExample.xml         # Test EPCIS XML file
├── scripts/
│   ├── reset_inbound.go             # Reset inbound pipeline state
│   ├── upload_test_file.go          # Upload test XML to Directus
│   └── verify_inbound.go            # Verify inbound pipeline results
├── vendor/                          # Vendored dependencies
├── .env.example                     # Environment template
├── Dockerfile                       # Multi-stage build with health check
├── Makefile                         # Build & test commands
├── CLAUDE.md                        # Claude Code guidance
└── MIGRATION_PLAN.md                # Migration from Mage documentation
```

## Getting Started

### Prerequisites

- Go 1.24+
- Docker (for containerized deployment)
- Access to Directus CMS
- TiDB/MySQL database
- TrustMed mTLS certificates

### Installation

```bash
# Clone repository
git clone https://github.com/trackvision/tv-pipelines-hudsci.git
cd tv-pipelines-hudsci

# Copy environment template
cp .env.example .env

# Edit .env with your configuration
vim .env

# Install dependencies
go mod download

# Build
make build
```

### Configuration

See `.env.example` for all available configuration options. Key variables:

```bash
# Directus CMS
CMS_BASE_URL=http://localhost:8055
DIRECTUS_CMS_API_KEY=your-api-key

# TiDB Database
DB_HOST=127.0.0.1
DB_PORT=4000
DB_NAME=huds_local

# TrustMed mTLS (Demo)
TRUSTMED_ENDPOINT=https://demo.partner.trust.med/v1/client/storage
TRUSTMED_CERTFILE=certs/trustmed/client-cert.crt
TRUSTMED_KEYFILE=certs/trustmed/client-key.key
TRUSTMED_CAFILE=certs/trustmed/trustmed-ca.crt
```

For production deployments, use `USE_PROD_CERTS=true` and set the `*_PROD` variants.

### TrustMed Certificate Setup

```bash
# Create certificate directory
mkdir -p certs/trustmed

# Copy your certificates
cp /path/to/client-cert.crt certs/trustmed/
cp /path/to/client-key.key certs/trustmed/
cp /path/to/trustmed-ca.crt certs/trustmed/

# For production certificates
cp /path/to/client-cert-prod.crt certs/trustmed/
cp /path/to/client-key-prod.key certs/trustmed/
cp /path/to/trustmed-ca-prod.crt certs/trustmed/
```

**CRITICAL:** Never commit certificates to version control. The `certs/` directory is gitignored.

### Running Locally

```bash
# Start the HTTP server
make run

# Or directly
go run .
```

The service will start on port 8080 (or `PORT` env var).

### HTTP API

**Local Development:**
```bash
# Health Check
curl http://localhost:8080/health

# Run Inbound Pipeline
curl -X POST http://localhost:8080/run/inbound \
  -H "Content-Type: application/json" \
  -d '{"id": "test-run"}'

# Run Outbound Pipeline
curl -X POST http://localhost:8080/run/outbound \
  -H "Content-Type: application/json" \
  -d '{"id": "test-run"}'
```

**Cloud Run (hudscidev):**

Set your Directus API key:
```bash
export DIRECTUS_KEY="your-directus-api-key"
```

```bash
# Health Check
curl https://pipelines-hudsci-c6i72vxwbq-uk.a.run.app/health \
  -H "Authorization: Bearer $DIRECTUS_KEY"

# Run Inbound Pipeline
curl -X POST https://pipelines-hudsci-c6i72vxwbq-uk.a.run.app/run/inbound \
  -H "Authorization: Bearer $DIRECTUS_KEY" \
  -H "Content-Type: application/json" \
  -d '{"id": "test-inbound-'$(date +%s)'"}'

# Run Outbound Pipeline
curl -X POST https://pipelines-hudsci-c6i72vxwbq-uk.a.run.app/run/outbound \
  -H "Authorization: Bearer $DIRECTUS_KEY" \
  -H "Content-Type: application/json" \
  -d '{"id": "test-outbound-'$(date +%s)'"}'

# Run Outbound WITHOUT TrustMed dispatch (dry run)
curl -X POST https://pipelines-hudsci-c6i72vxwbq-uk.a.run.app/run/outbound \
  -H "Authorization: Bearer $DIRECTUS_KEY" \
  -H "Content-Type: application/json" \
  -d '{"id": "test-dry-'$(date +%s)'", "dry_run": true}'
```

## Development

### Running Tests

```bash
# All tests (unit + E2E)
make test

# Unit tests only (fast, no external dependencies)
make test-unit

# E2E tests (requires services running)
make test-e2e

# Inbound pipeline test (reset → upload → verify)
make test-inbound

# Outbound pipeline test
make test-outbound

# Code quality checks (vet + lint + unit tests)
make check
```

### Testing Strategy

**Unit Tests:**
- Every `tasks/*.go` file has a corresponding `*_test.go`
- Use `httptest.Server` for mocking HTTP APIs
- Use `go-sqlmock` for mocking database queries
- Run with: `make test-unit`

**E2E Tests:**
- Located in `tests/e2e_*_test.go`
- Require actual services (Directus, EPCIS Converter, TiDB, TrustMed)
- Tagged with `// +build integration`
- Run with: `make test-e2e`

**Manual Testing:**
```bash
# 1. Reset inbound pipeline state
make reset-inbound

# 2. Upload test file
go run scripts/upload_test_file.go

# 3. Start server
make run

# 4. Trigger pipeline (in another terminal)
curl -X POST http://localhost:8080/run/inbound \
  -H "Content-Type: application/json" \
  -d '{"id": "manual-test"}'

# 5. Verify results
go run scripts/verify_inbound.go
```

### Helper Scripts

```bash
# Reset inbound pipeline (clean folders, delete watermark)
go run scripts/reset_inbound.go

# Upload test XML file to Directus
go run scripts/upload_test_file.go

# Verify inbound pipeline results
go run scripts/verify_inbound.go
```

### Code Quality

Before committing, always run:

```bash
make check   # Runs go vet, golangci-lint, and unit tests
```

## Pipelines

### Inbound Pipeline

Processes incoming EPCIS XML files:

1. **poll_xml_files** - Query Directus for new XML files (watermark-based)
2. **convert_xml_to_json** - Convert XML to JSON via EPCIS Converter service
3. **upload_json_files** - Upload JSON to Directus
4. **extract_shipment_data** - Extract shipping events, products, containers
5. **insert_epcis_inbox** - Insert to `epcis_inbox` collection
6. **update_watermark** - Update processing watermark

### Outbound Pipeline

Dispatches approved shipments to TrustMed:

1. **poll_approved_shipments** - Query shipments with status=approved
2. **query_shipment_events** - Fetch related events via TiDB CTE query
3. **build_epcis_documents** - Build EPCIS 2.0 JSON-LD documents
4. **add_xml_headers** - Add SBDH, DSCSA, VocabularyList
5. **manage_dispatch_records** - Create/update dispatch records in Directus
6. **dispatch_via_trustmed** - Send via TrustMed Partner API (mTLS)
7. **poll_dispatch_confirmation** - Check delivery status
8. **notify_on_errors** - Log permanent failures

## Deployment

### Docker

```bash
# Build image
docker build -t tv-pipelines-hudsci:latest .

# Run container
docker run -p 8080:8080 \
  --env-file .env \
  -v $(pwd)/certs:/root/certs \
  tv-pipelines-hudsci:latest
```

### Cloud Run

The project includes GitHub Actions CI/CD that builds and pushes Docker images on push to main.

For secret management in Cloud Run:
- Secrets are mounted as files in `/etc/creds/`
- Use `USE_PROD_CERTS=true` to enable production TrustMed certificates
- Set `TRUSTMED_CERTFILE_PROD`, `TRUSTMED_KEYFILE_PROD`, `TRUSTMED_CAFILE_PROD` to `/etc/creds/trustmed/*`

## Migration from Mage

This project replaces the Mage AI pipelines in `tv-mage/hudsci`:

- `inbound_shipment_received` → `pipelines/inbound`
- `outbound_shipment_dispatch` → `pipelines/outbound`

See `MIGRATION_PLAN.md` for detailed mapping.

## Contributing

1. Create feature branch
2. Make changes
3. Run `make check` to verify
4. Submit PR

## License

Proprietary - TrackVision
