package main

import "testing"

// The preview precheck contract must fail closed: any production realm/client/
// host, an unknown realm, an HTTP origin, or an invalid URL is rejected before
// any request. Re-running the validation is idempotent.
func TestValidatePreviewTarget(t *testing.T) {
	if err := validatePreviewTarget("https://"+previewHost, previewRealm, previewClient); err != nil {
		t.Fatalf("valid preview target rejected: %v", err)
	}
	reject := func(name, publicURL, realm, client string) {
		t.Helper()
		if err := validatePreviewTarget(publicURL, realm, client); err == nil {
			t.Fatalf("%s: expected rejection, got nil", name)
		}
	}
	reject("production host", "https://crane.youdaozd.com", previewRealm, previewClient)
	reject("production realm", "https://"+previewHost, "crane", previewClient)
	reject("production client", "https://"+previewHost, previewRealm, "crane-bff")
	reject("unknown realm", "https://"+previewHost, "unknown-realm", previewClient)
	reject("http scheme", "http://"+previewHost, previewRealm, previewClient)
	reject("invalid url", "://bad", previewRealm, previewClient)
}

func TestValidatePreviewTargetIdempotent(t *testing.T) {
	for i := 0; i < 3; i++ {
		if err := validatePreviewTarget("https://"+previewHost, previewRealm, previewClient); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
}
