package tasks

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/trackvision/tv-pipelines-hudsci/types"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

// EPCIS namespace constants
const (
	EPCISNS = "urn:epcglobal:epcis:xsd:2"
)

// EPCISDocument represents the root EPCIS XML document
type EPCISDocument struct {
	XMLName     xml.Name     `xml:"EPCISDocument"`
	EPCISBody   EPCISBody    `xml:"EPCISBody"`
	EPCISHeader *EPCISHeader `xml:"EPCISHeader"`
}

// EPCISHeader contains master data
type EPCISHeader struct {
	Extension *HeaderExtension `xml:"extension"`
}

// HeaderExtension contains EPCIS master data
type HeaderExtension struct {
	EPCISMasterData *EPCISMasterData `xml:"EPCISMasterData"`
}

// EPCISMasterData contains vocabularies
type EPCISMasterData struct {
	VocabularyList *VocabularyList `xml:"VocabularyList"`
}

// VocabularyList contains vocabulary definitions
type VocabularyList struct {
	Vocabulary []Vocabulary `xml:"Vocabulary"`
}

// Vocabulary represents a vocabulary (products or locations)
type Vocabulary struct {
	Type                   string                  `xml:"type,attr"`
	VocabularyElementList  *VocabularyElementList  `xml:"VocabularyElementList"`
}

// VocabularyElementList contains vocabulary elements
type VocabularyElementList struct {
	VocabularyElement []VocabularyElement `xml:"VocabularyElement"`
}

// VocabularyElement represents a single vocabulary entry
type VocabularyElement struct {
	ID        string      `xml:"id,attr"`
	Attribute []Attribute `xml:"attribute"`
}

// Attribute represents a vocabulary attribute
type Attribute struct {
	ID    string `xml:"id,attr"`
	Value string `xml:",chardata"`
}

// EPCISBody contains the event list
type EPCISBody struct {
	EventList EventList `xml:"EventList"`
}

// EventList contains all events
type EventList struct {
	ObjectEvents      []ObjectEvent      `xml:"ObjectEvent"`
	AggregationEvents []AggregationEvent `xml:"AggregationEvent"`
}

// ObjectEventExtension contains sourceList/destinationList that may be nested in extension
type ObjectEventExtension struct {
	SourceList      *SourceList      `xml:"sourceList"`
	DestinationList *DestinationList `xml:"destinationList"`
}

// ObjectEvent represents an EPCIS object event
// Note: sourceList/destinationList can be either at root level OR inside extension
type ObjectEvent struct {
	EventTime       string                `xml:"eventTime"`
	BizStep         string                `xml:"bizStep"`
	Disposition     string                `xml:"disposition"`
	EPCList         *EPCList              `xml:"epcList"`
	SourceList      *SourceList           `xml:"sourceList"`
	DestinationList *DestinationList      `xml:"destinationList"`
	Extension       *ObjectEventExtension `xml:"extension"`
}

// AggregationEvent represents an EPCIS aggregation event
type AggregationEvent struct {
	EventTime       string           `xml:"eventTime"`
	BizStep         string           `xml:"bizStep"`
	ParentID        string           `xml:"parentID"`
	ChildEPCs       *EPCList         `xml:"childEPCs"`
	SourceList      *SourceList      `xml:"sourceList"`
	DestinationList *DestinationList `xml:"destinationList"`
}

// EPCList contains EPC identifiers
type EPCList struct {
	EPC []string `xml:"epc"`
}

// SourceList contains source parties
type SourceList struct {
	Source []Party `xml:"source"`
}

// DestinationList contains destination parties
type DestinationList struct {
	Destination []Party `xml:"destination"`
}

// Party represents a source or destination party
type Party struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

// ExtractEPCISInboxData extracts shipping event data from EPCIS XML files.
// It parses the XML, finds shipping events, and extracts seller, buyer, ship_from, ship_to, etc.
func ExtractEPCISInboxData(ctx context.Context, cms *DirectusClient, xmlFiles []types.XMLFile) ([]EPCISInboxItem, error) {
	if len(xmlFiles) == 0 {
		logger.Info("No XML files to extract")
		return []EPCISInboxItem{}, nil
	}

	logger.Info("Extracting EPCIS inbox data", zap.Int("count", len(xmlFiles)))

	inboxItems := make([]EPCISInboxItem, 0)

	for i, xmlFile := range xmlFiles {
		logger.Info("Processing XML file",
			zap.Int("index", i+1),
			zap.Int("total", len(xmlFiles)),
			zap.String("filename", xmlFile.Filename),
		)

		items, err := extractFromXML(ctx, xmlFile, cms)
		if err != nil {
			logger.Error("Failed to extract from XML",
				zap.String("filename", xmlFile.Filename),
				zap.Error(err),
			)
			continue
		}

		inboxItems = append(inboxItems, items...)
		logger.Info("Extracted data",
			zap.String("filename", xmlFile.Filename),
			zap.Int("events", len(items)),
		)
	}

	logger.Info("Extraction complete", zap.Int("total_items", len(inboxItems)))
	return inboxItems, nil
}

