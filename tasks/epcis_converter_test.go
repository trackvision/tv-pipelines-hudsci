package tasks

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-pipelines-hudsci/types"
)

func TestConvertXMLToJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/convert/json/2.0", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/xml", r.Header.Get("Content-Type"))
		assert.Equal(t, "Always_EPC_URN", r.Header.Get("GS1-EPC-Format"))

		// Read and verify input
		body, _ := io.ReadAll(r.Body)
		assert.Contains(t, string(body), "<xml>")

		// Return JSON
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"@context": ["https://gs1.org/voc/"], "type": "EPCISDocument"}`))
	}))
	defer server.Close()

	cfg := &configs.Config{
		EPCISConverterURL: server.URL,
		FailureThreshold:  0.5,
	}

	xmlFiles := []types.XMLFile{
		{
			ID:       "xml1",
			Filename: "test.xml",
			Content:  []byte("<xml>test</xml>"),
		},
	}

	convertedFiles, err := ConvertXMLToJSON(context.Background(), cfg, xmlFiles)
	require.NoError(t, err)
	assert.Len(t, convertedFiles, 1)
	assert.Equal(t, "xml1", convertedFiles[0].SourceID)
	assert.Equal(t, "test.json", convertedFiles[0].Filename)
	assert.Contains(t, string(convertedFiles[0].JSONData), "EPCISDocument")
}

func TestConvertJSONToXML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/convert/xml/1.2", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "Always_EPC_URN", r.Header.Get("GS1-EPC-Format"))

		// Read and verify input
		body, _ := io.ReadAll(r.Body)
		assert.Contains(t, string(body), "EPCISDocument")

		// Return XML
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0"?><EPCISDocument></EPCISDocument>`))
	}))
	defer server.Close()

	cfg := &configs.Config{
		EPCISConverterURL: server.URL,
	}

	jsonContent := []byte(`{"type": "EPCISDocument"}`)

	xmlData, err := ConvertJSONToXML(context.Background(), cfg, jsonContent)
	require.NoError(t, err)
	assert.Contains(t, string(xmlData), "EPCISDocument")
	assert.Contains(t, string(xmlData), "<?xml")
}

func TestEPCISConverterHealthCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/health", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewEPCISConverterClient(server.URL)

	err := client.HealthCheck(context.Background())
	require.NoError(t, err)
}

func TestConvertXMLToJSON_FailureThreshold(t *testing.T) {
	// Mock server that always fails
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Conversion error"))
	}))
	defer server.Close()

	cfg := &configs.Config{
		EPCISConverterURL: server.URL,
		FailureThreshold:  0.5, // 50% threshold
	}

	xmlFiles := []types.XMLFile{
		{ID: "xml1", Filename: "test1.xml", Content: []byte("<xml>test1</xml>")},
		{ID: "xml2", Filename: "test2.xml", Content: []byte("<xml>test2</xml>")},
	}

	// All files fail, should exceed threshold
	_, err := ConvertXMLToJSON(context.Background(), cfg, xmlFiles)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failure rate")
	assert.Contains(t, err.Error(), "exceeds threshold")
}
