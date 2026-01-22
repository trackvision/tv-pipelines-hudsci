package types

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPipelineResultJSON(t *testing.T) {
	result := PipelineResult{
		Success:      true,
		Message:      "Pipeline completed",
		ItemsCreated: 5,
		Metadata:     map[string]string{"key": "value"},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded PipelineResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.Success != result.Success {
		t.Errorf("Success = %v, want %v", decoded.Success, result.Success)
	}
}

func TestInboundShipmentJSON(t *testing.T) {
	shipment := InboundShipment{
		SourceFileID: "file-123",
		EventID:      "event-456",
		EventTime:    time.Now(),
		BusinessStep: "shipping",
		Products: []Product{
			{
				EPC:      "urn:epc:id:sgtin:0614141.107346.2017",
				GTIN:     "00614141107346",
				SerialNo: "2017",
			},
		},
	}

	data, err := json.Marshal(shipment)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	var decoded InboundShipment
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if decoded.EventID != shipment.EventID {
		t.Errorf("EventID = %v, want %v", decoded.EventID, shipment.EventID)
	}
}
