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
	"github.com/trackvision/tv-pipelines-hudsci/configs"
)

func TestSearchAllFiles(t *testing.T) {
	// Create mock server
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(TokenResponse{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresIn:   600,
			})
			return
		}

		if r.URL.Path == "/de-status/company/37018/log/" {
			callCount++
			w.Header().Set("Content-Type", "application/json")

			// First page has data, second page is empty
			if callCount == 1 {
				resp := FileSearchResponse{
					Count: 2,
					Next:  strPtr("/de-status/company/37018/log/?page=2"),
					Results: []FileRecord{
						{
							LogGuid:     "uuid-1",
							StatusMsg:   "Acknowledged",
							StatusCode:  200,
							IsSender:    false, // Received file
							DateCreated: time.Now(),
						},
						{
							LogGuid:     "uuid-2",
							StatusMsg:   "Acknowledged",
							StatusCode:  200,
							IsSender:    true, // Sent file (should be filtered)
							DateCreated: time.Now(),
						},
					},
				}
				json.NewEncoder(w).Encode(resp)
			} else {
				// Second page - empty
				json.NewEncoder(w).Encode(FileSearchResponse{
					Count:   0,
					Next:    nil,
					Results: []FileRecord{},
				})
			}
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := &configs.Config{
		TrustMedDashboardURL: server.URL,
		TrustMedUsername:     "test-user",
		TrustMedPassword:     "test-pass",
		TrustMedClientID:     "37018",
		TrustMedCompanyID:    "37018",
	}

	client := NewTrustMedDashboardClient(cfg)

	ctx := context.Background()
	startDate := time.Now().Add(-7 * 24 * time.Hour)
	endDate := time.Now()

	// Test with receiver only filter
	records, err := client.SearchAllFiles(ctx, startDate, endDate, true)
	require.NoError(t, err)

	// Should only have 1 record (the received one, not the sent one)
	assert.Len(t, records, 1)
	assert.Equal(t, "uuid-1", records[0].LogGuid)
	assert.False(t, records[0].IsSender)
}

func TestGetDownloadURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(TokenResponse{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresIn:   600,
			})
			return
		}

		if r.URL.Path == "/de-status/log/test-uuid/download" {
			// Return quoted URL string
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(`"https://example.com/signed-download-url"`))
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := &configs.Config{
		TrustMedDashboardURL: server.URL,
		TrustMedUsername:     "test-user",
		TrustMedPassword:     "test-pass",
		TrustMedClientID:     "37018",
		TrustMedCompanyID:    "37018",
	}

	client := NewTrustMedDashboardClient(cfg)

	ctx := context.Background()
	url, err := client.GetDownloadURL(ctx, "test-uuid")
	require.NoError(t, err)

	assert.Equal(t, "https://example.com/signed-download-url", url)
}

func TestDownloadFile(t *testing.T) {
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<epcis:EPCISDocument xmlns:epcis="urn:epcglobal:epcis:xsd:2">
  <EPCISBody>
    <EventList>
      <ObjectEvent>
        <eventTime>2024-01-15T10:30:00Z</eventTime>
      </ObjectEvent>
    </EventList>
  </EPCISBody>
</epcis:EPCISDocument>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(TokenResponse{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresIn:   600,
			})
			return
		}

		if r.URL.Path == "/de-status/log/test-uuid/download" {
			// Return download URL pointing to our mock server (need full URL with scheme)
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(`"http://` + r.Host + `/download/file"`))
			return
		}

		if r.URL.Path == "/download/file" {
			w.Header().Set("Content-Type", "application/xml")
			w.Write([]byte(xmlContent))
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := &configs.Config{
		TrustMedDashboardURL: server.URL,
		TrustMedUsername:     "test-user",
		TrustMedPassword:     "test-pass",
		TrustMedClientID:     "37018",
		TrustMedCompanyID:    "37018",
	}

	client := NewTrustMedDashboardClient(cfg)

	ctx := context.Background()
	content, err := client.DownloadFile(ctx, "test-uuid")
	require.NoError(t, err)

	assert.Contains(t, string(content), "EPCISDocument")
	assert.Contains(t, string(content), "ObjectEvent")
}

func TestIsXMLFile(t *testing.T) {
	tests := []struct {
		name     string
		content  []byte
		expected bool
	}{
		{
			name:     "XML with declaration",
			content:  []byte(`<?xml version="1.0"?><root/>`),
			expected: true,
		},
		{
			name:     "XML without declaration",
			content:  []byte(`<root><child/></root>`),
			expected: true,
		},
		{
			name:     "XML with whitespace",
			content:  []byte(`  <?xml version="1.0"?><root/>`),
			expected: true,
		},
		{
			name:     "JSON content",
			content:  []byte(`{"key": "value"}`),
			expected: false,
		},
		{
			name:     "Plain text",
			content:  []byte(`Hello World`),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsXMLFile(tt.content)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func strPtr(s string) *string {
	return &s
}
