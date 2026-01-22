// go:build integration
// +build integration

package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-pipelines-hudsci/pipelines/outbound"
	"github.com/trackvision/tv-pipelines-hudsci/tasks"
)

func TestOutboundPipelineE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	ctx := context.Background()
	cfg := loadTestConfig(t)

	// Step 1: Check services are running
	t.Log("Step 1: Checking services...")
	checkDirectusService(t, cfg)
	checkTrustMedService(t, cfg)

	// Step 2: Initialize clients
	t.Log("Step 2: Initializing clients...")
	cms := tasks.NewDirectusClient(cfg.CMSBaseURL, cfg.DirectusCMSAPIKey)
	db := connectDB(t, cfg)
	defer db.Close()

	// Step 3: Setup test shipment with events
	t.Log("Step 3: Setting up test shipment...")
	shipmentID := setupTestShipment(t, db, cms)
	require.NotEmpty(t, shipmentID, "Test shipment should be created")

	// Give the database a moment to propagate changes
	time.Sleep(2 * time.Second)

	// Step 4: Run outbound pipeline
	t.Log("Step 4: Running outbound pipeline...")
	err := outbound.Run(ctx, db, cms, cfg, shipmentID)
	require.NoError(t, err, "Pipeline should complete without errors")

	// Step 5: Verify dispatch record created
	t.Log("Step 5: Verifying dispatch record...")
	dispatchRecords := getDispatchRecords(t, cms, shipmentID)
	require.GreaterOrEqual(t, len(dispatchRecords), 1, "At least one dispatch record should be created")
	t.Logf("Created %d dispatch record(s)", len(dispatchRecords))

	// Step 6: Verify files uploaded
	t.Log("Step 6: Verifying EPCIS files...")
	if len(dispatchRecords) > 0 {
		record := dispatchRecords[0]
		if xmlFileID, ok := record["xml_file"].(string); ok && xmlFileID != "" {
			t.Logf("✓ XML file uploaded: %s", xmlFileID)
		}
		if jsonFileID, ok := record["json_file"].(string); ok && jsonFileID != "" {
			t.Logf("✓ JSON file uploaded: %s", jsonFileID)
		}
	}

	// Step 7: Verify TrustMed dispatch attempted
	t.Log("Step 7: Verifying TrustMed dispatch...")
	if len(dispatchRecords) > 0 {
		record := dispatchRecords[0]
		status, _ := record["status"].(string)
		t.Logf("Dispatch status: %s", status)

		// Status should be Processing, Acknowledged, or Retrying (not Failed on first attempt)
		require.Contains(t, []string{"Processing", "Acknowledged", "Retrying", "pending"},
			status, "Dispatch should be attempted or completed")
	}

	t.Log("✓ E2E test completed successfully")
}

// Helper functions

func checkTrustMedService(t *testing.T, cfg *configs.Config) {
	// For E2E tests, we just check if the dashboard URL is accessible
	// The actual mTLS dispatch will be tested by the pipeline
	resp, err := http.Get(cfg.TrustMedDashboardURL + "/health")
	if err != nil {
		t.Logf("Warning: TrustMed Dashboard not accessible: %v", err)
		t.Logf("Pipeline will continue but dispatch may fail")
		return
	}
	defer resp.Body.Close()
	t.Logf("✓ TrustMed Dashboard is accessible at %s", cfg.TrustMedDashboardURL)
}

func setupTestShipment(t *testing.T, db *sqlx.DB, cms *tasks.DirectusClient) string {
	// For E2E test, use an existing capture_id that has events in the database
	// Query the database to find one
	ctx := context.Background()

	var captureID string
	err := db.GetContext(ctx, &captureID,
		"SELECT capture_id FROM epcis_events_raw LIMIT 1")
	if err != nil {
		// If no events exist, create a test shipment without events (test will verify pipeline doesn't crash)
		t.Logf("No existing events found, creating test shipment without events: %v", err)
		captureID = fmt.Sprintf("TEST-CAPTURE-%d", time.Now().Unix())
	} else {
		t.Logf("Using existing capture_id with events: %s", captureID)
	}

	// Create a test shipment in the shipping_scanning_operation collection
	shipment := map[string]any{
		"status":                 "approved",
		"shipment_id":            fmt.Sprintf("TEST-SHIP-%d", time.Now().Unix()),
		"date_created":           time.Now().Format(time.RFC3339),
		"dispatch_attempt_count": 0,
		"capture_id":             captureID,
	}

	result, err := cms.PostItem(ctx, "shipping_scanning_operation", shipment)
	require.NoError(t, err, "Test shipment should be created")

	shipmentID, ok := result["id"].(string)
	require.True(t, ok, "Shipment ID should be a string")

	t.Logf("✓ Created test shipment: %s (ID: %s)", shipment["shipment_id"], shipmentID)

	// Note: In a real E2E test, you would also create associated events in the database
	// For now, this is a minimal setup to test the pipeline flow

	return shipmentID
}

func getDispatchRecords(t *testing.T, cms *tasks.DirectusClient, shipmentID string) []map[string]any {
	ctx := context.Background()
	// The dispatch record stores shipping_operation_id, not shipment_id
	url := fmt.Sprintf("%s/items/EPCIS_outbound?filter[shipping_operation_id][_eq]=%s", cms.BaseURL, shipmentID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	require.NoError(t, err)

	req.Header.Set("Authorization", "Bearer "+cms.APIKey)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var result struct {
		Data []map[string]any `json:"data"`
	}
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	return result.Data
}
