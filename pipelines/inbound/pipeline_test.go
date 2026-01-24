package inbound

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-pipelines-hudsci/tasks"
)

func TestRun(t *testing.T) {
	// Create mock server for both TrustMed Dashboard and Directus
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		// TrustMed Dashboard endpoints
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(tasks.TokenResponse{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresIn:   600,
			})

		case "/de-status/company/37018/log/":
			// Return empty file list (no files to process)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(tasks.FileSearchResponse{
				Count:   0,
				Next:    nil,
				Results: []tasks.FileRecord{},
			})

		// Directus endpoints
		case "/items/global_config":
			// Return watermark
			watermarkValue := tasks.Watermark{
				LastCheckTimestamp: tasks.WatermarkTime{Time: time.Now().Add(-1 * time.Hour)},
				TotalProcessed:     0,
			}
			watermarkJSON, _ := json.Marshal(watermarkValue)
			config := tasks.GlobalConfigValue{
				ID:    "1",
				Key:   "trustmed_inbound_watermark",
				Value: watermarkJSON,
			}
			resp := tasks.DirectusResponse{
				Data: mustMarshal([]tasks.GlobalConfigValue{config}),
			}
			json.NewEncoder(w).Encode(resp)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	cms := tasks.NewDirectusClient(server.URL, "test-key")
	cfg := &configs.Config{
		TrustMedDashboardURL: server.URL,
		TrustMedUsername:     "test-user",
		TrustMedPassword:     "test-pass",
		TrustMedClientID:     "37018",
		TrustMedCompanyID:    "37018",
		CMSBaseURL:           server.URL,
		FailureThreshold:     0.5,
	}

	// Run pipeline with empty file list
	err := Run(ctx, nil, cms, cfg, "test-id")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func mustMarshal(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
