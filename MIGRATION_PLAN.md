# Migration Plan: HudSci Mage Pipelines to Go

## Overview

Migrate two Mage AI pipelines from `tv-mage/hudsci` to the new Go-based `tv-pipelines-hudsci` repository using the `tv-pipelines-template` architecture.

| Pipeline | Source | Description |
|----------|--------|-------------|
| `inbound_shipments` | `inbound_shipment_received` | Poll XML from Directus → Convert to JSON → Extract shipping data → Insert to DB |
| `outbound_shipments_received` | `outbound_shipment_dispatch` | Query approved shipments → Build EPCIS documents → Dispatch via TrustMed mTLS |

---

## Architecture Mapping

### Mage → Go Equivalents

| Mage Concept | Go Equivalent |
|--------------|---------------|
| Pipeline (YAML + blocks) | `pipelines/<name>/pipeline.go` with Flow API |
| Data Loader | Task function that fetches data |
| Transformer | Task function that processes data (pure) |
| Data Exporter | Task function with side effects |
| `io_config.yaml` | Environment variables + `configs/env.go` |
| `kwargs` config access | `os.Getenv()` |
| DataFrame passing | Go structs/slices via closures |
| `.env` secrets | `.env` file + Cloud Run Secret Manager |

### Directory Structure

```
tv-pipelines-hudsci/
├── main.go                           # HTTP API server
├── pipelines/
│   ├── flow.go                       # DAG orchestration (from template)
│   ├── inbound/pipeline.go           # Inbound shipments pipeline
│   └── outbound/pipeline.go          # Outbound shipments pipeline
├── tasks/
│   ├── directus_client.go            # Directus REST API client
│   ├── directus_files.go             # File operations (poll, upload, download)
│   ├── directus_collections.go       # Collection operations (CRUD, watermark)
│   ├── epcis_converter.go            # XML ↔ JSON conversion service client
│   ├── epcis_extractor.go            # Extract shipping data from EPCIS XML
│   ├── epcis_builder.go              # Build EPCIS 2.0 JSON-LD documents
│   ├── epcis_enhancer.go             # Add SBDH headers, DSCSA, VocabularyList
│   ├── trustmed_client.go            # TrustMed Partner API (mTLS dispatch)
│   ├── trustmed_dashboard.go         # TrustMed Dashboard API (confirmation polling)
│   ├── tidb_queries.go               # TiDB event queries (CTE for hierarchical)
│   └── *_test.go                     # Unit tests for each task
├── types/
│   ├── epcis.go                      # EPCIS event types
│   ├── shipment.go                   # Shipment, dispatch record types
│   └── directus.go                   # Directus response types
├── configs/
│   └── env.go                        # Configuration management
├── certs/                            # TrustMed mTLS certificates (gitignored)
│   └── trustmed/
│       ├── client-cert.crt           # Demo client cert
│       ├── client-key.key            # Demo private key
│       ├── trustmed-ca.crt           # Demo CA cert
│       ├── client-cert-prod.crt      # Production client cert
│       ├── client-key-prod.key       # Production private key
│       └── trustmed-ca-prod.crt      # Production CA cert
├── tests/
│   ├── e2e_inbound_test.go           # E2E test for inbound pipeline
│   ├── e2e_outbound_test.go          # E2E test for outbound pipeline
│   └── fixtures/                     # Test XML/JSON files
│       └── DSCSAExample.xml
├── scripts/
│   ├── reset_inbound.go              # Reset inbound pipeline state
│   └── reset_outbound.go             # Reset outbound pipeline state
├── .env.example                      # Environment template
├── .gitignore                        # Include certs/ directory
├── Makefile
├── Dockerfile
├── go.mod
└── README.md
```

---

## Pipeline 1: Inbound Shipments

### Current Mage Flow (5 blocks)

