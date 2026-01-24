package configs

import (
	"fmt"
	"os"
	"strconv"

	"github.com/trackvision/tv-shared-go/env"
)

// Config holds all configuration for HudSci pipelines
type Config struct {
	// Server
	Port   string
	APIKey string // API key for authenticating requests

	// Directus CMS
	CMSBaseURL        string
	DirectusCMSAPIKey string

	// TiDB Database
	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string
	DBSSL      bool

	// EPCIS Converter Service
	EPCISConverterURL string

	// TrustMed Dashboard API
	TrustMedDashboardURL string
	TrustMedUsername     string
	TrustMedPassword     string
	TrustMedClientID     string
	TrustMedCompanyID    string

	// TrustMed Partner API (mTLS)
	TrustMedEndpoint string
	TrustMedCertFile string
	TrustMedKeyFile  string
	TrustMedCAFile   string

	// Directus Folder IDs
	FolderInputXML   string
	FolderInputJSON  string
	FolderOutputXML  string
	FolderOutputJSON string

	// Pipeline Settings
	DispatchBatchSize  int
	DispatchMaxRetries int
	FailureThreshold   float64

	// Default GLNs for SBDH fallback
	DefaultSenderGLN   string
	DefaultReceiverGLN string

	// GCP Configuration (for logs viewer)
	GCPProjectID    string
	CloudRunService string
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	// Load secrets using env.GetSecret (tries mounted file first, then env var)
	apiKey, err := env.GetSecret("DIRECTUS_CMS_API_KEY")
	if err != nil {
		return nil, fmt.Errorf("DIRECTUS_CMS_API_KEY: %w", err)
	}

	// TrustMed passwords (try demo first, fall back to prod if not found)
	trustmedPassword, _ := env.GetSecret("TRUSTMED_PASSWORD_DEMO")
	if trustmedPassword == "" {
		trustmedPassword, _ = env.GetSecret("TRUSTMED_PASSWORD_PROD")
	}

	// Database password (optional for local dev)
	dbPassword, _ := env.GetSecret("DB_PASSWORD")

	// Determine if we're running in production (use prod certs)
	useProdCerts := os.Getenv("USE_PROD_CERTS") == "true"

	// Select TrustMed config based on environment
	trustmedEndpoint := getEnv("TRUSTMED_ENDPOINT", "https://demo.partner.trust.med/v1/client/storage")
	trustmedCertFile := getEnv("TRUSTMED_CERTFILE", "certs/trustmed/client-cert.crt")
	trustmedKeyFile := getEnv("TRUSTMED_KEYFILE", "certs/trustmed/client-key.key")
	trustmedCAFile := getEnv("TRUSTMED_CAFILE", "certs/trustmed/trustmed-ca.crt")

	if useProdCerts {
		if prodEndpoint := os.Getenv("TRUSTMED_ENDPOINT_PROD"); prodEndpoint != "" {
			trustmedEndpoint = prodEndpoint
		}
		if prodCert := os.Getenv("TRUSTMED_CERTFILE_PROD"); prodCert != "" {
			trustmedCertFile = prodCert
		}
		if prodKey := os.Getenv("TRUSTMED_KEYFILE_PROD"); prodKey != "" {
			trustmedKeyFile = prodKey
		}
		if prodCA := os.Getenv("TRUSTMED_CAFILE_PROD"); prodCA != "" {
			trustmedCAFile = prodCA
		}
	}

	// API key for auth (optional - if not set, auth is disabled)
	cmsAPIKey, _ := env.GetSecret("CMS_API_KEY")

	cfg := &Config{
		// Server
		Port:   getEnv("PORT", "8080"),
		APIKey: cmsAPIKey,

		// Directus
		CMSBaseURL:        os.Getenv("CMS_BASE_URL"),
		DirectusCMSAPIKey: apiKey,

		// Database
		DBHost:     getEnv("DB_HOST", "127.0.0.1"),
		DBPort:     getEnv("DB_PORT", "4000"),
		DBName:     getEnv("DB_NAME", "huds_local"),
		DBUser:     getEnv("DB_USER", "root"),
		DBPassword: dbPassword,
		DBSSL:      getEnvBool("DB_SSL", false),

		// EPCIS Converter
		EPCISConverterURL: getEnv("EPCIS_CONVERTER_URL", "http://localhost:8075"),

		// TrustMed Dashboard
		TrustMedDashboardURL: getEnv("TRUSTMED_DASHBOARD_URL", "https://demo.dashboard.trust.med/api/v1.0"),
		TrustMedUsername:     os.Getenv("TRUSTMED_USERNAME"),
		TrustMedPassword:     trustmedPassword,
		TrustMedClientID:     getEnv("TRUSTMED_CLIENT_ID", "37018"),
		TrustMedCompanyID:    getEnv("TRUSTMED_COMPANY_ID", "37018"),

		// TrustMed Partner API
		TrustMedEndpoint: trustmedEndpoint,
		TrustMedCertFile: trustmedCertFile,
		TrustMedKeyFile:  trustmedKeyFile,
		TrustMedCAFile:   trustmedCAFile,

		// Directus Folders
		FolderInputXML:   os.Getenv("DIRECTUS_FOLDER_INPUT_XML"),
		FolderInputJSON:  os.Getenv("DIRECTUS_FOLDER_INPUT_JSON"),
		FolderOutputXML:  os.Getenv("DIRECTUS_FOLDER_OUTPUT_XML"),
		FolderOutputJSON: os.Getenv("DIRECTUS_FOLDER_OUTPUT_JSON"),

		// Pipeline Settings
		DispatchBatchSize:  getEnvInt("DISPATCH_BATCH_SIZE", 10),
		DispatchMaxRetries: getEnvInt("DISPATCH_MAX_RETRIES", 3),
		FailureThreshold:   getEnvFloat("FAILURE_THRESHOLD", 0.5),

		// Default GLNs (fallback if not in events)
		DefaultSenderGLN:   getEnv("DEFAULT_SENDER_GLN", "1234567.89012"), // 7+5 format (company prefix + location ref)
		DefaultReceiverGLN: getEnv("DEFAULT_RECEIVER_GLN", "9876543.21098"),

		// GCP Configuration
		GCPProjectID:    os.Getenv("GCP_PROJECT_ID"),
		CloudRunService: os.Getenv("CLOUD_RUN_SERVICE"),
	}

	// Validate required fields
	if cfg.CMSBaseURL == "" {
		return nil, fmt.Errorf("CMS_BASE_URL is required")
	}

	return cfg, nil
}

// getEnv gets an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt gets an integer environment variable or returns a default value
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

// getEnvFloat gets a float environment variable or returns a default value
func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
			return floatVal
		}
	}
	return defaultValue
}

// getEnvBool gets a boolean environment variable or returns a default value
func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolVal, err := strconv.ParseBool(value); err == nil {
			return boolVal
		}
	}
	return defaultValue
}
