// go:build integration
// +build integration

package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-pipelines-hudsci/pipelines/inbound"
	"github.com/trackvision/tv-pipelines-hudsci/tasks"
)

func TestInboundPipelineE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	ctx := context.Background()
	cfg := loadTestConfig(t)

	// Step 1: Check services are running
	t.Log("Step 1: Checking services...")
	checkDirectusService(t, cfg)
	checkEPCISConverterService(t, cfg)

	// Step 2: Initialize clients
	t.Log("Step 2: Initializing clients...")
	cms := tasks.NewDirectusClient(cfg.CMSBaseURL, cfg.DirectusCMSAPIKey)

	// Step 3: Clean Directus folders
	t.Log("Step 3: Cleaning Directus folders...")
	cleanDirectusFolder(t, cms, cfg.FolderInputXML)
	cleanDirectusFolder(t, cms, cfg.FolderInputJSON)

	// Step 4: Delete watermark
	t.Log("Step 4: Deleting watermark...")
	deleteWatermark(t, cms)

	// Step 5: Upload test XML file
	t.Log("Step 5: Uploading test XML file...")
	fileID := uploadTestXML(t, cms, cfg)
	require.NotEmpty(t, fileID, "Test file should be uploaded")

	// Give Directus a moment to process the file
	time.Sleep(2 * time.Second)

	// Step 6: Initialize database connection
	t.Log("Step 6: Connecting to database...")
	db := connectDB(t, cfg)
	defer db.Close()

	// Step 7: Run inbound pipeline
	t.Log("Step 7: Running inbound pipeline...")
	err := inbound.Run(ctx, db, cms, cfg, "e2e-test")
	require.NoError(t, err, "Pipeline should complete without errors")

	// Step 8: Verify results
	t.Log("Step 8: Verifying results...")

	// Check JSON files were created
	jsonFiles := listDirectusFiles(t, cms, cfg.FolderInputJSON)
	require.GreaterOrEqual(t, len(jsonFiles), 1, "At least one JSON file should be created")
	t.Logf("Created %d JSON file(s)", len(jsonFiles))

	// Check watermark was updated
	watermark := getWatermark(t, cms)
	require.NotNil(t, watermark, "Watermark should exist")
	t.Logf("Watermark: last_check=%s, total_processed=%v", watermark["last_check"], watermark["total_processed"])

	// Check inbox records were created
	inboxRecords := getInboxRecords(t, cms)
	require.GreaterOrEqual(t, len(inboxRecords), 1, "At least one inbox record should be created")
	t.Logf("Created %d inbox record(s)", len(inboxRecords))

	t.Log("✓ E2E test completed successfully")
}

// Helper functions

func loadTestConfig(t *testing.T) *configs.Config {
	// Load .env file if it exists
	if _, err := os.Stat(".env"); err == nil {
		// .env file exists, it will be loaded by the env package
		t.Log("Using .env file for configuration")
	}

	cfg, err := configs.Load()
	require.NoError(t, err, "Configuration should load successfully")

	// Validate required fields for E2E test
	require.NotEmpty(t, cfg.CMSBaseURL, "CMS_BASE_URL must be set")
	require.NotEmpty(t, cfg.DirectusCMSAPIKey, "DIRECTUS_CMS_API_KEY must be set")
	require.NotEmpty(t, cfg.EPCISConverterURL, "EPCIS_CONVERTER_URL must be set")
	require.NotEmpty(t, cfg.FolderInputXML, "DIRECTUS_FOLDER_INPUT_XML must be set")
	require.NotEmpty(t, cfg.FolderInputJSON, "DIRECTUS_FOLDER_INPUT_JSON must be set")

	return cfg
}

func checkDirectusService(t *testing.T, cfg *configs.Config) {
	resp, err := http.Get(cfg.CMSBaseURL + "/server/health")
	require.NoError(t, err, "Directus should be accessible")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "Directus health check should return 200")
	t.Logf("✓ Directus is running at %s", cfg.CMSBaseURL)
}

func checkEPCISConverterService(t *testing.T, cfg *configs.Config) {
	resp, err := http.Get(cfg.EPCISConverterURL + "/q/health")
	require.NoError(t, err, "EPCIS Converter should be accessible")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "EPCIS Converter health check should return 200")
	t.Logf("✓ EPCIS Converter is running at %s", cfg.EPCISConverterURL)
}