```
directus_poll_xml_files (Data Loader)
         ↓
xml_to_json_converter (Transformer)
         ├→ directus_upload_json (Data Exporter)
         └→ extract_epcis_inbox_data (Transformer)
              ↓
         insert_epcis_inbox (Data Exporter)
```

### Go Implementation

```go
// pipelines/inbound/pipeline.go
func Run(ctx context.Context, db *sqlx.DB, cms *tasks.DirectusClient, id string) error {
    var xmlFiles []tasks.XMLFile
    var convertedFiles []tasks.ConvertedFile
    var extractedShipments []types.InboundShipment

    flow := pipelines.NewFlow("inbound-shipments")

    // Task 1: Poll XML files from Directus (with watermark)
    flow.AddTask("poll_xml_files", func() error {
        var err error
        xmlFiles, err = tasks.PollXMLFiles(ctx, cms)
        return err
    })

    // Task 2: Convert XML to JSON via EPCIS Converter service
    flow.AddTask("convert_xml_to_json", func() error {
        var err error
        convertedFiles, err = tasks.ConvertXMLToJSON(ctx, xmlFiles)
        return err
    }, "poll_xml_files")

    // Task 3: Upload JSON files to Directus
    flow.AddTask("upload_json_files", func() error {
        return tasks.UploadJSONFiles(ctx, cms, convertedFiles)
    }, "convert_xml_to_json")

    // Task 4: Extract shipping data from XML
    flow.AddTask("extract_shipment_data", func() error {
        var err error
        extractedShipments, err = tasks.ExtractEPCISInboxData(ctx, cms, xmlFiles)
        return err
    }, "convert_xml_to_json")

    // Task 5: Insert to epcis_inbox collection
    flow.AddTask("insert_epcis_inbox", func() error {
        return tasks.InsertEPCISInbox(ctx, cms, extractedShipments)
    }, "extract_shipment_data")

    // Task 6: Update watermark (after all processing)
    flow.AddTask("update_watermark", func() error {
        return tasks.UpdateInboundWatermark(ctx, cms, len(xmlFiles))
    }, "upload_json_files", "insert_epcis_inbox")

    return flow.Run(ctx)
}
```

### Task Implementations Required

| Task | Source File | Description |
|------|-------------|-------------|
| `PollXMLFiles` | `utils/watermark_tracker.py` + `data_loaders/directus_poll_xml_files.py` | Get watermark, query Directus files, fetch XML content |
| `ConvertXMLToJSON` | `modules/epcis_converter_client.py` + `transformers/xml_to_json_converter.py` | Call EPCIS converter service |
| `UploadJSONFiles` | `data_exporters/directus_upload_json.py` | Upload to Directus JSON folder |
| `ExtractEPCISInboxData` | `transformers/extract_epcis_inbox_data.py` + `utils/epcis/*` | Parse XML, extract shipping events, products, containers |
| `InsertEPCISInbox` | `data_exporters/insert_epcis_inbox.py` | Insert to Directus epcis_inbox collection |
| `UpdateInboundWatermark` | `utils/watermark_tracker.py` | Update global_config watermark |

---

## Pipeline 2: Outbound Shipments Received

### Current Mage Flow (8 blocks)

```
poll_approved_shipments (Data Loader)
         ↓
query_shipment_events (Transformer)
         ↓
build_epcis_documents (Transformer)
         ↓
add_xml_headers (Transformer)
         ↓
manage_dispatch_records (Data Exporter)
         ↓
dispatch_via_trustmed (Data Exporter)
         ↓
poll_dispatch_confirmation (Data Exporter)
         ↓
notify_on_errors (Data Exporter)
```

### Go Implementation

