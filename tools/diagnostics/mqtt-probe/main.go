// Local integration probe: verifies certificate ACL and publishes synthetic events.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/platform"
	"net/url"
	"os"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	broker := flag.String("broker", "tls://localhost:8883", "broker")
	dir := flag.String("cert-dir", "deploy/compose/runtime/certs/devices", "certs")
	device := flag.String("device", "SIM-001", "runtime identity")
	project := flag.String("project", "Project-A", "own project")
	forbiddenProject := flag.String("forbidden-project", "Project-A", "denied target project")
	forbiddenDevice := flag.String("forbidden-device", "SIM-002", "denied target device")
	messageFile := flag.String("message", "", "optional anonymous test envelope")
	flag.Parse()
	tlsCfg, e := platform.TLS(*dir, *device)
	if e != nil {
		return e
	}
	u, e := url.Parse(*broker)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cm, e := autopaho.NewConnection(ctx, autopaho.ClientConfig{ServerUrls: []*url.URL{u}, TlsCfg: tlsCfg, ClientConfig: paho.ClientConfig{ClientID: "phase0-acl-probe"}, CleanStartOnInitialConnection: true})
	if e != nil {
		return e
	}
	defer cm.Disconnect(context.Background())
	if e = cm.AwaitConnection(ctx); e != nil {
		return e
	}
	forbidden, _ := contract.Topic("demo", *forbiddenProject, *forbiddenDevice, "alarm")
	resp, err := cm.Publish(ctx, &paho.Publish{Topic: forbidden, QoS: 1, Payload: []byte(`{}`)})
	if err == nil && (resp == nil || resp.ReasonCode < 128) {
		return fmt.Errorf("ACL allowed cross-device publication")
	}
	command, _ := contract.Topic("demo", *project, *device, "command/watch/start")
	resp, err = cm.Publish(ctx, &paho.Publish{Topic: command, QoS: 1, Payload: []byte(`{}`)})
	if err == nil && (resp == nil || resp.ReasonCode < 128) {
		return fmt.Errorf("ACL allowed device command publication")
	}
	own, _ := contract.Topic("demo", *project, *device, "alarm")
	ownResponse, ownErr := cm.Publish(ctx, &paho.Publish{Topic: own, QoS: 1, Payload: []byte(`{}`)})
	if ownErr != nil || ownResponse == nil || ownResponse.ReasonCode >= 128 {
		return fmt.Errorf("ACL rejected own project scope")
	}
	if *messageFile != "" {
		b, e := os.ReadFile(*messageFile)
		if e != nil {
			return e
		}
		var m contract.Envelope
		if e = json.Unmarshal(b, &m); e != nil {
			return e
		}
		suffix := "alarm"
		if m.Kind == "SNAPSHOT" {
			suffix = "shadow/reported"
		}
		topic, e := contract.Topic(m.TenantId, m.ProjectId, m.DeviceId, suffix)
		if e != nil {
			return e
		}
		for i := 0; i < 2; i++ {
			resp, e = cm.Publish(ctx, &paho.Publish{Topic: topic, QoS: 1, Payload: b})
			if e != nil {
				return e
			}
			if resp.ReasonCode >= 128 {
				return fmt.Errorf("own device publication rejected")
			}
		}
	}
	fmt.Println("MQTT TLS ACL probe passed")
	return nil
}
