package tasks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-pipelines-hudsci/types"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// TrustMedWatermark tracks the last poll timestamp for TrustMed inbound
type TrustMedWatermark struct {
	LastCheckTimestamp time.Time `json:"last_check_timestamp"`
	TotalProcessed     int       `json:"total_processed"`
	LastLogUUID        string    `json:"last_log_uuid,omitempty"`
}

// PollTrustMedFiles polls the TrustMed Dashboard API for received XML files.
// It downloads files that were sent TO us (inbound shipments) and archives them to Directus.
func PollTrustMedFiles(ctx context.Context, dashboard *TrustMedDashboardClient, cms *DirectusClient, cfg *configs.Config) ([]types.XMLFile, error) {
	logger.Info("Polling TrustMed Dashboard for received files")

	// Get watermark from Directus
	watermarkKey := "trustmed_inbound_watermark"
	watermark, err := GetWatermark(ctx, cms, watermarkKey)
	if err != nil {
		logger.Warn("Failed to get TrustMed watermark, using default", zap.Error(err))
	}

	// Determine start date from watermark
	var startDate time.Time
	if watermark == nil || watermark.LastCheckTimestamp.Time.IsZero() {
		// No watermark - first run, go back 7 days
		startDate = time.Now().Add(-7 * 24 * time.Hour)
		logger.Info("No TrustMed watermark found, using default lookback",
			zap.Time("since", startDate),
		)
	} else {
		startDate = watermark.LastCheckTimestamp.Time
		logger.Info("Using TrustMed watermark",
			zap.Time("since", startDate),
			zap.Int("previous_total", watermark.TotalProcessed),
		)
	}

	endDate := time.Now()

	// Search for all received files (is_sender=false means WE received it)
	records, err := dashboard.SearchAllFiles(ctx, startDate, endDate, true)
	if err != nil {
		// Don't update watermark on API error - files might be missed
		logger.Error("Failed to search TrustMed files", zap.Error(err))
		return nil, fmt.Errorf("searching TrustMed files: %w", err)
	}

	if len(records) == 0 {
		logger.Info("No new files found in TrustMed")
		// Update watermark even if no files (to advance the timestamp)
		if err := UpdateWatermark(ctx, cms, watermarkKey, endDate, 0); err != nil {
			logger.Warn("Failed to update watermark", zap.Error(err))
		}
		return []types.XMLFile{}, nil
	}

	logger.Info("Found received files in TrustMed", zap.Int("count", len(records)))

	// Download each file
	var xmlFiles []types.XMLFile
	var lastLogUUID string
	failedCount := 0

	for i, record := range records {
		logger.Info("Downloading file from TrustMed",
			zap.Int("index", i+1),
			zap.Int("total", len(records)),
			zap.String("log_uuid", record.LogGuid),
		)

		content, err := dashboard.DownloadFile(ctx, record.LogGuid)
		if err != nil {
			logger.Error("Failed to download file from TrustMed",
				zap.String("log_uuid", record.LogGuid),
				zap.Error(err),
			)
			failedCount++
			continue
		}

		// Generate filename
		filename := fmt.Sprintf("trustmed_%s.xml", record.LogGuid)

		// Upload to Directus INPUT_XML folder
		if cfg.FolderInputXML != "" {
			uploadParams := UploadFileParams{
				Filename:    filename,
				Content:     content,
				FolderID:    cfg.FolderInputXML,
				Title:       fmt.Sprintf("TrustMed Inbound - %s", record.LogGuid),
				ContentType: "application/xml",
			}

			result, err := cms.UploadFile(ctx, uploadParams)
			if err != nil {
				logger.Error("Failed to upload file to Directus",
					zap.String("log_uuid", record.LogGuid),
					zap.Error(err),
				)
				failedCount++
				continue
			}

			xmlFiles = append(xmlFiles, types.XMLFile{
				ID:       result.ID,
				Filename: filename,
				Content:  content,
				Uploaded: time.Now(),
			})

			logger.Info("Archived TrustMed file to Directus",
				zap.String("log_uuid", record.LogGuid),
				zap.String("directus_file_id", result.ID),
			)
		} else {
			// No folder configured - just return the content without archiving
			xmlFiles = append(xmlFiles, types.XMLFile{
				ID:       record.LogGuid,
				Filename: filename,
				Content:  content,
				Uploaded: time.Now(),
			})
		}

		lastLogUUID = record.LogGuid
	}

	// Check failure threshold
	if len(records) > 0 {
		failureRate := float64(failedCount) / float64(len(records))
		if failureRate > cfg.FailureThreshold {
			return nil, fmt.Errorf("failure rate %.0f%% exceeds threshold %.0f%%",
				failureRate*100, cfg.FailureThreshold*100)
		}
	}

	// Update watermark with current time and count
	if err := UpdateWatermark(ctx, cms, watermarkKey, endDate, len(xmlFiles)); err != nil {
		logger.Warn("Failed to update TrustMed watermark", zap.Error(err))
	}

	logger.Info("Successfully polled TrustMed files",
		zap.Int("downloaded", len(xmlFiles)),
		zap.Int("failed", failedCount),
		zap.String("last_log_uuid", lastLogUUID),
	)

	// Suppress unused variable warning
	_ = lastLogUUID

	return xmlFiles, nil
}

// FileRecord extension with additional fields for inbound
func (r *FileRecord) SenderGLN() string {
	// Parse sender GLN from the record if available
	// This may need adjustment based on actual API response structure
	return ""
}

// IsXMLFile checks if the file appears to be XML based on content
func IsXMLFile(content []byte) bool {
	contentStr := strings.TrimSpace(string(content))
	return strings.HasPrefix(contentStr, "<?xml") || strings.HasPrefix(contentStr, "<")
}
