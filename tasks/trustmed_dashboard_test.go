package tasks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTrustMedDashboardClient_GetToken(t *testing.T) {
	// Create mock server for token endpoint
	tokenCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			t.Errorf("Expected /token path, got %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}

		tokenCalled = true

		response := TokenResponse{
			AccessToken: "test-token-123",
			TokenType:   "Bearer",
			ExpiresIn:   600,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := &TrustMedDashboardClient{
		dashboardURL: server.URL,
		username:     "testuser",
		password:     "testpass",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}

	ctx := context.Background()
	token, err := client.getToken(ctx)
	if err != nil {
		t.Fatalf("getToken failed: %v", err)
	}

	if token != "test-token-123" {
		t.Errorf("Expected token 'test-token-123', got '%s'", token)
	}

	if !tokenCalled {
		t.Error("Token endpoint was not called")
	}

	// Second call should use cached token
	tokenCalled = false
	token2, err := client.getToken(ctx)
	if err != nil {
		t.Fatalf("getToken (cached) failed: %v", err)
	}

	if token2 != token {
		t.Errorf("Expected cached token '%s', got '%s'", token, token2)
	}

	if tokenCalled {
		t.Error("Token endpoint should not be called for cached token")
	}
}

func TestTrustMedDashboardClient_SearchFiles(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			response := TokenResponse{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresIn:   600,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/de-status/company/") {
			// Verify authorization header
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				t.Errorf("Expected Bearer token, got '%s'", auth)
			}

			// Return mock search results
			response := FileSearchResponse{
				Count: 2,
				Next:  nil,
				Results: []FileRecord{
					{
						LogGuid:    "log-123",
						StatusMsg:  "Acknowledged",
						StatusCode: 200,
						IsSender:   true,
					},
					{
						LogGuid:    "log-456",
						StatusMsg:  "Processing",
						StatusCode: 200,
						IsSender:   true,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client := &TrustMedDashboardClient{
		dashboardURL: server.URL,
		username:     "testuser",
		password:     "testpass",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}

	ctx := context.Background()
	startDate := time.Now().Add(-24 * time.Hour)
	endDate := time.Now()

	results, err := client.SearchFiles(ctx, startDate, endDate, 1)
	if err != nil {
		t.Fatalf("SearchFiles failed: %v", err)
	}

	if results.Count != 2 {
		t.Errorf("Expected count 2, got %d", results.Count)
	}

	if len(results.Results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results.Results))
	}

	if results.Results[0].LogGuid != "log-123" {
		t.Errorf("Expected first result 'log-123', got '%s'", results.Results[0].LogGuid)
	}
}

func TestTrustMedDashboardClient_GetFileStatus(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			response := TokenResponse{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresIn:   600,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/de-status/company/") {
			response := FileSearchResponse{
				Count: 1,
				Next:  nil,
				Results: []FileRecord{
					{
						LogGuid:    "target-log-uuid",
						StatusMsg:  "Acknowledged",
						StatusCode: 200,
						IsSender:   true,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client := &TrustMedDashboardClient{
		dashboardURL: server.URL,
		username:     "testuser",
		password:     "testpass",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}

	ctx := context.Background()

	record, err := client.GetFileStatus(ctx, "target-log-uuid")
	if err != nil {
		t.Fatalf("GetFileStatus failed: %v", err)
	}

	if record.LogGuid != "target-log-uuid" {
		t.Errorf("Expected log UUID 'target-log-uuid', got '%s'", record.LogGuid)
	}

	if record.StatusMsg != "Acknowledged" {
		t.Errorf("Expected status 'Acknowledged', got '%s'", record.StatusMsg)
	}
}

func TestTrustMedDashboardClient_GetFileStatus_NotFound(t *testing.T) {
	// Create mock server that returns empty results
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			response := TokenResponse{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresIn:   600,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/de-status/company/") {
			response := FileSearchResponse{
				Count:   0,
				Next:    nil,
				Results: []FileRecord{},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client := &TrustMedDashboardClient{
		dashboardURL: server.URL,
		username:     "testuser",
		password:     "testpass",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}

	ctx := context.Background()

	record, err := client.GetFileStatus(ctx, "nonexistent-uuid")
	if err == nil {
		t.Fatal("Expected error for nonexistent file, got nil")
	}

	if record != nil {
		t.Errorf("Expected nil record, got %v", record)
	}

	if !strings.Contains(err.Error(), "file not found") {
		t.Errorf("Expected 'file not found' error, got: %v", err)
	}
}

func TestTrustMedDashboardClient_PollDispatchConfirmation(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			response := TokenResponse{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresIn:   600,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/de-status/company/") {
			response := FileSearchResponse{
				Count: 1,
				Next:  nil,
				Results: []FileRecord{
					{
						LogGuid:    "test-log-uuid",
						StatusMsg:  "Acknowledged",
						StatusCode: 200,
						IsSender:   true,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client := &TrustMedDashboardClient{
		dashboardURL: server.URL,
		username:     "testuser",
		password:     "testpass",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}

	ctx := context.Background()

	status, err := client.PollDispatchConfirmation(ctx, "test-log-uuid")
	if err != nil {
		t.Fatalf("PollDispatchConfirmation failed: %v", err)
	}

	if status.Status != "Acknowledged" {
		t.Errorf("Expected status 'Acknowledged', got '%s'", status.Status)
	}

	if !status.IsDelivered {
		t.Error("Expected IsDelivered to be true")
	}

	if !status.IsPermanent {
		t.Error("Expected IsPermanent to be true")
	}
}

func TestMapTrustMedStatus(t *testing.T) {
	tests := []struct {
		name          string
		statusCode    int
		statusMsg     string
		expectedState string
		isDelivered   bool
		isPermanent   bool
	}{
		{
			name:          "Acknowledged",
			statusCode:    200,
			statusMsg:     "Acknowledged",
			expectedState: "Acknowledged",
			isDelivered:   true,
			isPermanent:   true,
		},
		{
			name:          "Processing",
			statusCode:    200,
			statusMsg:     "Processing",
			expectedState: "Processing",
			isDelivered:   false,
			isPermanent:   false,
		},
		{
			name:          "4xx error (permanent failure)",
			statusCode:    400,
			statusMsg:     "Bad Request",
			expectedState: "Failed",
			isDelivered:   false,
			isPermanent:   true,
		},
		{
			name:          "5xx error (temporary failure)",
			statusCode:    500,
			statusMsg:     "Internal Server Error",
			expectedState: "Retrying",
			isDelivered:   false,
			isPermanent:   false,
		},
		{
			name:          "Unknown status",
			statusCode:    300,
			statusMsg:     "Redirect",
			expectedState: "Processing",
			isDelivered:   false,
			isPermanent:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := mapTrustMedStatus(tt.statusCode, tt.statusMsg)

			if status.Status != tt.expectedState {
				t.Errorf("Expected status '%s', got '%s'", tt.expectedState, status.Status)
			}

			if status.IsDelivered != tt.isDelivered {
				t.Errorf("Expected IsDelivered %v, got %v", tt.isDelivered, status.IsDelivered)
			}

			if status.IsPermanent != tt.isPermanent {
				t.Errorf("Expected IsPermanent %v, got %v", tt.isPermanent, status.IsPermanent)
			}

			if status.StatusCode != tt.statusCode {
				t.Errorf("Expected StatusCode %d, got %d", tt.statusCode, status.StatusCode)
			}

			if status.StatusMsg != tt.statusMsg {
				t.Errorf("Expected StatusMsg '%s', got '%s'", tt.statusMsg, status.StatusMsg)
			}
		})
	}
}
