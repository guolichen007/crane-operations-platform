// Anonymous, isolated integration fixtures only. This is not a production PKI or enrollment tool.
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eclipse/paho.golang/paho"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/auditlog"
	"github.com/guolichen007/crane-operations-platform/internal/fleet"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
	"github.com/guolichen007/crane-operations-platform/internal/inventory"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func prepare(out string) error {
	if !strings.Contains(filepath.ToSlash(out), "/runtime/") {
		return errors.New("fixture output must stay in ignored runtime")
	}
	document, err := inventory.Load("deploy/inventory/inventory.example.yaml")
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return err
	}
	if err = write(filepath.Join(out, "inventory.json"), encoded); err != nil {
		return err
	}
	manifest := inventory.Fleet(document)
	encoded, err = json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err = write(filepath.Join(out, "fleet.json"), encoded); err != nil {
		return err
	}
	acl, err := fleet.ACL(manifest)
	if err != nil {
		return err
	}
	if err = write(filepath.Join(out, "acl"), []byte(acl)); err != nil {
		return err
	}
	for _, name := range []string{"postgres_admin_password", "app_db_password", "keycloak_db_password", "keycloak_bootstrap_password", "oidc_client_secret", "session_server_secret", "browser_test_password"} {
		value := make([]byte, 32)
		if _, err = rand.Read(value); err != nil {
			return err
		}
		if err = write(filepath.Join(out, "secrets", name), []byte(hex.EncodeToString(value))); err != nil {
			return err
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Anonymous A1 Fixture Root"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		return err
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	names := []string{"mosquitto", "cloud-api", "ingress"}
	for _, runtime := range manifest.Devices {
		names = append(names, runtime.RuntimeDeviceId)
	}
	for index, name := range names {
		leafKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return e
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(index + 2)), Subject: pkix.Name{CommonName: name}, NotBefore: root.NotBefore, NotAfter: root.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}, DNSNames: []string{name, "crane.test", "auth.crane.test"}}
		cert, e := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, key)
		if e != nil {
			return e
		}
		private, e := x509.MarshalPKCS8PrivateKey(leafKey)
		if e != nil {
			return e
		}
		folder := name
		if strings.HasPrefix(name, "SIM-") {
			folder = filepath.Join("devices", name)
		}
		if name == "cloud-api" {
			folder = "cloud"
		}
		if name == "mosquitto" {
			folder = "broker"
		}
		for file, data := range map[string][]byte{"ca.crt": ca, name + ".crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), name + ".key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})} {
			if err = write(filepath.Join(out, folder, file), data); err != nil {
				return err
			}
		}
	}
	// No CA private key is written. Fixture leaves expire after 24 hours and are never release artifacts.
	return nil
}

