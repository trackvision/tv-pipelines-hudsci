package inbound_trustmed

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-pipelines-hudsci/pipelines"
	"github.com/trackvision/tv-pipelines-hudsci/tasks"
	"github.com/trackvision/tv-pipelines-hudsci/types"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// Steps lists all task names in this pipeline (for API discovery).
var Steps = []string{
	"poll_trustmed_files",
	"convert_xml_to_json",
	"upload_json_files",
	"extract_shipment_data",
	"insert_epcis_inbox",
	"link_json_files",
	"update_watermark",
}

// Run executes the TrustMed inbound shipments pipeline.
// This pipeline polls XML files from TrustMed Dashboard API (files sent TO us),
// converts them to JSON, extracts shipping data, and inserts to the database.
func Run(ctx context.Context, db *sqlx.DB, cms *tasks.DirectusClient, cfg *configs.Config, id string) error {
	// Shared state via closures
	var xmlFiles []types.XMLFile
	var convertedFiles []types.ConvertedFile
	var extractedShipments []tasks.EPCISInboxItem
	var jsonFileIDMap map[string]string

	// Initialize TrustMed Dashboard client
	dashboard := tasks.NewTrustMedDashboardClient(cfg)

	flow := pipelines.NewFlow("inbound-trustmed")

	// Task 1: Poll XML files from TrustMed Dashboard (received files)
	flow.AddTask("poll_trustmed_files", func() error {
		var err error
		xmlFiles, err = tasks.PollTrustMedFiles(ctx, dashboard, cms, cfg)
		if err != nil {
			return err
		}
		logger.Info("Polled TrustMed files", zap.Int("count", len(xmlFiles)))
		return nil
	})

	// Task 2: Convert XML to JSON via EPCIS Converter service
	flow.AddTask("convert_xml_to_json", func() error {
		if len(xmlFiles) == 0 {
			logger.Info("No files to convert, skipping")
			return nil
		}
		var err error
		convertedFiles, err = tasks.ConvertXMLToJSON(ctx, cfg, xmlFiles)
		if err != nil {
			return err
		}
		logger.Info("Converted XML to JSON", zap.Int("count", len(convertedFiles)))
		return nil
	}, "poll_trustmed_files")

	// Task 3: Upload JSON files to Directus
	flow.AddTask("upload_json_files", func() error {
		if len(convertedFiles) == 0 {
			logger.Info("No converted files to upload, skipping")
			jsonFileIDMap = make(map[string]string)
			return nil
		}
		var err error
		jsonFileIDMap, err = tasks.UploadJSONFiles(ctx, cms, cfg, convertedFiles)
		if err != nil {
			return err
		}
		logger.Info("Uploaded JSON files to Directus", zap.Int("mapped_count", len(jsonFileIDMap)))
		return nil
	}, "convert_xml_to_json")

	// Task 4: Extract shipping data from XML
	flow.AddTask("extract_shipment_data", func() error {
		if len(xmlFiles) == 0 {
			logger.Info("No XML files to extract, skipping")
			return nil
		}
		var err error
		extractedShipments, err = tasks.ExtractEPCISInboxData(ctx, cms, xmlFiles)
		if err != nil {
			return err
		}
		logger.Info("Extracted shipment data", zap.Int("count", len(extractedShipments)))
		return nil
	}, "convert_xml_to_json")

	// Task 5: Insert to epcis_inbox collection
	flow.AddTask("insert_epcis_inbox", func() error {
		if len(extractedShipments) == 0 {
			logger.Info("No shipments to insert, skipping")
			return nil
		}
		if err := tasks.InsertEPCISInbox(ctx, cms, extractedShipments); err != nil {
			return err
		}
		logger.Info("Inserted to epcis_inbox")
		return nil
	}, "extract_shipment_data")

	// Task 6: Link JSON files to epcis_inbox records
	flow.AddTask("link_json_files", func() error {
		if len(jsonFileIDMap) == 0 {
			logger.Info("No JSON files to link, skipping")
			return nil
		}
		if err := tasks.LinkJSONFilesToInbox(ctx, cms, jsonFileIDMap); err != nil {
			return err
		}
		logger.Info("Linked JSON files to inbox records")
		return nil
	}, "upload_json_files", "insert_epcis_inbox")

	// Task 7: Update watermark (after all processing)
	// Note: TrustMed watermark is already updated in PollTrustMedFiles
	// This step is for logging/consistency
	flow.AddTask("update_watermark", func() error {
		if len(xmlFiles) > 0 {
			logger.Info("Pipeline completed, watermark already updated",
				zap.Int("processed_count", len(xmlFiles)),
				zap.Time("completed_at", time.Now()),
			)
		}
		return nil
	}, "link_json_files")

	// Suppress unused warnings
	_ = db

	return flow.Run(ctx)
}
