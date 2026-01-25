package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trackvision/tv-pipelines-hudsci/types"
)

func TestExtractEPCISInboxData(t *testing.T) {
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<EPCISDocument xmlns="urn:epcglobal:epcis:xsd:2">
  <EPCISBody>
    <EventList>
      <ObjectEvent>
        <eventTime>2024-01-15T10:00:00Z</eventTime>
        <bizStep>urn:epcglobal:cbv:bizstep:shipping</bizStep>
        <epcList>
          <epc>urn:epc:id:sgtin:0368462.050165.123456</epc>
        </epcList>
        <sourceList>
          <source type="owning_party">urn:epc:id:sgln:030001.111111.0</source>
          <source type="location">urn:epc:id:sgln:030001.222222.0</source>
        </sourceList>
        <destinationList>
          <destination type="owning_party">urn:epc:id:sgln:030002.111111.0</destination>
          <destination type="location">urn:epc:id:sgln:030002.222222.0</destination>
        </destinationList>
      </ObjectEvent>
    </EventList>
  </EPCISBody>
</EPCISDocument>`

	xmlFiles := []types.XMLFile{
		{
			ID:       "xml123",
			Filename: "test.xml",
			Content:  []byte(xmlContent),
			Uploaded: time.Now(),
		},
	}

	// Extract without Directus client (will use GLNs as-is)
	items, err := ExtractEPCISInboxData(context.Background(), nil, xmlFiles)
	require.NoError(t, err)
	assert.Len(t, items, 1)

	item := items[0]
	assert.Equal(t, "pending", item.Status)
	assert.Contains(t, item.Seller, "0300011111116") // GLN from owning_party source (with check digit)
	assert.Contains(t, item.Buyer, "0300021111113")  // GLN from owning_party destination (with check digit)
	assert.Contains(t, item.ShipFrom, "0300012222224") // GLN from location source (with check digit)
	assert.Contains(t, item.ShipTo, "0300022222221")   // GLN from location destination (with check digit)
	assert.Equal(t, "2024-01-15", item.ShipDate)
	assert.Equal(t, "xml123", item.EPCISXMLFileID)

	// Check products extracted
	// SGTIN 0368462.050165 = indicator(0) + company(0368462) + itemRef(50165) = GTIN-13: 0036846250165
	// With check digit: 00368462501658
	assert.Len(t, item.Products, 1)
	assert.Equal(t, "00368462501658", item.Products[0]["GTIN"]) // GTIN with calculated check digit
	assert.Equal(t, 1, item.Products[0]["quantity"])
}

// TestExtractEPCISInboxData_ExtensionNesting tests Bug fix #1: sourceList/destinationList inside extension
func TestExtractEPCISInboxData_ExtensionNesting(t *testing.T) {
	// This XML has sourceList/destinationList inside <extension> tags (like DSCSAExample.xml)
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<EPCISDocument xmlns="urn:epcglobal:epcis:xsd:2">
  <EPCISBody>
    <EventList>
      <ObjectEvent>
        <eventTime>2023-04-01T07:48:16Z</eventTime>
        <bizStep>urn:epcglobal:cbv:bizstep:shipping</bizStep>
        <epcList>
          <epc>urn:epc:id:sscc:030001.41234567890</epc>
        </epcList>
        <extension>
          <sourceList>
            <source type="urn:epcglobal:cbv:sdt:owning_party">urn:epc:id:sgln:030001.111111.0</source>
            <source type="urn:epcglobal:cbv:sdt:location">urn:epc:id:sgln:030001.111121.0</source>
          </sourceList>
          <destinationList>
            <destination type="urn:epcglobal:cbv:sdt:owning_party">urn:epc:id:sgln:039999.999999.0</destination>
            <destination type="urn:epcglobal:cbv:sdt:location">urn:epc:id:sgln:039999.345678.0</destination>
          </destinationList>
        </extension>
      </ObjectEvent>
    </EventList>
  </EPCISBody>
</EPCISDocument>`

	xmlFiles := []types.XMLFile{
		{
			ID:       "xml-extension-test",
			Filename: "test-extension.xml",
			Content:  []byte(xmlContent),
			Uploaded: time.Now(),
		},
	}

	items, err := ExtractEPCISInboxData(context.Background(), nil, xmlFiles)
	require.NoError(t, err)
	assert.Len(t, items, 1)

	item := items[0]
	// Verify parties were extracted from extension
	assert.Contains(t, item.Seller, "0300011111116", "Seller GLN should be extracted from extension/sourceList")
	assert.Contains(t, item.Buyer, "0399999999991", "Buyer GLN should be extracted from extension/destinationList")
	assert.Contains(t, item.ShipFrom, "0300011111215", "ShipFrom GLN should be extracted from extension/sourceList")
	assert.Contains(t, item.ShipTo, "0399993456780", "ShipTo GLN should be extracted from extension/destinationList")

	// Verify SSCC container was extracted (17 digits, no check digit per Bug fix #3)
	assert.Len(t, item.Containers, 1)
	assert.Equal(t, "03000141234567890", item.Containers[0]["SSCC"], "SSCC should be 17 digits without check digit")
}

