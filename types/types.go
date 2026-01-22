package types

import "time"

// PipelineResult holds the result of a pipeline execution
type PipelineResult struct {
	Success      bool              `json:"success"`
	Message      string            `json:"message"`
	ItemsCreated int               `json:"items_created"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// XMLFile represents an XML file from Directus
type XMLFile struct {
	ID       string    `json:"id"`
	Filename string    `json:"filename"`
	Content  []byte    `json:"content"`
	Uploaded time.Time `json:"uploaded"`
}

// ConvertedFile represents a converted JSON file
type ConvertedFile struct {
	SourceID   string `json:"source_id"`
	Filename   string `json:"filename"`
	JSONData   []byte `json:"json_data"`
	XMLContent []byte `json:"xml_content"`
}

// InboundShipment represents extracted shipping data from EPCIS XML
type InboundShipment struct {
	SourceFileID     string    `json:"source_file_id"`
	EventID          string    `json:"event_id"`
	EventTime        time.Time `json:"event_time"`
	BusinessStep     string    `json:"business_step"`
	Disposition      string    `json:"disposition"`
	ReadPoint        string    `json:"read_point"`
	BizLocation      string    `json:"biz_location"`
	ShipFrom         string    `json:"ship_from"`
	ShipTo           string    `json:"ship_to"`
	Products         []Product `json:"products"`
	ContainerNumbers []string  `json:"container_numbers"`
}

// Product represents a product in the shipment
type Product struct {
	EPC         string `json:"epc"`
	GTIN        string `json:"gtin"`
	SerialNo    string `json:"serial_no"`
	LotNo       string `json:"lot_no"`
	ExpiryDate  string `json:"expiry_date"`
	ProductName string `json:"product_name"`
}

// ApprovedShipment represents a shipment ready for dispatch
type ApprovedShipment struct {
	ID              string    `json:"id"`
	CaptureID       string    `json:"capture_id"`
	Status          string    `json:"status"`
	ApprovedAt      time.Time `json:"approved_at"`
	DispatchAttempt int       `json:"dispatch_attempt"`
}

// ShipmentWithEvents represents a shipment with its related events
type ShipmentWithEvents struct {
	Shipment ApprovedShipment `json:"shipment"`
	Events   []EPCISEvent     `json:"events"`
}

// EPCISEvent represents a single EPCIS event
type EPCISEvent struct {
	EventID      string                 `json:"event_id"`
	EventType    string                 `json:"event_type"`
	EventTime    time.Time              `json:"event_time"`
	BusinessStep string                 `json:"business_step"`
	Disposition  string                 `json:"disposition"`
	EventBody    map[string]interface{} `json:"event_body"`
}

// EPCISDocument represents an EPCIS 2.0 JSON-LD document
type EPCISDocument struct {
	ShipmentID    string                   `json:"shipment_id"`
	DocumentID    string                   `json:"document_id"`
	Context       []string                 `json:"@context"`
	Type          string                   `json:"type"`
	EventList     []map[string]interface{} `json:"eventList"`
	JSONContent   []byte                   `json:"json_content"`
	XMLContent    []byte                   `json:"xml_content"`
	CreatedAt     time.Time                `json:"created_at"`
}

// EnhancedDocument represents an EPCIS document with SBDH headers
type EnhancedDocument struct {
	ShipmentID  string    `json:"shipment_id"`
	DocumentID  string    `json:"document_id"`
	XMLContent  []byte    `json:"xml_content"`
	JSONContent []byte    `json:"json_content"`
	FileID      string    `json:"file_id"`
	CreatedAt   time.Time `json:"created_at"`
}

// DispatchResult represents the result of a TrustMed dispatch
type DispatchResult struct {
	ShipmentID      string    `json:"shipment_id"`
	DocumentID      string    `json:"document_id"`
	Success         bool      `json:"success"`
	Status          string    `json:"status"`
	TransactionID   string    `json:"transaction_id"`
	ErrorMessage    string    `json:"error_message,omitempty"`
	DispatchedAt    time.Time `json:"dispatched_at"`
	ConfirmedAt     time.Time `json:"confirmed_at,omitempty"`
	RetryCount      int       `json:"retry_count"`
}
