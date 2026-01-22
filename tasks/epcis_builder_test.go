package tasks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildEPCISJSONDocument(t *testing.T) {
	events := []map[string]interface{}{
		{
			"type":         "ObjectEvent",
			"eventTime":    "2024-01-15T10:00:00Z",
			"bizStep":      "commissioning",
			"eventID":      "event-1",
			"epcList":      []interface{}{"urn:epc:id:sgtin:0372835.020102.ABC123"},
		},
		{
			"type":         "ObjectEvent",
			"eventTime":    "2024-01-15T11:00:00Z",
			"bizStep":      "shipping",
			"eventID":      "event-2",
			"epcList":      []interface{}{"urn:epc:id:sgtin:0372835.020102.ABC123"},
		},
	}

	doc := buildEPCISJSONDocument(events)

	assert.NotNil(t, doc)
	assert.Equal(t, "EPCISDocument", doc["type"])
	assert.Equal(t, "2.0", doc["schemaVersion"])

	epcisBody, ok := doc["epcisBody"].(map[string]interface{})
	assert.True(t, ok)

	eventList, ok := epcisBody["eventList"].([]map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, 2, len(eventList))
}

func TestBuildEPCISJSONDocumentFiltersReceivingEvents(t *testing.T) {
	events := []map[string]interface{}{
		{
			"type":         "ObjectEvent",
			"eventTime":    "2024-01-15T10:00:00Z",
			"bizStep":      "commissioning",
			"eventID":      "event-1",
		},
		{
			"type":         "ObjectEvent",
			"eventTime":    "2024-01-15T11:00:00Z",
			"bizStep":      "receiving", // Should be filtered out
			"eventID":      "event-2",
		},
		{
			"type":         "ObjectEvent",
			"eventTime":    "2024-01-15T12:00:00Z",
			"bizStep":      "shipping",
			"eventID":      "event-3",
		},
	}

	doc := buildEPCISJSONDocument(events)

	epcisBody := doc["epcisBody"].(map[string]interface{})
	eventList := epcisBody["eventList"].([]map[string]interface{})

	// Only 2 events should remain (commissioning and shipping)
	assert.Equal(t, 2, len(eventList))

	// Verify receiving event was filtered out
	for _, event := range eventList {
		assert.NotEqual(t, "receiving", event["bizStep"])
	}
}
