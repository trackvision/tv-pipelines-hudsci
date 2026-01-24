package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// TrustMedDashboardClient handles TrustMed Dashboard API operations
type TrustMedDashboardClient struct {
	dashboardURL string
	username     string
	password     string
	clientID     string
	companyID    string
	httpClient   *http.Client
	token        string
	tokenExpiry  time.Time
}

// TokenResponse represents the JWT token response
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// FileSearchResponse represents the search results from Dashboard API
type FileSearchResponse struct {
	Count   int           `json:"count"`
	Next    *string       `json:"next"`
	Results []FileRecord  `json:"results"`
}

// FileRecord represents a single file record from Dashboard API
type FileRecord struct {
	LogGuid      string    `json:"logGuid"`
	StatusMsg    string    `json:"statusMsg"`
	StatusCode   int       `json:"statusCode"`
	IsSender     bool      `json:"is_sender"`
	DateCreated  time.Time `json:"date_created"`
	DateModified time.Time `json:"date_modified"`
}

// DispatchStatus represents the mapped dispatch confirmation status
type DispatchStatus struct {
	Status       string    `json:"status"`
	IsDelivered  bool      `json:"is_delivered"`
	IsPermanent  bool      `json:"is_permanent"`
	StatusCode   int       `json:"status_code"`
	StatusMsg    string    `json:"status_msg"`
	LastChecked  time.Time `json:"last_checked"`
}

// NewTrustMedDashboardClient creates a new TrustMed Dashboard API client
func NewTrustMedDashboardClient(cfg *configs.Config) *TrustMedDashboardClient {
	// Default client/company IDs for demo environment
	clientID := cfg.TrustMedClientID
	if clientID == "" {
		clientID = "37018" // Demo default
	}
	companyID := cfg.TrustMedCompanyID
	if companyID == "" {
		companyID = "37018" // Demo default
	}

	logger.Info("Initializing TrustMed Dashboard client",
		zap.String("dashboard_url", cfg.TrustMedDashboardURL),
		zap.String("username", cfg.TrustMedUsername),
		zap.String("client_id", clientID),
		zap.String("company_id", companyID),
	)

	return &TrustMedDashboardClient{
		dashboardURL: strings.TrimSuffix(cfg.TrustMedDashboardURL, "/"),
		username:     cfg.TrustMedUsername,
		password:     cfg.TrustMedPassword,
		clientID:     clientID,
		companyID:    companyID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// getToken obtains or refreshes the JWT access token
func (c *TrustMedDashboardClient) getToken(ctx context.Context) (string, error) {
	// Check if we have a valid cached token (with 1 min buffer)
	if c.token != "" && time.Now().Before(c.tokenExpiry.Add(-1*time.Minute)) {
		return c.token, nil
	}

	logger.Info("Requesting new JWT access token from TrustMed Dashboard")

	url := fmt.Sprintf("%s/token", c.dashboardURL)

	payload := map[string]string{
		"username":  c.username,
		"password":  c.password,
		"client_id": c.clientID,
		"scope":     "openid",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshaling token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("creating token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}

	// Cache token (typically expires in ~10 minutes)
	c.token = tokenResp.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn-60) * time.Second)

	logger.Info("Successfully obtained JWT access token",
		zap.Time("expires_at", c.tokenExpiry),
	)

	return c.token, nil
}

// getAuthHeaders returns headers with current JWT token
func (c *TrustMedDashboardClient) getAuthHeaders(ctx context.Context) (map[string]string, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", token),
	}, nil
}

// SearchFiles searches for EPCIS files in the specified date range
func (c *TrustMedDashboardClient) SearchFiles(ctx context.Context, startDate, endDate time.Time, page int) (*FileSearchResponse, error) {
	logger.Info("Searching TrustMed Dashboard for files",
		zap.Time("start_date", startDate),
		zap.Time("end_date", endDate),
		zap.Int("page", page),
	)

	headers, err := c.getAuthHeaders(ctx)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/de-status/company/%s/log/?start=%s&end=%s&page=%d",
		c.dashboardURL,
		c.companyID,
		startDate.Format("2006-01-02T15:04:05Z"),
		endDate.Format("2006-01-02T15:04:05Z"),
		page,
	)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating search request: %w", err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("search request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var searchResp FileSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("decoding search response: %w", err)
	}

	logger.Info("Search completed",
		zap.Int("count", searchResp.Count),
		zap.Int("results", len(searchResp.Results)),
	)

	return &searchResp, nil
}

// GetFileStatus gets the status of a specific file by log UUID
func (c *TrustMedDashboardClient) GetFileStatus(ctx context.Context, logUUID string) (*FileRecord, error) {
	logger.Info("Getting file status from TrustMed Dashboard",
		zap.String("log_uuid", logUUID),
	)

	// Search in a wide date range (last 90 days)
	endDate := time.Now()
	startDate := endDate.Add(-90 * 24 * time.Hour)

	// Search for the file (may require pagination)
	page := 1
	for {
		searchResp, err := c.SearchFiles(ctx, startDate, endDate, page)
		if err != nil {
			return nil, err
		}

		// Look for the specific file
		for _, record := range searchResp.Results {
			if record.LogGuid == logUUID {
				logger.Info("Found file status",
					zap.String("log_uuid", logUUID),
					zap.String("status", record.StatusMsg),
					zap.Int("status_code", record.StatusCode),
				)
				return &record, nil
			}
		}

		// Check if there are more pages
		if searchResp.Next == nil {
			break
		}
		page++
	}

	return nil, fmt.Errorf("file not found with log UUID: %s", logUUID)
}

