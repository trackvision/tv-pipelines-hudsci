package tasks

import (
	"fmt"
	"strings"
)

// CalculateGS1CheckDigit calculates the GS1 check digit for a numeric string.
// The input should be the base identifier without the check digit.
// Returns the check digit as a single character string (0-9).
func CalculateGS1CheckDigit(base string) string {
	if base == "" {
		return ""
	}

	// GS1 check digit algorithm:
	// 1. Starting from the rightmost digit, alternate multipliers of 3 and 1
	// 2. Sum all products
	// 3. Check digit = (10 - (sum mod 10)) mod 10
	sum := 0
	for i := len(base) - 1; i >= 0; i-- {
		digit := int(base[i] - '0')
		if digit < 0 || digit > 9 {
			// Non-digit character, skip
			continue
		}
		// Position from right (0-indexed from right)
		posFromRight := len(base) - 1 - i
		if posFromRight%2 == 0 {
			// Even position from right: multiply by 3
			sum += digit * 3
		} else {
			// Odd position from right: multiply by 1
			sum += digit
		}
	}

	checkDigit := (10 - (sum % 10)) % 10
	return fmt.Sprintf("%d", checkDigit)
}

// ParseGLNFromSGLN extracts and calculates the 13-digit GLN from an SGLN URN.
// Input formats supported:
//   - urn:epc:id:sgln:CompanyPrefix.LocationRef.Extension
//   - https://id.gs1.org/414/GLN13/... (location GLN)
//   - https://id.gs1.org/417/GLN13/... (party GLN)
//
// Returns the 13-digit GLN with check digit.
func ParseGLNFromSGLN(sglnURN string) string {
	if sglnURN == "" {
		return ""
	}

	// Handle URN format: urn:epc:id:sgln:030001.111111.0
	// Also handles double-dot format: urn:epc:id:sgln:120018020383..0
	if parts, found := strings.CutPrefix(sglnURN, "urn:epc:id:sgln:"); found {
		segments := strings.Split(parts, ".")
		if len(segments) < 2 {
			return ""
		}

		// Concatenate company prefix and location ref (12 digits total)
		// Note: location ref may be empty for some GLNs (double-dot format)
		gln12 := segments[0] + segments[1]

		// Ensure exactly 12 digits by padding or truncating
		gln12 = normalizeToLength(gln12, 12)

		// Calculate and append check digit
		return gln12 + CalculateGS1CheckDigit(gln12)
	}

	// Handle Digital Link format for location GLN (414) and party GLN (417)
	// https://id.gs1.org/414/0300011111116
	// https://id.gs1.org/417/0300011111116
	for _, ai := range []string{"/414/", "/417/"} {
		if strings.Contains(sglnURN, ai) {
			parts := strings.Split(sglnURN, ai)
			if len(parts) > 1 {
				gln := parts[1]
				// Remove any trailing path elements
				if idx := strings.Index(gln, "/"); idx > 0 {
					gln = gln[:idx]
				}
				// Digital Link already contains check digit (13 digits)
				if len(gln) == 13 {
					return gln
				}
			}
		}
	}

	return ""
}

// ParseGTINFromSGTIN extracts and calculates the 14-digit GTIN from an SGTIN URN.
// Input formats supported:
//   - urn:epc:id:sgtin:CompanyPrefix.ItemRef.Serial
//   - urn:epc:idpat:sgtin:CompanyPrefix.ItemRef.* (pattern format)
//   - https://id.gs1.org/01/GTIN14/...
//
// Returns the 14-digit GTIN with check digit.
func ParseGTINFromSGTIN(sgtinURN string) string {
	if sgtinURN == "" {
		return ""
	}

	// Handle URN format: urn:epc:id:sgtin:0368462.050165.123456
	// Also handle idpat format: urn:epc:idpat:sgtin:0368462.050165.*
	var parts string
	var found bool
	if parts, found = strings.CutPrefix(sgtinURN, "urn:epc:id:sgtin:"); !found {
		parts, found = strings.CutPrefix(sgtinURN, "urn:epc:idpat:sgtin:")
	}

	if found {
		segments := strings.Split(parts, ".")
		if len(segments) < 2 {
			return ""
		}

		companyPrefix := segments[0]
		indicatorAndItemRef := segments[1]

		// SGTIN structure: CompanyPrefix.IndicatorAndItemRef.Serial
		// The IndicatorAndItemRef contains the indicator digit as the FIRST character
		// followed by the item reference.
		//
		// To reconstruct GTIN-14:
		// 1. Extract indicator digit (first char of item ref field)
		// 2. Extract item reference (remaining chars)
		// 3. GTIN-13 = indicator + company prefix + item reference
		// 4. Add check digit for GTIN-14

		indicator := "0"
		itemRef := indicatorAndItemRef
		if len(indicatorAndItemRef) > 0 {
			indicator = indicatorAndItemRef[0:1]
			itemRef = indicatorAndItemRef[1:]
		}

		// Build GTIN-13 (without check digit): indicator + company prefix + item ref
		gtin13 := indicator + companyPrefix + itemRef

		// Ensure exactly 13 digits before check digit
		gtin13 = normalizeToLength(gtin13, 13)

		// Calculate and append check digit
		return gtin13 + CalculateGS1CheckDigit(gtin13)
	}

	// Handle Digital Link format: https://id.gs1.org/01/00368462501658
	if strings.Contains(sgtinURN, "/01/") {
		parts := strings.Split(sgtinURN, "/01/")
		if len(parts) > 1 {
			gtin := parts[1]
			// Remove any trailing path elements (like serial /21/xxx)
			if idx := strings.Index(gtin, "/"); idx > 0 {
				gtin = gtin[:idx]
			}
			// Digital Link already contains check digit (14 digits)
			if len(gtin) >= 14 {
				return gtin[:14]
			}
		}
	}

	return ""
}

