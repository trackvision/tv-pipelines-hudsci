package tasks

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/beevik/etree"
	"github.com/google/uuid"
	"github.com/trackvision/tv-pipelines-hudsci/configs"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// EnhancedDocument represents an EPCIS document with SBDH headers and master data
type EnhancedDocument struct {
	ShippingOperationID string  `json:"shipping_operation_id"`
	CaptureID           string  `json:"capture_id"`
	DispatchRecordID    *string `json:"dispatch_record_id,omitempty"`
	TargetGLN           string  `json:"target_gln"`
	EnhancedXML         []byte  `json:"enhanced_xml"`
	EPCISJSONContent    []byte  `json:"epcis_json_content"` // Pass through for upload
}

// LocationMasterData represents location master data for VocabularyList
type LocationMasterData struct {
	GLN           string `json:"gln"`
	URN           string `json:"urn"`
	Name          string `json:"name"`
	StreetAddress string `json:"street_address"`
	City          string `json:"city"`
	State         string `json:"state"`
	PostalCode    string `json:"postal_code"`
	CountryCode   string `json:"country_code"`
}

// ProductMasterData represents product master data for VocabularyList
type ProductMasterData struct {
	GTIN                  string `json:"gtin"`
	URN                   string `json:"urn"` // Base URN without serial
	ProductName           string `json:"product_name"`
	NDC                   string `json:"ndc"`
	Manufacturer          string `json:"manufacturer"`
	DosageFormType        string `json:"dosage_form_type"`
	StrengthDescription   string `json:"strength_description"`
	NetContentDescription string `json:"net_content_description"`
}

// AddXMLHeaders enhances EPCIS XML with SBDH headers, DSCSA statements, and VocabularyList.
// This is a pure function with no side effects - file uploads happen later in ManageDispatchRecords.
func AddXMLHeaders(ctx context.Context, cms *DirectusClient, cfg *configs.Config, documents []EPCISDocumentWithMetadata) ([]EnhancedDocument, error) {
	logger.Info("Adding XML headers to EPCIS documents", zap.Int("count", len(documents)))

	if len(documents) == 0 {
		return []EnhancedDocument{}, nil
	}

	results := make([]EnhancedDocument, 0, len(documents))
	failedCount := 0

	for i, doc := range documents {
		logger.Info("Enhancing XML",
			zap.Int("index", i+1),
			zap.Int("total", len(documents)),
			zap.String("shipping_operation_id", doc.ShippingOperationID),
		)

		// Extract master data from events
		locations, products, err := extractMasterDataFromEvents(ctx, cms, doc.Events)
		if err != nil {
			logger.Error("Failed to extract master data",
				zap.String("shipping_operation_id", doc.ShippingOperationID),
				zap.Error(err),
			)
			failedCount++
			continue
		}

		logger.Info("Extracted master data",
			zap.String("shipping_operation_id", doc.ShippingOperationID),
			zap.Int("locations", len(locations)),
			zap.Int("products", len(products)),
		)

		// Extract sender/receiver URNs from shipping event
		senderURN, receiverURN := extractShippingURNs(doc.Events, cfg)

		// Enhance XML with SBDH, DSCSA, and VocabularyList
		enhancedXML, err := enhanceEPCISXML(doc.BaseXMLContent, senderURN, receiverURN, locations, products)
		if err != nil {
			logger.Error("Failed to enhance XML",
				zap.String("shipping_operation_id", doc.ShippingOperationID),
				zap.Error(err),
			)
			failedCount++
			continue
		}

		logger.Info("Enhanced XML",
			zap.String("shipping_operation_id", doc.ShippingOperationID),
			zap.Int("xml_size", len(enhancedXML)),
		)

		// Parse GLN from receiver URN for routing
		targetGLN := parseGLNFromSGLNURN(receiverURN)
		if targetGLN == "" {
			targetGLN = receiverURN // Fallback to URN if parsing fails
		}

		results = append(results, EnhancedDocument{
			ShippingOperationID: doc.ShippingOperationID,
			CaptureID:           doc.CaptureID,
			DispatchRecordID:    doc.DispatchRecordID,
			TargetGLN:           targetGLN,
			EnhancedXML:         enhancedXML,
			EPCISJSONContent:    doc.EPCISJSONContent,
		})

		logger.Info("Successfully enhanced EPCIS document",
			zap.String("shipping_operation_id", doc.ShippingOperationID),
		)
	}

	// Check failure threshold
	if len(documents) > 0 {
		failureRate := float64(failedCount) / float64(len(documents))
		if failureRate > cfg.FailureThreshold {
			return nil, fmt.Errorf("XML enhancement failure rate %.0f%% exceeds threshold %.0f%%",
				failureRate*100, cfg.FailureThreshold*100)
		}
	}

	logger.Info("XML enhancement complete",
		zap.Int("successful", len(results)),
		zap.Int("failed", failedCount),
	)

	return results, nil
}

