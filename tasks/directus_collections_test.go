package tasks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetWatermark(t *testing.T) {
	watermarkValue := Watermark{
		LastCheckTimestamp: WatermarkTime{Time: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)},
		TotalProcessed:     42,
	}
	watermarkJSON, _ := json.Marshal(watermarkValue)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/items/global_config", r.URL.Path)

		config := GlobalConfigValue{
			ID:    "1",
			Key:   "test_watermark",
			Value: watermarkJSON,
		}

		resp := DirectusResponse{
			Data: mustMarshal([]GlobalConfigValue{config}),
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cms := NewDirectusClient(server.URL, "test-token")

	watermark, err := GetWatermark(context.Background(), cms, "test_watermark")
	require.NoError(t, err)
	assert.Equal(t, 42, watermark.TotalProcessed)
	assert.Equal(t, time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), watermark.LastCheckTimestamp.Time)
}

func TestGetWatermark_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := DirectusResponse{
			Data: mustMarshal([]GlobalConfigValue{}),
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cms := NewDirectusClient(server.URL, "test-token")

	watermark, err := GetWatermark(context.Background(), cms, "nonexistent")
	require.NoError(t, err)
	assert.True(t, watermark.LastCheckTimestamp.Time.IsZero())
	assert.Equal(t, 0, watermark.TotalProcessed)
}

func TestUpdateWatermark(t *testing.T) {
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/items/global_config":
			if r.Method == "GET" {
				// Return existing watermark
				watermarkValue := Watermark{
					LastCheckTimestamp: WatermarkTime{Time: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)},
					TotalProcessed:     10,
				}
				watermarkJSON, _ := json.Marshal(watermarkValue)
				config := GlobalConfigValue{
					ID:    "1",
					Key:   "test_watermark",
					Value: watermarkJSON,
				}
				resp := DirectusResponse{
					Data: mustMarshal([]GlobalConfigValue{config}),
				}
				json.NewEncoder(w).Encode(resp)
			}

		case "/items/global_config/test_watermark":
			if r.Method == "PATCH" {
				requestCount++
				// Verify update
				var body map[string]json.RawMessage
				json.NewDecoder(r.Body).Decode(&body)
				assert.Contains(t, body, "value")

				w.WriteHeader(http.StatusOK)
			}

		default:
			t.Logf("Unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cms := NewDirectusClient(server.URL, "test-token")

	timestamp := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	err := UpdateWatermark(context.Background(), cms, "test_watermark", timestamp, 5)
	require.NoError(t, err)
	assert.Equal(t, 1, requestCount, "Should have made one PATCH request")
}

func TestInsertEPCISInbox(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/items/epcis_inbox":
			if r.Method == "GET" {
				// Return empty list (no existing records)
				resp := DirectusResponse{
					Data: mustMarshal([]map[string]interface{}{}),
				}
				json.NewEncoder(w).Encode(resp)
			} else if r.Method == "POST" {
				// Accept the insert
				resp := DirectusResponse{
					Data: mustMarshal(map[string]interface{}{"id": "123"}),
				}
				json.NewEncoder(w).Encode(resp)
			}

		default:
			t.Logf("Unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cms := NewDirectusClient(server.URL, "test-token")

	shipments := []EPCISInboxItem{
		{
			Status:         "pending",
			Seller:         "Seller A",
			Buyer:          "Buyer B",
			ShipDate:       "2024-01-15",
			CaptureMessage: map[string]interface{}{"file_id": "xml123"},
			Products: []map[string]interface{}{
				{"GTIN": "12345", "quantity": 10},
			},
		},
	}

	err := InsertEPCISInbox(context.Background(), cms, shipments)
	require.NoError(t, err)
}

func TestInsertEPCISInbox_SkipsDuplicates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/items/epcis_inbox" {
			// Return existing record
			existing := []map[string]interface{}{
				{
					"capture_message": map[string]interface{}{"file_id": "xml123"},
				},
			}
			resp := DirectusResponse{
				Data: mustMarshal(existing),
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	cms := NewDirectusClient(server.URL, "test-token")

	shipments := []EPCISInboxItem{
		{
			Status:         "pending",
			CaptureMessage: map[string]interface{}{"file_id": "xml123"}, // Duplicate
		},
	}

	err := InsertEPCISInbox(context.Background(), cms, shipments)
	require.NoError(t, err) // Should succeed but skip the duplicate
}
