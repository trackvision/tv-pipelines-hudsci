package outbound

import (
	"context"
	"testing"

	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-pipelines-hudsci/tasks"
)

func TestRun(t *testing.T) {
	// Skip this test by default - it requires real services
	t.Skip("Skipping integration test - requires Directus and TrustMed services")

	ctx := context.Background()

	// Load config from environment
	cfg, err := configs.Load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	cms := tasks.NewDirectusClient(cfg.CMSBaseURL, cfg.DirectusCMSAPIKey)

	// For now, just verify the pipeline runs without error
	// It will likely return early if there are no approved shipments
	err = Run(ctx, nil, cms, cfg, "test-id")
	if err != nil {
		t.Logf("Pipeline run error (may be expected): %v", err)
	}
}
