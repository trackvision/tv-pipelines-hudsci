package tasks

import (
	"testing"
)

func TestCalculateGS1CheckDigit(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		expected string
	}{
		{
			name:     "GTIN-14 base (13 digits)",
			base:     "0036846205016",
			expected: "3",
		},
		{
			name:     "GLN base (12 digits)",
			base:     "030001111111",
			expected: "6",
		},
		{
			name:     "SSCC base (17 digits)",
			base:     "03000112345678901",
			expected: "8",
		},
		{
			name:     "All zeros",
			base:     "0000000000000",
			expected: "0",
		},
		{
			name:     "Empty string",
			base:     "",
			expected: "",
		},
		{
			name:     "Known GTIN-13 (EAN-13)",
			base:     "590123412345",
			expected: "7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateGS1CheckDigit(tt.base)
			if result != tt.expected {
				t.Errorf("CalculateGS1CheckDigit(%q) = %q, want %q", tt.base, result, tt.expected)
			}
		})
	}
}

func TestParseGLNFromSGLN(t *testing.T) {
	tests := []struct {
		name     string
		sglnURN  string
		expected string
	}{
		{
			name:     "URN format with extension",
			sglnURN:  "urn:epc:id:sgln:030001.111111.0",
			expected: "0300011111116",
		},
		{
			name:     "URN format without extension",
			sglnURN:  "urn:epc:id:sgln:0614141.12345.0",
			expected: "0614141123452",
		},
		{
			name:     "Digital Link format",
			sglnURN:  "https://id.gs1.org/414/0300011111116",
			expected: "0300011111116",
		},
		{
			name:     "Digital Link with extension",
			sglnURN:  "https://id.gs1.org/414/0300011111116/254/0",
			expected: "0300011111116",
		},
		{
			name:     "Digital Link party GLN (417)",
			sglnURN:  "https://id.gs1.org/417/0300011111116",
			expected: "0300011111116",
		},
		{
			name:     "Empty string",
			sglnURN:  "",
			expected: "",
		},
		{
			name:     "Invalid format",
			sglnURN:  "not-a-valid-urn",
			expected: "",
		},
		{
			name:     "URN with only prefix (invalid)",
			sglnURN:  "urn:epc:id:sgln:030001",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseGLNFromSGLN(tt.sglnURN)
			if result != tt.expected {
				t.Errorf("ParseGLNFromSGLN(%q) = %q, want %q", tt.sglnURN, result, tt.expected)
			}
		})
	}
}

func TestParseGTINFromSGTIN(t *testing.T) {
	tests := []struct {
		name     string
		sgtinURN string
		expected string
	}{
		{
			// SGTIN: 0368462.050165 means:
			// - Company prefix: 0368462
			// - Indicator + ItemRef: 050165 (indicator=0, item_ref=50165)
			// - GTIN-13: 0 + 0368462 + 50165 = 0036846250165
			// - GTIN-14: 0036846250165 + check digit (8) = 00368462501658
			name:     "URN format with serial",
			sgtinURN: "urn:epc:id:sgtin:0368462.050165.123456",
			expected: "00368462501658",
		},
		{
			// SGTIN: 0614141.012345 means:
			// - Company prefix: 0614141
			// - Indicator + ItemRef: 012345 (indicator=0, item_ref=12345)
			// - GTIN-13: 0 + 0614141 + 12345 = 0061414112345
			// - GTIN-14: 0061414112345 + check digit (2) = 00614141123452
			name:     "URN format with different prefix length",
			sgtinURN: "urn:epc:id:sgtin:0614141.012345.12345",
			expected: "00614141123452",
		},
		{
			name:     "Digital Link format",
			sgtinURN: "https://id.gs1.org/01/00368462501655/21/123456",
			expected: "00368462501655",
		},
		{
			name:     "Digital Link format (GTIN only)",
			sgtinURN: "https://id.gs1.org/01/00368462501655",
			expected: "00368462501655",
		},
		{
			name:     "Empty string",
			sgtinURN: "",
			expected: "",
		},
		{
			name:     "Invalid format",
			sgtinURN: "not-a-valid-urn",
			expected: "",
		},
		{
			// idpat format (pattern URN) should also work
			name:     "idpat URN format",
			sgtinURN: "urn:epc:idpat:sgtin:0368462.050165.*",
			expected: "00368462501658",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseGTINFromSGTIN(tt.sgtinURN)
			if result != tt.expected {
				t.Errorf("ParseGTINFromSGTIN(%q) = %q, want %q", tt.sgtinURN, result, tt.expected)
			}
		})
	}
}

