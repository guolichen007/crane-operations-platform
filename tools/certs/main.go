// Development CA only. Generated private material stays outside Git.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	out := flag.String("out", "deploy/compose/runtime", "local output")
	flag.Parse()
	if e := generate(*out); e != nil {
		fmt.Fprintln(os.Stderr, "certificate generation failed:", e)
		os.Exit(1)
	}
}
func generate(out string) error {
	caKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return e
	}
	now := time.Now()
	serial := func() *big.Int { v, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128)); return v }
	ca := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "Crane Development CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(30 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if e != nil {
		return e
	}
	if e = writeKey(filepath.Join(out, "private", "ca.key"), caKey); e != nil {
		return e
	}
	for i := 0; i < 22; i++ {
		id, group := "cloud-api", "cloud"
		if i == 1 {
			id, group = "mosquitto", "broker"
		}
		if i >= 2 {
			id, group = fmt.Sprintf("SIM-%03d", i-1), "devices"
		}
		dir := filepath.Join(out, "certs", group)
		if e = os.MkdirAll(dir, 0700); e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(dir, "ca.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); e != nil {
			return e
		}
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		cert := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: id}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(7 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		if id == "mosquitto" {
			cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			cert.DNSNames = []string{"mosquitto", "localhost"}
			cert.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		}
		b, err := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(dir, id+".crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: b}), 0644); err != nil {
			return err
		}
		if err = writeKey(filepath.Join(dir, id+".key"), key); err != nil {
			return err
		}
	}
	return nil
}
func writeKey(path string, key *ecdsa.PrivateKey) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	b, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		return e
	}
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: b}), 0600)
}