// TestExtractEPCISInboxData_ProductsFromAllEvents tests Bug fix #2: products from all events
// IMPORTANT: Matches Mage behavior - extracts from epcList ONLY, not childEPCs
func TestExtractEPCISInboxData_ProductsFromAllEvents(t *testing.T) {
	// This XML simulates the DSCSAExample structure:
	// - Commissioning events with SGTINs in epcList (products)
	// - Aggregation events with parentID (case SGTIN) - Mage adds qty=1 for parentID
	// - Aggregation childEPCs are NOT extracted by Mage inbound transformer
	// - Shipping event with only the pallet SSCC
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<EPCISDocument xmlns="urn:epcglobal:epcis:xsd:2">
  <EPCISBody>
    <EventList>
      <!-- Commissioning: 4 product units (extracted from epcList) -->
      <ObjectEvent>
        <eventTime>2023-03-27T06:45:16Z</eventTime>
        <bizStep>urn:epcglobal:cbv:bizstep:commissioning</bizStep>
        <epcList>
          <epc>urn:epc:id:sgtin:030001.0012345.11</epc>
          <epc>urn:epc:id:sgtin:030001.0012345.12</epc>
          <epc>urn:epc:id:sgtin:030001.0012345.13</epc>
          <epc>urn:epc:id:sgtin:030001.0012345.14</epc>
        </epcList>
      </ObjectEvent>
      <!-- Aggregation: pack products into case (parentID extracted, childEPCs NOT extracted by Mage) -->
      <AggregationEvent>
        <eventTime>2023-03-27T06:50:16Z</eventTime>
        <bizStep>urn:epcglobal:cbv:bizstep:packing</bizStep>
        <parentID>urn:epc:id:sgtin:030001.1012345.110</parentID>
        <childEPCs>
          <epc>urn:epc:id:sgtin:030001.0012345.11</epc>
          <epc>urn:epc:id:sgtin:030001.0012345.12</epc>
          <epc>urn:epc:id:sgtin:030001.0012345.13</epc>
          <epc>urn:epc:id:sgtin:030001.0012345.14</epc>
        </childEPCs>
      </AggregationEvent>
      <!-- Aggregation: pack case into pallet (parentID is SSCC, childEPCs has case SGTIN) -->
      <AggregationEvent>
        <eventTime>2023-04-01T06:48:16Z</eventTime>
        <bizStep>urn:epcglobal:cbv:bizstep:packing</bizStep>
        <parentID>urn:epc:id:sscc:030001.41234567890</parentID>
        <childEPCs>
          <epc>urn:epc:id:sgtin:030001.1012345.110</epc>
        </childEPCs>
      </AggregationEvent>
      <!-- Shipping: only has the pallet SSCC -->
      <ObjectEvent>
        <eventTime>2023-04-01T07:48:16Z</eventTime>
        <bizStep>urn:epcglobal:cbv:bizstep:shipping</bizStep>
        <epcList>
          <epc>urn:epc:id:sscc:030001.41234567890</epc>
        </epcList>
        <extension>
          <sourceList>
            <source type="urn:epcglobal:cbv:sdt:owning_party">urn:epc:id:sgln:030001.111111.0</source>
          </sourceList>
          <destinationList>
            <destination type="urn:epcglobal:cbv:sdt:owning_party">urn:epc:id:sgln:039999.999999.0</destination>
          </destinationList>
        </extension>
      </ObjectEvent>
    </EventList>
  </EPCISBody>
</EPCISDocument>`

	xmlFiles := []types.XMLFile{
		{
			ID:       "xml-products-test",
			Filename: "test-products.xml",
			Content:  []byte(xmlContent),
			Uploaded: time.Now(),
		},
	}

	items, err := ExtractEPCISInboxData(context.Background(), nil, xmlFiles)
	require.NoError(t, err)
	assert.Len(t, items, 1)

	item := items[0]

	// Products extracted (Mage behavior):
	// 1. From commissioning epcList: 4 SGTINs of GTIN 00300010123455
	// 2. From first aggregation parentID: 1 GTIN 10300010123452 (case)
	// 3. From second aggregation parentID: SSCC (not a GTIN, ignored)
	// 4. childEPCs are NOT extracted by Mage inbound transformer
	// Total: 2 unique GTINs

	assert.Len(t, item.Products, 2, "Should extract 2 unique GTINs")

	// Find the product GTINs
	productGTINs := make(map[string]int)
	for _, p := range item.Products {
		gtin := p["GTIN"].(string)
		productGTINs[gtin] = p["quantity"].(int)
	}

	// Verify we have the expected GTINs
	assert.Contains(t, productGTINs, "00300010123455", "Should have product GTIN from commissioning epcList")
	assert.Contains(t, productGTINs, "10300010123452", "Should have case GTIN from aggregation parentID")

	// Verify quantities match Mage:
	// - Product GTIN: 4 (only from commissioning epcList, childEPCs NOT counted)
	// - Case GTIN: 1 (from first aggregation parentID only)
	assert.Equal(t, 4, productGTINs["00300010123455"], "Product GTIN should have quantity 4 (from commissioning epcList)")
	assert.Equal(t, 1, productGTINs["10300010123452"], "Case GTIN should have quantity 1 (from parentID)")

	// Verify SSCC container (17 digits, no check digit)
	assert.GreaterOrEqual(t, len(item.Containers), 1, "Should have at least 1 container (the pallet SSCC)")

	// Verify SSCC format (17 digits, no check digit)
	for _, c := range item.Containers {
		sscc := c["SSCC"].(string)
		assert.Len(t, sscc, 17, "SSCC should be 17 digits without check digit")
	}
}

func TestExtractGLNFromURN(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "URN format",
			input:    "urn:epc:id:sgln:030001.111111.0",
			expected: "0300011111116", // 13-digit GLN with check digit
		},
		{
			name:     "Digital Link format",
			input:    "https://id.gs1.org/414/0300011111116",
			expected: "0300011111116",
		},
		{
			name:     "Digital Link with trailing path",
			input:    "https://id.gs1.org/414/0300011111116/other",
			expected: "0300011111116",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractGLNFromURN(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractGTINFromEPC(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			// SGTIN 0368462.050165 = indicator(0) + company(0368462) + itemRef(50165)
			// GTIN-13: 0036846250165, GTIN-14 with check digit: 00368462501658
			name:     "URN format",
			input:    "urn:epc:id:sgtin:0368462.050165.123456",
			expected: "00368462501658", // 14-digit GTIN with check digit
		},
		{
			name:     "Digital Link format",
			input:    "https://id.gs1.org/01/00368462501658",
			expected: "00368462501658",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractGTINFromEPC(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractSSCCFromEPC(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			// Bug fix #3: Returns 17 digits WITHOUT check digit to match Mage
			name:     "URN format - 17 digits no check digit",
			input:    "urn:epc:id:sscc:030001.1234567890",
			expected: "00300011234567890", // 17-digit SSCC WITHOUT check digit
		},
		{
			// Digital Link has 18 digits, we strip the check digit
			name:     "Digital Link format - strips check digit",
			input:    "https://id.gs1.org/00/403000112345678901",
			expected: "40300011234567890", // First 17 digits
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			// Real SSCC from DSCSAExample.xml
			name:     "DSCSAExample SSCC",
			input:    "urn:epc:id:sscc:030001.41234567890",
			expected: "03000141234567890", // 17 digits
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractSSCCFromEPC(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractEPCISInboxData_MultipleEvents(t *testing.T) {
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<EPCISDocument xmlns="urn:epcglobal:epcis:xsd:2">
  <EPCISBody>
    <EventList>
      <ObjectEvent>
        <eventTime>2024-01-15T10:00:00Z</eventTime>
        <bizStep>urn:epcglobal:cbv:bizstep:shipping</bizStep>
        <epcList>
          <epc>urn:epc:id:sgtin:0368462.050165.123456</epc>
        </epcList>
        <sourceList>
          <source type="owning_party">urn:epc:id:sgln:030001.111111.0</source>
        </sourceList>
        <destinationList>
          <destination type="owning_party">urn:epc:id:sgln:030002.111111.0</destination>
        </destinationList>
      </ObjectEvent>
      <ObjectEvent>
        <eventTime>2024-01-15T11:00:00Z</eventTime>
        <bizStep>urn:epcglobal:cbv:bizstep:shipping</bizStep>
        <epcList>
          <epc>urn:epc:id:sgtin:0368462.050165.789012</epc>
        </epcList>
        <sourceList>
          <source type="owning_party">urn:epc:id:sgln:030003.111111.0</source>
        </sourceList>
        <destinationList>
          <destination type="owning_party">urn:epc:id:sgln:030004.111111.0</destination>
        </destinationList>
      </ObjectEvent>
    </EventList>
  </EPCISBody>
</EPCISDocument>`

	xmlFiles := []types.XMLFile{
		{
			ID:       "xml123",
			Filename: "test.xml",
			Content:  []byte(xmlContent),
		},
	}

	items, err := ExtractEPCISInboxData(context.Background(), nil, xmlFiles)
	require.NoError(t, err)
	assert.Len(t, items, 2, "Should extract 2 shipping events")

	// Both events share the same products (extracted from all events)
	// Products are aggregated across the entire document
	assert.Equal(t, len(items[0].Products), len(items[1].Products), "Both events should have same products")
}

func TestExtractEPCISInboxData_NoShippingEvents(t *testing.T) {
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<EPCISDocument xmlns="urn:epcglobal:epcis:xsd:2">
  <EPCISBody>
    <EventList>
      <ObjectEvent>
        <eventTime>2024-01-15T10:00:00Z</eventTime>
        <bizStep>urn:epcglobal:cbv:bizstep:commissioning</bizStep>
        <epcList>
          <epc>urn:epc:id:sgtin:0368462.050165.123456</epc>
        </epcList>
      </ObjectEvent>
    </EventList>
  </EPCISBody>
</EPCISDocument>`

	xmlFiles := []types.XMLFile{
		{
			ID:       "xml123",
			Filename: "test.xml",
			Content:  []byte(xmlContent),
		},
	}

	items, err := ExtractEPCISInboxData(context.Background(), nil, xmlFiles)
	require.NoError(t, err)
	assert.Len(t, items, 0, "Should not extract any items when no shipping events")
}
