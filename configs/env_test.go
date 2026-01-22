package configs

import (
	"os"
	"testing"
)

func TestLoad(t *testing.T) {
	// Set required env vars
	os.Setenv("CMS_BASE_URL", "http://test.example.com")
	os.Setenv("DIRECTUS_CMS_API_KEY", "test-key")
	defer func() {
		os.Unsetenv("CMS_BASE_URL")
		os.Unsetenv("DIRECTUS_CMS_API_KEY")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.CMSBaseURL != "http://test.example.com" {
		t.Errorf("CMSBaseURL = %v, want %v", cfg.CMSBaseURL, "http://test.example.com")
	}

	if cfg.DirectusCMSAPIKey != "test-key" {
		t.Errorf("DirectusCMSAPIKey = %v, want %v", cfg.DirectusCMSAPIKey, "test-key")
	}

	// Check defaults
	if cfg.Port != "8080" {
		t.Errorf("Port = %v, want %v", cfg.Port, "8080")
	}

	if cfg.DispatchBatchSize != 10 {
		t.Errorf("DispatchBatchSize = %v, want %v", cfg.DispatchBatchSize, 10)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	os.Unsetenv("CMS_BASE_URL")
	os.Unsetenv("DIRECTUS_CMS_API_KEY")

	_, err := Load()
	if err == nil {
		t.Error("Load() expected error for missing required fields")
	}
}

func TestGetEnvInt(t *testing.T) {
	os.Setenv("TEST_INT", "42")
	defer os.Unsetenv("TEST_INT")

	val := getEnvInt("TEST_INT", 10)
	if val != 42 {
		t.Errorf("getEnvInt() = %v, want %v", val, 42)
	}

	val = getEnvInt("MISSING_INT", 10)
	if val != 10 {
		t.Errorf("getEnvInt() default = %v, want %v", val, 10)
	}
}

func TestGetEnvBool(t *testing.T) {
	os.Setenv("TEST_BOOL", "true")
	defer os.Unsetenv("TEST_BOOL")

	val := getEnvBool("TEST_BOOL", false)
	if val != true {
		t.Errorf("getEnvBool() = %v, want %v", val, true)
	}

	val = getEnvBool("MISSING_BOOL", false)
	if val != false {
		t.Errorf("getEnvBool() default = %v, want %v", val, false)
	}
}