// extractShippingURNs extracts sender and receiver URNs from shipping event.
// Handles multiple bizStep formats (short form, CBV URN, GS1 Digital Link).
// IMPORTANT: Uses location type for SBDH sender/receiver (matching Mage behavior).
// The location type points to physical location, owning_party points to legal entity.
func extractShippingURNs(events []map[string]interface{}, cfg *configs.Config) (string, string) {
	for _, event := range events {
		bizStep, ok := event["bizStep"].(string)
		if !ok || !IsShippingBizStep(bizStep) {
			continue
		}

		// Extract sender from sourceList
		// Priority: location first (for SBDH), then owning_party
		var senderURN string
		if sourceList, ok := event["sourceList"].([]interface{}); ok {
			for _, source := range sourceList {
				if sourceMap, ok := source.(map[string]interface{}); ok {
					sourceType, _ := sourceMap["type"].(string)
					urn, _ := sourceMap["source"].(string)
					if urn == "" {
						continue
					}
					// Prefer location for SBDH (physical destination)
					if sourceType == "location" || strings.HasSuffix(sourceType, ":location") {
						senderURN = urn
						break
					}
					// Fall back to owning_party if no location found yet
					if senderURN == "" && (sourceType == "owning_party" || strings.HasSuffix(sourceType, ":owning_party")) {
						senderURN = urn
					}
				}
			}
		}

		// Extract receiver from destinationList
		// Priority: location first (for SBDH), then owning_party
		var receiverURN string
		if destList, ok := event["destinationList"].([]interface{}); ok {
			for _, dest := range destList {
				if destMap, ok := dest.(map[string]interface{}); ok {
					destType, _ := destMap["type"].(string)
					urn, _ := destMap["destination"].(string)
					if urn == "" {
						continue
					}
					// Prefer location for SBDH (physical destination)
					if destType == "location" || strings.HasSuffix(destType, ":location") {
						receiverURN = urn
						break
					}
					// Fall back to owning_party if no location found yet
					if receiverURN == "" && (destType == "owning_party" || strings.HasSuffix(destType, ":owning_party")) {
						receiverURN = urn
					}
				}
			}
		}

		if senderURN != "" || receiverURN != "" {
			logger.Info("Extracted URNs from shipping event",
				zap.String("sender", senderURN),
				zap.String("receiver", receiverURN),
			)
			return senderURN, receiverURN
		}
	}

	// Fallback to default GLNs if not found
	logger.Warn("No shipping event found with sender/receiver URNs, using defaults")
	return fmt.Sprintf("urn:epc:id:sgln:%s.0", cfg.DefaultSenderGLN),
		fmt.Sprintf("urn:epc:id:sgln:%s.0", cfg.DefaultReceiverGLN)
}

// parseGLNFromSGLNURN parses GLN from SGLN URN format.
// Returns a 13-digit GLN with proper GS1 check digit.
func parseGLNFromSGLNURN(sglnURN string) string {
	return ParseGLNFromSGLN(sglnURN)
}

// enhanceEPCISXML adds SBDH, DSCSA, and VocabularyList to EPCIS XML
func enhanceEPCISXML(baseXML []byte, senderURN, receiverURN string, locations []LocationMasterData, products []ProductMasterData) ([]byte, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(baseXML); err != nil {
		return nil, fmt.Errorf("parsing XML: %w", err)
	}

	root := doc.Root()
	if root == nil {
		return nil, fmt.Errorf("no root element in XML")
	}

	// Add namespace declarations for SBDH, DSCSA, and cbvmda elements (matching Mage)
	root.CreateAttr("xmlns:sbdh", "http://www.unece.org/cefact/namespaces/StandardBusinessDocumentHeader")
	root.CreateAttr("xmlns:gs1ushc", "http://epcis.gs1us.org/hc/ns")
	root.CreateAttr("xmlns:cbvmda", "urn:epcglobal:cbv:mda")

	// Create EPCISHeader as first child
	header := root.CreateElement("EPCISHeader")
	root.InsertChildAt(0, header)

	// Add SBDH
	addSBDH(header, senderURN, receiverURN)

	// Add extension with VocabularyList (before DSCSA)
	if len(locations) > 0 || len(products) > 0 {
		extension := header.CreateElement("extension")
		masterData := extension.CreateElement("EPCISMasterData")
		vocabList := masterData.CreateElement("VocabularyList")

		// Add products first, then locations (matches Mage output order)
		if len(products) > 0 {
			addProductVocabulary(vocabList, products)
		}
		if len(locations) > 0 {
			addLocationVocabulary(vocabList, locations)
		}
	}

	// Add guidelineVersion (required by TrustMed)
	guideline := header.CreateElement("gs1ushc:guidelineVersion")
	guideline.SetText("GS1 US DSCSA R1.3")

	// Add DSCSA transaction statement (last)
	addDSCSA(header)

	doc.Indent(2)
	return doc.WriteToBytes()
}

