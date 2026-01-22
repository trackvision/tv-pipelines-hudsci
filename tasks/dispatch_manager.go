package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// DispatchRecordWithFiles represents a dispatch record with uploaded file IDs
type DispatchRecordWithFiles struct {
	ShippingOperationID    string  `json:"shipping_operation_id"`
	CaptureID              string  `json:"capture_id"`
	DispatchRecordID       string  `json:"dispatch_record_id"`
	TargetGLN              string  `json:"target_gln"`
	EPCISJSONFileID        string  `json:"epcis_json_file_id"`
	EPCISXMLFileID         string  `json:"epcis_xml_file_id"`
	EPCISXMLEnhancedFileID string  `json:"epcis_xml_enhanced_file_id"` // For dispatch
}

// ManageDispatchRecords handles all EPCIS_outbound write operations:
// - Creates dispatch record if not exists
// - Uploads EPCIS JSON and XML files to Directus
// - Updates dispatch record with file IDs and status
func ManageDispatchRecords(ctx context.Context, cms *DirectusClient, cfg *configs.Config, documents []EnhancedDocument) ([]DispatchRecordWithFiles, error) {
	logger.Info("Managing dispatch records", zap.Int("count", len(documents)))

	if len(documents) == 0 {
		return []DispatchRecordWithFiles{}, nil
	}

	results := make([]DispatchRecordWithFiles, 0, len(documents))
	failedCount := 0

	for i, doc := range documents {
		logger.Info("Managing dispatch record",
			zap.Int("index", i+1),
			zap.Int("total", len(documents)),
			zap.String("shipping_operation_id", doc.ShippingOperationID),
		)

		var dispatchRecordID string
		if doc.DispatchRecordID != nil && *doc.DispatchRecordID != "" {
			dispatchRecordID = *doc.DispatchRecordID
			logger.Info("Using existing dispatch record", zap.String("dispatch_record_id", dispatchRecordID))
		} else {
			// Create new dispatch record
			id, err := CreateDispatchRecord(ctx, cms, doc.ShippingOperationID, doc.TargetGLN)
			if err != nil {
				logger.Error("Failed to create dispatch record",
					zap.String("shipping_operation_id", doc.ShippingOperationID),
					zap.Error(err),
				)
				failedCount++
				continue
			}
			dispatchRecordID = id
			logger.Info("Created dispatch record", zap.String("dispatch_record_id", dispatchRecordID))
		}

		// Upload EPCIS JSON file
		var jsonFileID string
		if len(doc.EPCISJSONContent) > 0 {
			jsonFilename := fmt.Sprintf("%s.json", doc.CaptureID)
			result, err := cms.UploadFile(ctx, UploadFileParams{
				Filename: jsonFilename,
				Content:  doc.EPCISJSONContent,
				FolderID: cfg.FolderOutputJSON,
			})
			if err != nil {
				logger.Error("Failed to upload JSON file",
					zap.String("shipping_operation_id", doc.ShippingOperationID),
					zap.Error(err),
				)
				// Continue even if JSON upload fails (XML is more important)
			} else {
				jsonFileID = result.ID
				logger.Info("Uploaded JSON file", zap.String("file_id", jsonFileID))
			}
		}

		// Upload enhanced EPCIS XML file
		xmlFilename := fmt.Sprintf("%s.xml", doc.CaptureID)
		result, err := cms.UploadFile(ctx, UploadFileParams{
			Filename: xmlFilename,
			Content:  doc.EnhancedXML,
			FolderID: cfg.FolderOutputXML,
		})
		if err != nil {
			logger.Error("Failed to upload XML file",
				zap.String("shipping_operation_id", doc.ShippingOperationID),
				zap.Error(err),
			)
			// Mark as failed
			UpdateDispatchStatus(ctx, cms, dispatchRecordID, "Failed", UpdateDispatchStatusParams{
				ErrorMessage: fmt.Sprintf("XML upload failed: %v", err),
			})
			failedCount++
			continue
		}
		xmlFileID := result.ID
		logger.Info("Uploaded XML file", zap.String("file_id", xmlFileID))

		// Update dispatch record with file IDs and status
		err = UpdateDispatchStatus(ctx, cms, dispatchRecordID, "Processing", UpdateDispatchStatusParams{
			EPCISJSONFileID: jsonFileID,
			EPCISXMLFileID:  xmlFileID,
			TargetGLN:       doc.TargetGLN,
		})
		if err != nil {
			logger.Error("Failed to update dispatch status",
				zap.String("dispatch_record_id", dispatchRecordID),
				zap.Error(err),
			)
			failedCount++
			continue
		}

		results = append(results, DispatchRecordWithFiles{
			ShippingOperationID:    doc.ShippingOperationID,
			CaptureID:              doc.CaptureID,
			DispatchRecordID:       dispatchRecordID,
			TargetGLN:              doc.TargetGLN,
			EPCISJSONFileID:        jsonFileID,
			EPCISXMLFileID:         xmlFileID,
			EPCISXMLEnhancedFileID: xmlFileID, // Same as XML file ID
		})

		logger.Info("Successfully managed dispatch record",
			zap.String("shipping_operation_id", doc.ShippingOperationID),
		)
	}

	// Check failure threshold
	if len(documents) > 0 {
		failureRate := float64(failedCount) / float64(len(documents))
		if failureRate > cfg.FailureThreshold {
			return nil, fmt.Errorf("dispatch record management failure rate %.0f%% exceeds threshold %.0f%%",
				failureRate*100, cfg.FailureThreshold*100)
		}
	}

	logger.Info("Dispatch record management complete",
		zap.Int("successful", len(results)),
		zap.Int("failed", failedCount),
	)

	return results, nil
}

