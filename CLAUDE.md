# CLAUDE.md

This file provides guidance to Claude Code when working with the HudSci Pipelines project.

## Project Overview

**tv-pipelines-hudsci** is a Go-based pipeline service for pharmaceutical supply chain tracking via EPCIS data. It replaces the Mage AI pipelines from `tv-mage/hudsci` with a production-ready, containerized Go service.

### Pipelines

1. **Inbound Pipeline** (`pipelines/inbound/pipeline.go`)
   - Poll XML files from Directus
   - Convert XML to JSON via EPCIS Converter service
   - Extract shipping data (events, products, containers)
   - Insert to `epcis_inbox` collection
   - Update watermark for incremental processing

2. **Outbound Pipeline** (`pipelines/outbound/pipeline.go`)
   - Query approved shipments from Directus
   - Fetch related events from TiDB (hierarchical CTE query)
   - Build EPCIS 2.0 JSON-LD documents
   - Add SBDH headers, DSCSA statements, VocabularyList
   - Dispatch via TrustMed Partner API (mTLS)
   - Poll delivery confirmation
   - Handle retries and error notifications

## Architecture Principles

### Keep It Simple

**CRITICAL RULES:**
- **Minimal code** that solves the problem
- **Wrap errors**: `fmt.Errorf("action: %w", err)`
- **Log start and completion** of each task with duration
- **Test every file** you create

**DON'T:**
- Add features "for the future"
- Create abstractions without immediate use
- Skip tests

### Use Shared Modules

Always use these instead of reinventing:
- `github.com/trackvision/tv-shared-go/logger` - Logging (GCP-optimized JSON)
- `github.com/trackvision/tv-shared-go/env` - Secret loading (Cloud Run mounts)

**CRITICAL: Use `tv-shared-go/logger`, NOT `zap.L()`**

```go
import (
    "github.com/trackvision/tv-shared-go/logger"
    "go.uber.org/zap"
)

// Correct - uses tv-shared-go logger
logger.Info("step started", zap.String("pipeline", name), zap.String("step", stepName))

// WRONG - zap.L() is a no-op by default
zap.L().Info("this won't appear in logs")
```

### Config & Secrets (Cloud Run)

Use `env.GetSecret()` for sensitive values - tries mounted file first, then env var:

```go
import "github.com/trackvision/tv-shared-go/env"

func Load() (*Config, error) {
    // Secrets (tries /SECRET_NAME/value first, falls back to env var)
    apiKey, err := env.GetSecret("DIRECTUS_CMS_API_KEY")
    if err != nil {
        return nil, fmt.Errorf("DIRECTUS_CMS_API_KEY: %w", err)
    }

    // Regular env vars
    cfg := &Config{
        Port:    getEnv("PORT", "8080"),
        BaseURL: os.Getenv("CMS_BASE_URL"),
        APIKey:  apiKey,
    }
    return cfg, nil
}
```

## Project Structure

```
tv-pipelines-hudsci/
├── main.go                    # HTTP server, pipeline registry
├── pipelines/
│   ├── flow.go                # Task orchestration with logging
│   ├── inbound/pipeline.go    # Inbound shipments pipeline
│   └── outbound/pipeline.go   # Outbound shipments pipeline
├── tasks/
│   ├── directus_client.go     # Directus REST API client (CRUD, files)
│   ├── directus_files.go      # File polling, upload operations
│   ├── directus_collections.go # Collection operations, watermark
│   ├── epcis_converter.go     # XML ↔ JSON conversion service
│   ├── epcis_extractor.go     # Extract shipping data from XML
│   ├── epcis_builder.go       # Build EPCIS 2.0 JSON-LD documents
│   ├── epcis_enhancer.go      # Add SBDH, DSCSA, VocabularyList
│   ├── trustmed_client.go     # TrustMed Partner API (mTLS)
│   ├── trustmed_dashboard.go  # TrustMed Dashboard API
│   ├── tidb_queries.go        # TiDB CTE queries for events
│   └── *_test.go              # Unit tests for each task
├── types/
│   └── types.go               # Shared type definitions
├── configs/
│   └── env.go                 # Configuration loading
├── certs/trustmed/            # TrustMed mTLS certificates (gitignored)
├── tests/
│   ├── e2e_inbound_test.go    # E2E test for inbound pipeline
│   ├── e2e_outbound_test.go   # E2E test for outbound pipeline
│   └── fixtures/              # Test data files
│       └── DSCSAExample.xml
├── scripts/
│   ├── reset_inbound.go       # Reset inbound pipeline state
│   ├── upload_test_file.go    # Upload test XML to Directus
│   └── verify_inbound.go      # Verify inbound pipeline results
├── .env.example               # Environment template
├── Dockerfile                 # Multi-stage build with health check
├── Makefile                   # Build & test commands
└── vendor/                    # Vendored dependencies
```