// Helper functions for XML building (simplified versions)

func addSBDH(header *etree.Element, senderURN, receiverURN string) {
	sbdh := header.CreateElement("sbdh:StandardBusinessDocumentHeader")

	version := sbdh.CreateElement("sbdh:HeaderVersion")
	version.SetText("1.0")

	sender := sbdh.CreateElement("sbdh:Sender")
	senderID := sender.CreateElement("sbdh:Identifier")
	senderID.CreateAttr("Authority", "SGLN")
	senderID.SetText(senderURN)

	receiver := sbdh.CreateElement("sbdh:Receiver")
	receiverID := receiver.CreateElement("sbdh:Identifier")
	receiverID.CreateAttr("Authority", "SGLN")
	receiverID.SetText(receiverURN)

	docID := sbdh.CreateElement("sbdh:DocumentIdentification")
	docID.CreateElement("sbdh:Standard").SetText("EPCglobal")
	docID.CreateElement("sbdh:TypeVersion").SetText("1.0")
	docID.CreateElement("sbdh:InstanceIdentifier").SetText(uuid.New().String())
	docID.CreateElement("sbdh:Type").SetText("Events")
	// Use microsecond precision with +00:00 timezone (matching Mage format)
	docID.CreateElement("sbdh:CreationDateAndTime").SetText(time.Now().UTC().Format("2006-01-02T15:04:05.000000+00:00"))
}

func addDSCSA(header *etree.Element) {
	dscsa := header.CreateElement("gs1ushc:dscsaTransactionStatement")
	dscsa.CreateElement("gs1ushc:affirmTransactionStatement").SetText("true")
	dscsa.CreateElement("gs1ushc:legalNotice").SetText("Seller has complied with each applicable subsection of FDCA Sec. 581(27)(A)-(G).")
}

func addLocationVocabulary(vocabList *etree.Element, locations []LocationMasterData) {
	vocab := vocabList.CreateElement("Vocabulary")
	vocab.CreateAttr("type", "urn:epcglobal:epcis:vtype:Location")
	elemList := vocab.CreateElement("VocabularyElementList")

	for _, loc := range locations {
		elem := elemList.CreateElement("VocabularyElement")
		elem.CreateAttr("id", loc.URN)

		if loc.Name != "" {
			attr := elem.CreateElement("attribute")
			attr.CreateAttr("id", "urn:epcglobal:cbv:mda#name")
			attr.SetText(loc.Name)
		}
		if loc.StreetAddress != "" {
			attr := elem.CreateElement("attribute")
			attr.CreateAttr("id", "urn:epcglobal:cbv:mda#streetAddressOne")
			attr.SetText(loc.StreetAddress)
		}
		if loc.City != "" {
			attr := elem.CreateElement("attribute")
			attr.CreateAttr("id", "urn:epcglobal:cbv:mda#city")
			attr.SetText(loc.City)
		}
		if loc.State != "" {
			attr := elem.CreateElement("attribute")
			attr.CreateAttr("id", "urn:epcglobal:cbv:mda#state")
			attr.SetText(loc.State)
		}
		if loc.PostalCode != "" {
			attr := elem.CreateElement("attribute")
			attr.CreateAttr("id", "urn:epcglobal:cbv:mda#postalCode")
			attr.SetText(loc.PostalCode)
		}
		if loc.CountryCode != "" {
			attr := elem.CreateElement("attribute")
			attr.CreateAttr("id", "urn:epcglobal:cbv:mda#countryCode")
			attr.SetText(loc.CountryCode)
		}
	}
}

