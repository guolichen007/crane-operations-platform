package main

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// The tool refuses writes without an explicit digest and actor, refuses flags out of place and
// never reaches the database on an argument error.
func TestArgumentsAreExplicit(t *testing.T) {
	never := func() (*sql.DB, error) { return nil, errors.New("must not open") }
	for _, args := range [][]string{
		{},
		{"seed"},
		{"apply"},
		{"apply", "--approve", "x"},
		{"apply", "--actor", "ops"},
		{"plan", "--approve", "x", "--actor", "ops"},
		{"verify", "--include-meter-data"},
		{"cleanup-apply", "--include-meter-data"},
		{"verify", "--fresh-minutes", "0"},
		{"plan", "extra"},
	} {
		var out bytes.Buffer
		code, err := execute(args, &out, never)
		if err == nil || code != 1 || out.Len() != 0 {
			t.Fatalf("%v accepted: %d %v", args, code, err)
		}
		if r := safeReason(err); strings.Contains(r, "password") || strings.Contains(r, "postgres://") {
			t.Fatalf("reason leaks: %s", r)
		}
	}
	if _, err := execute([]string{"plan"}, &bytes.Buffer{}, never); err == nil || safeReason(err) != "lhk-sim database unavailable" {
		t.Fatalf("database error %v", err)
	}
}
