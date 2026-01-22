package outbound

import (
	"context"
	"encoding/json"

	"github.com/jmoiron/sqlx"
	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-pipelines-hudsci/pipelines"
	"github.com/trackvision/tv-pipelines-hudsci/tasks"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// Steps lists all task names in this pipeline (for API discovery).
var Steps = []string{
	"poll_approved_shipments",
	"query_shipment_events",
	"build_epcis_documents",
	"add_xml_headers",
	"manage_dispatch_records",
	"dispatch_via_trustmed",
	"poll_dispatch_confirmation",
	"notify_on_errors",
}

// Run executes the outbound shipments received pipeline.
// This pipeline queries approved shipments, builds EPCIS documents,
// and dispatches them via TrustMed mTLS.
func Run(ctx context.Context, db *sqlx.DB, cms *tasks.DirectusClient, cfg *configs.Config, id string) error {
	// Shared state via closures
	var approvedShipments []tasks.ApprovedShipment
	var shipmentsWithEvents []tasks.ShipmentWithEvents
	var epcisDocuments []tasks.EPCISDocumentWithMetadata
	var enhancedDocuments []tasks.EnhancedDocument
	var dispatchRecords []tasks.DispatchRecordWithFiles
	var dispatchResults []tasks.DispatchResult

	flow := pipelines.NewFlow("outbound-shipments")

	// Task 1: Poll approved shipments from Directus
	flow.AddTask("poll_approved_shipments", func() error {
		logger.Info("Polling approved shipments", zap.String("id", id))
		var err error
		approvedShipments, err = tasks.PollApprovedShipments(ctx, cms, cfg)
		if err != nil {
			return err
		}
		logger.Info("Polled approved shipments", zap.Int("count", len(approvedShipments)))
		return nil
	})

	// Task 2: Query related events from TiDB (CTE for hierarchy)
	flow.AddTask("query_shipment_events", func() error {
		logger.Info("Querying shipment events", zap.Int("shipment_count", len(approvedShipments)))

		if len(approvedShipments) == 0 {
			logger.Info("No approved shipments to query events for")
			shipmentsWithEvents = []tasks.ShipmentWithEvents{}
			return nil
		}

		// Query events for each approved shipment
		shipmentsWithEvents = make([]tasks.ShipmentWithEvents, 0, len(approvedShipments))
		for _, shipment := range approvedShipments {
			events, err := tasks.QueryShipmentEventsByCaptureID(ctx, db, shipment.CaptureID)
			if err != nil {
				logger.Warn("Failed to query events for shipment",
					zap.String("capture_id", shipment.CaptureID),
					zap.Error(err),
				)
				continue
			}

			if len(events) == 0 {
				logger.Info("No events found for shipment",
					zap.String("capture_id", shipment.CaptureID),
				)
				continue
			}

			// Convert EventRow to map[string]interface{} for the Events field
			eventMaps := make([]map[string]interface{}, len(events))
			eventIDs := make([]string, len(events))
			for i, event := range events {
				eventIDs[i] = event.EventID
				// Parse event body JSON into map
				var eventMap map[string]interface{}
				if err := json.Unmarshal([]byte(event.EventBody), &eventMap); err != nil {
					logger.Warn("Failed to parse event body",
						zap.String("event_id", event.EventID),
						zap.Error(err),
					)
					continue
				}
				eventMaps[i] = eventMap

				// Debug: Log event details to verify all expected events are present
				eventType, _ := eventMap["type"].(string)
				bizStep, _ := eventMap["bizStep"].(string)
				bizLocation := ""
				if bl, ok := eventMap["bizLocation"].(map[string]interface{}); ok {
					bizLocation, _ = bl["id"].(string)
				}
				disposition, _ := eventMap["disposition"].(string)

				logger.Info("Parsed event from TiDB",
					zap.Int("index", i),
					zap.String("capture_id", shipment.CaptureID),
					zap.String("event_id", event.EventID),
					zap.String("event_type", eventType),
					zap.String("bizStep", bizStep),
					zap.String("bizLocation", bizLocation),
					zap.String("disposition", disposition),
				)
			}

			shipmentsWithEvents = append(shipmentsWithEvents, tasks.ShipmentWithEvents{
				ShippingOperationID: shipment.ShippingOperationID,
				CaptureID:           shipment.CaptureID,
				DispatchRecordID:    shipment.DispatchRecordID,
				EventIDs:            eventIDs,
				Events:              eventMaps,
			})
		}

		logger.Info("Queried shipment events", zap.Int("with_events", len(shipmentsWithEvents)))
		return nil
	}, "poll_approved_shipments")

	// Task 3: Build EPCIS 2.0 JSON-LD documents
	flow.AddTask("build_epcis_documents", func() error {
		logger.Info("Building EPCIS documents", zap.Int("shipment_count", len(shipmentsWithEvents)))
		if len(shipmentsWithEvents) == 0 {
			logger.Info("No shipments with events to build documents for")
			epcisDocuments = []tasks.EPCISDocumentWithMetadata{}
			return nil
		}
		var err error
		epcisDocuments, err = tasks.BuildEPCISDocuments(ctx, cfg, shipmentsWithEvents)
		if err != nil {
			return err
		}
		logger.Info("Built EPCIS documents", zap.Int("count", len(epcisDocuments)))
		return nil
	}, "query_shipment_events")

	// Task 4: Add SBDH headers, DSCSA statements, VocabularyList
	flow.AddTask("add_xml_headers", func() error {
		logger.Info("Adding XML headers", zap.Int("document_count", len(epcisDocuments)))
		if len(epcisDocuments) == 0 {
			logger.Info("No EPCIS documents to enhance")
			enhancedDocuments = []tasks.EnhancedDocument{}
			return nil
		}
		var err error
		enhancedDocuments, err = tasks.AddXMLHeaders(ctx, cms, cfg, epcisDocuments)
		if err != nil {
			return err
		}
		logger.Info("Added XML headers", zap.Int("count", len(enhancedDocuments)))
		return nil
	}, "build_epcis_documents")

	// Task 5: Create/update dispatch records, upload files to Directus
	flow.AddTask("manage_dispatch_records", func() error {
		logger.Info("Managing dispatch records", zap.Int("document_count", len(enhancedDocuments)))
		if len(enhancedDocuments) == 0 {
			logger.Info("No enhanced documents to manage")
			dispatchRecords = []tasks.DispatchRecordWithFiles{}
			return nil
		}
		var err error
		dispatchRecords, err = tasks.ManageDispatchRecords(ctx, cms, cfg, enhancedDocuments)
		if err != nil {
			return err
		}
		logger.Info("Managed dispatch records", zap.Int("count", len(dispatchRecords)))
		return nil
	}, "add_xml_headers")

	// Task 6: Dispatch via TrustMed Partner API (mTLS)
	flow.AddTask("dispatch_via_trustmed", func() error {
		logger.Info("Dispatching via TrustMed", zap.Int("record_count", len(dispatchRecords)))
		if len(dispatchRecords) == 0 {
			logger.Info("No dispatch records to send")
			dispatchResults = []tasks.DispatchResult{}
			return nil
		}
		var err error
		dispatchResults, err = tasks.DispatchViaTrustMed(ctx, cms, cfg, dispatchRecords)
		if err != nil {
			return err
		}
		logger.Info("Dispatched via TrustMed", zap.Int("count", len(dispatchResults)))
		return nil
	}, "manage_dispatch_records")

	// Task 7: Poll TrustMed Dashboard for delivery confirmation
	flow.AddTask("poll_dispatch_confirmation", func() error {
		logger.Info("Polling dispatch confirmation", zap.Int("result_count", len(dispatchResults)))
		if len(dispatchResults) == 0 {
			logger.Info("No dispatch results to poll")
			return nil
		}
		return tasks.PollDispatchConfirmation(ctx, cms, cfg, dispatchResults)
	}, "dispatch_via_trustmed")

	// Task 8: Log and notify on permanent failures
	flow.AddTask("notify_on_errors", func() error {
		logger.Info("Checking for errors to notify", zap.Int("result_count", len(dispatchResults)))
		return tasks.NotifyOnErrors(ctx, cms, cfg, dispatchResults)
	}, "poll_dispatch_confirmation")

	return flow.Run(ctx)
}