func addProductVocabulary(vocabList *etree.Element, products []ProductMasterData) {
	vocab := vocabList.CreateElement("Vocabulary")
	vocab.CreateAttr("type", "urn:epcglobal:epcis:vtype:EPCClass")
	elemList := vocab.CreateElement("VocabularyElementList")

	for _, prod := range products {
		// Convert base URN to idpat pattern
		sgtinPattern := convertToIDPat(prod.URN)

		elem := elemList.CreateElement("VocabularyElement")
		elem.CreateAttr("id", sgtinPattern)

		if prod.NDC != "" {
			attr1 := elem.CreateElement("attribute")
			attr1.CreateAttr("id", "urn:epcglobal:cbv:mda#additionalTradeItemIdentification")
			attr1.SetText(prod.NDC)

			attr2 := elem.CreateElement("attribute")
			attr2.CreateAttr("id", "urn:epcglobal:cbv:mda#additionalTradeItemIdentificationTypeCode")
			attr2.SetText("US_FDA_NDC")
		}
		if prod.ProductName != "" {
			attr := elem.CreateElement("attribute")
			attr.CreateAttr("id", "urn:epcglobal:cbv:mda#regulatedProductName")
			attr.SetText(prod.ProductName)
		}
		if prod.Manufacturer != "" {
			attr := elem.CreateElement("attribute")
			attr.CreateAttr("id", "urn:epcglobal:cbv:mda#manufacturerOfTradeItemPartyName")
			attr.SetText(prod.Manufacturer)
		}
		if prod.DosageFormType != "" {
			attr := elem.CreateElement("attribute")
			attr.CreateAttr("id", "urn:epcglobal:cbv:mda#dosageFormType")
			attr.SetText(prod.DosageFormType)
		}
		if prod.StrengthDescription != "" {
			attr := elem.CreateElement("attribute")
			attr.CreateAttr("id", "urn:epcglobal:cbv:mda#strengthDescription")
			attr.SetText(prod.StrengthDescription)
		}
		if prod.NetContentDescription != "" {
			attr := elem.CreateElement("attribute")
			attr.CreateAttr("id", "urn:epcglobal:cbv:mda#netContentDescription")
			attr.SetText(prod.NetContentDescription)
		}
	}
}

// convertToIDPat converts urn:epc:id:sgtin:X.Y to urn:epc:idpat:sgtin:X.Y.*
func convertToIDPat(baseURN string) string {
	if strings.Contains(baseURN, ":id:") {
		return strings.Replace(baseURN, ":id:", ":idpat:", 1) + ".*"
	}
	return baseURN + ".*"
}

