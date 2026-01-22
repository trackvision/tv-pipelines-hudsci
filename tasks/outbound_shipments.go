package tasks

import (
	"context"
	"fmt"

	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// ApprovedShipment represents a shipment ready for dispatch
type ApprovedShipment struct {
	ShippingOperationID string  `json:"shipping_operation_id"`
	CaptureID           string  `json:"capture_id"`
	Status              string  `json:"status"`
	DispatchRecordID    *string `json:"dispatch_record_id,omitempty"`
	DispatchStatus      *string `json:"dispatch_status,omitempty"`
	DispatchAttemptCount int    `json:"dispatch_attempt_count"`
}

// DispatchRecord represents an EPCIS_outbound record
type DispatchRecord struct {
	ID                   string  `json:"id"`
	ShippingOperationID  string  `json:"shipping_operation_id"`
	Status               string  `json:"status"`
	DispatchAttemptCount int     `json:"dispatch_attempt_count"`
	TargetGLN            *string `json:"target_gln,omitempty"`
	TrustMedUUID         *string `json:"trustmed_uuid,omitempty"`
}

// PollApprovedShipments queries Directus for approved shipping operations ready for dispatch.
// Returns shipments that:
// - Have status='approved'
// - Are not already successfully dispatched (not Acknowledged/Sent)
// - Include failed records eligible for retry (attempt count < max)
func PollApprovedShipments(ctx context.Context, cms *DirectusClient, cfg *configs.Config) ([]ApprovedShipment, error) {
	logger.Info("Polling approved shipments for outbound dispatch")

	batchSize := cfg.DispatchBatchSize
	maxAttempts := cfg.DispatchMaxRetries

	// Query approved shipments
	filter := map[string]interface{}{
		"status": map[string]interface{}{
			"_eq": "approved",
		},
	}

	approvedShipments, err := cms.QueryItems(ctx, "shipping_scanning_operation", filter, []string{"id", "capture_id", "status"}, batchSize*2)
	if err != nil {
		return nil, fmt.Errorf("querying approved shipments: %w", err)
	}

	logger.Info("Found approved shipments", zap.Int("count", len(approvedShipments)))

	if len(approvedShipments) == 0 {
		return []ApprovedShipment{}, nil
	}

	// Extract shipping operation IDs
	shipOpIDs := make([]string, len(approvedShipments))
	for i, s := range approvedShipments {
		shipOpIDs[i] = s["id"].(string)
	}

	// Query dispatch records for these shipments
	dispatchFilter := map[string]interface{}{
		"shipping_operation_id": map[string]interface{}{
			"_in": shipOpIDs,
		},
	}

	dispatchRecords, err := cms.QueryItems(ctx, "EPCIS_outbound", dispatchFilter, []string{"id", "shipping_operation_id", "status", "dispatch_attempt_count"}, len(shipOpIDs))
	if err != nil {
		return nil, fmt.Errorf("querying dispatch records: %w", err)
	}

	logger.Info("Found dispatch records", zap.Int("count", len(dispatchRecords)))

	// Build lookup map
	dispatchLookup := make(map[string]DispatchRecord)
	for _, record := range dispatchRecords {
		shipOpID, _ := record["shipping_operation_id"].(string)
		if shipOpID == "" {
			continue
		}

		// Handle both string and numeric IDs
		var recordID string
		switch v := record["id"].(type) {
		case string:
			recordID = v
		case float64:
			recordID = fmt.Sprintf("%.0f", v)
		}

		status, _ := record["status"].(string)

		dispatchRec := DispatchRecord{
			ID:                   recordID,
			ShippingOperationID:  shipOpID,
			Status:               status,
			DispatchAttemptCount: 0,
		}
		if count, ok := record["dispatch_attempt_count"].(float64); ok {
			dispatchRec.DispatchAttemptCount = int(count)
		}
		dispatchLookup[shipOpID] = dispatchRec
	}

	// Filter shipments and build results
	var results []ApprovedShipment
	skippedAcknowledged := 0
	skippedSent := 0
	skippedMaxRetries := 0

	for _, shipment := range approvedShipments {
		shipOpID, ok := shipment["id"].(string)
		if !ok || shipOpID == "" {
			logger.Warn("Skipping shipment with missing ID")
			continue
		}
		captureID, _ := shipment["capture_id"].(string)
		if captureID == "" {
			logger.Warn("Skipping shipment with missing capture_id", zap.String("id", shipOpID))
			continue
		}

		dispatchRecord, hasDispatch := dispatchLookup[shipOpID]

		shouldDispatch := false
		var dispatchRecordID *string
		var dispatchStatus *string
		dispatchAttemptCount := 0

		if !hasDispatch {
			// No dispatch record yet - needs first dispatch attempt
			shouldDispatch = true
		} else {
			// Has dispatch record - check status
			dispatchRecordID = &dispatchRecord.ID
			dispatchStatus = &dispatchRecord.Status
			dispatchAttemptCount = dispatchRecord.DispatchAttemptCount

			switch dispatchRecord.Status {
			case "Acknowledged", "Sent":
				// Already successfully dispatched
				if dispatchRecord.Status == "Acknowledged" {
					skippedAcknowledged++
				} else {
					skippedSent++
				}
				shouldDispatch = false

			case "Failed", "Retrying":
				// Check retry eligibility
				if dispatchAttemptCount < maxAttempts {
					shouldDispatch = true
				} else {
					skippedMaxRetries++
					shouldDispatch = false
				}

			default:
				// Pending/Processing - resume interrupted attempts
				shouldDispatch = true
			}
		}

		if shouldDispatch {
			results = append(results, ApprovedShipment{
				ShippingOperationID:  shipOpID,
				CaptureID:            captureID,
				Status:               shipment["status"].(string),
				DispatchRecordID:     dispatchRecordID,
				DispatchStatus:       dispatchStatus,
				DispatchAttemptCount: dispatchAttemptCount,
			})
		}

		// Stop if we have enough
		if len(results) >= batchSize {
			break
		}
	}

	// Log summary
	if len(results) == 0 {
		logger.Info("No shipments to process",
			zap.Int("skipped_acknowledged", skippedAcknowledged),
			zap.Int("skipped_sent", skippedSent),
			zap.Int("skipped_max_retries", skippedMaxRetries),
		)
	} else {
		logger.Info("Dispatching shipments",
			zap.Int("count", len(results)),
			zap.Int("batch_size", batchSize),
			zap.Int("skipped_acknowledged", skippedAcknowledged),
			zap.Int("skipped_sent", skippedSent),
			zap.Int("skipped_max_retries", skippedMaxRetries),
		)
	}

	return results, nil
}

// ShouldRetry checks if a dispatch should be retried based on attempt count
func ShouldRetry(attemptCount int, maxAttempts int) bool {
	return attemptCount < maxAttempts
}

// GetDispatchRecord retrieves an existing dispatch record for a shipping operation
func GetDispatchRecord(ctx context.Context, cms *DirectusClient, shippingOpID string) (*DispatchRecord, error) {
	filter := map[string]interface{}{
		"shipping_operation_id": map[string]interface{}{
			"_eq": shippingOpID,
		},
	}

	records, err := cms.QueryItems(ctx, "EPCIS_outbound", filter, []string{"id", "shipping_operation_id", "status", "dispatch_attempt_count", "target_gln", "trustmed_uuid"}, 1)
	if err != nil {
		return nil, fmt.Errorf("querying dispatch record: %w", err)
	}

	if len(records) == 0 {
		return nil, nil
	}

	record := records[0]
	dispatchRec := &DispatchRecord{
		ID:                   record["id"].(string),
		ShippingOperationID:  record["shipping_operation_id"].(string),
		Status:               record["status"].(string),
		DispatchAttemptCount: 0,
	}

	if count, ok := record["dispatch_attempt_count"].(float64); ok {
		dispatchRec.DispatchAttemptCount = int(count)
	}
	if gln, ok := record["target_gln"].(string); ok {
		dispatchRec.TargetGLN = &gln
	}
	if uuid, ok := record["trustmed_uuid"].(string); ok {
		dispatchRec.TrustMedUUID = &uuid
	}

	return dispatchRec, nil
}
