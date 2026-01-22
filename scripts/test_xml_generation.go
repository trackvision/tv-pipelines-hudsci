// scripts/test_xml_generation.go
// Test script to run XML generation pipeline steps without TrustMed dispatch.
// Compares output with known-good Mage-generated XML.
//
// Usage: go run scripts/test_xml_generation.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-pipelines-hudsci/tasks"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

func main() {
	ctx := context.Background()

	// Load config (needs EPCIS converter URL)
	cfg, err := configs.Load()
	if err != nil {
		fmt.Printf("Warning: Could not load full config: %v\n", err)
		fmt.Println("Using default converter URL...")
		cfg = &configs.Config{
			EPCISConverterURL: getEnv("EPCIS_CONVERTER_URL", "http://localhost:8081"),
		}
	}

	fmt.Println("=== TrustMed XML Generation Test ===")
	fmt.Printf("Converter URL: %s\n\n", cfg.EPCISConverterURL)

	// Step 1: Load test JSON (the Mage-generated JSON that was accepted)
	fmt.Println("Step 1: Loading test JSON...")
	jsonContent, err := os.ReadFile("test-samples/mage-accepted.json")
	if err != nil {
		fmt.Printf("ERROR: Could not read test JSON: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Loaded %d bytes\n", len(jsonContent))

	// Parse JSON to count events
	var jsonDoc map[string]interface{}
	if err := json.Unmarshal(jsonContent, &jsonDoc); err != nil {
		fmt.Printf("ERROR: Could not parse JSON: %v\n", err)
		os.Exit(1)
	}

	if body, ok := jsonDoc["epcisBody"].(map[string]interface{}); ok {
		if eventList, ok := body["eventList"].([]interface{}); ok {
			fmt.Printf("  Event count: %d\n", len(eventList))
			for i, event := range eventList {
				if e, ok := event.(map[string]interface{}); ok {
					eventType, _ := e["type"].(string)
					bizStep, _ := e["bizStep"].(string)
					fmt.Printf("    Event %d: type=%s, bizStep=%s\n", i+1, eventType, bizStep)
				}
			}
		}
	}

	// Step 2: Convert JSON to XML via converter service
	fmt.Println("\nStep 2: Converting JSON to XML via converter service...")
	xmlContent, err := tasks.ConvertJSONToXML(ctx, cfg, jsonContent)
	if err != nil {
		fmt.Printf("ERROR: Conversion failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Generated %d bytes of XML\n", len(xmlContent))

	// Check root element
	rootElement := extractRootElement(xmlContent)
	fmt.Printf("  Root element: %s\n", rootElement)

	// Save base XML for inspection
	os.WriteFile("test-samples/go-generated-base.xml", xmlContent, 0644)
	fmt.Println("  Saved to: test-samples/go-generated-base.xml")

	// Step 3: Parse events for master data extraction
	fmt.Println("\nStep 3: Preparing events for enhancement...")
	var events []map[string]interface{}
	if body, ok := jsonDoc["epcisBody"].(map[string]interface{}); ok {
		if eventList, ok := body["eventList"].([]interface{}); ok {
			for _, event := range eventList {
				if e, ok := event.(map[string]interface{}); ok {
					events = append(events, e)
				}
			}
		}
	}

	// Step 4: Enhance XML with headers (requires CMS for master data)
	fmt.Println("\nStep 4: Enhancing XML with SBDH headers...")

	// Create a mock CMS client or use real one if available
	cmsURL := getEnv("CMS_BASE_URL", "")
	cmsKey := getEnv("DIRECTUS_CMS_API_KEY", "")

	if cmsURL == "" || cmsKey == "" {
		fmt.Println("  WARNING: No CMS credentials - using minimal enhancement (no master data)")

		// Do minimal enhancement without master data
		enhancedXML, err := minimalEnhance(xmlContent)
		if err != nil {
			fmt.Printf("ERROR: Enhancement failed: %v\n", err)
			os.Exit(1)
		}

		os.WriteFile("test-samples/go-generated-enhanced.xml", enhancedXML, 0644)
		fmt.Println("  Saved to: test-samples/go-generated-enhanced.xml")
	} else {
		fmt.Printf("  Using CMS at: %s\n", cmsURL)

		cms := tasks.NewDirectusClient(cmsURL, cmsKey)
		cfg.DefaultSenderGLN = "1200180203836"
		cfg.DefaultReceiverGLN = "0860014070520"

		docs := []tasks.EPCISDocumentWithMetadata{{
			ShippingOperationID: "test",
			CaptureID:           "test",
			BaseXMLContent:      xmlContent,
			EPCISJSONContent:    jsonContent,
			Events:              events,
		}}

		enhanced, err := tasks.AddXMLHeaders(ctx, cms, cfg, docs)
		if err != nil {
			fmt.Printf("ERROR: Enhancement failed: %v\n", err)
			os.Exit(1)
		}

		if len(enhanced) > 0 {
			os.WriteFile("test-samples/go-generated-enhanced.xml", enhanced[0].EnhancedXML, 0644)
			fmt.Printf("  Generated %d bytes of enhanced XML\n", len(enhanced[0].EnhancedXML))
			fmt.Println("  Saved to: test-samples/go-generated-enhanced.xml")
		}
	}

	// Step 5: Compare with Mage-generated XML
	fmt.Println("\nStep 5: Comparing with Mage-generated XML...")
	mageXML, err := os.ReadFile("test-samples/mage-accepted.xml")
	if err != nil {
		fmt.Printf("ERROR: Could not read Mage XML: %v\n", err)
		os.Exit(1)
	}

	goXML, err := os.ReadFile("test-samples/go-generated-enhanced.xml")
	if err != nil {
		goXML, _ = os.ReadFile("test-samples/go-generated-base.xml")
	}

	// Compare key structural elements
	fmt.Println("\n=== Structural Comparison ===")
	compareXMLStructure(mageXML, goXML)

	fmt.Println("\n=== Done ===")
	fmt.Println("Compare files manually:")
	fmt.Println("  diff test-samples/mage-accepted.xml test-samples/go-generated-enhanced.xml")
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func extractRootElement(xmlData []byte) string {
	xmlStr := string(xmlData)
	start := strings.Index(xmlStr, "<")
	if start == -1 {
		return "unknown"
	}
	// Skip XML declaration
	if strings.HasPrefix(xmlStr[start:], "<?xml") {
		end := strings.Index(xmlStr, "?>")
		if end > 0 {
			start = strings.Index(xmlStr[end+2:], "<") + end + 2
		}
	}
	if start >= len(xmlStr) {
		return "unknown"
	}
	end := start + 1
	for end < len(xmlStr) && xmlStr[end] != ' ' && xmlStr[end] != '>' && xmlStr[end] != '/' {
		end++
	}
	return xmlStr[start+1 : end]
}

func compareXMLStructure(mageXML, goXML []byte) {
	mageStr := string(mageXML)
	goStr := string(goXML)

	checks := []struct {
		name    string
		pattern string
	}{
		{"Root element epcis:EPCISDocument", "<epcis:EPCISDocument"},
		{"EPCISHeader present", "<EPCISHeader"},
		{"SBDH present", "<sbdh:StandardBusinessDocumentHeader"},
		{"VocabularyList present", "<VocabularyList"},
		{"EPCClass vocabulary (products)", `type="urn:epcglobal:epcis:vtype:EPCClass"`},
		{"Location vocabulary", `type="urn:epcglobal:epcis:vtype:Location"`},
		{"guidelineVersion present", "<gs1ushc:guidelineVersion"},
		{"DSCSA statement present", "<gs1ushc:dscsaTransactionStatement"},
		{"EPCISBody present", "<EPCISBody"},
		{"EventList present", "<EventList"},
		{"TransformationEvent present", "<TransformationEvent"},
		{"ObjectEvent present", "<ObjectEvent"},
		{"NDC type US_FDA_NDC", "US_FDA_NDC"},
	}

	for _, check := range checks {
		mageHas := strings.Contains(mageStr, check.pattern)
		goHas := strings.Contains(goStr, check.pattern)

		status := "✓"
		if mageHas && !goHas {
			status = "✗ MISSING in Go"
		} else if !mageHas && goHas {
			status = "? Extra in Go"
		} else if !mageHas && !goHas {
			status = "- Neither"
		}

		fmt.Printf("  %s: %s\n", check.name, status)
	}
}

// minimalEnhance adds basic SBDH headers without master data lookup
func minimalEnhance(baseXML []byte) ([]byte, error) {
	// For now, just return the base XML if no CMS available
	// The AddXMLHeaders function requires CMS for master data
	logger.Info("Minimal enhancement - no master data", zap.Int("xml_size", len(baseXML)))

	// Check if XML already has EPCISDocument root
	if bytes.Contains(baseXML, []byte("<epcis:EPCISDocument")) ||
		bytes.Contains(baseXML, []byte("<EPCISDocument")) {
		return baseXML, nil
	}

	return nil, fmt.Errorf("base XML does not have EPCISDocument root - converter may have failed")
}