## Pipeline Signature

All pipelines must implement this signature:

```go
func Run(ctx context.Context, db *sqlx.DB, cms *tasks.DirectusClient, cfg *configs.Config, id string) error
```

## Flow API with Logging

```go
func Run(ctx context.Context, db *sqlx.DB, cms *tasks.DirectusClient, cfg *configs.Config, id string) error {
    var xmlFiles []XMLFile
    var convertedFiles []ConvertedFile

    flow := pipelines.NewFlow("inbound-shipments")

    // Task 1: Fetch data
    flow.AddTask("poll_xml_files", func() error {
        var err error
        xmlFiles, err = tasks.PollXMLFiles(ctx, cms, cfg)
        return err
    })

    // Task 2: Depends on Task 1
    flow.AddTask("convert_xml_to_json", func() error {
        var err error
        convertedFiles, err = tasks.ConvertXMLToJSON(ctx, xmlFiles, cfg)
        return err
    }, "poll_xml_files")

    // Task 3: Parallel with Task 2
    flow.AddTask("upload_json_files", func() error {
        return tasks.UploadJSONFiles(ctx, cms, convertedFiles, cfg)
    }, "convert_xml_to_json")

    return flow.Run(ctx)
}
```

**Sharing State:** Use closures to share variables between tasks (see above).

## Testing Strategy

### Unit Tests

Every task file has a corresponding `_test.go` file:

```go
// tasks/directus_client_test.go
func TestPostItem(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Mock Directus API response
        json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "123"}})
    }))
    defer server.Close()

    client := NewDirectusClient(server.URL, "test-key")
    result, err := client.PostItem(context.Background(), "test", map[string]string{"name": "test"})
    require.NoError(t, err)
}
```

### E2E Tests

**Prerequisites:**
1. Start dev environment: use `/dev-environment` skill or `cd ~/github/dev-env && make app`
2. Load environment variables from `.env`

**Run E2E tests:**
```bash
# Load .env and run specific test
set -a; source .env; set +a; go test -mod=vendor -v -tags=integration ./tests/... -run TestOutboundPipelineE2E

# Or run all E2E tests
set -a; source .env; set +a; go test -mod=vendor -v -tags=integration ./tests/...
```

**Example test structure** (requires services running):

```go
// tests/e2e_inbound_test.go
// +build integration

func TestInboundPipelineE2E(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping E2E test in short mode")
    }

    ctx := context.Background()
    cfg := loadTestConfig(t)

    // 1. Check services running
    checkDirectusService(t, cfg)
    checkEPCISConverterService(t, cfg)

    // 2. Clean folders and watermark
    cleanDirectusFolder(t, cms, cfg.FolderInputXML)
    deleteWatermark(t, cms)

    // 3. Upload test file
    uploadTestXML(t, cms, cfg)

    // 4. Run pipeline
    err := inbound.Run(ctx, db, cms, cfg, "e2e-test")
    require.NoError(t, err)

    // 5. Verify results
    verifyJSONFilesCreated(t, cms, cfg)
    verifyWatermarkUpdated(t, cms)
}
```

