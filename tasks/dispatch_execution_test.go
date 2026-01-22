package tasks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCapitalizeStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "lowercase",
			input:    "failed",
			expected: "Failed",
		},
		{
			name:     "already capitalized",
			input:    "Failed",
			expected: "Failed",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := capitalizeStatus(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDispatchResultStructure(t *testing.T) {
	result := DispatchResult{
		ShippingOperationID: "ship-123",
		DispatchRecordID:    "disp-456",
		Status:              "sent",
		TrustMedUUID:        "uuid-789",
	}

	assert.Equal(t, "ship-123", result.ShippingOperationID)
	assert.Equal(t, "sent", result.Status)
	assert.Equal(t, "uuid-789", result.TrustMedUUID)
	assert.Empty(t, result.ErrorMessage)
}