// ParseSSCCFromURN extracts and calculates the 18-digit SSCC from an SSCC URN.
// Input formats supported:
//   - urn:epc:id:sscc:CompanyPrefix.SerialRef
//   - https://id.gs1.org/00/SSCC18
//
// Returns the 18-digit SSCC with check digit.
func ParseSSCCFromURN(ssccURN string) string {
	if ssccURN == "" {
		return ""
	}

	// Handle URN format: urn:epc:id:sscc:030001.1234567890
	if parts, found := strings.CutPrefix(ssccURN, "urn:epc:id:sscc:"); found {
		segments := strings.Split(parts, ".")
		if len(segments) < 2 {
			return ""
		}

		// SSCC = extension digit + company prefix + serial reference
		// Total: 17 digits + check digit = 18 digits
		sscc17 := segments[0] + segments[1]

		// Ensure exactly 17 digits before check digit
		sscc17 = normalizeToLength(sscc17, 17)

		// Calculate and append check digit
		return sscc17 + CalculateGS1CheckDigit(sscc17)
	}

	// Handle Digital Link format: https://id.gs1.org/00/403000112345678901
	if strings.Contains(ssccURN, "/00/") {
		parts := strings.Split(ssccURN, "/00/")
		if len(parts) > 1 {
			sscc := parts[1]
			// Remove any trailing path elements
			if idx := strings.Index(sscc, "/"); idx > 0 {
				sscc = sscc[:idx]
			}
			// Digital Link already contains check digit (18 digits)
			if len(sscc) >= 18 {
				return sscc[:18]
			}
		}
	}

	return ""
}

// StripSerialFromSGTIN removes the serial number from an SGTIN URN.
// Returns the base URN (company prefix + item reference) without serial.
// Example: urn:epc:id:sgtin:0368462.050165.123456 -> urn:epc:id:sgtin:0368462.050165
func StripSerialFromSGTIN(sgtinURN string) string {
	if !strings.HasPrefix(sgtinURN, "urn:epc:id:sgtin:") {
		return ""
	}

	parts := strings.TrimPrefix(sgtinURN, "urn:epc:id:sgtin:")
	segments := strings.Split(parts, ".")

	// Need at least company prefix and item reference
	if len(segments) < 2 {
		return ""
	}

	// Return URN with only company prefix and item reference (no serial)
	return fmt.Sprintf("urn:epc:id:sgtin:%s.%s", segments[0], segments[1])
}

// IsShippingBizStep checks if a bizStep value represents a shipping step.
// Handles multiple formats:
//   - "shipping" (short form)
//   - "urn:epcglobal:cbv:bizstep:shipping" (CBV URN)
//   - "https://ref.gs1.org/cbv/BizStep-shipping" (GS1 Digital Link)
func IsShippingBizStep(bizStep string) bool {
	if bizStep == "" {
		return false
	}

	bizStepLower := strings.ToLower(bizStep)

	return bizStepLower == "shipping" ||
		strings.HasSuffix(bizStepLower, ":shipping") ||
		strings.HasSuffix(bizStepLower, "bizstep-shipping")
}

// IsReceivingBizStep checks if a bizStep value represents a receiving step.
// Handles multiple formats similar to IsShippingBizStep.
func IsReceivingBizStep(bizStep string) bool {
	if bizStep == "" {
		return false
	}

	bizStepLower := strings.ToLower(bizStep)

	return bizStepLower == "receiving" ||
		strings.HasSuffix(bizStepLower, ":receiving") ||
		strings.HasSuffix(bizStepLower, "bizstep-receiving")
}

// normalizeToLength pads or truncates a string to the specified length.
// Pads with leading zeros if too short, truncates from the right if too long.
func normalizeToLength(s string, length int) string {
	if len(s) < length {
		return strings.Repeat("0", length-len(s)) + s
	}
	if len(s) > length {
		return s[:length]
	}
	return s
}
