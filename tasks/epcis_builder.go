package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// ShipmentWithEvents represents a shipment with its queried events
type ShipmentWithEvents struct {
	ShippingOperationID string                   `json:"shipping_operation_id"`
	CaptureID           string                   `json:"capture_id"`
	DispatchRecordID    *string                  `json:"dispatch_record_id,omitempty"`
	EventIDs            []string                 `json:"event_ids"`
	Events              []map[string]interface{} `json:"events"` // Raw event bodies from TiDB
}

// EPCISDocumentWithMetadata represents an EPCIS document with JSON and XML
type EPCISDocumentWithMetadata struct {
	ShippingOperationID string                   `json:"shipping_operation_id"`
	CaptureID           string                   `json:"capture_id"`
	DispatchRecordID    *string                  `json:"dispatch_record_id,omitempty"`
	BaseXMLContent      []byte                   `json:"base_xml_content"`
	EPCISJSONContent    []byte                   `json:"epcis_json_content"`
	EventIDs            []string                 `json:"event_ids"`
	Events              []map[string]interface{} `json:"events"` // Pass through for master data extraction
}

// BuildEPCISDocuments builds clean EPCIS 2.0 JSON-LD documents and converts them to XML.
// This creates "base XML" without SBDH headers or master data - that's added later by AddXMLHeaders.
func BuildEPCISDocuments(ctx context.Context, cfg *configs.Config, shipmentsWithEvents []ShipmentWithEvents) ([]EPCISDocumentWithMetadata, error) {
	logger.Info("Building EPCIS documents", zap.Int("count", len(shipmentsWithEvents)))

	if len(shipmentsWithEvents) == 0 {
		return []EPCISDocumentWithMetadata{}, nil
	}

	results := make([]EPCISDocumentWithMetadata, 0, len(shipmentsWithEvents))
	failedCount := 0

	for i, shipment := range shipmentsWithEvents {
		logger.Info("Building EPCIS document",
			zap.Int("index", i+1),
			zap.Int("total", len(shipmentsWithEvents)),
			zap.String("shipping_operation_id", shipment.ShippingOperationID),
			zap.Int("event_count", len(shipment.Events)),
		)

		// Validate events
		if len(shipment.Events) == 0 {
			logger.Error("No events found for shipment",
				zap.String("shipping_operation_id", shipment.ShippingOperationID),
			)
			failedCount++
			continue
		}

		// Build EPCIS 2.0 JSON-LD document
		epcisDoc := buildEPCISJSONDocument(shipment.Events)
		epcisJSONBytes, err := json.Marshal(epcisDoc)
		if err != nil {
			logger.Error("Failed to marshal EPCIS JSON",
				zap.String("shipping_operation_id", shipment.ShippingOperationID),
				zap.Error(err),
			)
			failedCount++
			continue
		}

		// Debug: Log event count and sample of JSON being sent to converter
		eventCount := 0
		if body, ok := epcisDoc["epcisBody"].(map[string]interface{}); ok {
			if eventList, ok := body["eventList"].([]map[string]interface{}); ok {
				eventCount = len(eventList)
			}
		}

		// Log a truncated sample of the JSON for debugging
		jsonSample := string(epcisJSONBytes)
		if len(jsonSample) > 500 {
			jsonSample = jsonSample[:500] + "..."
		}

		logger.Info("Created EPCIS 2.0 JSON-LD document",
			zap.String("shipping_operation_id", shipment.ShippingOperationID),
			zap.Int("json_size", len(epcisJSONBytes)),
			zap.Int("event_count", eventCount),
			zap.String("json_sample", jsonSample),
		)

		// Convert JSON to XML via converter service
		xmlContent, err := ConvertJSONToXML(ctx, cfg, epcisJSONBytes)
		if err != nil {
			logger.Error("Failed to convert JSON to XML",
				zap.String("shipping_operation_id", shipment.ShippingOperationID),
				zap.Error(err),
			)
			failedCount++
			continue
		}

		logger.Info("Converted to EPCIS 1.2 XML",
			zap.String("shipping_operation_id", shipment.ShippingOperationID),
			zap.Int("xml_size", len(xmlContent)),
		)

		results = append(results, EPCISDocumentWithMetadata{
			ShippingOperationID: shipment.ShippingOperationID,
			CaptureID:           shipment.CaptureID,
			DispatchRecordID:    shipment.DispatchRecordID,
			BaseXMLContent:      xmlContent,
			EPCISJSONContent:    epcisJSONBytes,
			EventIDs:            shipment.EventIDs,
			Events:              shipment.Events, // Pass through for master data extraction
		})

		logger.Info("Successfully built EPCIS document",
			zap.String("shipping_operation_id", shipment.ShippingOperationID),
		)
	}

	// Check failure threshold
	if len(shipmentsWithEvents) > 0 {
		failureRate := float64(failedCount) / float64(len(shipmentsWithEvents))
		if failureRate > cfg.FailureThreshold {
			return nil, fmt.Errorf("EPCIS document build failure rate %.0f%% exceeds threshold %.0f%%",
				failureRate*100, cfg.FailureThreshold*100)
		}
	}

	logger.Info("EPCIS document building complete",
		zap.Int("successful", len(results)),
		zap.Int("failed", failedCount),
	)

	return results, nil
}

// buildEPCISJSONDocument creates an EPCIS 2.0 JSON-LD document from event list.
// Returns a clean EPCIS document with events only (no master data - that's added to XML later).
func buildEPCISJSONDocument(events []map[string]interface{}) map[string]interface{} {
	// Filter out receiving events - outbound dispatch shouldn't include these.
	// Handles multiple bizStep formats (short form, CBV URN, GS1 Digital Link).
	filteredEvents := make([]map[string]interface{}, 0, len(events))
	for _, event := range events {
		bizStep, ok := event["bizStep"].(string)
		if ok && IsReceivingBizStep(bizStep) {
			continue
		}
		filteredEvents = append(filteredEvents, event)
	}

	// Sort by eventTime ascending for chronological order
	// (Already sorted by TiDB query, but ensure it's maintained)

	doc := map[string]interface{}{
		"@context":     "https://ref.gs1.org/standards/epcis/2.0.0/epcis-context.jsonld",
		"type":         "EPCISDocument",
		"schemaVersion": "2.0",
		"creationDate": time.Now().UTC().Format(time.RFC3339),
		"epcisBody": map[string]interface{}{
			"eventList": filteredEvents,
		},
	}

	return doc
}
