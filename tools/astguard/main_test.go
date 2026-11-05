package main

import (
	"strings"
	"testing"
)

// Counter-examples are in-memory sources: the test never reads repository files,
// so its binary is location-independent.
const good = `package app
func reg(api mux) {
	api.HandleFunc("PATCH /api/v1/assets/{id}", func(w W, r R) {
		out, err := asset.PatchAsset(r.Context(), h.db, id, patch, expected, u, rid)
		_ = out; _ = err
	})
}`

func TestCheckAssetPatch(t *testing.T) {
	cases := []struct {
		name  string
		files []source
		want  string
	}{
		{"compliant", []source{{"a.go", good}}, ""},
		{"whole-object update in route", []source{{"a.go", strings.Replace(good, "asset.PatchAsset", "asset.UpdateAsset", 1)}}, "calls asset.UpdateAsset"},
		{"route without PatchAsset", []source{{"a.go", strings.Replace(good, "asset.PatchAsset", "asset.Other", 1)}}, "does not call asset.PatchAsset"},
		{"UpdateAsset elsewhere in package", []source{{"a.go", good}, {"b.go", "package app\nfunc f() { asset.UpdateAsset(ctx, db, in, 1, a, r) }"}}, "b.go"},
		{"route missing", []source{{"a.go", "package app\nfunc f() {}"}}, "not registered"},
	}
	for _, tc := range cases {
		err := checkAssetPatch(tc.files)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want containing %q", tc.name, err, tc.want)
		}
	}
}
