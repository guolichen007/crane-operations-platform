package geo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestEmbeddedIndexMatchesBrowserManifest(t *testing.T) {
	ix, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../apps/web-console/public/geo/geo-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if ix.Version() != hex.EncodeToString(sum[:]) {
		t.Fatalf("embedded index version %s is not the browser manifest %x — run node tools/codegen/geoindex.mjs", ix.Version(), sum)
	}
	var manifest map[string]struct {
		Adcode int    `json:"adcode"`
		Parent *int   `json:"parent"`
		Level  string `json:"level"`
		Name   string `json:"name"`
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest) != len(ix.regions) {
		t.Fatalf("region count %d != manifest %d", len(ix.regions), len(manifest))
	}
	for _, m := range manifest {
		r, ok := ix.Region(m.Adcode)
		parent := 0
		if m.Parent != nil {
			parent = *m.Parent
		}
		if !ok || r.Parent != parent || string(r.Level) != m.Level || r.Name != m.Name {
			t.Fatalf("region %d differs from browser manifest", m.Adcode)
		}
	}
}

func TestHierarchyQueries(t *testing.T) {
	ix, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	// Country covers everything.
	all, _ := ix.Descendants(Country)
	if len(all) != len(ix.regions) {
		t.Fatalf("country descendants %d != %d", len(all), len(ix.regions))
	}
	// Province 330000 → prefectures and counties, includes Haining 330481, excludes Guangdong.
	zj, err := ix.Descendants(330000)
	if err != nil || !contains(zj, 330000) || !contains(zj, 330400) || !contains(zj, 330481) || contains(zj, 440300) {
		t.Fatalf("zhejiang descendants wrong: %v", err)
	}
	// Prefecture 330400 → itself + counties.
	jx, _ := ix.Descendants(330400)
	if !contains(jx, 330481) || contains(jx, 330100) {
		t.Fatal("jiaxing descendants wrong")
	}
	// County: only itself.
	if c, _ := ix.Descendants(330481); len(c) != 1 || c[0] != 330481 {
		t.Fatalf("county descendants = %v", c)
	}
	// Province-administered county (Beijing districts sit directly under the province).
	if r, ok := ix.Region(110105); !ok || r.Parent != 110000 || r.Level != LevelCounty {
		t.Fatalf("province-level county = %+v", r)
	}
	if _, err := ix.Descendants(999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown region err = %v", err)
	}
	// ChildOf: which child of a scope contains a code.
	for _, tc := range []struct {
		scope, code, want int
		ok                bool
	}{
		{Country, 330481, 330000, true}, // county → its province under the country
		{330000, 330481, 330400, true},  // county → its prefecture under the province
		{330400, 330481, 330481, true},  // county is a direct child of its prefecture
		{110000, 110105, 110105, true},  // province-administered county
		{330481, 330481, 0, false},      // a region is not below itself
		{440000, 330481, 0, false},      // other province
		{Country, 999999, 0, false},     // unknown code never matches
	} {
		got, ok := ix.ChildOf(tc.scope, tc.code)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ChildOf(%d,%d) = %d,%v want %d,%v", tc.scope, tc.code, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCorruptOrMissingIndexIsUnavailable(t *testing.T) {
	good := `{"version":"` + string(make64('a')) + `","source":"x","regions":[[100000,0,"C","全国"],[330000,100000,"P","浙江省"]]}`
	if _, err := Parse([]byte(good)); err != nil {
		t.Fatalf("minimal valid index rejected: %v", err)
	}
	for name, body := range map[string]string{
		"empty":            ``,
		"not json":         `{`,
		"no version":       `{"regions":[[100000,0,"C","全国"]]}`,
		"no root":          `{"version":"` + make64('a') + `","regions":[[330000,100000,"P","浙江省"]]}`,
		"orphan parent":    `{"version":"` + make64('a') + `","regions":[[100000,0,"C","全国"],[330400,330000,"M","嘉兴市"]]}`,
		"duplicate":        `{"version":"` + make64('a') + `","regions":[[100000,0,"C","全国"],[330000,100000,"P","浙江省"],[330000,100000,"P","浙江省"]]}`,
		"level not deeper": `{"version":"` + make64('a') + `","regions":[[100000,0,"C","全国"],[330000,100000,"P","浙江省"],[330400,330000,"P","嘉兴市"]]}`,
		"bad level":        `{"version":"` + make64('a') + `","regions":[[100000,0,"C","全国"],[330000,100000,"Z","浙江省"]]}`,
		"bad code":         `{"version":"` + make64('a') + `","regions":[[100000,0,"C","全国"],[33,100000,"P","浙江省"]]}`,
	} {
		if _, err := Parse([]byte(body)); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: err = %v, want ErrUnavailable", name, err)
		}
	}
}

func make64(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
