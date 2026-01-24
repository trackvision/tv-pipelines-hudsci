package tasks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGroupByRun_EmptyInput(t *testing.T) {
	result := GroupByRun(nil, "test-project", "test-service")
	assert.Nil(t, result)

	result = GroupByRun([]LogEntry{}, "test-project", "test-service")
	assert.Nil(t, result)
}

func TestGroupByRun_SingleRun(t *testing.T) {
	now := time.Now()
	entries := []LogEntry{
		{
			Timestamp: now,
			Pipeline:  "inbound",
			Message:   "flow started",
		},
		{
			Timestamp: now.Add(1 * time.Second),
			Pipeline:  "inbound",
			Step:      "poll_xml_files",
			Message:   "step completed",
			Duration:  0.5,
		},
		{
			Timestamp: now.Add(2 * time.Second),
			Pipeline:  "inbound",
			Step:      "convert_xml",
			Message:   "step completed",
			Duration:  1.2,
		},
		{
			Timestamp: now.Add(3 * time.Second),
			Pipeline:  "inbound",
			Message:   "flow completed",
			Duration:  3.0,
		},
	}

	runs := GroupByRun(entries, "test-project", "test-service")
	assert.Len(t, runs, 1)

	run := runs[0]
	assert.Equal(t, "inbound", run.Pipeline)
	assert.True(t, run.Success)
	assert.Equal(t, 3.0, run.Duration)
	assert.Len(t, run.Steps, 2)
	assert.Equal(t, "poll_xml_files", run.Steps[0].Name)
	assert.Equal(t, "completed", run.Steps[0].Status)
	assert.Equal(t, 0.5, run.Steps[0].Duration)
	assert.NotEmpty(t, run.LogsURL)
}

func TestGroupByRun_FailedRun(t *testing.T) {
	now := time.Now()
	entries := []LogEntry{
		{
			Timestamp: now,
			Pipeline:  "outbound",
			Message:   "flow started",
		},
		{
			Timestamp: now.Add(1 * time.Second),
			Pipeline:  "outbound",
			Step:      "fetch_shipments",
			Message:   "step completed",
			Duration:  0.8,
		},
		{
			Timestamp: now.Add(2 * time.Second),
			Pipeline:  "outbound",
			Step:      "dispatch_epcis",
			Message:   "step failed",
			Error:     "connection timeout",
		},
	}

	runs := GroupByRun(entries, "test-project", "test-service")
	assert.Len(t, runs, 1)

	run := runs[0]
	assert.Equal(t, "outbound", run.Pipeline)
	assert.False(t, run.Success)
	assert.Equal(t, "connection timeout", run.Error)
	assert.Len(t, run.Steps, 2)
	assert.Equal(t, "completed", run.Steps[0].Status)
	assert.Equal(t, "failed", run.Steps[1].Status)
}

func TestGroupByRun_MultipleRuns(t *testing.T) {
	now := time.Now()
	entries := []LogEntry{
		// First run
		{
			Timestamp: now,
			Pipeline:  "inbound",
			Message:   "flow started",
		},
		{
			Timestamp: now.Add(1 * time.Second),
			Pipeline:  "inbound",
			Message:   "flow completed",
			Duration:  1.0,
		},
		// Second run (later)
		{
			Timestamp: now.Add(10 * time.Second),
			Pipeline:  "inbound",
			Message:   "flow started",
		},
		{
			Timestamp: now.Add(11 * time.Second),
			Pipeline:  "inbound",
			Message:   "flow completed",
			Duration:  1.0,
		},
	}

	runs := GroupByRun(entries, "test-project", "test-service")
	assert.Len(t, runs, 2)

	// Runs should be sorted newest first
	assert.True(t, runs[0].StartTime.After(runs[1].StartTime))
}

func TestGroupByRun_ImplicitRun(t *testing.T) {
	// Test when we see steps without an explicit "flow started" message
	now := time.Now()
	entries := []LogEntry{
		{
			Timestamp: now,
			Pipeline:  "inbound",
			Step:      "poll_xml_files",
			Message:   "step completed",
			Duration:  0.5,
		},
	}

	runs := GroupByRun(entries, "test-project", "test-service")
	assert.Len(t, runs, 1)
	assert.Equal(t, "inbound", runs[0].Pipeline)
	assert.Len(t, runs[0].Steps, 1)
}

func TestBuildLogsURL(t *testing.T) {
	now := time.Now()
	url := buildLogsURL("test-project", "test-service", now)

	assert.Contains(t, url, "console.cloud.google.com/logs/query")
	assert.Contains(t, url, "project=test-project")
	assert.Contains(t, url, "test-service")
	assert.Contains(t, url, "cursorTimestamp=")
}
