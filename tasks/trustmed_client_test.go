package tasks

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trackvision/tv-pipelines-hudsci/configs"
)

func TestTrustMedClient_SubmitEPCIS_Success(t *testing.T) {
	// Create mock TLS server
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and content type
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/xml" {
			t.Errorf("Expected Content-Type 'application/xml', got '%s'", ct)
		}

		// Return success response
		response := TrustMedSubmitResponse{
			ID:        "test-transaction-123",
			CreatedAt: FlexibleTime{Time: time.Now()},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create client with test server's TLS config
	client := &TrustMedClient{
		endpoint: server.URL,
		httpClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, // OK for testing
				},
			},
			Timeout: 30 * time.Second,
		},
	}

	ctx := context.Background()
	xmlContent := `<?xml version="1.0"?><epcis:EPCISDocument xmlns:epcis="urn:epcglobal:epcis:xsd:1"></epcis:EPCISDocument>`

	result, err := client.SubmitEPCIS(ctx, xmlContent)
	if err != nil {
		t.Fatalf("SubmitEPCIS failed: %v", err)
	}

	if result.ID != "test-transaction-123" {
		t.Errorf("Expected transaction ID 'test-transaction-123', got '%s'", result.ID)
	}
}

func TestTrustMedClient_SubmitEPCIS_HTTPError(t *testing.T) {
	// Create mock server that returns error
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Invalid XML format"))
	}))
	defer server.Close()

	client := &TrustMedClient{
		endpoint: server.URL,
		httpClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
			Timeout: 30 * time.Second,
		},
	}

	ctx := context.Background()
	xmlContent := `<invalid>xml</invalid>`

	result, err := client.SubmitEPCIS(ctx, xmlContent)
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	if result != nil {
		t.Errorf("Expected nil result on error, got %v", result)
	}

	if !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("Expected HTTP 400 error, got: %v", err)
	}
}

func TestTrustMedClient_SubmitEPCIS_InvalidJSON(t *testing.T) {
	// Create mock server that returns invalid JSON
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	client := &TrustMedClient{
		endpoint: server.URL,
		httpClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
			Timeout: 30 * time.Second,
		},
	}

	ctx := context.Background()
	xmlContent := `<?xml version="1.0"?><epcis></epcis>`

	result, err := client.SubmitEPCIS(ctx, xmlContent)
	if err == nil {
		t.Fatal("Expected error parsing invalid JSON, got nil")
	}

	if result != nil {
		t.Errorf("Expected nil result on error, got %v", result)
	}

	if !strings.Contains(err.Error(), "parsing response JSON") {
		t.Errorf("Expected JSON parsing error, got: %v", err)
	}
}

func TestTrustMedClient_GetStatusCodeFromError(t *testing.T) {
	client := &TrustMedClient{}

	tests := []struct {
		name     string
		err      error
		expected int
	}{
		{"nil error", nil, 0},
		{"HTTP 400", &testError{"TrustMed API error (HTTP 400): Bad Request"}, 400},
		{"HTTP 401", &testError{"TrustMed API error (HTTP 401): Unauthorized"}, 401},
		{"HTTP 403", &testError{"TrustMed API error (HTTP 403): Forbidden"}, 403},
		{"HTTP 404", &testError{"TrustMed API error (HTTP 404): Not Found"}, 404},
		{"HTTP 500", &testError{"TrustMed API error (HTTP 500): Server Error"}, 500},
		{"HTTP 502", &testError{"TrustMed API error (HTTP 502): Bad Gateway"}, 502},
		{"HTTP 503", &testError{"TrustMed API error (HTTP 503): Service Unavailable"}, 503},
		{"Unknown error", &testError{"connection timeout"}, 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := client.GetStatusCodeFromError(tt.err)
			if code != tt.expected {
				t.Errorf("Expected status code %d, got %d", tt.expected, code)
			}
		})
	}
}

func TestNewTrustMedClient_InvalidCert(t *testing.T) {
	// Create test certificates directory
	tempDir := t.TempDir()

	// Try to create client with non-existent certificates
	cfg := &configs.Config{
		TrustMedEndpoint: "https://demo.partner.trust.med",
		TrustMedCertFile: tempDir + "/nonexistent-cert.crt",
		TrustMedKeyFile:  tempDir + "/nonexistent-key.key",
		TrustMedCAFile:   tempDir + "/nonexistent-ca.crt",
	}

	client, err := NewTrustMedClient(cfg)
	if err == nil {
		t.Fatal("Expected error with invalid certificates, got nil")
	}

	if client != nil {
		t.Errorf("Expected nil client on error, got %v", client)
	}
}

func TestNewTrustMedClient_InvalidCA(t *testing.T) {
	// This test is skipped because it requires actual certificate files
	// In real usage, invalid certificates will be caught by tls.LoadX509KeyPair
	t.Skip("Skipping certificate validation test - requires actual certificate files")
}

// Helper types for testing

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