```go
// pipelines/outbound/pipeline.go
func Run(ctx context.Context, db *sqlx.DB, cms *tasks.DirectusClient, id string) error {
    var approvedShipments []types.ApprovedShipment
    var shipmentsWithEvents []types.ShipmentWithEvents
    var epcisDocuments []types.EPCISDocument
    var enhancedDocuments []types.EnhancedDocument
    var dispatchResults []types.DispatchResult

    flow := pipelines.NewFlow("outbound-shipments")

    // Task 1: Poll approved shipments from Directus
    flow.AddTask("poll_approved_shipments", func() error {
        var err error
        approvedShipments, err = tasks.PollApprovedShipments(ctx, cms)
        return err
    })

    // Task 2: Query related events from TiDB (CTE for hierarchy)
    flow.AddTask("query_shipment_events", func() error {
        var err error
        shipmentsWithEvents, err = tasks.QueryShipmentEvents(ctx, db, approvedShipments)
        return err
    }, "poll_approved_shipments")

    // Task 3: Build EPCIS 2.0 JSON-LD documents
    flow.AddTask("build_epcis_documents", func() error {
        var err error
        epcisDocuments, err = tasks.BuildEPCISDocuments(ctx, shipmentsWithEvents)
        return err
    }, "query_shipment_events")

    // Task 4: Add SBDH headers, DSCSA statements, VocabularyList
    flow.AddTask("add_xml_headers", func() error {
        var err error
        enhancedDocuments, err = tasks.AddXMLHeaders(ctx, cms, epcisDocuments)
        return err
    }, "build_epcis_documents")

    // Task 5: Create/update dispatch records, upload files to Directus
    flow.AddTask("manage_dispatch_records", func() error {
        return tasks.ManageDispatchRecords(ctx, cms, enhancedDocuments)
    }, "add_xml_headers")

    // Task 6: Dispatch via TrustMed Partner API (mTLS)
    flow.AddTask("dispatch_via_trustmed", func() error {
        var err error
        dispatchResults, err = tasks.DispatchViaTrustMed(ctx, cms, enhancedDocuments)
        return err
    }, "manage_dispatch_records")

    // Task 7: Poll TrustMed Dashboard for delivery confirmation
    flow.AddTask("poll_dispatch_confirmation", func() error {
        return tasks.PollDispatchConfirmation(ctx, cms, dispatchResults)
    }, "dispatch_via_trustmed")

    // Task 8: Log and notify on permanent failures
    flow.AddTask("notify_on_errors", func() error {
        return tasks.NotifyOnErrors(ctx, dispatchResults)
    }, "poll_dispatch_confirmation")

    return flow.Run(ctx)
}
```

### Task Implementations Required

| Task | Source File | Description |
|------|-------------|-------------|
| `PollApprovedShipments` | `data_loaders/poll_approved_shipments.py` | Query shipping_scanning_operation with status=approved, check retry eligibility |
| `QueryShipmentEvents` | `transformers/query_shipment_events.py` | TiDB CTE query for hierarchical events (shipping + commissioning + aggregation) |
| `BuildEPCISDocuments` | `transformers/build_epcis_documents.py` + `utils/epcis/epcis_builder.py` | Create EPCIS 2.0 JSON-LD, convert to XML |
| `AddXMLHeaders` | `transformers/add_xml_headers.py` + `utils/epcis/xml_enhancer.py` | Add SBDH, DSCSA, VocabularyList |
| `ManageDispatchRecords` | `data_exporters/manage_dispatch_records.py` | Create EPCIS_outbound record, upload files |
| `DispatchViaTrustMed` | `data_exporters/dispatch_via_trustmed.py` + `utils/trustmed_client.py` | mTLS dispatch to Partner API |
| `PollDispatchConfirmation` | `data_exporters/poll_dispatch_confirmation.py` + `clients/trustmed_dashboard_client.py` | Check delivery status |
| `NotifyOnErrors` | `data_exporters/notify_on_errors.py` | Log permanent failures |

---

## TrustMed mTLS Certificate Handling

### Certificate Locations

