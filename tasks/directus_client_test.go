package tasks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPostItem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Expected Bearer token")
		}

		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{
			"data": map[string]any{
				"id":   "123",
				"name": "test",
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewDirectusClient(server.URL, "test-key")
	result, err := client.PostItem(context.Background(), "test_collection", map[string]string{"name": "test"})

	if err != nil {
		t.Fatalf("PostItem() error = %v", err)
	}
	if result["id"] != "123" {
		t.Errorf("Expected id=123, got %v", result["id"])
	}
}

func TestPatchItem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" {
			t.Errorf("Expected PATCH, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": {}}`))
	}))
	defer server.Close()

	client := NewDirectusClient(server.URL, "test-key")
	err := client.PatchItem(context.Background(), "test_collection", "123", map[string]any{"status": "published"})

	if err != nil {
		t.Fatalf("PatchItem() error = %v", err)
	}
}

func TestUploadFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST, got %s", r.Method)
		}

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("ParseMultipartForm error: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{
			"data": map[string]any{
				"id": "file-123",
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewDirectusClient(server.URL, "test-key")
	result, err := client.UploadFile(context.Background(), UploadFileParams{
		Filename: "test.txt",
		Content:  []byte("test content"),
		FolderID: "folder-123",
	})

	if err != nil {
		t.Fatalf("UploadFile() error = %v", err)
	}
	if result.ID != "file-123" {
		t.Errorf("Expected file-123, got %v", result.ID)
	}
}