// extractMasterDataFromEvents queries Directus for location and product master data.
// Location order is deterministic to match Mage output:
// 1. Destination locations from shipping event (in order)
// 2. Source locations from shipping event (in order)
// 3. Other locations from bizLocation/readPoint (sorted by URN)
func extractMasterDataFromEvents(ctx context.Context, cms *DirectusClient, events []map[string]interface{}) ([]LocationMasterData, []ProductMasterData, error) {
	// Collect location URNs in order matching Mage:
	// 1. First, destination locations from shipping event
	// 2. Then, source locations from shipping event
	// 3. Then, other locations sorted by URN
	var orderedLocationURNs []string
	seen := make(map[string]bool)

	// Find shipping event and extract destination/source URNs in order
	for _, event := range events {
		bizStep, ok := event["bizStep"].(string)
		if !ok || !IsShippingBizStep(bizStep) {
			continue
		}

		// Destination locations first (matches Mage order)
		if destList, ok := event["destinationList"].([]interface{}); ok {
			for _, dst := range destList {
				if dstMap, ok := dst.(map[string]interface{}); ok {
					if urn, ok := dstMap["destination"].(string); ok && strings.Contains(urn, "sgln") && !seen[urn] {
						orderedLocationURNs = append(orderedLocationURNs, urn)
						seen[urn] = true
					}
				}
			}
		}

		// Then source locations
		if sourceList, ok := event["sourceList"].([]interface{}); ok {
			for _, src := range sourceList {
				if srcMap, ok := src.(map[string]interface{}); ok {
					if urn, ok := srcMap["source"].(string); ok && strings.Contains(urn, "sgln") && !seen[urn] {
						orderedLocationURNs = append(orderedLocationURNs, urn)
						seen[urn] = true
					}
				}
			}
		}
		break // Only process first shipping event
	}

	// Collect remaining locations from bizLocation/readPoint
	var otherURNs []string
	for _, event := range events {
		if readPoint, ok := event["readPoint"].(map[string]interface{}); ok {
			if id, ok := readPoint["id"].(string); ok && strings.Contains(id, "sgln") && !seen[id] {
				otherURNs = append(otherURNs, id)
				seen[id] = true
			}
		}
		if bizLoc, ok := event["bizLocation"].(map[string]interface{}); ok {
			if id, ok := bizLoc["id"].(string); ok && strings.Contains(id, "sgln") && !seen[id] {
				otherURNs = append(otherURNs, id)
				seen[id] = true
			}
		}
	}
	// Sort other URNs for deterministic output
	sort.Strings(otherURNs)
	orderedLocationURNs = append(orderedLocationURNs, otherURNs...)

	// Extract unique product URNs (strip serial numbers)
	productURNs := make(map[string]bool)
	for _, event := range events {
		lists := []string{"epcList", "childEPCs", "inputEPCList", "outputEPCList"}
		for _, listName := range lists {
			if list, ok := event[listName].([]interface{}); ok {
				for _, epc := range list {
					if epcStr, ok := epc.(string); ok && strings.Contains(epcStr, "sgtin") {
						baseURN := stripSerialFromSGTIN(epcStr)
						if baseURN != "" {
							productURNs[baseURN] = true
						}
					}
				}
			}
		}
	}

	logger.Info("Querying master data",
		zap.Int("location_urns", len(orderedLocationURNs)),
		zap.Int("product_urns", len(productURNs)),
	)

	// Query locations from Directus (preserving order)
	var locations []LocationMasterData
	for _, urn := range orderedLocationURNs {
		gln := parseGLNFromSGLNURN(urn)
		if gln == "" {
			continue
		}

		filter := map[string]interface{}{
			"gln": map[string]interface{}{"_eq": gln},
		}
		items, err := cms.QueryItems(ctx, "location", filter, []string{"gln", "location_name", "address", "city", "state", "postal_code", "country_code"}, 1)
		if err != nil || len(items) == 0 {
			// Try organisation table
			filter = map[string]interface{}{
				"pgln": map[string]interface{}{"_eq": gln},
			}
			items, err = cms.QueryItems(ctx, "organisation", filter, []string{"pgln", "organisation_name", "address", "city", "state", "postal_code", "country_code"}, 1)
			if err != nil || len(items) == 0 {
				logger.Warn("Location not found", zap.String("gln", gln))
				continue
			}
			// Map organisation fields
			item := items[0]
			locations = append(locations, LocationMasterData{
				GLN:           gln,
				URN:           urn,
				Name:          getStringField(item, "organisation_name"),
				StreetAddress: getStringField(item, "address"),
				City:          getStringField(item, "city"),
				State:         getStringField(item, "state"),
				PostalCode:    getStringField(item, "postal_code"),
				CountryCode:   getStringField(item, "country_code"),
			})
		} else {
			item := items[0]
			locations = append(locations, LocationMasterData{
				GLN:           gln,
				URN:           urn,
				Name:          getStringField(item, "location_name"),
				StreetAddress: getStringField(item, "address"),
				City:          getStringField(item, "city"),
				State:         getStringField(item, "state"),
				PostalCode:    getStringField(item, "postal_code"),
				CountryCode:   getStringField(item, "country_code"),
			})
		}
	}

	// Sort product URNs for deterministic output
	var sortedProductURNs []string
	for urn := range productURNs {
		sortedProductURNs = append(sortedProductURNs, urn)
	}
	sort.Strings(sortedProductURNs)

	// Query products from Directus (in sorted order)
	var products []ProductMasterData
	for _, urn := range sortedProductURNs {
		filter := map[string]interface{}{
			"urn": map[string]interface{}{"_eq": urn},
		}
		items, err := cms.QueryItems(ctx, "product", filter, []string{
			"gtin", "urn", "product_name", "ndc",
			"product_manufacturer.organisation_name",
			"net_content_description", "dosage_form_type", "strength_description",
		}, 1)
		if err != nil || len(items) == 0 {
			logger.Warn("Product not found", zap.String("urn", urn))
			continue
		}

		item := items[0]
		manufacturer := ""
		if mfg, ok := item["product_manufacturer"].(map[string]interface{}); ok {
			manufacturer = getStringField(mfg, "organisation_name")
		}

		products = append(products, ProductMasterData{
			GTIN:                  getStringField(item, "gtin"),
			URN:                   urn,
			ProductName:           getStringField(item, "product_name"),
			NDC:                   getStringField(item, "ndc"),
			Manufacturer:          manufacturer,
			NetContentDescription: getStringField(item, "net_content_description"),
			DosageFormType:        getStringField(item, "dosage_form_type"),
			StrengthDescription:   getStringField(item, "strength_description"),
		})
	}

	return locations, products, nil
}

// stripSerialFromSGTIN removes serial number from SGTIN URN.
// Uses flexible parsing that handles variable company prefix lengths.
func stripSerialFromSGTIN(sgtinURN string) string {
	return StripSerialFromSGTIN(sgtinURN)
}

// getStringField safely extracts a string field from a map
func getStringField(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}
