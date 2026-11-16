package main

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guolichen007/crane-operations-platform/internal/testsupport"
)

func TestParseRefusesUnsafeInvocations(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"drop"},
		{"apply", "--all"},
		{"apply", "--all", "--approve", "x"},
		{"apply", "--all", "--actor", "ops"},
		{"plan", "--all", "--approve", "x"},
		{"plan", "--all", "extra"},
	} {
		if _, err := parse(args); err == nil {
			t.Errorf("parse(%q) accepted", args)
		}
	}
}

// The CLI end to end on a real database: plan (read-only) → apply with the
// approved digest → verify consistent → second apply noop. The database password
// is read from a private file and never appears in any output.
func TestCLIPlanApplyVerifyNeverPrintsSecret(t *testing.T) {
	db := testsupport.Database(t)
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	base, err := url.Parse(os.Getenv("A1_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	password, hasPassword := base.User.Password()
	if !hasPassword {
		password = "local-trust-auth-placeholder-secret-0123"
	}
	secret := filepath.Join(t.TempDir(), "db_password")
	if err := os.WriteFile(secret, []byte(password+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base.User = url.User(base.User.Username())
	base.Path = "/" + name
	t.Setenv("DATABASE_URL", base.String())
	t.Setenv("DATABASE_PASSWORD_FILE", secret)
	t.Setenv("APP_ENV", "production")

	run := func(args ...string) (int, string) {
		t.Helper()
		var out bytes.Buffer
		code, err := execute(args, &out)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		text := out.String()
		if strings.Contains(text, password) || strings.Contains(text, "postgres://") {
			t.Fatalf("%v output leaks connection details", args)
		}
		return code, text
	}
	code, text := run("plan", "--types", "MOTOR,PLC")
	if code != 0 {
		t.Fatalf("plan exit %d", code)
	}
	var plan struct {
		Plan struct {
			Digest   string   `json:"digest"`
			ToCreate []string `json:"toCreate"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(text), &plan); err != nil || len(plan.Plan.ToCreate) != 2 {
		t.Fatalf("plan output = %s (err=%v)", text, err)
	}
	if code, _ := run("verify", "--types", "MOTOR,PLC"); code != exitDrift {
		t.Fatalf("verify before apply exit %d, want %d", code, exitDrift)
	}
	if _, err := execute([]string{"apply", "--types", "MOTOR,PLC", "--approve", strings.Repeat("0", 64), "--actor", "ops"}, &bytes.Buffer{}); err == nil {
		t.Fatal("apply with an unapproved digest accepted")
	}
	if code, _ := run("apply", "--types", "MOTOR,PLC", "--approve", plan.Plan.Digest, "--actor", "ops"); code != 0 {
		t.Fatalf("apply exit %d", code)
	}
	if code, _ := run("verify", "--types", "MOTOR,PLC"); code != 0 {
		t.Fatalf("verify after apply exit %d", code)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM equipment_types`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows = %d (err=%v), want 2", n, err)
	}
	if _, err := execute([]string{"plan", "--types", "NOT_A_TYPE"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unregistered code accepted")
	}
	if got := safeReason(os.ErrPermission); strings.Contains(got, "permission") {
		t.Fatalf("unknown errors must be withheld, got %q", got)
	}
}
