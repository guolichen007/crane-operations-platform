package equipment

import "testing"

// The registry and the spec-schema switch must list exactly the same codes, and
// display names must be unique and non-empty (two codes never share a label).
func TestTypeRegistryMatchesSpecSchemas(t *testing.T) {
	seenCode := map[string]bool{}
	seenName := map[string]string{}
	for _, ti := range Types() {
		if !ValidTypeCode(ti.Code) {
			t.Errorf("registry code %s has no spec schema", ti.Code)
		}
		if ti.NameZh == "" {
			t.Errorf("registry code %s has no display name", ti.Code)
		}
		if seenCode[ti.Code] {
			t.Errorf("duplicate code %s", ti.Code)
		}
		seenCode[ti.Code] = true
		if other, ok := seenName[ti.NameZh]; ok {
			t.Errorf("codes %s and %s share the display name %q", other, ti.Code, ti.NameZh)
		}
		seenName[ti.NameZh] = ti.Code
		switch ti.Domain {
		case "MECHANICAL", "CONTROL", "PERCEPTION", "COMPUTE", "NETWORK", "POWER", "SENSOR", "OTHER":
		default:
			t.Errorf("code %s has invalid domain %s", ti.Code, ti.Domain)
		}
	}
	// The spec-schema map and the registry list exactly the same codes (both are
	// in-memory: no source files are read, so the test binary runs anywhere).
	for code := range specFileByType {
		if !seenCode[code] {
			t.Errorf("spec type %s missing from registry", code)
		}
	}
	if len(specFileByType) != len(seenCode) {
		t.Fatalf("specFileByType lists %d codes, registry %d", len(specFileByType), len(seenCode))
	}
}

func TestTypesReturnsCopy(t *testing.T) {
	a := Types()
	a[0].NameZh = "changed"
	if Types()[0].NameZh == "changed" {
		t.Fatal("Types must return a copy")
	}
}
