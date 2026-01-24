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
			name:     "URN format",
			input:    "urn:epc:id:sscc:030001.1234567890",
			expected: "003000112345678903", // 18-digit SSCC with check digit
		},
		{
			name:     "Digital Link format",
			input:    "https://id.gs1.org/00/403000112345678901",
			expected: "403000112345678901",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
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

	// Each shipping event has its OWN products extracted (matching Mage behavior)
	// Products are NOT aggregated across events
	assert.Len(t, items[0].Products, 1, "Event 1 should have 1 product")
	assert.Len(t, items[1].Products, 1, "Event 2 should have 1 product")

	// Each event has quantity=1 because each has only 1 EPC in its epcList
	assert.Equal(t, 1, items[0].Products[0]["quantity"], "Event 1 quantity from its own epcList")
	assert.Equal(t, 1, items[1].Products[0]["quantity"], "Event 2 quantity from its own epcList")
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