| Environment | Location | Source |
|-------------|----------|--------|
| Local Dev (Demo) | `certs/trustmed/client-cert.crt`, `client-key.key`, `trustmed-ca.crt` | Local files (gitignored) |
| Local Dev (Prod) | `certs/trustmed/client-cert-prod.crt`, `client-key-prod.key`, `trustmed-ca-prod.crt` | Local files (gitignored) |
| Cloud Run (Prod) | `/etc/creds/trustmed/` | Google Cloud Secret Manager mounts |

### Environment Variables

```bash
# Demo (default)
TRUSTMED_CERTFILE=certs/trustmed/client-cert.crt
TRUSTMED_KEYFILE=certs/trustmed/client-key.key
TRUSTMED_CAFILE=certs/trustmed/trustmed-ca.crt

# Production (override)
TRUSTMED_CERTFILE_PROD=/etc/creds/trustmed/client-cert.crt
TRUSTMED_KEYFILE_PROD=/etc/creds/trustmed/client-key.key
TRUSTMED_CAFILE_PROD=/etc/creds/trustmed/trustmed-ca.crt
```

### Go mTLS Client Implementation

```go
// tasks/trustmed_client.go
type TrustMedClient struct {
    endpoint   string
    httpClient *http.Client
}

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

func (c *TrustMedClient) SubmitEPCIS(ctx context.Context, xmlContent string) (*SubmitResponse, error) {
    req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, strings.NewReader(xmlContent))
    if err != nil {
        return nil, err
    }
    req.Header.Set("Content-Type", "application/xml")

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("mTLS request failed: %w", err)
    }
    defer resp.Body.Close()

    // Parse response...
}
```

### Certificate Setup Script

```bash
#!/bin/bash
# scripts/setup-certs.sh

mkdir -p certs/trustmed

echo "Copy TrustMed certificates to certs/trustmed/"
echo ""
echo "Required files:"
echo "  - client-cert.crt     (Demo client certificate)"
echo "  - client-key.key      (Demo private key)"
echo "  - trustmed-ca.crt     (Demo CA certificate)"
echo ""
echo "For production:"
echo "  - client-cert-prod.crt"
echo "  - client-key-prod.key"
echo "  - trustmed-ca-prod.crt"
```

---

## Configuration

### Environment Variables (.env.example)

```bash
# Server
PORT=8080

# Directus CMS
CMS_BASE_URL=http://localhost:8055
DIRECTUS_CMS_API_KEY=your-api-key

# TiDB Database
DB_HOST=127.0.0.1
DB_PORT=4000
DB_NAME=huds_local
DB_USER=root
DB_PASSWORD=

# EPCIS Converter Service
EPCIS_CONVERTER_URL=http://localhost:8075

# TrustMed Dashboard API
TRUSTMED_DASHBOARD_URL=https://demo.dashboard.trust.med/api/v1.0
TRUSTMED_USERNAME=your-username
TRUSTMED_PASSWORD_DEMO=your-demo-password
TRUSTMED_PASSWORD_PROD=your-prod-password

# TrustMed Partner API (mTLS) - Demo
TRUSTMED_ENDPOINT=https://demo.partner.trust.med/v1/client/storage
TRUSTMED_CERTFILE=certs/trustmed/client-cert.crt
TRUSTMED_KEYFILE=certs/trustmed/client-key.key
TRUSTMED_CAFILE=certs/trustmed/trustmed-ca.crt

# TrustMed Partner API - Production (override)
TRUSTMED_ENDPOINT_PROD=https://partner.trust.med/v1/client/storage
TRUSTMED_CERTFILE_PROD=/etc/creds/trustmed/client-cert.crt
TRUSTMED_KEYFILE_PROD=/etc/creds/trustmed/client-key.key
TRUSTMED_CAFILE_PROD=/etc/creds/trustmed/trustmed-ca.crt

# Directus Folder IDs
DIRECTUS_FOLDER_INPUT_XML=uuid-here
DIRECTUS_FOLDER_INPUT_JSON=uuid-here
DIRECTUS_FOLDER_OUTPUT_XML=uuid-here
DIRECTUS_FOLDER_OUTPUT_JSON=uuid-here

# Pipeline Settings
DISPATCH_BATCH_SIZE=10
DISPATCH_MAX_RETRIES=3
```

