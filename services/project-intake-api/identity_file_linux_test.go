//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityRootFile(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("NOT_RUN: root-only Linux ownership fixture")
	}
	root := t.TempDir()
	path := filepath.Join(root, "identity.json")
	if e := os.WriteFile(path, identityFixture(), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := readIdentityFile(path); e != nil {
		t.Fatal(e)
	}
	for _, mode := range []os.FileMode{0644, 0660, 0604} {
		_ = os.Chmod(path, mode)
		if _, e := readIdentityFile(path); e == nil {
			t.Fatal("insecure mode accepted")
		}
	}
	_ = os.Chmod(path, 0400)
	if _, e := readIdentityFile(path); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(root, "symlink")
	_ = os.Symlink(path, link)
	if _, e := readIdentityFile(link); e == nil {
		t.Fatal("symlink accepted")
	}
	if _, e := readIdentityFile(root); e == nil {
		t.Fatal("directory accepted")
	}
	_ = os.Chown(path, 10001, 10001)
	if _, e := readIdentityFile(path); e == nil {
		t.Fatal("nonroot owner accepted")
	}
	_ = os.Chown(path, 0, 0)
}
func TestDatabaseIdentityCLI(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("NOT_RUN: root-only CLI fixture")
	}
	db := b2Database(t)
	path := filepath.Join(t.TempDir(), "identity.json")
	_ = os.WriteFile(path, identityFixture(), 0600)
	var out bytes.Buffer
	for _, dry := range []bool{true, false, false} {
		args := []string{"--file", path, "--operator", "synthetic-operator"}
		if dry {
			args = append(args, "--dry-run")
		}
		out.Reset()
		if e := identityCommand(context.Background(), db, args, &out); e != nil {
			t.Fatal(e)
		}
		var sum IdentitySummary
		if e := json.Unmarshal(out.Bytes(), &sum); e != nil || sum.DryRun != dry {
			t.Fatal("summary", e)
		}
		if strings.Contains(out.String(), "77777777") || strings.Contains(out.String(), "Synthetic Identity") {
			t.Fatal("sensitive output")
		}
	}
	for _, args := range [][]string{{"--file", path}, {"--file", path, "--operator", "synthetic-operator", "--oops"}} {
		if e := identityCommand(context.Background(), db, args, &out); e == nil {
			t.Fatal("invalid CLI arguments accepted")
		}
	}
	_ = os.Chmod(path, 0644)
	out.Reset()
	if e := identityCommand(context.Background(), db, []string{"--file", path, "--operator", "synthetic-operator"}, &out); e == nil || out.Len() != 0 {
		t.Fatal("insecure CLI input")
	}
}
