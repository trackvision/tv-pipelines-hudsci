package tasks

import (
	"context"
	"fmt"

	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// DispatchResult represents the result of a TrustMed dispatch attempt
type DispatchResult struct {
	ShippingOperationID string `json:"shipping_operation_id"`
	DispatchRecordID    string `json:"dispatch_record_id"`
	Status              string `json:"status"` // "sent", "retrying", "failed"
	TrustMedUUID        string `json:"trustmed_uuid,omitempty"`
	ErrorMessage        string `json:"error_message,omitempty"`
}

// DispatchViaTrustMed dispatches EPCIS XML documents to TrustMed Partner API via mTLS
func DispatchViaTrustMed(ctx context.Context, cms *DirectusClient, cfg *configs.Config, dispatchRecords []DispatchRecordWithFiles) ([]DispatchResult, error) {
	logger.Info("Dispatching to TrustMed", zap.Int("count", len(dispatchRecords)))

	if len(dispatchRecords) == 0 {
		return []DispatchResult{}, nil
	}

	// Initialize TrustMed client
	trustmedClient, err := NewTrustMedClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("initializing TrustMed client: %w", err)
	}

	results := make([]DispatchResult, 0, len(dispatchRecords))

	for i, record := range dispatchRecords {
		logger.Info("Dispatching shipment",
			zap.Int("index", i+1),
			zap.Int("total", len(dispatchRecords)),
			zap.String("shipping_operation_id", record.ShippingOperationID),
		)

		// Increment dispatch attempt count
		attemptCount, err := IncrementDispatchAttempt(ctx, cms, record.DispatchRecordID)
		if err != nil {
			logger.Error("Failed to increment dispatch attempt",
				zap.String("dispatch_record_id", record.DispatchRecordID),
				zap.Error(err),
			)
			results = append(results, DispatchResult{
				ShippingOperationID: record.ShippingOperationID,
				DispatchRecordID:    record.DispatchRecordID,
				Status:              "failed",
				ErrorMessage:        fmt.Sprintf("Failed to increment attempt: %v", err),
			})
			continue
		}

		logger.Info("Dispatch attempt",
			zap.String("dispatch_record_id", record.DispatchRecordID),
			zap.Int("attempt_count", attemptCount),
		)

		// Read enhanced XML from Directus
		xmlContent, err := cms.GetFileContent(ctx, record.EPCISXMLEnhancedFileID)
		if err != nil {
			logger.Error("Failed to read XML file",
				zap.String("file_id", record.EPCISXMLEnhancedFileID),
				zap.Error(err),
			)
			UpdateDispatchStatus(ctx, cms, record.DispatchRecordID, "Failed", UpdateDispatchStatusParams{
				ErrorMessage: fmt.Sprintf("Failed to read XML: %v", err),
			})
			results = append(results, DispatchResult{
				ShippingOperationID: record.ShippingOperationID,
				DispatchRecordID:    record.DispatchRecordID,
				Status:              "failed",
				ErrorMessage:        fmt.Sprintf("Failed to read XML: %v", err),
			})
			continue
		}

		// Submit to TrustMed
		xmlString := string(xmlContent)
		resp, err := trustmedClient.SubmitEPCIS(ctx, xmlString)
		if err != nil {
			// Extract HTTP status code
			httpStatus := trustmedClient.GetStatusCodeFromError(err)

			logger.Error("TrustMed dispatch failed",
				zap.String("shipping_operation_id", record.ShippingOperationID),
				zap.Int("http_status", httpStatus),
				zap.Error(err),
			)

			// Determine if should retry
			finalStatus := "retrying"
			if attemptCount >= cfg.DispatchMaxRetries {
				finalStatus = "failed"
				logger.Error("Max attempts reached",
					zap.String("shipping_operation_id", record.ShippingOperationID),
					zap.Int("attempts", attemptCount),
					zap.Int("max", cfg.DispatchMaxRetries),
				)
			}

			// Update dispatch record
			UpdateDispatchStatus(ctx, cms, record.DispatchRecordID, capitalizeStatus(finalStatus), UpdateDispatchStatusParams{
				ErrorMessage:   err.Error(),
				HTTPStatusCode: httpStatus,
			})

			results = append(results, DispatchResult{
				ShippingOperationID: record.ShippingOperationID,
				DispatchRecordID:    record.DispatchRecordID,
				Status:              finalStatus,
				ErrorMessage:        err.Error(),
			})
			continue
		}

		// Success
		logger.Info("TrustMed dispatch successful",
			zap.String("shipping_operation_id", record.ShippingOperationID),
			zap.String("trustmed_uuid", resp.ID),
		)

		// Update dispatch record
		UpdateDispatchStatus(ctx, cms, record.DispatchRecordID, "Acknowledged", UpdateDispatchStatusParams{
			TrustMedUUID:   resp.ID,
			HTTPStatusCode: 200,
		})

		results = append(results, DispatchResult{
			ShippingOperationID: record.ShippingOperationID,
			DispatchRecordID:    record.DispatchRecordID,
			Status:              "sent",
			TrustMedUUID:        resp.ID,
		})
	}

	// Summary stats
	sentCount := 0
	retryingCount := 0
	failedCount := 0
	for _, r := range results {
		switch r.Status {
		case "sent":
			sentCount++
		case "retrying":
			retryingCount++
		case "failed":
			failedCount++
		}
	}

	logger.Info("Dispatch summary",
		zap.Int("sent", sentCount),
		zap.Int("retrying", retryingCount),
		zap.Int("failed", failedCount),
	)

	return results, nil
}