// extractFromXML parses a single XML file and extracts inbox items
func extractFromXML(ctx context.Context, xmlFile types.XMLFile, cms *DirectusClient) ([]EPCISInboxItem, error) {
	// Parse XML
	var doc EPCISDocument
	if err := xml.Unmarshal(xmlFile.Content, &doc); err != nil {
		return nil, fmt.Errorf("parsing XML: %w", err)
	}

	// Extract location master data for name lookups
	locationsByGLN := make(map[string]ExtractedLocation)
	if doc.EPCISHeader != nil && doc.EPCISHeader.Extension != nil && doc.EPCISHeader.Extension.EPCISMasterData != nil {
		locations := extractExtractedLocation(doc.EPCISHeader.Extension.EPCISMasterData)
		for _, loc := range locations {
			locationsByGLN[loc.GLN] = loc
		}
		logger.Info("Extracted location master data", zap.Int("count", len(locationsByGLN)))
	}

	// Find shipping events (passing full event list for product extraction)
	shippingEvents := findShippingEvents(doc.EPCISBody.EventList)
	if len(shippingEvents) == 0 {
		logger.Warn("No shipping events found in file", zap.String("filename", xmlFile.Filename))
		return []EPCISInboxItem{}, nil
	}

	logger.Info("Found shipping events", zap.Int("count", len(shippingEvents)))

	// Extract products and containers from ALL events in the document (matching Mage behavior)
	products := extractProductsFromAllEvents(ctx, doc.EPCISBody.EventList, cms)
	containers := extractContainersFromAllEvents(doc.EPCISBody.EventList)

	logger.Info("Extracted from all events",
		zap.Int("products", len(products)),
		zap.Int("containers", len(containers)),
	)

	// Extract inbox data from each shipping event
	items := make([]EPCISInboxItem, 0, len(shippingEvents))

	for _, event := range shippingEvents {
		item := extractInboxDataFromEvent(event, xmlFile, locationsByGLN, products, containers)
		if item != nil {
			items = append(items, *item)
		}
	}

	return items, nil
}

// ShippingEvent represents a shipping event (can be ObjectEvent or AggregationEvent)
type ShippingEvent struct {
	EventTime       string
	SourceList      *SourceList
	DestinationList *DestinationList
	EPCList         *EPCList // For ObjectEvent
	ChildEPCs       *EPCList // For AggregationEvent
	ParentID        string   // For AggregationEvent (SSCC container)
}

// findShippingEvents finds all shipping events in the event list
// Bug fix #1: Check both root-level and extension-nested sourceList/destinationList
func findShippingEvents(eventList EventList) []ShippingEvent {
	events := make([]ShippingEvent, 0)

	// Check object events
	for _, objEvent := range eventList.ObjectEvents {
		if strings.Contains(strings.ToLower(objEvent.BizStep), "shipping") {
			// Bug fix #1: Get sourceList/destinationList from extension if not at root level
			sourceList := objEvent.SourceList
			destList := objEvent.DestinationList
			if sourceList == nil && objEvent.Extension != nil {
				sourceList = objEvent.Extension.SourceList
			}
			if destList == nil && objEvent.Extension != nil {
				destList = objEvent.Extension.DestinationList
			}

			events = append(events, ShippingEvent{
				EventTime:       objEvent.EventTime,
				SourceList:      sourceList,
				DestinationList: destList,
				EPCList:         objEvent.EPCList,
			})
		}
	}

	// Check aggregation events
	for _, aggEvent := range eventList.AggregationEvents {
		if strings.Contains(strings.ToLower(aggEvent.BizStep), "shipping") {
			events = append(events, ShippingEvent{
				EventTime:       aggEvent.EventTime,
				SourceList:      aggEvent.SourceList,
				DestinationList: aggEvent.DestinationList,
				ChildEPCs:       aggEvent.ChildEPCs,
				ParentID:        aggEvent.ParentID,
			})
		}
	}

	return events
}

// ExtractedLocation represents location information from EPCIS master data
type ExtractedLocation struct {
	GLN        string
	Name       string
	Address    string
	City       string
	State      string
	PostalCode string
}

