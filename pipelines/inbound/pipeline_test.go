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
	// Create mock Directus server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/items/global_config":
			// Return empty watermark
			watermarkValue := tasks.Watermark{
				LastCheckTimestamp: tasks.WatermarkTime{Time: time.Now().Add(-1 * time.Hour)},
				TotalProcessed:     0,
			}
			watermarkJSON, _ := json.Marshal(watermarkValue)
			config := tasks.GlobalConfigValue{
				ID:    "1",
				Key:   "inbound_shipment_received_watermark",
				Value: watermarkJSON,
			}
			resp := tasks.DirectusResponse{
				Data: mustMarshal([]tasks.GlobalConfigValue{config}),
			}
			json.NewEncoder(w).Encode(resp)

		case "/files":
			// Return empty file list (no files to process)
			resp := tasks.DirectusResponse{
				Data: mustMarshal([]tasks.DirectusFile{}),
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
		FailureThreshold: 0.5,
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
