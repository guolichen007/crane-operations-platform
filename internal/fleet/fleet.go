// Package fleet binds each runtime identity to exactly one tenant/project.
package fleet

import (
	"bytes"
	"errors"
	"fmt"
	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"go.yaml.in/yaml/v3"
	"io"
	"os"
	"sort"
	"strings"
)

type Device struct {
	TenantID string
	contract.RuntimeDescriptor
}

func Read(path string, value any) error {
	file, err := os.Open(path)
	if err != nil {
		return errors.New("cannot open inventory/fleet file")
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err != nil || len(b) > 1024*1024 {
		return errors.New("inventory/fleet exceeds size limit")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)
	if decoder.Decode(value) != nil {
		return errors.New("invalid YAML or unknown/duplicate fields")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("only one YAML document is allowed")
	}
	return nil
}
func LegacyDescriptor(id string) Device {
	return Device{TenantID: "demo", RuntimeDescriptor: contract.RuntimeDescriptor{RuntimeDeviceId: id, ProjectId: "Project-A", SourceMode: "SIMULATION"}}
}
func Legacy(count int, fixture bool) contract.FleetManifest {
	m := contract.FleetManifest{TenantId: "demo", Devices: []contract.RuntimeDescriptor{}}
	for i := 1; i <= count; i++ {
		m.Devices = append(m.Devices, LegacyDescriptor(fmt.Sprintf("SIM-%03d", i)).RuntimeDescriptor)
	}
	if fixture {
		d := LegacyDescriptor("TEST-001")
		d.SourceMode = "TEST"
		m.Devices = append(m.Devices, d.RuntimeDescriptor)
	}
	return m
}
func Validate(m contract.FleetManifest, simulationOnly bool) error {
	if err := contract.ValidateDocument("FleetManifest", m); err != nil {
		return err
	}
	if len(m.Devices) < 1 || len(m.Devices) > 100 {
		return errors.New("fleet must contain 1..100 identities")
	}
	ids := map[string]bool{}
	for _, d := range m.Devices {
		if d.RuntimeDeviceId == "cloud-api" || d.RuntimeDeviceId == "mosquitto" {
			return errors.New("reserved runtime identity")
		}
		if ids[d.RuntimeDeviceId] {
			return errors.New("duplicate runtime identity")
		}
		ids[d.RuntimeDeviceId] = true
		if simulationOnly && d.SourceMode != "SIMULATION" {
			return errors.New("mock runner only accepts SIMULATION identities")
		}
		if d.SourceMode == "SIMULATION" && !strings.HasPrefix(d.RuntimeDeviceId, "SIM-") || d.SourceMode == "REAL" && !strings.HasPrefix(d.RuntimeDeviceId, "EDGE-") {
			return errors.New("runtime identity prefix does not match source mode")
		}
	}
	return nil
}
func Load(path string, count int, fixture, simulationOnly bool) (contract.FleetManifest, error) {
	if path == "" {
		m := Legacy(count, fixture)
		return m, Validate(m, simulationOnly)
	}
	var m contract.FleetManifest
	if err := Read(path, &m); err != nil {
		return m, err
	}
	return m, Validate(m, simulationOnly)
}
func Devices(m contract.FleetManifest) []Device {
	result := make([]Device, 0, len(m.Devices))
	for _, d := range m.Devices {
		result = append(result, Device{TenantID: m.TenantId, RuntimeDescriptor: d})
	}
	return result
}
func Allows(m contract.FleetManifest, e contract.Envelope) bool {
	for _, d := range m.Devices {
		if m.TenantId == e.TenantId && d.ProjectId == e.ProjectId && d.RuntimeDeviceId == e.DeviceId {
			return true
		}
	}
	return false
}
func Subscriptions(m contract.FleetManifest) []string {
	result := []string{}
	for _, d := range m.Devices {
		for _, suffix := range suffixes() {
			kinds := contract.TopicKinds[suffix]
			if kinds[0] == "WATCH_START" || kinds[0] == "WATCH_RENEW" || kinds[0] == "WATCH_STOP" || kinds[0] == "EVENT_STORED_ACK" {
				continue
			}
			t, _ := contract.Topic(m.TenantId, d.ProjectId, d.RuntimeDeviceId, suffix)
			result = append(result, t)
		}
	}
	return result
}
func ACL(m contract.FleetManifest) (string, error) {
	if err := Validate(m, false); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# Generated development ACL: exact tenant/project/device scopes; not production PKI.\n")
	for _, d := range m.Devices {
		fmt.Fprintf(&b, "user %s\n", d.RuntimeDeviceId)
		for _, suffix := range suffixes() {
			kinds := contract.TopicKinds[suffix]
			mode := "write"
			if strings.HasPrefix(kinds[0], "WATCH_") && kinds[0] != "WATCH_ACK" || kinds[0] == "EVENT_STORED_ACK" {
				mode = "read"
			}
			topic, _ := contract.Topic(m.TenantId, d.ProjectId, d.RuntimeDeviceId, suffix)
			fmt.Fprintf(&b, "topic %s %s\n", mode, topic)
		}
	}
	b.WriteString("user cloud-api\n")
	for _, d := range m.Devices {
		for _, suffix := range suffixes() {
			kinds := contract.TopicKinds[suffix]
			mode := "read"
			if strings.HasPrefix(kinds[0], "WATCH_") && kinds[0] != "WATCH_ACK" || kinds[0] == "EVENT_STORED_ACK" {
				mode = "write"
			}
			topic, _ := contract.Topic(m.TenantId, d.ProjectId, d.RuntimeDeviceId, suffix)
			fmt.Fprintf(&b, "topic %s %s\n", mode, topic)
		}
	}
	return b.String(), nil
}

func suffixes() []string {
	keys := []string{}
	for key := range contract.TopicKinds {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