func seed(ctx context.Context) error {
	password, err := platform.PrivateSecret("/fixture/secrets/keycloak_bootstrap_password", 32)
	if err != nil {
		return err
	}
	userPassword, err := platform.PrivateSecret("/fixture/secrets/browser_test_password", 32)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	form := url.Values{"grant_type": {"password"}, "client_id": {"admin-cli"}, "username": {"bootstrap-crane"}, "password": {string(password)}}
	r, err := http.NewRequestWithContext(ctx, "POST", "http://keycloak:8080/realms/master/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(r)
	if err != nil {
		return errors.New("fixture admin unavailable")
	}
	var auth struct {
		Token string `json:"access_token"`
	}
	e := json.NewDecoder(response.Body).Decode(&auth)
	_ = response.Body.Close()
	if response.StatusCode != 200 || e != nil || auth.Token == "" {
		return errors.New("fixture admin rejected")
	}
	request := func(method, path string, body any, output any) error {
		data, _ := json.Marshal(body)
		r, err := http.NewRequestWithContext(ctx, method, "http://keycloak:8080/admin/realms/crane"+path, bytes.NewReader(data))
		if err != nil {
			return err
		}
		r.Header.Set("Authorization", "Bearer "+auth.Token)
		r.Header.Set("Content-Type", "application/json")
		response, err := client.Do(r)
		if err != nil {
			return errors.New("fixture admin request failed")
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return errors.New("fixture admin request rejected")
		}
		if output != nil {
			return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output)
		}
		return nil
	}
	dbPassword, err := platform.PrivateSecret("/fixture/secrets/app_db_password", 32)
	if err != nil {
		return err
	}
	dsn, err := platform.DatabaseWithPassword("postgres://crane_app@postgres:5432/crane_app?sslmode=disable", dbPassword)
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, item := range []struct {
		name, project, role string
		updatePassword      bool
	}{{"a1-viewer", "P001", "viewer", false}, {"a1-operator", "P002", "operator", false}, {"a1-admin", "P001", "platform_admin", true}} {
		actions := []string{}
		if item.updatePassword {
			actions = append(actions, "UPDATE_PASSWORD")
		}
		body := map[string]any{"username": item.name, "email": item.name + "@example.test", "emailVerified": true, "enabled": true, "firstName": "Anonymous", "lastName": item.name, "requiredActions": actions, "credentials": []any{map[string]any{"type": "password", "value": string(userPassword), "temporary": item.updatePassword}}}
		if err = request("POST", "/users", body, nil); err != nil {
			return err
		}
		var users []struct {
			ID string `json:"id"`
		}
		if err = request("GET", "/users?exact=true&username="+url.QueryEscape(item.name), nil, &users); err != nil || len(users) != 1 {
			return errors.New("fixture sub lookup failed")
		}
		event := auditlog.Event{Actor: "anonymous-a1-fixture", RequestID: platform.ID()}
		if err = identity.Bind(ctx, db, item.name, os.Getenv("OIDC_ISSUER"), users[0].ID, "Anonymous "+item.name, true, event); err != nil {
			return err
		}
		if err = identity.ChangeGrant(ctx, db, item.name, item.project, item.role, false, event); err != nil {
			return err
		}
	}
	document, err := inventory.Load("/fixture/inventory.json")
	if err != nil {
		return err
	}
	for _, runtime := range document.RuntimeDevices {
		config := platform.Config{MQTTURL: "tls://mosquitto:8883", CertDir: "/fixture/devices/" + runtime.RuntimeDeviceId}
		connected := make(chan struct{}, 1)
		broker, e := platform.Connect(ctx, config, runtime.RuntimeDeviceId, nil, nil, func(string, []byte, time.Duration) error { return nil }, func() {
			select {
			case connected <- struct{}{}:
			default:
			}
		}, nil)
		if e != nil {
			return e
		}
		select {
		case <-connected:
		case <-ctx.Done():
			return errors.New("fixture MQTT timeout")
		}
		envelope := contract.WithPayload(contract.Envelope{SchemaVersion: 1, MessageId: platform.ID(), TenantId: document.TenantId, ProjectId: runtime.ProjectId, DeviceId: runtime.RuntimeDeviceId, BootId: "a1-browser-fixture", BootSeq: 1, Sequence: 1, Kind: "BIRTH", SentAt: time.Now().UTC()}, contract.BirthPayload{EdgeHealth: "EDGE_HEALTHY", ControllerHealth: "CONTROLLER_HEALTHY", SensorHealth: "HEALTHY", Reported: contract.ReportedState{OperatingState: "SILENT", Mode: "AUTO", AlarmState: "UNKNOWN"}})
		payload, _ := json.Marshal(envelope)
		topic, _ := contract.Topic(document.TenantId, runtime.ProjectId, runtime.RuntimeDeviceId, "shadow/reported")
		if _, e = broker.CM.Publish(ctx, &paho.Publish{Topic: topic, Payload: payload, QoS: 1, Retain: true}); e != nil {
			return e
		}
		if e = broker.CM.Disconnect(ctx); e != nil {
			return e
		}
	}
	return nil
}

// HTTPS probes use only the short-lived anonymous fixture CA, never skip verification.
func probe(ctx context.Context, discoveryOnly bool) error {
	ca, err := os.ReadFile("/fixture/ingress/ca.crt")
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return errors.New("fixture CA rejected")
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	origin := os.Getenv("PUBLIC_URL")
	targets := map[string]int{"/realms/crane/.well-known/openid-configuration": 200}
	if !discoveryOnly {
		for path, status := range map[string]int{"/": 200, "/auth/session": 401, "/api/v1/projects": 401, "/auth/callback?code=fixture-oidc-marker&state=fixture-oidc-marker": 401, "/resources/": 404, "/admin": 403, "/admin/": 403, "/health": 403, "/health/ready": 403, "/metrics": 403} {
			targets[path] = status
		}
	}
	for path, expected := range targets {
		for {
			r, err := http.NewRequestWithContext(ctx, "GET", origin+path, nil)
			if err != nil {
				return err
			}
			response, e := client.Do(r)
			valid := false
			if e == nil {
				valid = response.StatusCode == expected
				if discoveryOnly || strings.Contains(path, "openid-configuration") {
					var metadata struct {
						Issuer string `json:"issuer"`
					}
					valid = valid && json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&metadata) == nil && metadata.Issuer == origin+"/realms/crane"
				}
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
			}
			if valid {
				break
			}
			select {
			case <-ctx.Done():
				return errors.New("production template HTTPS topology probe failed")
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	return nil
}

func main() {
	operation := flag.String("operation", "prepare", "prepare|seed|probe-discovery|probe-topology")
	out := flag.String("out", "deploy/compose/runtime/a1-production", "ignored fixture output")
	flag.Parse()
	var err error
	if *operation == "prepare" {
		err = prepare(*out)
	} else if *operation == "seed" {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		err = seed(ctx)
	} else if *operation == "probe-discovery" || *operation == "probe-topology" {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		err = probe(ctx, *operation == "probe-discovery")
	} else {
		err = errors.New("unknown fixture operation")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Anonymous A1 fixture failed; no credentials printed.")
		os.Exit(1)
	}
	fmt.Println("ANONYMOUS_A1_FIXTURE=READY")
}
