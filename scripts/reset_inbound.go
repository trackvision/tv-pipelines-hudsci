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
	fmt.Println("  Resetting Inbound Pipeline State")
	fmt.Println(sep)
	fmt.Println()

	cms := tasks.NewDirectusClient(cfg.CMSBaseURL, cfg.DirectusCMSAPIKey)

	// Step 1: Clean XML folder
	fmt.Printf("Step 1: Cleaning XML folder (%s)...\n", cfg.FolderInputXML)
	xmlFiles, err := listDirectusFiles(cms, cfg.FolderInputXML)
	if err != nil {
		log.Printf("Warning: Failed to list XML files: %v", err)
	} else {
		for _, file := range xmlFiles {
			fileID := file["id"].(string)
			if err := deleteFile(cms, fileID); err != nil {
				log.Printf("Warning: Failed to delete file %s: %v", fileID, err)
			}
		}
		fmt.Printf("✓ Removed %d XML file(s)\n", len(xmlFiles))
	}

	// Step 2: Clean JSON folder
	fmt.Printf("\nStep 2: Cleaning JSON folder (%s)...\n", cfg.FolderInputJSON)
	jsonFiles, err := listDirectusFiles(cms, cfg.FolderInputJSON)
	if err != nil {
		log.Printf("Warning: Failed to list JSON files: %v", err)
	} else {
		for _, file := range jsonFiles {
			fileID := file["id"].(string)
			if err := deleteFile(cms, fileID); err != nil {
				log.Printf("Warning: Failed to delete file %s: %v", fileID, err)
			}
		}
		fmt.Printf("✓ Removed %d JSON file(s)\n", len(jsonFiles))
	}

	// Step 3: Delete watermark
	fmt.Println("\nStep 3: Deleting watermark...")
	if err := deleteWatermark(cms); err != nil {
		log.Printf("Warning: Failed to delete watermark: %v", err)
	} else {
		fmt.Println("✓ Watermark deleted")
	}

	// Step 4: Optional - clean inbox records
	fmt.Println("\nStep 4: Cleaning inbox records (optional)...")
	fmt.Println("Skipping inbox cleanup (keep historical data)")
	// Uncomment to clean inbox:
	// if err := cleanInboxRecords(cms); err != nil {
	//     log.Printf("Warning: Failed to clean inbox: %v", err)
	// }

	fmt.Println()
	fmt.Println(sep)
	fmt.Println("✓ Reset complete")
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

func deleteFile(cms *tasks.DirectusClient, fileID string) error {
	ctx := context.Background()
	url := fmt.Sprintf("%s/files/%s", cms.BaseURL, fileID)

	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+cms.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("delete failed with status %d", resp.StatusCode)
	}

	return nil
}

func deleteWatermark(cms *tasks.DirectusClient) error {
	ctx := context.Background()
	watermarkKey := "inbound_shipment_received_watermark"

	// Try to get the watermark first
	watermark, err := tasks.GetWatermark(ctx, cms, watermarkKey)
	if err != nil {
		return nil // Watermark doesn't exist, nothing to delete
	}

	// If watermark exists and is not zero, delete it
	if !watermark.LastCheckTimestamp.IsZero() {
		// Delete by querying the collection
		url := fmt.Sprintf("%s/items/global_config?filter[key][_eq]=%s", cms.BaseURL, watermarkKey)
		req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
		if err != nil {
			return err
		}

		req.Header.Set("Authorization", "Bearer "+cms.APIKey)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
	}

	return nil
}