// PollDispatchConfirmation polls TrustMed Dashboard API for delivery confirmation
func PollDispatchConfirmation(ctx context.Context, cms *DirectusClient, cfg *configs.Config, dispatchResults []DispatchResult) error {
	logger.Info("Polling dispatch confirmation", zap.Int("count", len(dispatchResults)))

	// Filter for successfully sent dispatches
	var sentResults []DispatchResult
	for _, r := range dispatchResults {
		if r.Status == "sent" && r.TrustMedUUID != "" {
			sentResults = append(sentResults, r)
		}
	}

	if len(sentResults) == 0 {
		logger.Info("No sent dispatches to check confirmation")
		return nil
	}

	logger.Info("Checking confirmation for sent dispatches", zap.Int("count", len(sentResults)))

	// Initialize TrustMed Dashboard client
	dashboardClient := NewTrustMedDashboardClient(cfg)

	// Also query for previously acknowledged dispatches that haven't been confirmed
	filter := map[string]interface{}{
		"_and": []interface{}{
			map[string]interface{}{
				"status": map[string]interface{}{"_eq": "Acknowledged"},
			},
			map[string]interface{}{
				"trustmed_uuid": map[string]interface{}{"_nnull": true},
			},
			map[string]interface{}{
				"trustmed_status": map[string]interface{}{"_null": true},
			},
		},
	}

	acknowledgedRecords, err := cms.QueryItems(ctx, "EPCIS_outbound", filter, []string{"id", "trustmed_uuid", "shipping_operation_id"}, 50)
	if err != nil {
		logger.Error("Failed to query acknowledged records", zap.Error(err))
	} else {
		logger.Info("Found previously acknowledged records to check", zap.Int("count", len(acknowledgedRecords)))
	}

	// Combine current results with previous records
	allToCheck := sentResults
	for _, rec := range acknowledgedRecords {
		uuid, _ := rec["trustmed_uuid"].(string)
		id, _ := rec["id"].(string)
		shipOpID, _ := rec["shipping_operation_id"].(string)
		if uuid != "" {
			allToCheck = append(allToCheck, DispatchResult{
				ShippingOperationID: shipOpID,
				DispatchRecordID:    id,
				TrustMedUUID:        uuid,
			})
		}
	}

	// Check status for each
	confirmedCount := 0
	pendingCount := 0
	failedCount := 0

	for _, result := range allToCheck {
		logger.Info("Checking status",
			zap.String("shipping_operation_id", result.ShippingOperationID),
			zap.String("trustmed_uuid", result.TrustMedUUID),
		)

		status, err := dashboardClient.PollDispatchConfirmation(ctx, result.TrustMedUUID)
		if err != nil {
			logger.Error("Failed to poll confirmation",
				zap.String("trustmed_uuid", result.TrustMedUUID),
				zap.Error(err),
			)
			continue
		}

		// Update dispatch record with confirmation status
		updates := map[string]interface{}{
			"trustmed_status":         status.Status,
			"trustmed_status_msg":     status.StatusMsg,
			"trustmed_status_updated": status.LastChecked.Format("2006-01-02T15:04:05Z07:00"),
		}
		if status.IsDelivered {
			updates["date_confirmed"] = status.LastChecked.Format("2006-01-02T15:04:05Z07:00")
		}

		err = cms.PatchItem(ctx, "EPCIS_outbound", result.DispatchRecordID, updates)
		if err != nil {
			logger.Error("Failed to update confirmation status",
				zap.String("dispatch_record_id", result.DispatchRecordID),
				zap.Error(err),
			)
		}

		if status.IsDelivered {
			confirmedCount++
		} else if status.IsPermanent {
			failedCount++
		} else {
			pendingCount++
		}

		logger.Info("Confirmation status",
			zap.String("shipping_operation_id", result.ShippingOperationID),
			zap.String("status", status.Status),
			zap.Bool("delivered", status.IsDelivered),
		)
	}

	logger.Info("Confirmation polling complete",
		zap.Int("confirmed", confirmedCount),
		zap.Int("pending", pendingCount),
		zap.Int("failed", failedCount),
	)

	return nil
}

