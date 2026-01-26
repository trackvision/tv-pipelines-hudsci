package tasks

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"cloud.google.com/go/logging/logadmin"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/structpb"
)

// LogEntry represents a parsed log entry for display
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Severity  string    `json:"severity"`
	Pipeline  string    `json:"pipeline,omitempty"`
	Step      string    `json:"step,omitempty"`
	Message   string    `json:"message"`
	Error     string    `json:"error,omitempty"`
	Duration  float64   `json:"duration,omitempty"`
}

// PipelineRun represents a single pipeline execution with its steps
type PipelineRun struct {
	Pipeline  string       `json:"pipeline"`
	StartTime time.Time    `json:"start_time"`
	EndTime   time.Time    `json:"end_time,omitempty"`
	Duration  float64      `json:"duration,omitempty"`
	Success   bool         `json:"success"`
	Steps     []StepResult `json:"steps"`
	Error     string       `json:"error,omitempty"`
	LogsURL   string       `json:"logs_url,omitempty"`
}

// StepResult represents a single step execution
type StepResult struct {
	Name     string  `json:"name"`
	Duration float64 `json:"duration,omitempty"`
	Status   string  `json:"status"` // "completed", "failed", "running"
	Error    string  `json:"error,omitempty"`
}

// LogQuery defines parameters for querying logs
type LogQuery struct {
	ProjectID   string
	ServiceName string
	Pipeline    string        // optional filter by pipeline name
	Severity    string        // optional filter (INFO, WARNING, ERROR)
	Since       time.Duration // how far back to look (default: 1 hour)
	Limit       int           // max entries to return (default: 100)
}

// LogClient wraps the GCP logadmin client
type LogClient struct {
	client      *logadmin.Client
	projectID   string
	serviceName string
}

// NewLogClient creates a new GCP logging client
func NewLogClient(ctx context.Context, projectID, serviceName string) (*LogClient, error) {
	client, err := logadmin.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to create logadmin client: %w", err)
	}
	return &LogClient{
		client:      client,
		projectID:   projectID,
		serviceName: serviceName,
	}, nil
}

// Close closes the logging client
func (c *LogClient) Close() error {
	return c.client.Close()
}

// QueryLogs queries Cloud Run logs and returns parsed entries
func (c *LogClient) QueryLogs(ctx context.Context, q LogQuery) ([]LogEntry, error) {
	// Set defaults
	if q.Since == 0 {
		q.Since = time.Hour
	}
	if q.Limit == 0 {
		q.Limit = 100
	}

	// Build filter - only include application logs with pipeline field
	filter := fmt.Sprintf(
		`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND timestamp>="%s" AND jsonPayload.pipeline!=""`,
		c.serviceName,
		time.Now().Add(-q.Since).Format(time.RFC3339),
	)

	// Add severity filter if specified
	if q.Severity != "" {
		filter += fmt.Sprintf(` AND severity>="%s"`, q.Severity)
	}

	// Add pipeline filter if specified
	if q.Pipeline != "" {
		filter += fmt.Sprintf(` AND jsonPayload.pipeline="%s"`, q.Pipeline)
	}

	// Query logs with newest first ordering
	iter := c.client.Entries(ctx,
		logadmin.Filter(filter),
		logadmin.NewestFirst(),
	)

	var entries []LogEntry
	for len(entries) < q.Limit {
		entry, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to iterate logs: %w", err)
		}

		logEntry := LogEntry{
			Timestamp: entry.Timestamp,
			Severity:  entry.Severity.String(),
		}

		// Parse payload based on type
		switch p := entry.Payload.(type) {
		case *structpb.Struct:
			// JSON payload from Cloud Logging API
			fields := p.GetFields()
			if msg := fields["msg"]; msg != nil {
				logEntry.Message = msg.GetStringValue()
			}
			if pipeline := fields["pipeline"]; pipeline != nil {
				logEntry.Pipeline = pipeline.GetStringValue()
			}
			if step := fields["step"]; step != nil {
				logEntry.Step = step.GetStringValue()
			}
			if errVal := fields["error"]; errVal != nil {
				logEntry.Error = errVal.GetStringValue()
			}
			if duration := fields["duration"]; duration != nil {
				logEntry.Duration = duration.GetNumberValue()
			}
		case map[string]interface{}:
			// Fallback for map type
			if msg, ok := p["msg"].(string); ok {
				logEntry.Message = msg
			}
			if pipeline, ok := p["pipeline"].(string); ok {
				logEntry.Pipeline = pipeline
			}
			if step, ok := p["step"].(string); ok {
				logEntry.Step = step
			}
			if errMsg, ok := p["error"].(string); ok {
				logEntry.Error = errMsg
			}
			if duration, ok := p["duration"].(float64); ok {
				logEntry.Duration = duration
			}
		case string:
			logEntry.Message = p
		}

		// Skip empty messages
		if logEntry.Message == "" {
			continue
		}

		entries = append(entries, logEntry)
	}

	return entries, nil
}