## Common Commands

```bash
# Development
make build          # Build binary
make run            # Start HTTP server
make test-unit      # Run unit tests (fast)
make check          # Vet + lint + test

# E2E Tests (requires dev environment running - use /dev-environment skill)
# Must load .env for credentials:
set -a; source .env; set +a; go test -mod=vendor -v -tags=integration ./tests/... -run TestInboundPipelineE2E
set -a; source .env; set +a; go test -mod=vendor -v -tags=integration ./tests/... -run TestOutboundPipelineE2E

# Testing Pipelines (legacy make targets)
make test-inbound   # Reset → upload → run → verify
make test-outbound  # Reset → run → verify
make reset-inbound  # Clean state for fresh test

# Docker
make docker-build   # Build Docker image
make docker-run     # Run in container

# Quality
make fmt            # Format code
make vet            # Static analysis
make lint           # Run golangci-lint
```

## HTTP API

```bash
# Health check
curl http://localhost:8080/health

# Run inbound pipeline
curl -X POST http://localhost:8080/run/inbound \
  -H "Content-Type: application/json" \
  -d '{"id": "test-run"}'

# Run outbound pipeline
curl -X POST http://localhost:8080/run/outbound \
  -H "Content-Type: application/json" \
  -d '{"id": "test-run"}'
```

## TrustMed mTLS Certificate Handling

### Certificate Locations

| Environment | Location | Notes |
|-------------|----------|-------|
| Local Dev (Demo) | `certs/trustmed/client-cert.crt` | Gitignored |
| Local Dev (Prod) | `certs/trustmed/client-cert-prod.crt` | Gitignored |
| Cloud Run | `/etc/creds/trustmed/` | Secret Manager mounts |

### Environment Variables

```bash
# Demo (default)
TRUSTMED_ENDPOINT=https://demo.partner.trust.med/v1/client/storage
TRUSTMED_CERTFILE=certs/trustmed/client-cert.crt
TRUSTMED_KEYFILE=certs/trustmed/client-key.key
TRUSTMED_CAFILE=certs/trustmed/trustmed-ca.crt

# Production (override with USE_PROD_CERTS=true)
TRUSTMED_ENDPOINT_PROD=https://partner.trust.med/v1/client/storage
TRUSTMED_CERTFILE_PROD=/etc/creds/trustmed/client-cert.crt
TRUSTMED_KEYFILE_PROD=/etc/creds/trustmed/client-key.key
TRUSTMED_CAFILE_PROD=/etc/creds/trustmed/trustmed-ca.crt
```

### Go mTLS Client

```go
// tasks/trustmed_client.go
func NewTrustMedClient(cfg TrustMedConfig) (*TrustMedClient, error) {
    // Load client certificate
    cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
    if err != nil {
        return nil, fmt.Errorf("load client cert: %w", err)
    }

    // Load CA certificate
    caCert, err := os.ReadFile(cfg.CAFile)
    if err != nil {
        return nil, fmt.Errorf("load CA cert: %w", err)
    }
    caCertPool := x509.NewCertPool()
    caCertPool.AppendCertsFromPEM(caCert)

    // Create mTLS transport
    tlsConfig := &tls.Config{
        Certificates: []tls.Certificate{cert},
        RootCAs:      caCertPool,
    }

    return &TrustMedClient{
        endpoint: cfg.Endpoint,
        httpClient: &http.Client{
            Transport: &http.Transport{TLSClientConfig: tlsConfig},
            Timeout:   30 * time.Second,
        },
    }, nil
}
```

## Deployment

### Docker Build

```bash
# Build with private repo access
docker build --build-arg GH_PAT=${GH_PAT} -t tv-pipelines-hudsci:latest .

# Run with certificates mounted
docker run -p 8080:8080 \
  --env-file .env \
  -v $(pwd)/certs:/root/certs \
  tv-pipelines-hudsci:latest
```

