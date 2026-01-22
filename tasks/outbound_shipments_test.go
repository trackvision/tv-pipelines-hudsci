package tasks

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/trackvision/tv-pipelines-hudsci/configs"
)

func TestShouldRetry(t *testing.T) {
	tests := []struct {
		name        string
		attemptCount int
		maxAttempts int
		expected    bool
	}{
		{
			name:        "first attempt",
			attemptCount: 0,
			maxAttempts: 3,
			expected:    true,
		},
		{
			name:        "second attempt",
			attemptCount: 1,
			maxAttempts: 3,
			expected:    true,
		},
		{
			name:        "at max attempts",
			attemptCount: 3,
			maxAttempts: 3,
			expected:    false,
		},
		{
			name:        "exceeded max attempts",
			attemptCount: 4,
			maxAttempts: 3,
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldRetry(tt.attemptCount, tt.maxAttempts)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPollApprovedShipments(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	cfg, err := configs.Load()
	if err != nil {
		t.Skip("Could not load config - skipping integration test")
	}
	cms := NewDirectusClient(cfg.CMSBaseURL, cfg.DirectusCMSAPIKey)

	// This is an integration test - it will query real Directus
	// In a real test environment, you would mock the DirectusClient
	shipments, err := PollApprovedShipments(ctx, cms, cfg)
	assert.NoError(t, err)
	assert.NotNil(t, shipments)

	// Should return a list (may be empty)
	t.Logf("Found %d approved shipments", len(shipments))
}