// extractExtractedLocation extracts location information from EPCIS master data
func extractExtractedLocation(masterData *EPCISMasterData) []ExtractedLocation {
	locations := make([]ExtractedLocation, 0)

	if masterData.VocabularyList == nil {
		return locations
	}

	for _, vocab := range masterData.VocabularyList.Vocabulary {
		// Only process Location vocabularies
		if !strings.Contains(vocab.Type, "Location") {
			continue
		}

		if vocab.VocabularyElementList == nil {
			continue
		}

		for _, elem := range vocab.VocabularyElementList.VocabularyElement {
			gln := extractGLNFromURN(elem.ID)
			if gln == "" {
				continue
			}

			loc := ExtractedLocation{
				GLN: gln,
			}

			// Extract attributes
			addressParts := []string{}
			for _, attr := range elem.Attribute {
				value := strings.TrimSpace(attr.Value)
				if value == "" {
					continue
				}

				if strings.Contains(strings.ToLower(attr.ID), "name") && !strings.Contains(strings.ToLower(attr.ID), "party") {
					loc.Name = value
				} else if strings.Contains(attr.ID, "streetAddressOne") || strings.Contains(attr.ID, "streetAddress1") {
					addressParts = append([]string{value}, addressParts...) // Insert at beginning
				} else if strings.Contains(attr.ID, "streetAddressTwo") || strings.Contains(attr.ID, "streetAddress2") {
					if value != "" {
						addressParts = append(addressParts, value)
					}
				} else if strings.Contains(strings.ToLower(attr.ID), "city") {
					loc.City = value
				} else if strings.Contains(strings.ToLower(attr.ID), "state") || strings.Contains(strings.ToLower(attr.ID), "region") {
					loc.State = value
				} else if strings.Contains(attr.ID, "postalCode") || strings.Contains(attr.ID, "zipCode") {
					loc.PostalCode = value
				}
			}

			if len(addressParts) > 0 {
				loc.Address = strings.Join(addressParts, ", ")
			}

			// Use GLN as fallback for name
			if loc.Name == "" {
				loc.Name = gln
			}

			locations = append(locations, loc)
		}
	}

	return locations
}

// extractInboxDataFromEvent extracts epcis_inbox fields from a shipping event
func extractInboxDataFromEvent(
	event ShippingEvent,
	xmlFile types.XMLFile,
	locationsByGLN map[string]ExtractedLocation,
	products []map[string]interface{},
	containers []map[string]interface{},
) *EPCISInboxItem {
	// Find parties
	sellerGLN := findParty(event.SourceList, "owning_party")
	buyerGLN := findParty(event.DestinationList, "owning_party")
	shipFromGLN := findParty(event.SourceList, "location")
	shipToGLN := findParty(event.DestinationList, "location")

	// Parse event time
	var shipDate string
	if event.EventTime != "" {
		if t, err := time.Parse(time.RFC3339, event.EventTime); err == nil {
			shipDate = t.Format("2006-01-02")
		}
	}

	// Helper to format location display (matching Mage output format)
	formatLocation := func(gln string) string {
		if gln == "" {
			return ""
		}
		loc, exists := locationsByGLN[gln]
		if !exists {
			return gln
		}

		parts := []string{loc.Name}
		if loc.Address != "" && loc.Address != "Unknown" {
			parts = append(parts, loc.Address)
		}
		if loc.City != "" && loc.City != "Unknown" {
			parts = append(parts, loc.City)
		}
		if loc.State != "" && loc.State != "Unknown" {
			parts = append(parts, loc.State)
		}
		if loc.PostalCode != "" && loc.PostalCode != "00000" {
			parts = append(parts, loc.PostalCode)
		}
		parts = append(parts, fmt.Sprintf("GLN: %s", gln))

		return strings.Join(parts, "\n")
	}

	item := &EPCISInboxItem{
		Status:         "pending",
		Seller:         formatLocation(sellerGLN),
		Buyer:          formatLocation(buyerGLN),
		ShipFrom:       formatLocation(shipFromGLN),
		ShipTo:         formatLocation(shipToGLN),
		ShipDate:       shipDate,
		CaptureMessage: map[string]interface{}{"file_id": xmlFile.ID},
		RawMessage:     string(xmlFile.Content),
		EPCISXMLFileID: xmlFile.ID,
		Products:       products,
		Containers:     containers,
	}

	return item
}

// findParty finds a party of a specific type in a source/destination list
func findParty(list interface{}, partyType string) string {
	var parties []Party

	switch v := list.(type) {
	case *SourceList:
		if v != nil {
			parties = v.Source
		}
	case *DestinationList:
		if v != nil {
			parties = v.Destination
		}
	default:
		return ""
	}

	for _, party := range parties {
		if party.Type == partyType || strings.HasSuffix(party.Type, ":"+partyType) {
			return extractGLNFromURN(party.Value)
		}
	}

	return ""
}

