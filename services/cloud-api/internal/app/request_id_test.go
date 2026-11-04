package app

import "testing"

// validRequestID must accept short alphanumeric trace ids and reject empty,
// over-long, whitespace or otherwise unprintable/attacker-controlled ids so a
// caller-supplied trace id is only ever a validated correlation input.
func TestValidRequestID(t *testing.T) {
	for _, tc := range []struct {
		id    string
		valid bool
	}{
		{"a1b2c3", true},
		{"req-123_abc", true},
		{"", false},
		{"   ", false},
		{"with space", false},
		{"含中文", false},
		{`semi;colon`, false},
		{`../etc/passwd`, false},
		{`quote"`, false},
		{string(make([]byte, 0)), false},
	} {
		if got := validRequestID(tc.id); got != tc.valid {
			t.Errorf("validRequestID(%q) = %v, want %v", tc.id, got, tc.valid)
		}
	}
	// over-long id rejected
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	if validRequestID(string(long)) {
		t.Error("65-char id must be rejected")
	}
	if !validRequestID(string(long[:64])) {
		t.Error("64-char id must be accepted")
	}
}
