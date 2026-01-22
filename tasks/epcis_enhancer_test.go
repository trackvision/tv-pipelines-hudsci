package tasks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseGLNFromSGLNURN(t *testing.T) {
	tests := []struct {
		name     string
		urn      string
		expected string
	}{
		{
			name:     "valid URN",
			urn:      "urn:epc:id:sgln:1200180.29390.0",
			expected: "1200180293905", // Check digit calculated
		},
		{
			name:     "invalid format",
			urn:      "invalid-urn",
			expected: "",
		},
		{
			name:     "empty string",
			urn:      "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseGLNFromSGLNURN(tt.urn)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStripSerialFromSGTIN(t *testing.T) {
	tests := []struct {
		name     string
		urn      string
		expected string
	}{
		{
			name:     "valid SGTIN with serial",
			urn:      "urn:epc:id:sgtin:0372835.020102.ABC123",
			expected: "urn:epc:id:sgtin:0372835.020102",
		},
		{
			name:     "already without serial (returns base URN)",
			urn:      "urn:epc:id:sgtin:0372835.020102",
			expected: "urn:epc:id:sgtin:0372835.020102",
		},
		{
			name:     "invalid format",
			urn:      "invalid-urn",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripSerialFromSGTIN(tt.urn)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertToIDPat(t *testing.T) {
	urn := "urn:epc:id:sgtin:0372835.020102"
	expected := "urn:epc:idpat:sgtin:0372835.020102.*"
	result := convertToIDPat(urn)
	assert.Equal(t, expected, result)
}
