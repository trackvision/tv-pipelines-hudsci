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
	"github.com/trackvision/tv-pipelines-hudsci/types"
)

func TestPollXMLFiles(t *testing.T) {
	// Create mock Directus server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/items/global_config":
			// Return watermark
			watermark := Watermark{
				LastCheckTimestamp: WatermarkTime{Time: time.Now().Add(-1 * time.Hour)},
				TotalProcessed:     0,
			}
			watermarkJSON, _ := json.Marshal(watermark)
			config := GlobalConfigValue{
				ID:    "1",
				Key:   "inbound_shipment_received_watermark",
				Value: watermarkJSON,
			}
			resp := DirectusResponse{
				Data: mustMarshal([]GlobalConfigValue{config}),
			}
			json.NewEncoder(w).Encode(resp)

		case "/files":
			// Return file list
			files := []DirectusFile{
				{
					ID:         "file1",
					Filename:   "test.xml",
					UploadedOn: time.Now(),
				},
			}
			resp := DirectusResponse{
				Data: mustMarshal(files),
			}
			json.NewEncoder(w).Encode(resp)

		case "/assets/file1":
			// Return file content
			w.Write([]byte("<xml>test</xml>"))

		default:
			t.Logf("Unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cms := NewDirectusClient(server.URL, "test-token")
	cfg := &configs.Config{
		FolderInputXML:   "folder1",
		FailureThreshold: 0.5,
	}

	files, err := PollXMLFiles(context.Background(), cms, cfg)
	require.NoError(t, err)
	assert.Len(t, files, 1)
	assert.Equal(t, "file1", files[0].ID)
	assert.Equal(t, "test.xml", files[0].Filename)
	assert.Equal(t, []byte("<xml>test</xml>"), files[0].Content)
}

func TestDownloadFileContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/assets/test123", r.URL.Path)
		w.Write([]byte("file content here"))
	}))
	defer server.Close()

	cms := NewDirectusClient(server.URL, "test-token")

	content, err := DownloadFileContent(context.Background(), cms, "test123")
	require.NoError(t, err)
	assert.Equal(t, []byte("file content here"), content)
}

func TestUploadJSONFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/files", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		// Parse multipart form
		err := r.ParseMultipartForm(10 << 20)
		require.NoError(t, err)

		assert.Equal(t, "folder1", r.FormValue("folder"))

		// Return success
		resp := DirectusResponse{
			Data: mustMarshal(UploadFileResult{ID: "json123"}),
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cms := NewDirectusClient(server.URL, "test-token")
	cfg := &configs.Config{
		FolderInputJSON: "folder1",
	}

	files := []types.ConvertedFile{
		{
			SourceID: "xml1",
			Filename: "test.json",
			JSONData: []byte(`{"test": "data"}`),
		},
	}

	fileIDMap, err := UploadJSONFiles(context.Background(), cms, cfg, files)
	require.NoError(t, err)
	require.NotNil(t, fileIDMap)
	require.Equal(t, "json123", fileIDMap["xml1"])
}

func mustMarshal(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
