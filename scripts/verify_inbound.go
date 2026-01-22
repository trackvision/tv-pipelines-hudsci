//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/joho/godotenv"
	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-pipelines-hudsci/tasks"
)

func main() {
	// Load .env file if it exists
	_ = godotenv.Load()

	// Load configuration
	cfg, err := configs.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	sep := strings.Repeat("=", 70)
	fmt.Println(sep)
	fmt.Println("  Verify Inbound Pipeline Results")
	fmt.Println(sep)
	fmt.Println()

	cms := tasks.NewDirectusClient(cfg.CMSBaseURL, cfg.DirectusCMSAPIKey)

	// Step 1: Check JSON files created
	fmt.Println("Step 1: Checking JSON files...")
	jsonFiles, err := listDirectusFiles(cms, cfg.FolderInputJSON)
	if err != nil {
		log.Printf("❌ Failed to list JSON files: %v", err)
	} else {
		fmt.Printf("✓ Found %d JSON file(s):\n", len(jsonFiles))
		for _, file := range jsonFiles {
			filename := file["filename_download"]
			fileID := file["id"]
			fmt.Printf("  - %s (ID: %s)\n", filename, fileID)
		}
	}

	// Step 2: Check watermark
	fmt.Println("\nStep 2: Checking watermark...")
	watermark, err := getWatermark(cms)
	if err != nil {
		fmt.Printf("❌ Failed to get watermark: %v\n", err)
	} else if watermark != nil {
		fmt.Println("✓ Watermark exists:")
		if lastCheck, ok := watermark["last_check"]; ok {
			fmt.Printf("  - Last check: %v\n", lastCheck)
		}
		if totalProcessed, ok := watermark["total_processed"]; ok {
			fmt.Printf("  - Total processed: %v\n", totalProcessed)
		}
	} else {
		fmt.Println("❌ Watermark not found")
	}

	// Step 3: Check inbox records
	fmt.Println("\nStep 3: Checking inbox records...")
	inboxRecords, err := getInboxRecords(cms)
	if err != nil {
		log.Printf("❌ Failed to get inbox records: %v", err)
	} else {
		fmt.Printf("✓ Found %d inbox record(s)\n", len(inboxRecords))
		for i, record := range inboxRecords {
			if i >= 5 {
				fmt.Printf("  ... and %d more\n", len(inboxRecords)-5)
				break
			}
			recordID := record["id"]
			shipmentID := record["shipment_id"]
			fmt.Printf("  - Record %v (Shipment: %v)\n", recordID, shipmentID)
		}
	}

	// Summary
	fmt.Println()
	fmt.Println(sep)
	success := true
	if len(jsonFiles) == 0 {
		fmt.Println("❌ No JSON files created")
		success = false
	}
	if watermark == nil {
		fmt.Println("❌ Watermark not created")
		success = false
	}
	if len(inboxRecords) == 0 {
		fmt.Println("⚠  No inbox records created (may be expected)")
	}

	if success {
		fmt.Println("✓ Verification passed")
	} else {
		fmt.Println("❌ Verification failed")
	}
	fmt.Println(sep)
}

func listDirectusFiles(cms *tasks.DirectusClient, folderID string) ([]map[string]any, error) {
	ctx := context.Background()
	url := fmt.Sprintf("%s/files?filter[folder][_eq]=%s", cms.BaseURL, folderID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+cms.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Data, nil
}

func getWatermark(cms *tasks.DirectusClient) (map[string]any, error) {
	ctx := context.Background()
	watermarkKey := "inbound_shipment_received_watermark"

	watermark, err := tasks.GetWatermark(ctx, cms, watermarkKey)
	if err != nil {
		return nil, err
	}

	// Convert to map for display
	result := map[string]any{
		"last_check":      watermark.LastCheckTimestamp,
		"total_processed": watermark.TotalProcessed,
	}

	return result, nil
}

func getInboxRecords(cms *tasks.DirectusClient) ([]map[string]any, error) {
	ctx := context.Background()
	url := fmt.Sprintf("%s/items/epcis_inbox?limit=100&sort=-date_created", cms.BaseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+cms.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Data, nil
}