// buildLogsURL creates a GCP Cloud Logging console URL for a pipeline run
// Shows all logs for the service, positioned at the pipeline start time
func buildLogsURL(projectID, serviceName string, startTime time.Time) string {
	// Build a simple filter for the service (no pipeline filter - show all logs)
	query := fmt.Sprintf(`resource.type="cloud_run_revision"
resource.labels.service_name="%s"`, serviceName)

	// URL encode the query
	encodedQuery := url.QueryEscape(query)

	// Use cursorTimestamp to position the log viewer at the pipeline start time
	cursorTime := startTime.Format(time.RFC3339Nano)

	return fmt.Sprintf("https://console.cloud.google.com/logs/query;query=%s;cursorTimestamp=%s?project=%s",
		encodedQuery, url.QueryEscape(cursorTime), projectID)
}

// GroupByRun groups log entries into pipeline runs
func GroupByRun(entries []LogEntry, projectID, serviceName string) []PipelineRun {
	if len(entries) == 0 {
		return nil
	}

	// Sort entries by timestamp (oldest first for processing)
	sorted := make([]LogEntry, len(entries))
	copy(sorted, entries)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i].Timestamp.After(sorted[j].Timestamp) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	runMap := make(map[string]*PipelineRun) // key: pipeline + start time bucket

	for _, entry := range sorted {
		if entry.Pipeline == "" {
			continue
		}

		// Find or create run based on "pipeline started" message
		if entry.Message == "pipeline started" || entry.Message == "flow started" {
			run := &PipelineRun{
				Pipeline:  entry.Pipeline,
				StartTime: entry.Timestamp,
				Success:   true, // assume success until we see failure
				Steps:     []StepResult{},
			}
			key := fmt.Sprintf("%s-%d", entry.Pipeline, entry.Timestamp.Unix())
			runMap[key] = run
			continue
		}

		// Find the most recent run for this pipeline
		var currentRun *PipelineRun
		for _, r := range runMap {
			if r.Pipeline == entry.Pipeline && entry.Timestamp.After(r.StartTime) {
				if currentRun == nil || r.StartTime.After(currentRun.StartTime) {
					currentRun = r
				}
			}
		}

		if currentRun == nil {
			// Create implicit run if we see steps without a start
			currentRun = &PipelineRun{
				Pipeline:  entry.Pipeline,
				StartTime: entry.Timestamp,
				Success:   true,
				Steps:     []StepResult{},
			}
			key := fmt.Sprintf("%s-%d", entry.Pipeline, entry.Timestamp.Unix())
			runMap[key] = currentRun
		}

		// Process step messages
		if entry.Message == "step completed" && entry.Step != "" {
			currentRun.Steps = append(currentRun.Steps, StepResult{
				Name:     entry.Step,
				Duration: entry.Duration,
				Status:   "completed",
			})
		} else if entry.Message == "step failed" && entry.Step != "" {
			currentRun.Steps = append(currentRun.Steps, StepResult{
				Name:   entry.Step,
				Status: "failed",
				Error:  entry.Error,
			})
			currentRun.Success = false
			currentRun.Error = entry.Error
		} else if entry.Message == "flow completed" {
			currentRun.Duration = entry.Duration
			currentRun.EndTime = entry.Timestamp
		} else if entry.Message == "pipeline complete" {
			currentRun.EndTime = entry.Timestamp
		} else if entry.Message == "Pipeline failed" {
			currentRun.Success = false
			currentRun.Error = entry.Error
			currentRun.EndTime = entry.Timestamp
		}
	}

	// Build result slice from map and add logs URLs
	result := make([]PipelineRun, 0, len(runMap))
	for _, run := range runMap {
		run.LogsURL = buildLogsURL(projectID, serviceName, run.StartTime)
		result = append(result, *run)
	}

	// Sort by start time descending (newest first)
	for i := 0; i < len(result)-1; i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].StartTime.Before(result[j].StartTime) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result
}