### Cloud Run

Secrets are mounted as files in `/etc/creds/`:

```bash
# Set production mode
USE_PROD_CERTS=true

# Certificate paths (mounted by Cloud Run)
TRUSTMED_CERTFILE_PROD=/etc/creds/trustmed/client-cert.crt
TRUSTMED_KEYFILE_PROD=/etc/creds/trustmed/client-key.key
TRUSTMED_CAFILE_PROD=/etc/creds/trustmed/trustmed-ca.crt
```

## Critical Implementation Details

### Watermark Tracking

Store in Directus `global_config` collection:

```go
type Watermark struct {
    LastCheckTimestamp time.Time `json:"last_check_timestamp"`
    TotalProcessed     int       `json:"total_processed"`
}

func GetWatermark(ctx context.Context, cms *DirectusClient, key string) (*Watermark, error)
func UpdateWatermark(ctx context.Context, cms *DirectusClient, key string, w *Watermark) error
```

### Hierarchical Event Query (TiDB CTE)

```sql
WITH
top_level_events AS (
    SELECT event_id FROM epcis_events_raw WHERE capture_id = ?
),
top_level_keys AS (
    SELECT epc_join_key FROM epc_events
    WHERE event_id IN (SELECT event_id FROM top_level_events)
),
aggregation_events AS (
    SELECT parent_epc_join_key, child_epc_join_key
    FROM view_aggregation_children
    WHERE parent_epc_join_key IN (SELECT epc_join_key FROM top_level_keys)
)
SELECT DISTINCT e.event_id, e.event_body
FROM epcis_events_raw e
WHERE e.event_id IN (...)
ORDER BY e.date_created ASC
```

### Retry Logic

Built into Flow API:
- 2 retries (3 total attempts)
- 5 second delay between retries
- Context cancellation support

For dispatch-specific retries:
- Track `dispatch_attempt_count` in EPCIS_outbound
- Max 3 attempts
- Status: pending → Processing → Acknowledged/Retrying/Failed

### Failure Thresholds

Fail pipeline if >50% of batch items fail:

```go
func checkFailureThreshold(total, failed int) error {
    if total > 0 && float64(failed)/float64(total) > 0.5 {
        return fmt.Errorf("failure rate %.0f%% exceeds 50%% threshold",
            float64(failed)/float64(total)*100)
    }
    return nil
}
```

## Migration from Mage

This project replaces `tv-mage/hudsci`:

| Mage Pipeline | Go Pipeline |
|---------------|-------------|
| `inbound_shipment_received` | `pipelines/inbound` |
| `outbound_shipment_dispatch` | `pipelines/outbound` |

See `MIGRATION_PLAN.md` for detailed block-by-block mapping.

## Before Making Changes

**Checklist:**
- [ ] Read the task you're modifying/creating
- [ ] Understand the Flow API pattern (closures for state sharing)
- [ ] Use `tv-shared-go/logger` for all logging
- [ ] Use `env.GetSecret()` for secrets
- [ ] Wrap errors with `fmt.Errorf("action: %w", err)`
- [ ] Create `_test.go` file for new tasks
- [ ] Run `make check` before committing
- [ ] Update this file if adding new patterns

## Vendoring Dependencies

**Always vendor dependencies** for reproducible builds:

```bash
go mod tidy
go mod vendor
git add vendor/
```

## Success Criteria

Before finishing any work:
- [ ] `main.go` at project root
- [ ] Every `.go` file has a `_test.go`
- [ ] Uses `tv-shared-go/logger` (NOT `zap.L()`)
- [ ] Uses `env.GetSecret()` for secrets
- [ ] Errors wrapped with `fmt.Errorf`
- [ ] Steps log start/completion with duration
- [ ] `go test -mod=vendor ./...` passes
- [ ] `make check` passes
- [ ] Dockerfile builds successfully
- [ ] E2E tests documented
