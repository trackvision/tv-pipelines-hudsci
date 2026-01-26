# EPCIS XML Output Fixes - Customer Feedback

## Summary

Three changes requested by customer for EPCIS XML output:
1. Change SBDH Authority attribute from "SGLN" to "GS1"
2. Use brand.brand_name for manufacturerOfTradeItemPartyName when available
3. Remove redundant namespace declarations from ILMD elements

## Analysis

### Current State

The outbound pipeline builds EPCIS 2.0 JSON-LD, converts to XML via external converter, then enhances with SBDH/master data in `epcis_enhancer.go`.

**Issue 1** - Authority="SGLN" hardcoded in `addSBDH()`:
```go
// tasks/epcis_enhancer.go:283, 288
senderID.CreateAttr("Authority", "SGLN")
receiverID.CreateAttr("Authority", "SGLN")
```

**Issue 2** - manufacturerOfTradeItemPartyName uses organisation only:
```go
// tasks/epcis_enhancer.go:558-569
items, err := cms.QueryItems(ctx, "product", filter, []string{
    "gtin", "urn", "product_name", "ndc",
    "product_manufacturer.organisation_name",  // only source currently
    ...
}, 1)
```

**Issue 3** - ILMD gets redundant namespace per element:
```xml
<!-- Current output -->
<ilmd>
  <cbvmda:itemExpirationDate xmlns:cbvmda="urn:epcglobal:cbv:mda">2027-08-31</cbvmda:itemExpirationDate>
  <cbvmda:lotNumber xmlns:cbvmda="urn:epcglobal:cbv:mda">OR251219-1</cbvmda:lotNumber>
</ilmd>
```
The root element already declares `xmlns:cbvmda`, so per-element declarations are redundant.

---

## Implementation Plan

### 1. Change Authority to "GS1"

**File**: `tasks/epcis_enhancer.go`

**Change**: Lines 283 and 288 - replace `"SGLN"` with `"GS1"`:
```go
senderID.CreateAttr("Authority", "GS1")
receiverID.CreateAttr("Authority", "GS1")
```

### 2. Add brand.brand_name support for manufacturerOfTradeItemPartyName

**File**: `tasks/epcis_enhancer.go`

**Step 2a**: Add `BrandName` field to `ProductMasterData` struct (line 40-49):
```go
type ProductMasterData struct {
    GTIN                  string
    URN                   string
    ProductName           string
    NDC                   string
    Manufacturer          string
    BrandName             string // NEW: brand.brand_name
    DosageFormType        string
    StrengthDescription   string
    NetContentDescription string
}
```

**Step 2b**: Update Directus query to include brand.brand_name (lines 556-560):
```go
items, err := cms.QueryItems(ctx, "product", filter, []string{
    "gtin", "urn", "product_name", "ndc",
    "product_manufacturer.organisation_name",
    "brand.brand_name",  // NEW
    "net_content_description", "dosage_form_type", "strength_description",
}, 1)
```

**Step 2c**: Extract brand_name from query result (after line 569):
```go
brandName := ""
if brand, ok := item["brand"].(map[string]interface{}); ok {
    brandName = getStringField(brand, "brand_name")
}
```

**Step 2d**: Update ProductMasterData construction (lines 572-581):
```go
products = append(products, ProductMasterData{
    ...
    Manufacturer: manufacturer,
    BrandName:    brandName,  // NEW
    ...
})
```

**Step 2e**: Update `addProductVocabulary()` to prefer brand_name (lines 374-378):
```go
// Prefer brand_name over manufacturer if available
partyName := prod.Manufacturer
if prod.BrandName != "" {
    partyName = prod.BrandName
}
if partyName != "" {
    attr := elem.CreateElement("attribute")
    attr.CreateAttr("id", "urn:epcglobal:cbv:mda#manufacturerOfTradeItemPartyName")
    attr.SetText(partyName)
}
```

### 3. Remove redundant ILMD namespace declarations

**File**: `tasks/epcis_enhancer.go`

**Location**: In `enhanceEPCISXML()` function, after the etree doc is loaded.

**Add helper function**:
```go
// removeRedundantNamespaces removes xmlns:cbvmda declarations from ILMD elements
// since the namespace is already declared at root level
func removeRedundantNamespaces(doc *etree.Document) {
    for _, ilmd := range doc.FindElements("//ilmd/*") {
        for _, attr := range ilmd.Attr {
            if attr.Key == "xmlns:cbvmda" || (attr.Space == "xmlns" && attr.Key == "cbvmda") {
                ilmd.RemoveAttr(attr.Space + ":" + attr.Key)
            }
        }
    }
}
```

**Call in `enhanceEPCISXML()`** after loading the document:
```go
doc := etree.NewDocument()
if err := doc.ReadFromBytes(baseXML); err != nil {
    return nil, fmt.Errorf("parsing XML: %w", err)
}

// Remove redundant namespace declarations from ILMD elements
removeRedundantNamespaces(doc)
```

---

## Files to Modify

| File | Changes |
|------|---------|
| `tasks/epcis_enhancer.go` | All 3 fixes |
| `tasks/epcis_enhancer_test.go` | Update tests for new behavior |

---

## Verification

1. **Unit tests**: Update `epcis_enhancer_test.go` to verify:
   - Authority attribute is "GS1" not "SGLN"
   - manufacturerOfTradeItemPartyName uses brand_name when present
   - ILMD elements don't have redundant xmlns:cbvmda

2. **E2E test**: Run outbound pipeline against local dev environment:
   ```bash
   set -a; source .env; set +a
   go test -mod=vendor -v -tags=integration ./tests/... -run TestOutboundPipelineE2E
   ```

3. **Manual verification**: Generate XML and confirm:
   - `<sbdh:Identifier Authority="GS1">`
   - manufacturerOfTradeItemPartyName shows brand name where applicable
   - `<ilmd>` children don't have xmlns:cbvmda attributes