### Config Struct

```go
// configs/env.go
type Config struct {
    Port string

    // Directus
    CMSBaseURL        string
    DirectusCMSAPIKey string

    // Database
    DBHost     string
    DBPort     string
    DBName     string
    DBUser     string
    DBPassword string

    // EPCIS Converter
    EPCISConverterURL string

    // TrustMed Dashboard
    TrustMedDashboardURL string
    TrustMedUsername     string
    TrustMedPassword     string

    // TrustMed Partner API (mTLS)
    TrustMedEndpoint string
    TrustMedCertFile string
    TrustMedKeyFile  string
    TrustMedCAFile   string

    // Directus Folders
    FolderInputXML   string
    FolderInputJSON  string
    FolderOutputXML  string
    FolderOutputJSON string

    // Pipeline Settings
    DispatchBatchSize  int
    DispatchMaxRetries int
}

func LoadConfig() *Config {
    return &Config{
        Port:              getEnvOrDefault("PORT", "8080"),
        CMSBaseURL:        os.Getenv("CMS_BASE_URL"),
        // ... etc
    }
}
```

---

## Testing Strategy

### Unit Tests

Each task gets a dedicated test file using mocks:

| Test File | What it Tests |
|-----------|---------------|
| `tasks/directus_client_test.go` | HTTP mocking with httptest.Server |
| `tasks/tidb_queries_test.go` | SQL mocking with go-sqlmock |
| `tasks/epcis_converter_test.go` | XML/JSON conversion with mock server |
| `tasks/trustmed_client_test.go` | mTLS client with test certificates |
| `tasks/epcis_extractor_test.go` | XML parsing with fixture files |
| `tasks/epcis_builder_test.go` | JSON-LD document construction |

### E2E Tests

Replicate the `make test-trustmed` pattern:

```go
// tests/e2e_inbound_test.go
func TestInboundPipelineE2E(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping E2E test in short mode")
    }

    ctx := context.Background()
    cfg := configs.LoadConfig()

    // 1. Check services are running
    checkServices(t, cfg)

    // 2. Clean Directus folders
    cms := tasks.NewDirectusClient(cfg.CMSBaseURL, cfg.DirectusCMSAPIKey)
    cleanDirectusFolders(t, cms, cfg)

    // 3. Delete watermark
    deleteWatermark(t, cms)

    // 4. Upload test XML file
    uploadTestXML(t, cms, cfg, "fixtures/DSCSAExample.xml")

    // 5. Run pipeline
    db := connectDB(t, cfg)
    err := inbound.Run(ctx, db, cms, "e2e-test")
    require.NoError(t, err)

    // 6. Verify results
    verifyJSONFilesCreated(t, cms, cfg)
    verifyWatermarkUpdated(t, cms)
    verifyInboxRecordCreated(t, cms)
}
```

```go
// tests/e2e_outbound_test.go
func TestOutboundPipelineE2E(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping E2E test in short mode")
    }

    ctx := context.Background()
    cfg := configs.LoadConfig()

    // 1. Check services (including TrustMed demo)
    checkServices(t, cfg)

    // 2. Setup: Create approved shipment with events in DB
    db := connectDB(t, cfg)
    cms := tasks.NewDirectusClient(cfg.CMSBaseURL, cfg.DirectusCMSAPIKey)
    shipmentID := setupTestShipment(t, db, cms)

    // 3. Run pipeline
    err := outbound.Run(ctx, db, cms, shipmentID)
    require.NoError(t, err)

    // 4. Verify dispatch record created
    verifyDispatchRecordCreated(t, cms, shipmentID)

    // 5. Verify files uploaded
    verifyEPCISFilesUploaded(t, cms, shipmentID)

    // 6. Verify TrustMed dispatch (check status)
    verifyTrustMedDispatch(t, cms, shipmentID)
}
```

