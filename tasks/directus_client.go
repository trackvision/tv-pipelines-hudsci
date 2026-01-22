package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// DirectusClient handles Directus API operations
type DirectusClient struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

// NewDirectusClient creates a new Directus client
func NewDirectusClient(baseURL, apiKey string) *DirectusClient {
	return &DirectusClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// DirectusResponse wraps the Directus API response
type DirectusResponse struct {
	Data json.RawMessage `json:"data"`
}

// PostItem creates an item in a collection and returns the created item
func (d *DirectusClient) PostItem(ctx context.Context, collection string, item any) (map[string]any, error) {
	logger.Info("Creating Directus item", zap.String("collection", collection))

	body, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("marshaling item: %w", err)
	}

	url := fmt.Sprintf("%s/items/%s", d.BaseURL, collection)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+d.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST request failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			logger.Warn("Failed to close response body", zap.Error(cerr))
		}
	}()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("POST failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var directusResp DirectusResponse
	if err := json.NewDecoder(resp.Body).Decode(&directusResp); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(directusResp.Data, &result); err != nil {
		return nil, fmt.Errorf("unmarshaling data: %w", err)
	}

	logger.Info("Item created", zap.Any("id", result["id"]))
	return result, nil
}

// QueryItems queries items from a collection with filters
func (d *DirectusClient) QueryItems(ctx context.Context, collection string, filter map[string]interface{}, fields []string, limit int) ([]map[string]interface{}, error) {
	logger.Info("Querying Directus items", zap.String("collection", collection), zap.Int("limit", limit))

	url := fmt.Sprintf("%s/items/%s", d.BaseURL, collection)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Build query parameters
	q := req.URL.Query()
	if filter != nil {
		filterJSON, err := json.Marshal(filter)
		if err != nil {
			return nil, fmt.Errorf("marshaling filter: %w", err)
		}
		q.Add("filter", string(filterJSON))
	}
	if len(fields) > 0 {
		for _, field := range fields {
			q.Add("fields[]", field)
		}
	}
	if limit > 0 {
		q.Add("limit", fmt.Sprintf("%d", limit))
	}
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Authorization", "Bearer "+d.APIKey)

	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET request failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			logger.Warn("Failed to close response body", zap.Error(cerr))
		}
	}()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var directusResp DirectusResponse
	if err := json.NewDecoder(resp.Body).Decode(&directusResp); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	var results []map[string]interface{}
	if err := json.Unmarshal(directusResp.Data, &results); err != nil {
		return nil, fmt.Errorf("unmarshaling data: %w", err)
	}

	logger.Info("Query completed", zap.Int("count", len(results)))
	return results, nil
}

// GetFileContent downloads the content of a file by its ID
func (d *DirectusClient) GetFileContent(ctx context.Context, fileID string) ([]byte, error) {
	logger.Info("Getting file content", zap.String("file_id", fileID))

	url := fmt.Sprintf("%s/assets/%s", d.BaseURL, fileID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+d.APIKey)

	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET request failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			logger.Warn("Failed to close response body", zap.Error(cerr))
		}
	}()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading content: %w", err)
	}

	logger.Info("File content retrieved", zap.Int("size", len(content)))
	return content, nil
}

// PatchItem updates an item in a collection
func (d *DirectusClient) PatchItem(ctx context.Context, collection, id string, updates map[string]any) error {
	logger.Info("Updating Directus item", zap.String("collection", collection), zap.String("id", id))

	body, err := json.Marshal(updates)
	if err != nil {
		return fmt.Errorf("marshaling updates: %w", err)
	}

	url := fmt.Sprintf("%s/items/%s/%s", d.BaseURL, collection, id)
	req, err := http.NewRequestWithContext(ctx, "PATCH", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+d.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.Client.Do(req)
	if err != nil {
		return fmt.Errorf("PATCH request failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			logger.Warn("Failed to close response body", zap.Error(cerr))
		}
	}()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PATCH failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	logger.Info("Item updated")
	return nil
}

// UploadFileParams contains parameters for file upload
type UploadFileParams struct {
	Filename    string
	Content     []byte
	FolderID    string
	Title       string
	ContentType string
}

// UploadFileResult contains the result of a file upload
type UploadFileResult struct {
	ID string `json:"id"`
}

// UploadFile uploads a file to Directus
func (d *DirectusClient) UploadFile(ctx context.Context, params UploadFileParams) (*UploadFileResult, error) {
	logger.Info("Uploading file to Directus",
		zap.String("filename", params.Filename),
		zap.Int("size", len(params.Content)),
	)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add folder ID if provided
	if params.FolderID != "" {
		if err := writer.WriteField("folder", params.FolderID); err != nil {
			return nil, fmt.Errorf("writing folder field: %w", err)
		}
	}

	// Add title if provided
	if params.Title != "" {
		if err := writer.WriteField("title", params.Title); err != nil {
			return nil, fmt.Errorf("writing title field: %w", err)
		}
	}

	// Add file
	part, err := writer.CreateFormFile("file", params.Filename)
	if err != nil {
		return nil, fmt.Errorf("creating form file: %w", err)
	}
	if _, err := part.Write(params.Content); err != nil {
		return nil, fmt.Errorf("writing file content: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("closing multipart writer: %w", err)
	}

	url := fmt.Sprintf("%s/files", d.BaseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+d.APIKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload request failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			logger.Warn("Failed to close response body", zap.Error(cerr))
		}
	}()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var directusResp DirectusResponse
	if err := json.NewDecoder(resp.Body).Decode(&directusResp); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	var result UploadFileResult
	if err := json.Unmarshal(directusResp.Data, &result); err != nil {
		return nil, fmt.Errorf("unmarshaling file data: %w", err)
	}

	logger.Info("File uploaded", zap.String("fileID", result.ID))
	return &result, nil
}
