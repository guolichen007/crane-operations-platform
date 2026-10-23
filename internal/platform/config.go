package platform

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"
)

type Config struct {
	FleetFile     string `json:"fleetFile"`
	InventoryFile string `json:"inventoryFile"`
	Env           string `json:"env"`
	HTTP          string `json:"http"`
	DatabaseURL   string `json:"databaseUrl"`
	MQTTURL       string `json:"mqttUrl"`
	CertDir       string `json:"certDir"`
	DataDir       string `json:"dataDir"`
	Token         string `json:"token"`
	Count         int    `json:"count"`
	Scenario      string `json:"scenario"`
	TTL           int    `json:"ttl"`
	Grace         int    `json:"grace"`
	Migrate       bool   `json:"migrate"`
	WillDelay     int    `json:"willDelaySeconds"`
}

func Load(args []string) (Config, error) {
	c := Config{Env: "development", HTTP: ":8081", MQTTURL: "tls://localhost:8883", CertDir: "deploy/compose/runtime/certs", DataDir: "deploy/compose/runtime/spool", Count: 20, Scenario: "normal", TTL: 300, Grace: 45, WillDelay: 20}
	path := os.Getenv("APP_CONFIG")
	for i, a := range args {
		if a == "--config" && i+1 < len(args) {
			path = args[i+1]
		}
	}
	if path != "" {
		b, e := os.ReadFile(path)
		if e != nil {
			return c, e
		}
		if e = json.Unmarshal(b, &c); e != nil {
			return c, e
		}
	}
	for k, p := range map[string]*string{"FLEET_FILE": &c.FleetFile, "INVENTORY_FILE": &c.InventoryFile, "APP_ENV": &c.Env, "HTTP_ADDR": &c.HTTP, "DATABASE_URL": &c.DatabaseURL, "MQTT_URL": &c.MQTTURL, "CERT_DIR": &c.CertDir, "DATA_DIR": &c.DataDir, "DEV_API_TOKEN": &c.Token, "SIM_SCENARIO": &c.Scenario} {
		if v, ok := os.LookupEnv(k); ok {
			*p = v
		}
	}
	for k, p := range map[string]*int{"SIM_COUNT": &c.Count, "LIVE_TTL_SECONDS": &c.TTL, "LIVE_GRACE_SECONDS": &c.Grace, "MQTT_WILL_DELAY_SECONDS": &c.WillDelay} {
		if v, ok := os.LookupEnv(k); ok {
			n, e := strconv.Atoi(v)
			if e != nil {
				return c, e
			}
			*p = n
		}
	}
	fs := flag.NewFlagSet("crane", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.String("config", path, "JSON configuration")
	fs.StringVar(&c.HTTP, "http", c.HTTP, "listen address")
	fs.StringVar(&c.MQTTURL, "mqtt-url", c.MQTTURL, "TLS broker")
	fs.StringVar(&c.CertDir, "cert-dir", c.CertDir, "certificates")
	fs.StringVar(&c.DataDir, "data-dir", c.DataDir, "bounded event spool")
	fs.StringVar(&c.FleetFile, "fleet-file", c.FleetFile, "explicit simulator identity manifest")
	fs.StringVar(&c.InventoryFile, "inventory-file", c.InventoryFile, "private startup inventory, development only")
	fs.IntVar(&c.Count, "count", c.Count, "simulated devices")
	fs.StringVar(&c.Scenario, "scenario", c.Scenario, "normal|intermittent|alarm-storm|offline")
	fs.BoolVar(&c.Migrate, "migrate", false, "apply ordered migrations")
	if e := fs.Parse(args); e != nil {
		return c, e
	}
	if (c.FleetFile == "" && (c.Count < 1 || c.Count > 20)) || c.TTL < 1 || c.TTL > 300 || c.Grace < 1 || c.Grace > 60 || c.WillDelay < 5 || c.WillDelay > 60 {
		return c, errors.New("configuration outside supported bounds")
	}
	switch c.Scenario {
	case "normal", "intermittent", "alarm-storm", "offline":
	default:
		return c, errors.New("unknown scenario")
	}
	return c, nil
}
func TLS(dir, id string) (*tls.Config, error) {
	ca, e := os.ReadFile(dir + "/ca.crt")
	if e != nil {
		return nil, e
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("invalid CA")
	}
	cert, e := tls.LoadX509KeyPair(dir+"/"+id+".crt", dir+"/"+id+".key")
	if e != nil {
		return nil, e
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, Certificates: []tls.Certificate{cert}}, nil
}
func Logger(service string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			a.Key = "timestamp"
		}
		if a.Key == slog.MessageKey {
			a.Key = "message"
		}
		return a
	}})).With("service", service, "version", "0.1.0", "traceId", "", "deviceId", "", "projectId", "", "sessionId", "")
}
func Timeout() time.Duration { return 8 * time.Second }
