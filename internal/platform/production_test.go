package platform

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProductionRejectsDevelopmentTokenAtLoad(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DEV_API_TOKEN", strings.Repeat("anonymous", 8))
	if _, err := Load(nil); err == nil {
		t.Fatal("production accepted development credentials")
	}
}

func TestProductionPrivateSecretPermissionsAndPasswordSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anonymous-secret")
	if err := os.WriteFile(path, []byte(strings.Repeat("anonymous-test-material", 3)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrivateSecret(path, 32); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := PrivateSecret(path, 32); err == nil {
			t.Fatal("world-readable production secret accepted")
		}
	}
	if _, err := DatabaseWithPassword("postgres://user:plaintext@localhost/app", []byte("anonymous")); err == nil {
		t.Fatal("inline production password accepted")
	}
	if _, err := DatabaseWithPassword("postgres://user@localhost/app", []byte(strings.Repeat("anonymous", 4))); err != nil {
		t.Fatal(err)
	}
}

func TestProductionRejectsDevelopmentTrustAndUnsafeOrigins(t *testing.T) {
	for _, name := range []string{"Crane Development CA", "Anonymous Production Test CA"} {
		directory := t.TempDir()
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
		der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, "ca.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
			t.Fatal(err)
		}
		err = ValidateProductionCA(directory)
		if (err == nil) == strings.Contains(name, "Development") {
			t.Fatal("production trust gate", name, err)
		}
	}
	for _, value := range []string{"http://app.example.test", "https://app.example.test/path", "https://user@app.example.test", "https://app.example.test?return=evil", "https://app.example.test/"} {
		if SecureURL(value, false) {
			t.Fatal("unsafe public origin accepted", value)
		}
	}
}