### Makefile Commands

```makefile
.PHONY: test test-unit test-e2e test-inbound test-outbound

test: test-unit test-e2e

test-unit:
	go test -v -short ./...

test-e2e:
	go test -v -run E2E ./tests/...

test-inbound: reset-inbound
	@echo "Uploading test XML file..."
	go run scripts/upload_test_file.go
	@echo "Running inbound pipeline..."
	go run . -pipeline=inbound -id=test
	@echo "Verifying results..."
	go run scripts/verify_inbound.go

test-outbound: reset-outbound
	@echo "Setting up test shipment..."
	go run scripts/setup_test_shipment.go
	@echo "Running outbound pipeline..."
	go run . -pipeline=outbound -id=test
	@echo "Verifying results..."
	go run scripts/verify_outbound.go

reset-inbound:
	go run scripts/reset_inbound.go

reset-outbound:
	go run scripts/reset_outbound.go
```

---

## Implementation Order

### Phase 1: Foundation (Week 1)

1. **Clone template and setup repo**
   - Copy from tv-pipelines-template
   - Update go.mod, README, CLAUDE.md
   - Setup .gitignore (include certs/)

2. **Configuration**
   - Implement configs/env.go with all variables
   - Create .env.example

3. **Base clients**
   - `tasks/directus_client.go` - Extend template client for file operations
   - `tasks/tidb_queries.go` - Basic query helpers

4. **Certificate handling**
   - Create certs/ directory structure
   - Implement mTLS client factory

### Phase 2: Inbound Pipeline (Week 2)

1. **Tasks**
   - `tasks/directus_files.go` - Poll XML, upload JSON
   - `tasks/directus_collections.go` - Watermark, epcis_inbox
   - `tasks/epcis_converter.go` - XML ↔ JSON service client
   - `tasks/epcis_extractor.go` - Parse XML, extract shipping data

2. **Pipeline**
   - `pipelines/inbound/pipeline.go`

3. **Unit tests**
   - All task test files

4. **E2E test**
   - `tests/e2e_inbound_test.go`
   - Test fixtures

### Phase 3: Outbound Pipeline (Week 3)

1. **Tasks**
   - `tasks/tidb_queries.go` - CTE query for hierarchical events
   - `tasks/epcis_builder.go` - Build EPCIS 2.0 JSON-LD
   - `tasks/epcis_enhancer.go` - Add SBDH, DSCSA, VocabularyList
   - `tasks/trustmed_client.go` - mTLS dispatch
   - `tasks/trustmed_dashboard.go` - Confirmation polling

2. **Pipeline**
   - `pipelines/outbound/pipeline.go`

3. **Unit tests**
   - All task test files
   - Mock mTLS tests

4. **E2E test**
   - `tests/e2e_outbound_test.go`

### Phase 4: Integration & Deployment (Week 4)

1. **HTTP API**
   - Register both pipelines in main.go
   - Health checks

2. **Docker**
   - Multi-stage build
   - Certificate mounting

3. **CI/CD**
   - GitHub Actions workflow
   - Secret management

4. **Documentation**
   - README.md
   - CLAUDE.md

---

## Critical Implementation Details

### 1. Watermark Tracking

Store in Directus `global_config` collection:

```go
type Watermark struct {
    LastCheckTimestamp time.Time `json:"last_check_timestamp"`
    TotalProcessed     int       `json:"total_processed"`
}

func GetWatermark(ctx context.Context, cms *DirectusClient, key string) (*Watermark, error)
func UpdateWatermark(ctx context.Context, cms *DirectusClient, key string, w *Watermark) error
```

### 2. Hierarchical Event Query (TiDB CTE)

