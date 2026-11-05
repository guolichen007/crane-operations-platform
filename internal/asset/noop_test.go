package asset

import (
	"reflect"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
)

func sp(v string) *string   { return &v }
func fp(v float64) *float64 { return &v }

// diffAsset compares values, distinguishing nil from "" and zero.
func TestDiffAssetValueComparison(t *testing.T) {
	base := contract.AssetMasterData{DisplayName: "A", AssetType: "CRANE", AssetStatus: "DRAFT", AssetCode: sp("C1"), ManufactureDate: sp("2020-01-02"), LiftingHeightM: fp(0)}
	same := base
	same.AssetCode = sp("C1") // different pointer, same value
	same.ManufactureDate = sp("2020-01-02")
	same.LiftingHeightM = fp(0)
	if d := diffAsset(base, same); len(d) != 0 {
		t.Fatalf("equal values reported as changed: %v", d)
	}
	next := base
	next.AssetCode = nil
	next.LiftingHeightM = nil
	next.SerialNumber = sp("")
	next.ManufactureDate = sp("2020-01-03")
	next.AssetStatus = "ACTIVE"
	want := []string{"assetCode", "assetStatus", "serialNumber", "manufactureDate", "liftingHeightM"}
	if d := diffAsset(base, next); !reflect.DeepEqual(d, want) {
		t.Fatalf("diff = %v, want %v", d, want)
	}
}

// Create applies the same text normalization as PATCH, so re-sending a stored
// value through PATCH is a no-op.
func TestNormalizeAssetTextMatchesPatch(t *testing.T) {
	in := contract.AssetMasterData{DisplayName: "  名称 ", AssetType: " CRANE ", BrandSnapshot: " 品牌 ", AssetCode: sp("  "), SerialNumber: sp(" SN "), ManufactureDate: sp(" ")}
	normalizeAssetText(&in)
	if in.DisplayName != "名称" || in.AssetType != "CRANE" || in.BrandSnapshot != "品牌" {
		t.Fatalf("text not trimmed: %+v", in)
	}
	if in.AssetCode != nil || in.ManufactureDate != nil || in.SerialNumber == nil || *in.SerialNumber != "SN" {
		t.Fatalf("nullable normalization wrong: code=%v date=%v sn=%v", in.AssetCode, in.ManufactureDate, in.SerialNumber)
	}
	// The PATCH merge of the same raw values yields the same stored values.
	merged := contract.AssetMasterData{DisplayName: "x", AssetType: "x"}
	p := AssetPatch{
		DisplayName:   Field[string]{Set: true, Value: "  名称 "},
		AssetType:     Field[string]{Set: true, Value: " CRANE "},
		BrandSnapshot: Field[string]{Set: true, Value: " 品牌 "},
		AssetCode:     Field[string]{Set: true, Value: "  "},
		SerialNumber:  Field[string]{Set: true, Value: " SN "},
	}
	if _, err := p.apply(&merged); err != nil {
		t.Fatal(err)
	}
	if merged.DisplayName != in.DisplayName || merged.AssetType != in.AssetType || merged.BrandSnapshot != in.BrandSnapshot || merged.AssetCode != nil || *merged.SerialNumber != *in.SerialNumber {
		t.Fatalf("create and PATCH normalization disagree: create %+v patch %+v", in, merged)
	}
}
