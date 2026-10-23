package main

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestNamedFleetCertificatesAndCAReuse(t *testing.T) {
	out := t.TempDir()
	ids := []string{}
	for i, n := range []int{1, 1, 1, 2, 2, 3} {
		for j := 1; j <= n; j++ {
			ids = append(ids, fmt.Sprintf("SIM-P%03d-%02d", i+1, j))
		}
	}
	if e := generate(out, ids); e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(filepath.Join(out, "certs", "devices"))
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".crt" || entry.Name() == "ca.crt" {
			continue
		}
		count++
		b, err := os.ReadFile(filepath.Join(out, "certs", "devices", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(b)
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || cert.Subject.CommonName+".crt" != entry.Name() {
			t.Fatal("certificate identity mismatch")
		}
	}
	if count != 10 {
		t.Fatal("named fleet did not generate exactly10 identities", count)
	}
	before, e := os.ReadFile(filepath.Join(out, "certs", "cloud", "ca.crt"))
	if e != nil {
		t.Fatal(e)
	}
	if e = generateDevice(out, ids[0]); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(out, "certs", "cloud", "ca.crt"))
	if !bytes.Equal(before, after) {
		t.Fatal("leaf issuance rotated CA")
	}
}
