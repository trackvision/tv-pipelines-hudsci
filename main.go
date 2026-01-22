package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-pipelines-hudsci/pipelines"
	"github.com/trackvision/tv-pipelines-hudsci/pipelines/inbound"
	"github.com/trackvision/tv-pipelines-hudsci/pipelines/outbound"
	"github.com/trackvision/tv-pipelines-hudsci/tasks"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

//go:embed templates/*.html
var templatesFS embed.FS

// PipelineFunc is the signature all pipelines must implement
type PipelineFunc func(ctx context.Context, db *sqlx.DB, cms *tasks.DirectusClient, cfg *configs.Config, id string) error

// Register your pipelines here
var pipelineRegistry = map[string]PipelineFunc{
	"inbound":  inbound.Run,
	"outbound": outbound.Run,
}

// pipelineSteps maps pipeline names to their step names (for API discovery)
var pipelineSteps = map[string][]string{
	"inbound":  inbound.Steps,
	"outbound": outbound.Steps,
}

// API response types
type jobListResponse struct {
	Jobs []string `json:"jobs"`
}

type jobInfoResponse struct {
	Name     string   `json:"name"`
	Tasks    []string `json:"tasks"`
	Schedule string   `json:"schedule"`
}

type runRequest struct {
	ID        string   `json:"id"`
	SkipSteps []string `json:"skip_steps"`
}

type runResponse struct {
	Success  bool   `json:"success"`
	Pipeline string `json:"pipeline"`
	ID       string `json:"id"`
	Error    string `json:"error,omitempty"`
}

// authMiddleware checks for valid API key in Authorization header or X-API-Key header
func authMiddleware(apiKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// If no API key configured, skip auth
		if apiKey == "" {
			next(w, r)
			return
		}

		// Check Authorization: Bearer <key>
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			if token == apiKey {
				next(w, r)
				return
			}
		}

		// Check X-API-Key header
		if r.Header.Get("X-API-Key") == apiKey {
			next(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
	}
}

func main() {
	// Load configuration
	cfg, err := configs.Load()
	if err != nil {
		logger.Fatal("Failed to load configuration", zap.Error(err))
	}

	port := cfg.Port
	if port == "" {
		port = "8080"
	}

	// Parse templates
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		logger.Fatal("Failed to parse templates", zap.Error(err))
	}

	mux := http.NewServeMux()

	// Health check (no auth required)
	mux.HandleFunc("/health", healthHandler)

	// API endpoints (auth required)
	mux.HandleFunc("/jobs", authMiddleware(cfg.APIKey, jobsHandler))
	mux.HandleFunc("/jobs/", authMiddleware(cfg.APIKey, jobInfoHandler))
	mux.HandleFunc("/run/", authMiddleware(cfg.APIKey, makeRunHandler(cfg)))

	// UI endpoints (no auth - for browser access)
	mux.HandleFunc("/", redirectToUI)
	mux.HandleFunc("/ui/", makeUIIndexHandler(tmpl))
	mux.HandleFunc("/ui/jobs/", makeUIJobHandler(tmpl))

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	done := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("Server shutdown error", zap.Error(err))
		}
		close(done)
	}()

	logger.Info("Starting HudSci pipeline service",
		zap.String("port", port),
		zap.Strings("pipelines", getPipelineNames()),
		zap.Bool("auth_enabled", cfg.APIKey != ""))

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		logger.Fatal("Server failed", zap.Error(err))
	}
	<-done
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

// jobsHandler returns list of all pipeline names (GET /jobs)
func jobsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jobListResponse{Jobs: getPipelineNames()})
}

// jobInfoHandler returns pipeline details (GET /jobs/{name})
func jobInfoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/jobs/")
	if name == "" {
		http.Error(w, "pipeline name required", http.StatusBadRequest)
		return
	}

	steps, ok := pipelineSteps[name]
	if !ok {
		http.Error(w, "unknown pipeline: "+name, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jobInfoResponse{
		Name:     name,
		Tasks:    steps,
		Schedule: "@manual",
	})
}

func makeRunHandler(cfg *configs.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Extract pipeline name from URL: /run/{name}
		name := strings.TrimPrefix(r.URL.Path, "/run/")
		if name == "" {
			respondError(w, "pipeline name required", http.StatusBadRequest)
			return
		}

		pipelineFn, ok := pipelineRegistry[name]
		if !ok {
			respondError(w, "unknown pipeline: "+name, http.StatusNotFound)
			return
		}

		var req runRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			respondError(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.ID == "" {
			req.ID = fmt.Sprintf("ID-%s", time.Now().Format("020106150405"))
		}

		// Initialize clients
		cms := tasks.NewDirectusClient(cfg.CMSBaseURL, cfg.DirectusCMSAPIKey)

		// Initialize database connection (if needed for this pipeline)
		var db *sqlx.DB
		if cfg.DBHost != "" {
			// Build DSN for MySQL/TiDB
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
				cfg.DBUser,
				cfg.DBPassword,
				cfg.DBHost,
				cfg.DBPort,
				cfg.DBName,
			)
			if cfg.DBSSL {
				dsn += "&tls=skip-verify"
			}

			var err error
			db, err = sqlx.Open("mysql", dsn)
			if err != nil {
				logger.Error("Database connection failed", zap.Error(err))
				respondError(w, fmt.Sprintf("database connection failed: %v", err), http.StatusInternalServerError)
				return
			}
			defer db.Close()

			// Test connection
			if err := db.Ping(); err != nil {
				logger.Error("Database ping failed", zap.Error(err))
				respondError(w, fmt.Sprintf("database ping failed: %v", err), http.StatusInternalServerError)
				return
			}
		}

		// Build context with skip steps
		ctx := r.Context()
		if len(req.SkipSteps) > 0 {
			ctx = context.WithValue(ctx, pipelines.SkipStepsKey, req.SkipSteps)
		}

		// Run pipeline
		logger.Info("Starting pipeline execution",
			zap.String("pipeline", name),
			zap.String("id", req.ID),
			zap.Strings("skip_steps", req.SkipSteps))

		if err := pipelineFn(ctx, db, cms, cfg, req.ID); err != nil {
			logger.Error("Pipeline failed",
				zap.String("pipeline", name),
				zap.String("id", req.ID),
				zap.Error(err))
			respondError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		logger.Info("Pipeline completed",
			zap.String("pipeline", name),
			zap.String("id", req.ID))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(runResponse{Success: true, Pipeline: name, ID: req.ID})
	}
}

// redirectToUI redirects root to UI
func redirectToUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		http.Redirect(w, r, "/ui/", http.StatusFound)
		return
	}
	http.NotFound(w, r)
}

// makeUIIndexHandler returns UI index page showing all pipelines
func makeUIIndexHandler(tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = tmpl.ExecuteTemplate(w, "index.html", map[string]any{
			"Jobs": getPipelineNames(),
		})
	}
}

// makeUIJobHandler returns UI page for a specific pipeline
func makeUIJobHandler(tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/ui/jobs/")
		if name == "" {
			http.Error(w, "pipeline name required", http.StatusBadRequest)
			return
		}

		steps, ok := pipelineSteps[name]
		if !ok {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = tmpl.ExecuteTemplate(w, "job.html", map[string]any{
			"Name":  name,
			"Tasks": steps,
		})
	}
}

func respondError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(runResponse{Success: false, Error: msg})
}

func getPipelineNames() []string {
	names := make([]string, 0, len(pipelineRegistry))
	for name := range pipelineRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