func TestParseSSCCFromURN(t *testing.T) {
	tests := []struct {
		name     string
		ssccURN  string
		expected string
	}{
		{
			name:     "URN format",
			ssccURN:  "urn:epc:id:sscc:030001.1234567890",
			expected: "003000112345678903",
		},
		{
			name:     "URN format with padding",
			ssccURN:  "urn:epc:id:sscc:0614141.1234567890",
			expected: "061414112345678902",
		},
		{
			name:     "Digital Link format",
			ssccURN:  "https://id.gs1.org/00/403000112345678901",
			expected: "403000112345678901",
		},
		{
			name:     "Empty string",
			ssccURN:  "",
			expected: "",
		},
		{
			name:     "Invalid format",
			ssccURN:  "not-a-valid-urn",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseSSCCFromURN(tt.ssccURN)
			if result != tt.expected {
				t.Errorf("ParseSSCCFromURN(%q) = %q, want %q", tt.ssccURN, result, tt.expected)
			}
		})
	}
}

func TestGS1StripSerialFromSGTIN(t *testing.T) {
	tests := []struct {
		name     string
		sgtinURN string
		expected string
	}{
		{
			name:     "URN with serial",
			sgtinURN: "urn:epc:id:sgtin:0368462.050165.123456",
			expected: "urn:epc:id:sgtin:0368462.050165",
		},
		{
			name:     "URN with long serial",
			sgtinURN: "urn:epc:id:sgtin:0614141.012345.ABC123DEF456",
			expected: "urn:epc:id:sgtin:0614141.012345",
		},
		{
			name:     "Empty string",
			sgtinURN: "",
			expected: "",
		},
		{
			name:     "Not an SGTIN URN",
			sgtinURN: "urn:epc:id:sscc:030001.1234567890",
			expected: "",
		},
		{
			name:     "URN without serial (invalid)",
			sgtinURN: "urn:epc:id:sgtin:0368462",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StripSerialFromSGTIN(tt.sgtinURN)
			if result != tt.expected {
				t.Errorf("StripSerialFromSGTIN(%q) = %q, want %q", tt.sgtinURN, result, tt.expected)
			}
		})
	}
}

func TestIsShippingBizStep(t *testing.T) {
	tests := []struct {
		name     string
		bizStep  string
		expected bool
	}{
		{
			name:     "Short form",
			bizStep:  "shipping",
			expected: true,
		},
		{
			name:     "CBV URN lowercase",
			bizStep:  "urn:epcglobal:cbv:bizstep:shipping",
			expected: true,
		},
		{
			name:     "CBV URN mixed case",
			bizStep:  "urn:epcglobal:cbv:bizstep:Shipping",
			expected: true,
		},
		{
			name:     "GS1 Digital Link",
			bizStep:  "https://ref.gs1.org/cbv/BizStep-shipping",
			expected: true,
		},
		{
			name:     "Receiving step",
			bizStep:  "receiving",
			expected: false,
		},
		{
			name:     "Other step",
			bizStep:  "packing",
			expected: false,
		},
		{
			name:     "Empty string",
			bizStep:  "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsShippingBizStep(tt.bizStep)
			if result != tt.expected {
				t.Errorf("IsShippingBizStep(%q) = %v, want %v", tt.bizStep, result, tt.expected)
			}
		})
	}
}

func TestIsReceivingBizStep(t *testing.T) {
	tests := []struct {
		name     string
		bizStep  string
		expected bool
	}{
		{
			name:     "Short form",
			bizStep:  "receiving",
			expected: true,
		},
		{
			name:     "CBV URN",
			bizStep:  "urn:epcglobal:cbv:bizstep:receiving",
			expected: true,
		},
		{
			name:     "GS1 Digital Link",
			bizStep:  "https://ref.gs1.org/cbv/BizStep-receiving",
			expected: true,
		},
		{
			name:     "Shipping step",
			bizStep:  "shipping",
			expected: false,
		},
		{
			name:     "Empty string",
			bizStep:  "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsReceivingBizStep(tt.bizStep)
			if result != tt.expected {
				t.Errorf("IsReceivingBizStep(%q) = %v, want %v", tt.bizStep, result, tt.expected)
			}
		})
	}
}

func TestNormalizeToLength(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		length   int
		expected string
	}{
		{
			name:     "Already correct length",
			input:    "12345",
			length:   5,
			expected: "12345",
		},
		{
			name:     "Needs padding",
			input:    "123",
			length:   5,
			expected: "00123",
		},
		{
			name:     "Needs truncation",
			input:    "1234567",
			length:   5,
			expected: "12345",
		},
		{
			name:     "Empty string",
			input:    "",
			length:   5,
			expected: "00000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeToLength(tt.input, tt.length)
			if result != tt.expected {
				t.Errorf("normalizeToLength(%q, %d) = %q, want %q", tt.input, tt.length, result, tt.expected)
			}
		})
	}
}
