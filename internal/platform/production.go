package platform

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type ProductionConfig struct {
	PublicURL, Issuer, ClientID                         string
	ClientSecretFile, SessionSecretFile, DBPasswordFile string
	TrustedProxyCIDRs                                   []string
	AbsoluteSeconds, IdleSeconds                        int
}

func loadProduction(c *ProductionConfig) {
	for key, value := range map[string]*string{
		"PUBLIC_URL": &c.PublicURL, "OIDC_ISSUER": &c.Issuer, "OIDC_CLIENT_ID": &c.ClientID,
		"OIDC_CLIENT_SECRET_FILE": &c.ClientSecretFile, "SESSION_SECRET_FILE": &c.SessionSecretFile,
		"DATABASE_PASSWORD_FILE": &c.DBPasswordFile,
	} {
		if v, ok := os.LookupEnv(key); ok {
			*value = v
		}
	}
	if value, ok := os.LookupEnv("TRUSTED_PROXY_CIDRS"); ok {
		c.TrustedProxyCIDRs = strings.Split(value, ",")
	}
	if c.AbsoluteSeconds == 0 {
		c.AbsoluteSeconds = 28800
	}
	if c.IdleSeconds == 0 {
		c.IdleSeconds = 1800
	}
	for key, target := range map[string]*int{"SESSION_ABSOLUTE_SECONDS": &c.AbsoluteSeconds, "SESSION_IDLE_SECONDS": &c.IdleSeconds} {
		if value, ok := os.LookupEnv(key); ok {
			n, err := strconv.Atoi(value)
			if err != nil {
				n = -1 // Validation fails closed rather than ignoring invalid input.
			}
			*target = n
		}
	}
}

func SecureURL(value string, allowPath bool) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
		(allowPath || u.Path == "") && !strings.HasSuffix(value, "/")
}

// PrivateSecret reads only explicit regular files. Errors deliberately omit paths and contents.
func PrivateSecret(path string, minimum int) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8192 ||
		(runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("secret requires a private regular file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("secret file unreadable")
	}
	b = []byte(strings.TrimSpace(string(b)))
	if len(b) < minimum || len(b) > 4096 {
		return nil, errors.New("secret file length outside supported bounds")
	}
	return b, nil
}

func (c *Config) ValidateCloud() error {
	if c.Env == "development" {
		if len(c.Token) < 32 {
			return errors.New("development requires a random development token")
		}
		return nil
	}
	if c.Env != "production" || c.Token != "" {
		return errors.New("invalid Cloud environment or development credentials in production")
	}
	p := c.Production
	if !SecureURL(p.PublicURL, false) || !SecureURL(p.Issuer, true) || p.ClientID == "" || len(p.ClientID) > 256 ||
		p.AbsoluteSeconds < 60 || p.AbsoluteSeconds > 86400 || p.IdleSeconds < 30 || p.IdleSeconds > p.AbsoluteSeconds {
		return errors.New("invalid production OIDC origin or session configuration")
	}
	for _, cidr := range p.TrustedProxyCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
		if err != nil || prefix.Bits() == 0 {
			return errors.New("trusted proxy must be an explicit non-universal CIDR")
		}
	}
	if c.FleetFile == "" || c.InventoryFile == "" || strings.Contains(filepath.Base(c.InventoryFile), ".example.") {
		return errors.New("production requires explicit provisioned Inventory and Fleet; anonymous fallback forbidden")
	}
	for _, input := range []struct {
		path string
		size int
	}{{p.ClientSecretFile, 32}, {p.SessionSecretFile, 32}} {
		if _, err := PrivateSecret(input.path, input.size); err != nil {
			return err
		}
	}
	password, err := PrivateSecret(p.DBPasswordFile, 20)
	if err != nil {
		return err
	}
	c.DatabaseURL, err = DatabaseWithPassword(c.DatabaseURL, password)
	if err != nil {
		return err
	}
	return ValidateProductionCA(c.CertDir)
}

func DatabaseWithPassword(source string, password []byte) (string, error) {
	u, err := url.Parse(source)
	if err != nil || u.User == nil || u.User.Username() == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", errors.New("production requires a password-free PostgreSQL URL and password file")
	}
	if _, present := u.User.Password(); present {
		return "", errors.New("production DB password must come only from its secret file")
	}
	u.User = url.UserPassword(u.User.Username(), string(password))
	return u.String(), nil
}

func ValidateProductionCA(directory string) error {
	b, err := os.ReadFile(filepath.Join(directory, "ca.crt"))
	if err != nil {
		return errors.New("production MQTT trust bundle unavailable")
	}
	count := 0
	for len(b) > 0 {
		block, remaining := pem.Decode(b)
		if block == nil || block.Type != "CERTIFICATE" {
			return errors.New("invalid production MQTT trust bundle")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !certificate.IsCA || strings.Contains(strings.ToLower(certificate.Subject.CommonName), "development") {
			return errors.New("development or invalid CA cannot establish production device identity")
		}
		count++
		b = remaining
	}
	if count == 0 {
		return errors.New("empty production MQTT trust bundle")
	}
	return nil
}
