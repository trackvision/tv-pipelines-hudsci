//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"os"
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
	fmt.Println("  Upload Test XML File")
	fmt.Println(sep)
	fmt.Println()

	// Read test XML file
	testFile := "tests/fixtures/DSCSAExample.xml"
	xmlContent, err := os.ReadFile(testFile)
	if err != nil {
		log.Fatalf("Failed to read test file: %v", err)
	}

	fmt.Printf("Found test file: %s (%d bytes)\n", testFile, len(xmlContent))

	// Upload to Directus
	cms := tasks.NewDirectusClient(cfg.CMSBaseURL, cfg.DirectusCMSAPIKey)
	ctx := context.Background()

	fmt.Printf("Uploading to Directus folder: %s\n", cfg.FolderInputXML)

	result, err := cms.UploadFile(ctx, tasks.UploadFileParams{
		Filename: "DSCSAExample.xml",
		Content:  xmlContent,
		FolderID: cfg.FolderInputXML,
		Title:    "Test EPCIS XML for pipeline testing",
	})
	if err != nil {
		log.Fatalf("Upload failed: %v", err)
	}

	fmt.Printf("✓ Uploaded successfully: %s\n", result.ID)
	fmt.Println()
	fmt.Println("You can now run the inbound pipeline:")
	fmt.Println("  make run")
	fmt.Println("  curl -X POST http://localhost:8080/run/inbound -d '{\"id\":\"test\"}'")
}
