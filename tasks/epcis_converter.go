package tasks

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-pipelines-hudsci/types"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// EPCISConverterClient handles communication with the EPCIS converter service
type EPCISConverterClient struct {
	BaseURL string
	Client  *http.Client
}

// NewEPCISConverterClient creates a new EPCIS converter client
func NewEPCISConverterClient(baseURL string) *EPCISConverterClient {
	return &EPCISConverterClient{
		BaseURL: baseURL,
		Client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// ConvertXMLToJSON converts EPCIS XML files to JSON via the converter service.
// It processes each XML file and returns the converted JSON files.
func ConvertXMLToJSON(ctx context.Context, cfg *configs.Config, xmlFiles []types.XMLFile) ([]types.ConvertedFile, error) {
	if len(xmlFiles) == 0 {
		logger.Info("No XML files to convert")
		return []types.ConvertedFile{}, nil
	}

	logger.Info("Converting XML to JSON", zap.Int("count", len(xmlFiles)))

	client := NewEPCISConverterClient(cfg.EPCISConverterURL)
	convertedFiles := make([]types.ConvertedFile, 0, len(xmlFiles))
	failedCount := 0

	for i, xmlFile := range xmlFiles {
		logger.Info("Converting file",
			zap.Int("index", i+1),
			zap.Int("total", len(xmlFiles)),
			zap.String("filename", xmlFile.Filename),
		)

		jsonData, err := client.ConvertToJSON(ctx, xmlFile.Content)
		if err != nil {
			logger.Error("Conversion failed",
				zap.String("filename", xmlFile.Filename),
				zap.Error(err),
			)
			failedCount++
			continue
		}

		// Generate JSON filename from XML filename
		jsonFilename := strings.TrimSuffix(xmlFile.Filename, filepath.Ext(xmlFile.Filename)) + ".json"

		convertedFiles = append(convertedFiles, types.ConvertedFile{
			SourceID:   xmlFile.ID,
			Filename:   jsonFilename,
			JSONData:   jsonData,
			XMLContent: xmlFile.Content,
		})

		logger.Info("Conversion successful",
			zap.String("filename", xmlFile.Filename),
			zap.Int("json_size", len(jsonData)),
		)
	}

	// Check failure threshold
	if len(xmlFiles) > 0 {
		failureRate := float64(failedCount) / float64(len(xmlFiles))
		if failureRate > cfg.FailureThreshold {
			return nil, fmt.Errorf("conversion failure rate %.0f%% exceeds threshold %.0f%%",
				failureRate*100, cfg.FailureThreshold*100)
		}
	}

	logger.Info("XML to JSON conversion complete",
		zap.Int("successful", len(convertedFiles)),
		zap.Int("failed", failedCount),
	)

	return convertedFiles, nil
}

// ConvertToJSON converts EPCIS XML to JSON-LD format using the converter service
func (c *EPCISConverterClient) ConvertToJSON(ctx context.Context, xmlContent []byte) ([]byte, error) {
	url := fmt.Sprintf("%s/api/convert/json/2.0", c.BaseURL)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(xmlContent))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("GS1-EPC-Format", "Always_EPC_URN") // Keep identifiers in URN format

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("conversion failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	jsonData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	logger.Debug("Conversion successful", zap.Int("json_size", len(jsonData)))
	return jsonData, nil
}

// ConvertJSONToXML converts EPCIS 2.0 JSON-LD to XML format (for outbound pipeline)
func ConvertJSONToXML(ctx context.Context, cfg *configs.Config, jsonContent []byte) ([]byte, error) {
	logger.Info("Converting JSON to XML")

	client := NewEPCISConverterClient(cfg.EPCISConverterURL)

	url := fmt.Sprintf("%s/api/convert/xml/1.2", client.BaseURL)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonContent))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("GS1-EPC-Format", "Always_EPC_URN") // Keep identifiers in URN format

	resp, err := client.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("conversion failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	xmlData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	// Debug: Extract and log the root element name to verify proper EPCIS document structure
	rootElement := extractRootElementName(xmlData)

	// Log a truncated sample of the XML for debugging
	xmlSample := string(xmlData)
	if len(xmlSample) > 500 {
		xmlSample = xmlSample[:500] + "..."
	}

	logger.Info("JSON to XML conversion successful",
		zap.Int("xml_size", len(xmlData)),
		zap.String("root_element", rootElement),
		zap.String("xml_sample", xmlSample),
	)
	return xmlData, nil
}

// extractRootElementName extracts the root element name from XML bytes.
// Used for debugging to verify converter returns proper EPCISDocument structure.
func extractRootElementName(xmlData []byte) string {
	xmlStr := string(xmlData)

	// Skip XML declaration if present
	start := 0
	if strings.HasPrefix(xmlStr, "<?xml") {
		end := strings.Index(xmlStr, "?>")
		if end > 0 {
			start = end + 2
		}
	}

	// Skip whitespace
	for start < len(xmlStr) && (xmlStr[start] == ' ' || xmlStr[start] == '\n' || xmlStr[start] == '\r' || xmlStr[start] == '\t') {
		start++
	}

	// Find opening tag
	if start < len(xmlStr) && xmlStr[start] == '<' {
		end := start + 1
		for end < len(xmlStr) && xmlStr[end] != ' ' && xmlStr[end] != '>' && xmlStr[end] != '/' {
			end++
		}
		return xmlStr[start+1 : end]
	}

	return "unknown"
}

// HealthCheck checks if the converter service is available
func (c *EPCISConverterClient) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("%s/health", c.BaseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}

	logger.Info("Converter service is healthy")
	return nil
}