func cleanDirectusFolder(t *testing.T, cms *tasks.DirectusClient, folderID string) {
	files := listDirectusFiles(t, cms, folderID)
	t.Logf("Cleaning folder %s (%d files)...", folderID, len(files))

	for _, file := range files {
		fileID := file["id"].(string)
		// Delete file using DELETE request
		url := fmt.Sprintf("%s/files/%s", cms.BaseURL, fileID)
		req, err := http.NewRequestWithContext(context.Background(), "DELETE", url, nil)
		if err != nil {
			t.Logf("Warning: Failed to create delete request for %s: %v", fileID, err)
			continue
		}
		req.Header.Set("Authorization", "Bearer "+cms.APIKey)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("Warning: Failed to delete file %s: %v", fileID, err)
			continue
		}
		resp.Body.Close()
	}
	t.Logf("✓ Cleaned folder %s", folderID)
}

func listDirectusFiles(t *testing.T, cms *tasks.DirectusClient, folderID string) []map[string]any {
	ctx := context.Background()
	url := fmt.Sprintf("%s/files?filter[folder][_eq]=%s", cms.BaseURL, folderID)

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

func deleteWatermark(t *testing.T, cms *tasks.DirectusClient) {
	ctx := context.Background()
	watermarkKey := "inbound_shipment_received_watermark"

	// Try to get the watermark first
	watermark, err := tasks.GetWatermark(ctx, cms, watermarkKey)
	if err != nil {
		t.Logf("Failed to get watermark: %v", err)
		return
	}

	// If watermark exists and is not zero, delete it
	if !watermark.LastCheckTimestamp.IsZero() {
		// Delete by querying the collection
		url := fmt.Sprintf("%s/items/global_config?filter[key][_eq]=%s", cms.BaseURL, watermarkKey)
		req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
		require.NoError(t, err)

		req.Header.Set("Authorization", "Bearer "+cms.APIKey)

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			t.Logf("✓ Deleted watermark: %s", watermarkKey)
		} else {
			t.Logf("Warning: Could not delete watermark: %v", err)
		}
	} else {
		t.Logf("Watermark doesn't exist or is zero (nothing to delete)")
	}
}

func uploadTestXML(t *testing.T, cms *tasks.DirectusClient, cfg *configs.Config) string {
	// Read test XML file - try multiple paths for different test contexts
	possiblePaths := []string{
		"tests/fixtures/DSCSAExample.xml",
		"../tests/fixtures/DSCSAExample.xml",
		"fixtures/DSCSAExample.xml",
	}

	var xmlContent []byte
	var xmlPath string
	var err error
	for _, path := range possiblePaths {
		xmlContent, err = os.ReadFile(path)
		if err == nil {
			xmlPath = path
			break
		}
	}
	require.NoError(t, err, "Test XML file should exist in one of: %v", possiblePaths)

	t.Logf("Uploading test file: %s (%d bytes)", xmlPath, len(xmlContent))

	// Upload to Directus
	result, err := cms.UploadFile(context.Background(), tasks.UploadFileParams{
		Filename: "DSCSAExample.xml",
		Content:  xmlContent,
		FolderID: cfg.FolderInputXML,
		Title:    "Test EPCIS XML for E2E test",
	})
	require.NoError(t, err, "File upload should succeed")

	t.Logf("✓ Uploaded file: %s", result.ID)
	return result.ID
}

func connectDB(t *testing.T, cfg *configs.Config) *sqlx.DB {
	if cfg.DBHost == "" {
		t.Skip("Database not configured (DB_HOST not set)")
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		cfg.DBUser,
		cfg.DBPassword,
		cfg.DBHost,
		cfg.DBPort,
		cfg.DBName,
	)
	if cfg.DBSSL {
		dsn += "&tls=skip-verify"
	}

	db, err := sqlx.Open("mysql", dsn)
	require.NoError(t, err, "Database connection should succeed")

	err = db.Ping()
	require.NoError(t, err, "Database ping should succeed")

	t.Logf("✓ Connected to database: %s@%s:%s/%s", cfg.DBUser, cfg.DBHost, cfg.DBPort, cfg.DBName)
	return db
}

func getWatermark(t *testing.T, cms *tasks.DirectusClient) map[string]any {
	ctx := context.Background()
	watermarkKey := "inbound_shipment_received_watermark"

	watermark, err := tasks.GetWatermark(ctx, cms, watermarkKey)
	if err != nil {
		t.Logf("Watermark not found: %v", err)
		return nil
	}

	// Convert to map for display
	result := map[string]any{
		"last_check":      watermark.LastCheckTimestamp,
		"total_processed": watermark.TotalProcessed,
	}

	return result
}

func getInboxRecords(t *testing.T, cms *tasks.DirectusClient) []map[string]any {
	ctx := context.Background()
	url := fmt.Sprintf("%s/items/epcis_inbox?limit=100", cms.BaseURL)

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