// NotifyOnErrors logs and notifies on permanent dispatch failures
func NotifyOnErrors(ctx context.Context, cms *DirectusClient, cfg *configs.Config, dispatchResults []DispatchResult) error {
	logger.Info("Checking for permanent failures")

	// Find failed dispatches
	var failedResults []DispatchResult
	for _, r := range dispatchResults {
		if r.Status == "failed" {
			failedResults = append(failedResults, r)
		}
	}

	if len(failedResults) > 0 {
		logger.Warn("Found permanently failed dispatches", zap.Int("count", len(failedResults)))

		for _, result := range failedResults {
			logger.Error("PERMANENT FAILURE",
				zap.String("shipping_operation_id", result.ShippingOperationID),
				zap.String("dispatch_record_id", result.DispatchRecordID),
				zap.String("error", result.ErrorMessage),
			)

			// TODO: Send notification via email/Slack/webhook
			// For now, just log the error
		}
	}

	// Also query for all failed dispatches exceeding max attempts
	filter := map[string]interface{}{
		"_and": []interface{}{
			map[string]interface{}{
				"status": map[string]interface{}{"_eq": "Failed"},
			},
			map[string]interface{}{
				"dispatch_attempt_count": map[string]interface{}{"_gte": cfg.DispatchMaxRetries},
			},
		},
	}

	allFailedRecords, err := cms.QueryItems(ctx, "EPCIS_outbound", filter, []string{"id", "shipping_operation_id", "dispatch_attempt_count", "last_error_message"}, 100)
	if err != nil {
		logger.Error("Failed to query failed dispatches", zap.Error(err))
	} else if len(allFailedRecords) > 0 {
		logger.Warn("Total failed dispatches needing notification", zap.Int("count", len(allFailedRecords)))

		for _, rec := range allFailedRecords {
			id, _ := rec["id"].(string)
			shipOpID, _ := rec["shipping_operation_id"].(string)
			attempts, _ := rec["dispatch_attempt_count"].(float64)
			lastError, _ := rec["last_error_message"].(string)

			logger.Info("Failed dispatch",
				zap.String("dispatch_id", id),
				zap.String("shipping_operation_id", shipOpID),
				zap.Int("attempts", int(attempts)),
				zap.String("last_error", lastError),
			)
		}
	} else {
		logger.Info("No failed dispatches requiring notification")
	}

	logger.Info("Error notification check complete")
	return nil
}

// capitalizeStatus capitalizes the first letter of a status string
func capitalizeStatus(s string) string {
	if len(s) == 0 {
		return s
	}
	// Convert first character to uppercase (a-z -> A-Z)
	first := s[0]
	if first >= 'a' && first <= 'z' {
		first = first - 32
	}
	return string(first) + s[1:]
}
