package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trackvision/tv-pipelines-hudsci/configs"
)

func TestCapitalizeStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "lowercase",
			input:    "failed",
			expected: "Failed",
		},
		{
			name:     "already capitalized",
			input:    "Failed",
			expected: "Failed",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := capitalizeStatus(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDispatchResultStructure(t *testing.T) {
	result := DispatchResult{
		ShippingOperationID: "ship-123",
		DispatchRecordID:    "disp-456",
		Status:              "sent",
		TrustMedUUID:        "uuid-789",
	}

	assert.Equal(t, "ship-123", result.ShippingOperationID)
	assert.Equal(t, "sent", result.Status)
	assert.Equal(t, "uuid-789", result.TrustMedUUID)
	assert.Empty(t, result.ErrorMessage)
}

func TestPollDispatchConfirmation_NumericIDs(t *testing.T) {
	// Track PATCH calls to verify:
	// 1. Flat payload (no "data" wrapper)
	// 2. Correct numeric IDs in URL path
	var patchCalls []struct {
		Path    string
		Payload map[string]interface{}
	}

	// Mock Directus server
	directusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/items/EPCIS_outbound"):
			// QueryItems: return records with numeric IDs (float64 in JSON)
			resp := map[string]interface{}{
				"data": []interface{}{
					map[string]interface{}{
						"id":                    float64(240003), // numeric ID
						"trustmed_uuid":         "uuid-new-record",
						"shipping_operation_id": "ship-003",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

		case r.Method == "PATCH" && strings.Contains(r.URL.Path, "/items/EPCIS_outbound/"):
			// PatchItem: capture the call
			body, _ := io.ReadAll(r.Body)
			var payload map[string]interface{}
			json.Unmarshal(body, &payload)
			patchCalls = append(patchCalls, struct {
				Path    string
				Payload map[string]interface{}
			}{Path: r.URL.Path, Payload: payload})
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{}})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer directusServer.Close()

	// Mock TrustMed Dashboard server
	trustmedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			json.NewEncoder(w).Encode(TokenResponse{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			})
		case strings.Contains(r.URL.Path, "/de-status/company/"):
			// Return file records matching our UUIDs
			resp := FileSearchResponse{
				Count: 2,
				Results: []FileRecord{
					{LogGuid: "lg-1", SourceFile: "uuid-001/api-xml/2026-02-13.xml", StatusMsg: "In Progress", StatusCode: 2},
					{LogGuid: "lg-3", SourceFile: "uuid-new-record/api-xml/2026-02-13.xml", StatusMsg: "In Progress", StatusCode: 2},
				},
			}
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer trustmedServer.Close()

	cms := NewDirectusClient(directusServer.URL, "test-key")
	cfg := &configs.Config{
		TrustMedDashboardURL: trustmedServer.URL,
		TrustMedUsername:     "test",
		TrustMedPassword:     "test",
		TrustMedClientID:     "12345",
		TrustMedCompanyID:    "12345",
	}

	// sentResults has one record with a string ID
	sentResults := []DispatchResult{
		{ShippingOperationID: "ship-001", DispatchRecordID: "240001", TrustMedUUID: "uuid-001", Status: "sent"},
	}

	err := PollDispatchConfirmation(context.Background(), cms, cfg, sentResults)
	require.NoError(t, err)

	// Should have 2 PATCH calls: one for sentResults (240001), one for acknowledgedRecords (240003)
	require.Len(t, patchCalls, 2, "expected 2 PATCH calls")

	// Verify first PATCH: sentResults record with string ID
	assert.Contains(t, patchCalls[0].Path, "/items/EPCIS_outbound/240001")
	assert.Contains(t, patchCalls[0].Payload, "trustmed_status", "payload should have flat trustmed_status field")
	assert.NotContains(t, patchCalls[0].Payload, "data", "payload should NOT have data wrapper")

	// Verify second PATCH: acknowledgedRecords record with numeric ID correctly formatted
	assert.Contains(t, patchCalls[1].Path, "/items/EPCIS_outbound/240003")
	assert.Contains(t, patchCalls[1].Payload, "trustmed_status", "payload should have flat trustmed_status field")
	assert.NotContains(t, patchCalls[1].Payload, "data", "payload should NOT have data wrapper")
}

func TestPollDispatchConfirmation_Dedup(t *testing.T) {
	// Verify that records appearing in both sentResults and acknowledgedRecords
	// are only checked once (not duplicated)
	var patchPaths []string

	// Mock Directus server
	directusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/items/EPCIS_outbound"):
			// Return a record whose ID overlaps with sentResults
			resp := map[string]interface{}{
				"data": []interface{}{
					map[string]interface{}{
						"id":                    float64(240001), // same as sentResults
						"trustmed_uuid":         "uuid-001",
						"shipping_operation_id": "ship-001",
					},
					map[string]interface{}{
						"id":                    float64(240002), // same as sentResults
						"trustmed_uuid":         "uuid-002",
						"shipping_operation_id": "ship-002",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

		case r.Method == "PATCH":
			patchPaths = append(patchPaths, r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{}})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer directusServer.Close()

	// Mock TrustMed Dashboard
	trustmedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			json.NewEncoder(w).Encode(TokenResponse{AccessToken: "test-token", ExpiresIn: 3600})
		case strings.Contains(r.URL.Path, "/de-status/company/"):
			resp := FileSearchResponse{
				Count: 2,
				Results: []FileRecord{
					{LogGuid: "lg-1", SourceFile: "uuid-001/api-xml/2026-02-13.xml", StatusMsg: "In Progress", StatusCode: 2},
					{LogGuid: "lg-2", SourceFile: "uuid-002/api-xml/2026-02-13.xml", StatusMsg: "In Progress", StatusCode: 2},
				},
			}
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer trustmedServer.Close()

	cms := NewDirectusClient(directusServer.URL, "test-key")
	cfg := &configs.Config{
		TrustMedDashboardURL: trustmedServer.URL,
		TrustMedUsername:     "test",
		TrustMedPassword:     "test",
		TrustMedClientID:     "12345",
		TrustMedCompanyID:    "12345",
	}

	// sentResults already has both IDs
	sentResults := []DispatchResult{
		{ShippingOperationID: "ship-001", DispatchRecordID: "240001", TrustMedUUID: "uuid-001", Status: "sent"},
		{ShippingOperationID: "ship-002", DispatchRecordID: "240002", TrustMedUUID: "uuid-002", Status: "sent"},
	}

	err := PollDispatchConfirmation(context.Background(), cms, cfg, sentResults)
	require.NoError(t, err)

	// Should only have 2 PATCH calls (not 4), because acknowledgedRecords duplicates are skipped
	require.Len(t, patchPaths, 2, "expected 2 PATCH calls, not 4 — dedup should prevent duplicates")

	// Verify both IDs were patched exactly once
	patchIDs := make(map[string]int)
	for _, path := range patchPaths {
		for _, id := range []string{"240001", "240002"} {
			if strings.Contains(path, fmt.Sprintf("/items/EPCIS_outbound/%s", id)) {
				patchIDs[id]++
			}
		}
	}
	assert.Equal(t, 1, patchIDs["240001"], "240001 should be patched exactly once")
	assert.Equal(t, 1, patchIDs["240002"], "240002 should be patched exactly once")
}
