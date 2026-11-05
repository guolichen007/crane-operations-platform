package equipment

import (
	"os"
	"regexp"
	"testing"
)

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
	// Every type-code case label in specFileFor must be in the registry (schema
	// file names are lowercase and are not matched).
	src, err := os.ReadFile("spec.go")
	if err != nil {
		t.Fatal(err)
	}
	body := regexp.MustCompile(`(?s)func specFileFor.*?\n}\n`).Find(src)
	found := 0
	for _, m := range regexp.MustCompile(`"([A-Z][A-Z0-9_]+)"`).FindAllSubmatch(body, -1) {
		found++
		if !seenCode[string(m[1])] {
			t.Errorf("spec type %s missing from registry", m[1])
		}
	}
	if found != len(seenCode) {
		t.Fatalf("specFileFor lists %d codes, registry %d", found, len(seenCode))
	}
}

func TestTypesReturnsCopy(t *testing.T) {
	a := Types()
	a[0].NameZh = "changed"
	if Types()[0].NameZh == "changed" {
		t.Fatal("Types must return a copy")
	}
}