// extractProductsFromAllEvents extracts products from ALL events in the document.
// IMPORTANT: Matches Mage behavior exactly:
// 1. Extract GTINs from epcList in ALL events (NOT childEPCs)
// 2. Extract GTINs from parentID in AggregationEvents (with quantity=1)
// 3. Aggregate by GTIN, summing quantities
func extractProductsFromAllEvents(ctx context.Context, eventList EventList, cms *DirectusClient) []map[string]interface{} {
	gtinCounts := make(map[string]int)
	gtinNames := make(map[string]string)

	// Extract from all ObjectEvents' epcList only (not childEPCs - matching Mage)
	for _, objEvent := range eventList.ObjectEvents {
		if objEvent.EPCList != nil {
			for _, epc := range objEvent.EPCList.EPC {
				gtin := extractGTINFromEPC(epc)
				if gtin != "" {
					gtinCounts[gtin]++
				}
			}
		}
	}

	// Extract from all AggregationEvents
	// IMPORTANT: Mage extracts from parentID (qty=1 each) but NOT from childEPCs
	for _, aggEvent := range eventList.AggregationEvents {
		// Extract GTIN from parentID if it's an SGTIN (case-level GTIN)
		// Add with quantity=1 (matching Mage behavior line 178)
		if aggEvent.ParentID != "" {
			gtin := extractGTINFromEPC(aggEvent.ParentID)
			if gtin != "" {
				gtinCounts[gtin]++
			}
		}
		// NOTE: Mage does NOT extract from childEPCs in the transformer
		// The childEPCs extraction is in event_master_data_extractor.py which is for outbound
	}

	// Query Directus for product names
	for gtin := range gtinCounts {
		// Default to GTIN as fallback
		gtinNames[gtin] = gtin

		// Try to query product collection for actual name
		if cms != nil {
			filter := map[string]interface{}{
				"gtin": map[string]interface{}{"_eq": gtin},
			}
			items, err := cms.QueryItems(ctx, "product", filter, []string{"gtin", "product_name"}, 1)
			if err == nil && len(items) > 0 {
				if productName, ok := items[0]["product_name"].(string); ok && productName != "" {
					gtinNames[gtin] = productName
					logger.Info("Found product name",
						zap.String("gtin", gtin),
						zap.String("product_name", productName),
					)
				}
			}
		}
	}

	// Build aggregated products list
	products := make([]map[string]interface{}, 0, len(gtinCounts))
	for gtin, count := range gtinCounts {
		products = append(products, map[string]interface{}{
			"GTIN":         gtin,
			"product_name": gtinNames[gtin],
			"quantity":     count,
		})
	}

	return products
}

// extractContainersFromAllEvents extracts containers (SSCCs) from ALL events in the document.
// Matches Mage behavior: extracts SSCCs from epcList and parentID
func extractContainersFromAllEvents(eventList EventList) []map[string]interface{} {
	ssccCounts := make(map[string]int)

	// Extract SSCCs from ObjectEvents' epcList
	for _, objEvent := range eventList.ObjectEvents {
		if objEvent.EPCList != nil {
			for _, epc := range objEvent.EPCList.EPC {
				sscc := extractSSCCFromEPC(epc)
				if sscc != "" {
					ssccCounts[sscc]++
				}
			}
		}
	}

	// Extract SSCCs from AggregationEvents' parentID (the container)
	for _, aggEvent := range eventList.AggregationEvents {
		if aggEvent.ParentID != "" {
			sscc := extractSSCCFromEPC(aggEvent.ParentID)
			if sscc != "" {
				ssccCounts[sscc]++
			}
		}
	}

	// Build aggregated containers list
	containers := make([]map[string]interface{}, 0, len(ssccCounts))
	for sscc, count := range ssccCounts {
		containers = append(containers, map[string]interface{}{
			"SSCC":  sscc,
			"count": count,
		})
	}

	return containers
}

// extractGLNFromURN extracts GLN from URN or Digital Link format.
// Returns a 13-digit GLN with proper GS1 check digit.
func extractGLNFromURN(value string) string {
	return ParseGLNFromSGLN(value)
}

// extractGTINFromEPC extracts GTIN from EPC URN or Digital Link format.
// Returns a 14-digit GTIN with proper GS1 check digit.
func extractGTINFromEPC(epc string) string {
	return ParseGTINFromSGTIN(epc)
}

// extractSSCCFromEPC extracts SSCC from EPC URN or Digital Link format.
// Bug fix #3: Returns 17-digit SSCC WITHOUT check digit to match Mage behavior.
func extractSSCCFromEPC(epc string) string {
	return ParseSSCCFromURNNoCheckDigit(epc)
}