```sql
WITH
top_level_events AS (
    SELECT event_id FROM epcis_events_raw WHERE capture_id = ?
),
top_level_keys AS (
    SELECT epc_join_key FROM epc_events
    WHERE event_id IN (SELECT event_id FROM top_level_events)
),
top_level_commissioning AS (
    SELECT event_id FROM epc_events
    WHERE biz_step = 'commissioning'
    AND epc_join_key IN (SELECT epc_join_key FROM top_level_keys)
),
aggregation_events AS (
    SELECT parent_epc_join_key, child_epc_join_key
    FROM view_aggregation_children
    WHERE parent_epc_join_key IN (SELECT epc_join_key FROM top_level_keys)
),
child_commissioning AS (
    SELECT event_id FROM epc_events
    WHERE biz_step = 'commissioning'
    AND epc_join_key IN (SELECT child_epc_join_key FROM aggregation_events)
)
SELECT DISTINCT e.event_id, e.event_body, e.date_created
FROM epcis_events_raw e
WHERE e.event_id IN (
    SELECT event_id FROM top_level_events
    UNION SELECT event_id FROM top_level_commissioning
    UNION SELECT event_id FROM child_commissioning
)
ORDER BY e.date_created ASC
```

### 3. Retry Logic

Built into Flow API (from template):
- 2 retries (3 total attempts)
- 5 second delay between retries
- Context cancellation support

For dispatch-specific retries:
- Track `dispatch_attempt_count` in EPCIS_outbound
- Max 3 attempts
- Status transitions: pending → Processing → Acknowledged/Retrying/Failed

### 4. Failure Thresholds

Replicate Mage pattern: fail pipeline if >50% of batch items fail

```go
func checkFailureThreshold(total, failed int) error {
    if total > 0 && float64(failed)/float64(total) > 0.5 {
        return fmt.Errorf("failure rate %.0f%% exceeds 50%% threshold", float64(failed)/float64(total)*100)
    }
    return nil
}
```

---

## Files to Reference During Implementation

### Inbound Pipeline

| Go Task | Mage Source |
|---------|-------------|
| `PollXMLFiles` | `data_loaders/directus_poll_xml_files.py` |
| `ConvertXMLToJSON` | `transformers/xml_to_json_converter.py`, `modules/epcis_converter_client.py` |
| `UploadJSONFiles` | `data_exporters/directus_upload_json.py` |
| `ExtractEPCISInboxData` | `transformers/extract_epcis_inbox_data.py`, `utils/epcis/event_extractor.py`, `utils/epcis/master_data_handler.py` |
| `InsertEPCISInbox` | `data_exporters/insert_epcis_inbox.py` |
| Watermark | `utils/watermark_tracker.py` |

### Outbound Pipeline

| Go Task | Mage Source |
|---------|-------------|
| `PollApprovedShipments` | `data_loaders/poll_approved_shipments.py` |
| `QueryShipmentEvents` | `transformers/query_shipment_events.py` |
| `BuildEPCISDocuments` | `transformers/build_epcis_documents.py`, `utils/epcis/epcis_builder.py` |
| `AddXMLHeaders` | `transformers/add_xml_headers.py`, `utils/epcis/xml_enhancer.py` |
| `ManageDispatchRecords` | `data_exporters/manage_dispatch_records.py`, `utils/dispatch_tracker.py` |
| `DispatchViaTrustMed` | `data_exporters/dispatch_via_trustmed.py`, `utils/trustmed_client.py` |
| `PollDispatchConfirmation` | `data_exporters/poll_dispatch_confirmation.py`, `clients/trustmed_dashboard_client.py` |
| `NotifyOnErrors` | `data_exporters/notify_on_errors.py` |

---

## Success Criteria

1. **Functional Parity**: Both pipelines produce identical outputs to Mage versions
2. **E2E Tests Pass**: `make test-inbound` and `make test-outbound` succeed
3. **Unit Test Coverage**: >80% coverage on task implementations
4. **mTLS Working**: Successfully dispatch to TrustMed demo environment
5. **Docker Build**: Multi-stage build with certificate support
6. **CI/CD**: GitHub Actions passing on push