// UpdateDispatchStatusParams holds optional parameters for updating dispatch status
type UpdateDispatchStatusParams struct {
	ErrorMessage    string
	TrustMedUUID    string
	HTTPStatusCode  int
	EPCISJSONFileID string
	EPCISXMLFileID  string
	TargetGLN       string
}

// CreateDispatchRecord creates a new EPCIS_outbound record
func CreateDispatchRecord(ctx context.Context, cms *DirectusClient, shippingOpID string, targetGLN string) (string, error) {
	logger.Info("Creating dispatch record",
		zap.String("shipping_operation_id", shippingOpID),
		zap.String("target_gln", targetGLN),
	)

	record := map[string]interface{}{
		"shipping_operation_id":  shippingOpID,
		"status":                 "pending",
		"dispatch_attempt_count": 0,
	}
	if targetGLN != "" {
		record["target_gln"] = targetGLN
	}

	result, err := cms.PostItem(ctx, "EPCIS_outbound", record)
	if err != nil {
		return "", fmt.Errorf("posting dispatch record: %w", err)
	}

	// Handle both string and numeric IDs
	var id string
	switch v := result["id"].(type) {
	case string:
		id = v
	case float64:
		id = fmt.Sprintf("%.0f", v)
	case int:
		id = fmt.Sprintf("%d", v)
	default:
		return "", fmt.Errorf("invalid response: unexpected id type %T", result["id"])
	}

	logger.Info("Created dispatch record", zap.String("id", id))
	return id, nil
}

// UpdateDispatchStatus updates a dispatch record with status and optional fields
func UpdateDispatchStatus(ctx context.Context, cms *DirectusClient, dispatchID string, status string, params UpdateDispatchStatusParams) error {
	logger.Info("Updating dispatch status",
		zap.String("dispatch_id", dispatchID),
		zap.String("status", status),
	)

	updates := map[string]interface{}{
		"status": status,
	}

	if params.ErrorMessage != "" {
		updates["last_error_message"] = params.ErrorMessage
	}
	if params.TrustMedUUID != "" {
		updates["trustmed_uuid"] = params.TrustMedUUID
		updates["date_acknowledged"] = time.Now().UTC().Format(time.RFC3339)
	}
	if params.HTTPStatusCode > 0 {
		updates["http_status_code"] = params.HTTPStatusCode
	}
	if params.EPCISJSONFileID != "" {
		updates["epcis_json_file_id"] = params.EPCISJSONFileID
	}
	if params.EPCISXMLFileID != "" {
		updates["epcis_xml_file_id"] = params.EPCISXMLFileID
	}
	if params.TargetGLN != "" {
		updates["target_gln"] = params.TargetGLN
	}

	// Update timestamps based on status
	switch status {
	case "Sent", "Acknowledged", "Failed", "Retrying":
		updates["last_dispatch_attempt"] = time.Now().UTC().Format(time.RFC3339)
	}
	if status == "Sent" || status == "Acknowledged" {
		updates["date_dispatched"] = time.Now().UTC().Format(time.RFC3339)
	}

	err := cms.PatchItem(ctx, "EPCIS_outbound", dispatchID, updates)
	if err != nil {
		return fmt.Errorf("patching dispatch record: %w", err)
	}

	logger.Info("Updated dispatch status",
		zap.String("dispatch_id", dispatchID),
		zap.String("status", status),
	)
	return nil
}

// IncrementDispatchAttempt increments the dispatch attempt counter
func IncrementDispatchAttempt(ctx context.Context, cms *DirectusClient, dispatchID string) (int, error) {
	// Get current record
	filter := map[string]interface{}{
		"id": map[string]interface{}{"_eq": dispatchID},
	}
	records, err := cms.QueryItems(ctx, "EPCIS_outbound", filter, []string{"dispatch_attempt_count"}, 1)
	if err != nil {
		return 0, fmt.Errorf("querying dispatch record: %w", err)
	}
	if len(records) == 0 {
		return 0, fmt.Errorf("dispatch record not found: %s", dispatchID)
	}

	currentCount := 0
	if count, ok := records[0]["dispatch_attempt_count"].(float64); ok {
		currentCount = int(count)
	}

	newCount := currentCount + 1

	// Update count
	updates := map[string]interface{}{
		"dispatch_attempt_count": newCount,
	}
	err = cms.PatchItem(ctx, "EPCIS_outbound", dispatchID, updates)
	if err != nil {
		return 0, fmt.Errorf("updating dispatch attempt count: %w", err)
	}

	logger.Info("Incremented dispatch attempt",
		zap.String("dispatch_id", dispatchID),
		zap.Int("attempt_count", newCount),
	)
	return newCount, nil
}
