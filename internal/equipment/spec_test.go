package equipment

import (
	"encoding/json"
	"testing"
)

func mustAccept(t *testing.T, typeCode, spec string) {
	t.Helper()
	if err := ValidateSpec(typeCode, SpecSchemaVersion, json.RawMessage(spec)); err != nil {
		t.Errorf("%s valid spec rejected: %v", typeCode, err)
	}
}

func mustReject(t *testing.T, typeCode, version, spec string) {
	t.Helper()
	if err := ValidateSpec(typeCode, version, json.RawMessage(spec)); err == nil {
		t.Errorf("invalid spec accepted: type=%s version=%s spec=%s", typeCode, version, spec)
	}
}

func TestValidateSpecAcceptsRepresentativeTypes(t *testing.T) {
	mustAccept(t, "LIDAR_3D", `{"channels": 128, "scan_type": "rotating", "maximum_range_m": 120, "supported_protocols": ["TCP"]}`)
	mustAccept(t, "CAMERA_RGB", `{"camera_type": "RGB", "resolution_width": 1920, "resolution_height": 1080, "interface_type": "GigE"}`)
	mustAccept(t, "PLC", `{"cpu_model": "S7-1200", "di_count": 16, "do_count": 16}`)
	mustAccept(t, "VFD", `{"rated_power_kw": 5.5, "input_voltage_v": 380}`)
	mustAccept(t, "MOTOR", `{"rated_power_kw": 7.5, "rated_speed_rpm": 1450, "brake_integrated": true}`)
	mustAccept(t, "INDUSTRIAL_PC", `{"cpu_model": "i5", "ram_gb": 16, "storage_gb": 256}`)
	mustAccept(t, "INDUSTRIAL_SWITCH", `{"port_count": 8, "poe_capable": true, "poe_budget_w": 120}`)
	mustAccept(t, "LOAD_CELL", `{"rated_capacity": 50, "accuracy_class": "C3"}`)
	mustAccept(t, "IMU", `{"axes": 6, "output_rate_hz": 100}`)
}

func TestValidateSpecRejectsInvalid(t *testing.T) {
	mustReject(t, "LIDAR_3D", SpecSchemaVersion, `{"nonsense": 1}`)
	mustReject(t, "LIDAR_3D", SpecSchemaVersion, `{"channels": "128"}`)
	mustReject(t, "LIDAR_3D", SpecSchemaVersion, `{"maximum_range_m": -5}`)
	mustReject(t, "LIDAR_3D", SpecSchemaVersion, `{"scan_type": "spinning"}`)
	mustReject(t, "CAMERA_RGB", SpecSchemaVersion, `{"resolution_width": 1.5}`)
	mustReject(t, "LIDAR_3D", "2", `{}`)
	mustReject(t, "QUANTUM_CRANE", SpecSchemaVersion, `{}`)
	mustReject(t, "PLC", SpecSchemaVersion, `{"password": "x"}`)
	mustReject(t, "INDUSTRIAL_PC", SpecSchemaVersion, `{"privateKey": "x"}`)
	mustReject(t, "IMU", SpecSchemaVersion, `{"mqttSecret": "x"}`)
	mustReject(t, "LIDAR_3D", SpecSchemaVersion, `null`)
}

// Connectivity / condition / runtime state are TELEMETRY_OWNED and deliberately
// absent from every BUSINESS specification schema.
func TestValidateSpecRejectsTelemetryOwnedFields(t *testing.T) {
	for _, spec := range []string{`{"condition": "NORMAL"}`, `{"connectivity": "ONLINE"}`, `{"temperature": 42}`} {
		if err := ValidateSpec("MOTOR", SpecSchemaVersion, json.RawMessage(spec)); err == nil {
			t.Fatalf("telemetry-owned field accepted in BUSINESS spec: %s", spec)
		}
	}
}

func TestValidTypeCode(t *testing.T) {
	for _, code := range []string{"LIDAR_3D", "MOTOR", "PLC", "LOAD_CELL", "IMU"} {
		if !ValidTypeCode(code) {
			t.Errorf("frozen type_code %s not registered", code)
		}
	}
	if ValidTypeCode("NOT_A_TYPE") {
		t.Error("unknown type_code registered")
	}
}
