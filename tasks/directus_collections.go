package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// Watermark represents the watermark state for incremental processing
type Watermark struct {
	LastCheckTimestamp WatermarkTime `json:"last_check_timestamp"`
	TotalProcessed     int           `json:"total_processed"`
}

// WatermarkTime is a custom time type that handles multiple timestamp formats
type WatermarkTime struct {
	time.Time
}

// UnmarshalJSON handles parsing timestamps with or without timezone
func (wt *WatermarkTime) UnmarshalJSON(data []byte) error {
	// Remove quotes
	s := string(data)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}

	if s == "" || s == "null" {
		wt.Time = time.Time{}
		return nil
	}

	// Try various formats
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
	}

	var err error
	for _, format := range formats {
		wt.Time, err = time.Parse(format, s)
		if err == nil {
			return nil
		}
	}

	return fmt.Errorf("unable to parse time %q", s)
}

// GlobalConfigValue represents a value in the global_config collection
type GlobalConfigValue struct {
	ID    string          `json:"id,omitempty"`
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// GetWatermark gets the watermark from Directus global_config collection
func GetWatermark(ctx context.Context, cms *DirectusClient, key string) (*Watermark, error) {
	logger.Info("Getting watermark", zap.String("key", key))

	url := fmt.Sprintf("%s/items/global_config", cms.BaseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Query for the specific key
	q := req.URL.Query()
	q.Add("filter[key][_eq]", key)
	q.Add("limit", "1")
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Authorization", "Bearer "+cms.APIKey)

	resp, err := cms.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var directusResp DirectusResponse
	if err := json.NewDecoder(resp.Body).Decode(&directusResp); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	var configs []GlobalConfigValue
	if err := json.Unmarshal(directusResp.Data, &configs); err != nil {
		return nil, fmt.Errorf("unmarshaling configs: %w", err)
	}

	// Return empty watermark if not found
	if len(configs) == 0 {
		logger.Info("No watermark found, returning zero value")
		return &Watermark{}, nil
	}

	// Parse the value JSON
	// Directus may return the value as either a JSON object or a JSON string containing the object
	var watermark Watermark
	valueBytes := configs[0].Value

	// First try to unmarshal as a string (Directus sometimes double-encodes)
	var valueStr string
	if err := json.Unmarshal(valueBytes, &valueStr); err == nil {
		// It was a string, now parse the inner JSON
		if err := json.Unmarshal([]byte(valueStr), &watermark); err != nil {
			return nil, fmt.Errorf("unmarshaling watermark from string value: %w", err)
		}
	} else {
		// Not a string, try direct unmarshal
		if err := json.Unmarshal(valueBytes, &watermark); err != nil {
			return nil, fmt.Errorf("unmarshaling watermark value: %w", err)
		}
	}

	logger.Info("Retrieved watermark",
		zap.Time("timestamp", watermark.LastCheckTimestamp.Time),
		zap.Int("total_processed", watermark.TotalProcessed),
	)

	return &watermark, nil
}

// UpdateWatermark updates the watermark in Directus global_config collection
func UpdateWatermark(ctx context.Context, cms *DirectusClient, key string, timestamp time.Time, processedCount int) error {
	logger.Info("Updating watermark",
		zap.String("key", key),
		zap.Time("timestamp", timestamp),
		zap.Int("processed_count", processedCount),
	)

	// Get current watermark to preserve total count
	current, err := GetWatermark(ctx, cms, key)
	if err != nil {
		logger.Warn("Failed to get current watermark, will create new", zap.Error(err))
		current = &Watermark{}
	}

	// Update total processed
	newWatermark := Watermark{
		LastCheckTimestamp: WatermarkTime{Time: timestamp},
		TotalProcessed:     current.TotalProcessed + processedCount,
	}

	// Serialize watermark to JSON
	valueJSON, err := json.Marshal(newWatermark)
	if err != nil {
		return fmt.Errorf("marshaling watermark: %w", err)
	}

	// Check if config exists
	url := fmt.Sprintf("%s/items/global_config", cms.BaseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	q := req.URL.Query()
	q.Add("filter[key][_eq]", key)
	q.Add("limit", "1")
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Authorization", "Bearer "+cms.APIKey)

	resp, err := cms.Client.Do(req)
	if err != nil {
		return fmt.Errorf("GET request failed: %w", err)
	}
	defer resp.Body.Close()

	var directusResp DirectusResponse
	if err := json.NewDecoder(resp.Body).Decode(&directusResp); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	var existingConfigs []GlobalConfigValue
	if err := json.Unmarshal(directusResp.Data, &existingConfigs); err != nil {
		return fmt.Errorf("unmarshaling configs: %w", err)
	}

	// Create or update
	configValue := GlobalConfigValue{
		Key:   key,
		Value: valueJSON,
	}

	if len(existingConfigs) == 0 {
		// Create new config
		logger.Info("Creating new watermark config")
		_, err := cms.PostItem(ctx, "global_config", configValue)
		if err != nil {
			return fmt.Errorf("creating config: %w", err)
		}
	} else {
		// Update existing config - global_config uses 'key' as primary key, not 'id'
		logger.Info("Updating existing watermark config", zap.String("key", existingConfigs[0].Key))
		updates := map[string]any{
			"value": string(valueJSON), // Pass as string to avoid base64 encoding
		}
		if err := cms.PatchItem(ctx, "global_config", existingConfigs[0].Key, updates); err != nil {
			return fmt.Errorf("updating config: %w", err)
		}
	}

	logger.Info("Watermark updated",
		zap.Int("total_processed", newWatermark.TotalProcessed),
	)

	return nil
}

// EPCISInboxItem represents an item in the epcis_inbox collection
type EPCISInboxItem struct {
	Status          string                 `json:"status"`
	Seller          string                 `json:"seller,omitempty"`
	Buyer           string                 `json:"buyer,omitempty"`
	ShipFrom        string                 `json:"ship_from,omitempty"`
	ShipTo          string                 `json:"ship_to,omitempty"`
	ShipDate        string                 `json:"ship_date,omitempty"`
	CaptureMessage  map[string]interface{} `json:"capture_message"`
	RawMessage      string                 `json:"raw_message,omitempty"`
	EPCISXMLFileID  string                 `json:"epcis_xml_file_id,omitempty"`
	EPCISJSONFileID string                 `json:"epcis_json_file_id,omitempty"`
	Products        []map[string]interface{} `json:"products,omitempty"`
	Containers      []map[string]interface{} `json:"containers,omitempty"`
}

// InsertEPCISInbox inserts shipment records into the epcis_inbox collection.
// It skips records that already exist (based on file_id in capture_message).
func InsertEPCISInbox(ctx context.Context, cms *DirectusClient, shipments []EPCISInboxItem) error {
	if len(shipments) == 0 {
		logger.Info("No shipments to insert")
		return nil
	}

	logger.Info("Inserting shipments to epcis_inbox", zap.Int("count", len(shipments)))

	// Get existing file IDs to prevent duplicates
	existingFileIDs, err := getExistingFileIDs(ctx, cms)
	if err != nil {
		logger.Warn("Failed to get existing file IDs, proceeding anyway", zap.Error(err))
		existingFileIDs = make(map[string]bool)
	}

	logger.Info("Found existing records", zap.Int("count", len(existingFileIDs)))

	// Filter out duplicates
	itemsToInsert := make([]EPCISInboxItem, 0, len(shipments))
	skippedCount := 0

	for i, shipment := range shipments {
		fileID, ok := shipment.CaptureMessage["file_id"].(string)
		if ok && existingFileIDs[fileID] {
			logger.Info("Skipping duplicate",
				zap.Int("index", i+1),
				zap.String("file_id", fileID),
			)
			skippedCount++
			continue
		}

		itemsToInsert = append(itemsToInsert, shipment)
	}

	if skippedCount > 0 {
		logger.Info("Skipped duplicate records", zap.Int("count", skippedCount))
	}

	if len(itemsToInsert) == 0 {
		logger.Info("No new records to insert (all were duplicates)")
		return nil
	}

	// Insert in batch
	logger.Info("Inserting new records", zap.Int("count", len(itemsToInsert)))

	for i, item := range itemsToInsert {
		_, err := cms.PostItem(ctx, "epcis_inbox", item)
		if err != nil {
			return fmt.Errorf("inserting item %d: %w", i, err)
		}

		logger.Info("Inserted record",
			zap.Int("index", i+1),
			zap.Int("total", len(itemsToInsert)),
			zap.String("seller", item.Seller),
			zap.String("buyer", item.Buyer),
		)
	}

	logger.Info("Successfully inserted all records")
	return nil
}

// getExistingFileIDs fetches all file_ids from epcis_inbox to prevent duplicates
func getExistingFileIDs(ctx context.Context, cms *DirectusClient) (map[string]bool, error) {
	url := fmt.Sprintf("%s/items/epcis_inbox", cms.BaseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	q := req.URL.Query()
	q.Add("fields", "capture_message")
	q.Add("limit", "-1") // Get all records
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Authorization", "Bearer "+cms.APIKey)

	resp, err := cms.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var directusResp DirectusResponse
	if err := json.NewDecoder(resp.Body).Decode(&directusResp); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	var records []struct {
		CaptureMessage map[string]interface{} `json:"capture_message"`
	}
	if err := json.Unmarshal(directusResp.Data, &records); err != nil {
		return nil, fmt.Errorf("unmarshaling records: %w", err)
	}

	// Extract file IDs
	fileIDs := make(map[string]bool)
	for _, record := range records {
		if record.CaptureMessage != nil {
			if fileID, ok := record.CaptureMessage["file_id"].(string); ok && fileID != "" {
				fileIDs[fileID] = true
			}
		}
	}

	return fileIDs, nil
}

// LinkJSONFilesToInbox updates epcis_inbox records with their corresponding JSON file IDs.
// The fileIDMap maps XML file IDs to JSON file IDs.
func LinkJSONFilesToInbox(ctx context.Context, cms *DirectusClient, fileIDMap map[string]string) error {
	if len(fileIDMap) == 0 {
		logger.Info("No JSON files to link to inbox records")
		return nil
	}

	logger.Info("Linking JSON files to epcis_inbox records", zap.Int("count", len(fileIDMap)))

	// Get all inbox records that match the XML file IDs
	url := fmt.Sprintf("%s/items/epcis_inbox", cms.BaseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	q := req.URL.Query()
	q.Add("fields", "id,epcis_xml_file_id")
	q.Add("limit", "-1")
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Authorization", "Bearer "+cms.APIKey)

	resp, err := cms.Client.Do(req)
	if err != nil {
		return fmt.Errorf("GET request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var directusResp DirectusResponse
	if err := json.NewDecoder(resp.Body).Decode(&directusResp); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	var records []struct {
		ID            string `json:"id"`
		EPCISXMLFileID string `json:"epcis_xml_file_id"`
	}
	if err := json.Unmarshal(directusResp.Data, &records); err != nil {
		return fmt.Errorf("unmarshaling records: %w", err)
	}

	// Update records that have matching XML file IDs
	updatedCount := 0
	for _, record := range records {
		jsonFileID, exists := fileIDMap[record.EPCISXMLFileID]
		if !exists || jsonFileID == "" {
			continue
		}

		// Update the record with JSON file ID
		updates := map[string]any{
			"epcis_json_file_id": jsonFileID,
		}
		if err := cms.PatchItem(ctx, "epcis_inbox", record.ID, updates); err != nil {
			logger.Warn("Failed to update epcis_inbox with JSON file ID",
				zap.String("record_id", record.ID),
				zap.Error(err),
			)
			continue
		}

		updatedCount++
		logger.Info("Linked JSON file to inbox record",
			zap.String("record_id", record.ID),
			zap.String("json_file_id", jsonFileID),
		)
	}

	logger.Info("Finished linking JSON files to inbox records",
		zap.Int("updated_count", updatedCount),
		zap.Int("total_mappings", len(fileIDMap)),
	)

	return nil
}
