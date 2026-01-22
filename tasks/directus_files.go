package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-pipelines-hudsci/types"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// DirectusFile represents a file in Directus
type DirectusFile struct {
	ID               string    `json:"id"`
	Filename         string    `json:"filename_download"`
	Title            string    `json:"title"`
	UploadedOn       time.Time `json:"uploaded_on"`
	Folder           string    `json:"folder"`
	Type             string    `json:"type"`
	ModifiedOn       time.Time `json:"modified_on"`
}

// PollXMLFiles polls Directus for new XML files since the watermark timestamp.
// It fetches up to 50 files per run, downloads their content, and returns them.
func PollXMLFiles(ctx context.Context, cms *DirectusClient, cfg *configs.Config) ([]types.XMLFile, error) {
	logger.Info("Polling Directus for new XML files")

	// Get watermark
	watermark, err := GetWatermark(ctx, cms, "inbound_shipment_received_watermark")
	if err != nil {
		return nil, fmt.Errorf("getting watermark: %w", err)
	}

	// Determine since date
	var sinceDate time.Time
	if watermark.LastCheckTimestamp.Time.IsZero() {
		// No watermark - first run, go back 7 days
		sinceDate = time.Now().Add(-7 * 24 * time.Hour)
		logger.Info("No watermark found, using default lookback", zap.Time("since", sinceDate))

		// Create initial watermark
		if err := UpdateWatermark(ctx, cms, "inbound_shipment_received_watermark", sinceDate, 0); err != nil {
			logger.Warn("Failed to create initial watermark", zap.Error(err))
		}
	} else {
		sinceDate = watermark.LastCheckTimestamp.Time
		logger.Info("Using watermark", zap.Time("since", sinceDate))
	}

	// Query Directus for files
	url := fmt.Sprintf("%s/files", cms.BaseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Add query parameters
	q := req.URL.Query()
	q.Add("filter[uploaded_on][_gte]", sinceDate.Format(time.RFC3339))
	if cfg.FolderInputXML != "" {
		q.Add("filter[folder][_eq]", cfg.FolderInputXML)
	}
	q.Add("filter[filename_download][_ends_with]", ".xml") // Match XML files by extension
	q.Add("sort", "uploaded_on")
	q.Add("limit", "50")
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

	var files []DirectusFile
	if err := json.Unmarshal(directusResp.Data, &files); err != nil {
		return nil, fmt.Errorf("unmarshaling files: %w", err)
	}

	logger.Info("Found XML files", zap.Int("count", len(files)))

	if len(files) == 0 {
		return []types.XMLFile{}, nil
	}

	// Fetch content for each file
	xmlFiles := make([]types.XMLFile, 0, len(files))
	failedCount := 0

	for i, file := range files {
		logger.Info("Fetching file content",
			zap.Int("index", i+1),
			zap.Int("total", len(files)),
			zap.String("fileID", file.ID),
			zap.String("filename", file.Filename),
		)

		content, err := DownloadFileContent(ctx, cms, file.ID)
		if err != nil {
			logger.Error("Failed to download file content",
				zap.String("fileID", file.ID),
				zap.Error(err),
			)
			failedCount++
			continue
		}

		xmlFiles = append(xmlFiles, types.XMLFile{
			ID:       file.ID,
			Filename: file.Filename,
			Content:  content,
			Uploaded: file.UploadedOn,
		})
		logger.Info("Successfully loaded file",
			zap.String("filename", file.Filename),
			zap.Int("size", len(content)),
		)
	}

	// Check failure threshold
	if len(files) > 0 {
		failureRate := float64(failedCount) / float64(len(files))
		if failureRate > cfg.FailureThreshold {
			return nil, fmt.Errorf("failure rate %.0f%% exceeds threshold %.0f%%",
				failureRate*100, cfg.FailureThreshold*100)
		}
	}

	logger.Info("Successfully loaded XML files",
		zap.Int("successful", len(xmlFiles)),
		zap.Int("failed", failedCount),
	)

	return xmlFiles, nil
}

// DownloadFileContent downloads the content of a file by ID
func DownloadFileContent(ctx context.Context, cms *DirectusClient, fileID string) ([]byte, error) {
	url := fmt.Sprintf("%s/assets/%s", cms.BaseURL, fileID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

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

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	return content, nil
}

// UploadJSONFiles uploads converted JSON files to Directus.
// Returns a map of source XML file ID to uploaded JSON file ID for linking.
func UploadJSONFiles(ctx context.Context, cms *DirectusClient, cfg *configs.Config, files []types.ConvertedFile) (map[string]string, error) {
	logger.Info("Uploading JSON files to Directus", zap.Int("count", len(files)))

	// Map from source XML file ID to JSON file ID
	fileIDMap := make(map[string]string)

	for i, file := range files {
		logger.Info("Uploading JSON file",
			zap.Int("index", i+1),
			zap.Int("total", len(files)),
			zap.String("filename", file.Filename),
			zap.String("source_id", file.SourceID),
		)

		params := UploadFileParams{
			Filename:    file.Filename,
			Content:     file.JSONData,
			FolderID:    cfg.FolderInputJSON,
			Title:       file.Filename,
			ContentType: "application/json",
		}

		result, err := cms.UploadFile(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("uploading file %s: %w", file.Filename, err)
		}

		logger.Info("JSON file uploaded",
			zap.String("fileID", result.ID),
			zap.String("source_id", file.SourceID),
		)

		// Store mapping from source XML file ID to JSON file ID
		if file.SourceID != "" {
			fileIDMap[file.SourceID] = result.ID
		}
	}

	logger.Info("All JSON files uploaded successfully", zap.Int("mapped_count", len(fileIDMap)))
	return fileIDMap, nil
}
