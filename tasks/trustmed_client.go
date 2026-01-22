package tasks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// TrustMedClient handles TrustMed Partner API operations with mTLS
type TrustMedClient struct {
	endpoint   string
	httpClient *http.Client
}

// FlexibleTime handles timestamps with or without timezone suffix
type FlexibleTime struct {
	time.Time
}

// UnmarshalJSON parses timestamps in various formats
func (ft *FlexibleTime) UnmarshalJSON(data []byte) error {
	// Remove quotes
	s := strings.Trim(string(data), "\"")
	if s == "" || s == "null" {
		return nil
	}

	// Try common timestamp formats
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999", // Without timezone
		"2006-01-02T15:04:05.999",       // Without timezone (milliseconds)
		"2006-01-02T15:04:05",           // Without timezone or fractional seconds
		"2006-01-02 15:04:05",           // Space separator
	}

	var lastErr error
	for _, format := range formats {
		t, err := time.Parse(format, s)
		if err == nil {
			ft.Time = t
			return nil
		}
		lastErr = err
	}

	return fmt.Errorf("unable to parse timestamp %q: %w", s, lastErr)
}

// TrustMedSubmitResponse represents the response from TrustMed Partner API
type TrustMedSubmitResponse struct {
	ID        string       `json:"id"`
	CreatedAt FlexibleTime `json:"created_at"`
}

// NewTrustMedClient creates a new TrustMed Partner API client with mTLS
func NewTrustMedClient(cfg *configs.Config) (*TrustMedClient, error) {
	logger.Info("Initializing TrustMed mTLS client",
		zap.String("endpoint", cfg.TrustMedEndpoint),
		zap.String("cert_file", cfg.TrustMedCertFile),
	)

	// Load client certificate and key
	cert, err := tls.LoadX509KeyPair(cfg.TrustMedCertFile, cfg.TrustMedKeyFile)
	if err != nil {
		return nil, fmt.Errorf("loading client certificate: %w", err)
	}

	// Load CA certificate for server verification
	caCert, err := os.ReadFile(cfg.TrustMedCAFile)
	if err != nil {
		return nil, fmt.Errorf("reading CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to append CA certificate to pool")
	}

	// Create TLS configuration with mTLS
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
	}

	// Create HTTP client with mTLS transport
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
		Timeout: 30 * time.Second,
	}

	logger.Info("TrustMed mTLS client initialized")

	return &TrustMedClient{
		endpoint:   cfg.TrustMedEndpoint,
		httpClient: client,
	}, nil
}

// SubmitEPCIS submits an EPCIS XML document to TrustMed Partner API
func (c *TrustMedClient) SubmitEPCIS(ctx context.Context, xmlContent string) (*TrustMedSubmitResponse, error) {
	logger.Info("Submitting EPCIS XML to TrustMed",
		zap.String("endpoint", c.endpoint),
		zap.Int("xml_size", len(xmlContent)),
	)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, strings.NewReader(xmlContent))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/xml")

	// Execute mTLS request
	startTime := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mTLS request failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			logger.Warn("Failed to close response body", zap.Error(cerr))
		}
	}()

	duration := time.Since(startTime)

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	// Handle non-2xx status codes
	if resp.StatusCode >= 400 {
		logger.Error("TrustMed submission failed",
			zap.Int("status", resp.StatusCode),
			zap.String("response", string(body)),
			zap.Duration("duration", duration),
		)
		return nil, fmt.Errorf("TrustMed API error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	// Parse JSON response
	var result TrustMedSubmitResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing response JSON: %w", err)
	}

	logger.Info("Successfully submitted to TrustMed",
		zap.Int("status", resp.StatusCode),
		zap.String("transaction_id", result.ID),
		zap.Time("created_at", result.CreatedAt.Time),
		zap.Duration("duration", duration),
	)

	return &result, nil
}

// GetStatusCodeFromError extracts HTTP status code from error
// Returns 500 if status code cannot be determined
func (c *TrustMedClient) GetStatusCodeFromError(err error) int {
	if err == nil {
		return 0
	}

	// Check if error message contains HTTP status code
	errStr := err.Error()
	if strings.Contains(errStr, "HTTP 400") {
		return 400
	}
	if strings.Contains(errStr, "HTTP 401") {
		return 401
	}
	if strings.Contains(errStr, "HTTP 403") {
		return 403
	}
	if strings.Contains(errStr, "HTTP 404") {
		return 404
	}
	if strings.Contains(errStr, "HTTP 500") {
		return 500
	}
	if strings.Contains(errStr, "HTTP 502") {
		return 502
	}
	if strings.Contains(errStr, "HTTP 503") {
		return 503
	}

	// Default to 500 for unknown errors
	return 500
}