// PollDispatchConfirmation checks delivery status for a dispatched document
// Returns DispatchStatus with mapped status from TrustMed Dashboard API
func (c *TrustMedDashboardClient) PollDispatchConfirmation(ctx context.Context, logUUID string) (*DispatchStatus, error) {
	logger.Info("Polling dispatch confirmation",
		zap.String("log_uuid", logUUID),
	)

	record, err := c.GetFileStatus(ctx, logUUID)
	if err != nil {
		return nil, err
	}

	// Map TrustMed status to our status
	status := mapTrustMedStatus(record.StatusCode, record.StatusMsg)

	logger.Info("Dispatch status retrieved",
		zap.String("log_uuid", logUUID),
		zap.String("status", status.Status),
		zap.Bool("is_delivered", status.IsDelivered),
	)

	return &status, nil
}

// mapTrustMedStatus maps TrustMed status codes to our dispatch status
// Based on the migration plan status mapping
func mapTrustMedStatus(statusCode int, statusMsg string) DispatchStatus {
	status := DispatchStatus{
		StatusCode:  statusCode,
		StatusMsg:   statusMsg,
		LastChecked: time.Now(),
	}

	// Map based on status code and message
	switch {
	case statusCode == 200 && strings.Contains(strings.ToLower(statusMsg), "acknowledged"):
		status.Status = "Acknowledged"
		status.IsDelivered = true
		status.IsPermanent = true

	case statusCode == 200 && strings.Contains(strings.ToLower(statusMsg), "processing"):
		status.Status = "Processing"
		status.IsDelivered = false
		status.IsPermanent = false

	case statusCode >= 400 && statusCode < 500:
		// 4xx errors are permanent failures (bad request, auth, etc)
		status.Status = "Failed"
		status.IsDelivered = false
		status.IsPermanent = true

	case statusCode >= 500:
		// 5xx errors are temporary failures (retry eligible)
		status.Status = "Retrying"
		status.IsDelivered = false
		status.IsPermanent = false

	default:
		// Unknown status, mark as processing
		status.Status = "Processing"
		status.IsDelivered = false
		status.IsPermanent = false
	}

	return status
}

// SearchAllFiles searches for all EPCIS files in the date range, handling pagination
func (c *TrustMedDashboardClient) SearchAllFiles(ctx context.Context, startDate, endDate time.Time, receiverOnly bool) ([]FileRecord, error) {
	logger.Info("Searching all TrustMed files with pagination",
		zap.Time("start_date", startDate),
		zap.Time("end_date", endDate),
		zap.Bool("receiver_only", receiverOnly),
	)

	var allRecords []FileRecord
	page := 1

	for {
		searchResp, err := c.SearchFiles(ctx, startDate, endDate, page)
		if err != nil {
			return nil, err
		}

		// Filter to only received files if requested
		for _, record := range searchResp.Results {
			// is_sender=false means we RECEIVED the file (inbound)
			if receiverOnly && record.IsSender {
				continue
			}
			allRecords = append(allRecords, record)
		}

		// Check if there are more pages
		if searchResp.Next == nil {
			break
		}
		page++

		// Safety limit to prevent infinite loops
		if page > 100 {
			logger.Warn("Reached pagination limit", zap.Int("pages", page))
			break
		}
	}

	logger.Info("Completed searching all files",
		zap.Int("total_records", len(allRecords)),
		zap.Int("pages", page),
	)

	return allRecords, nil
}

// GetDownloadURL gets a temporary download URL for a file (valid ~10 minutes)
func (c *TrustMedDashboardClient) GetDownloadURL(ctx context.Context, logUUID string) (string, error) {
	logger.Info("Getting download URL from TrustMed Dashboard",
		zap.String("log_uuid", logUUID),
	)

	headers, err := c.getAuthHeaders(ctx)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/de-status/log/%s/download", c.dashboardURL, logUUID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("creating download URL request: %w", err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download URL request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("download URL request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	// Response is a plain text quoted string
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading download URL response: %w", err)
	}

	// Remove quotes from the URL string
	downloadURL := strings.Trim(string(body), "\"")

	logger.Info("Got temporary download URL",
		zap.String("log_uuid", logUUID),
	)

	return downloadURL, nil
}

// DownloadFile downloads file content using the two-step process
func (c *TrustMedDashboardClient) DownloadFile(ctx context.Context, logUUID string) ([]byte, error) {
	logger.Info("Downloading file from TrustMed",
		zap.String("log_uuid", logUUID),
	)

	// Step 1: Get temporary download URL
	downloadURL, err := c.GetDownloadURL(ctx, logUUID)
	if err != nil {
		return nil, fmt.Errorf("getting download URL: %w", err)
	}

	// Step 2: Download file content (no auth needed - signed URL)
	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating download request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading file content: %w", err)
	}

	logger.Info("Successfully downloaded file",
		zap.String("log_uuid", logUUID),
		zap.Int("size_bytes", len(content)),
	)

	return content, nil
}
