// Package equipment owns versioned Equipment specification validation.
//
// Equipment type-specific fields live in JSONB but are never uncontrolled:
// specifications are validated against a versioned JSON Schema before any Go
// write. Connectivity / runtime state / condition are TELEMETRY_OWNED and are
// deliberately absent from every schema, so any attempt to store them as a
// BUSINESS field is rejected as an unknown field.
package equipment

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

//go:embed specs/v1/*.json
var specFiles embed.FS

// SpecSchemaVersion is the only schema version currently accepted.
const SpecSchemaVersion = "1"

var (
	ErrUnsupportedType        = errors.New("unsupported equipment type_code")
	ErrUnsupportedSpecVersion = errors.New("unsupported equipment spec schema version")
)

// specFileByType maps each registered stable type_code to its specification schema
// file (without extension). Code-owned and forward-extensible; type_code is
// never a DB hard enum.
var specFileByType = map[string]string{
	"LIDAR_3D":              "lidar_3d",
	"LIDAR_2D":              "lidar_3d",
	"CAMERA_RGB":            "camera",
	"CAMERA_DEPTH":          "camera",
	"CAMERA_STEREO":         "camera",
	"CAMERA_THERMAL":        "camera",
	"PLC":                   "plc",
	"SAFETY_PLC":            "plc",
	"VFD":                   "vfd",
	"SERVO_DRIVE":           "vfd",
	"MOTOR":                 "motor",
	"INDUSTRIAL_PC":         "industrial_pc",
	"EDGE_GATEWAY":          "industrial_pc",
	"INDUSTRIAL_SWITCH":     "industrial_switch",
	"POE_SWITCH":            "industrial_switch",
	"LOAD_CELL":             "load_cell",
	"IMU":                   "imu",
	"REDUCER":               "generic",
	"BRAKE":                 "generic",
	"DRUM":                  "generic",
	"HOOK":                  "generic",
	"GRAB":                  "generic",
	"WHEEL":                 "generic",
	"COUPLING":              "generic",
	"PLC_IO":                "generic",
	"RELAY":                 "generic",
	"CONTACTOR":             "generic",
	"REMOTE_CONTROLLER":     "generic",
	"ENCODER":               "generic",
	"ROPE_ENCODER":          "generic",
	"LIMIT_SWITCH":          "generic",
	"PROXIMITY_SENSOR":      "generic",
	"ANTI_COLLISION_SENSOR": "generic",
	"WIND_SENSOR":           "generic",
	"TEMPERATURE_SENSOR":    "generic",
	"VIBRATION_SENSOR":      "generic",
	"GPU":                   "generic",
	"ROUTER":                "generic",
	"MEDIA_CONVERTER":       "generic",
	"POWER_SUPPLY":          "generic",
	"UPS":                   "generic",
	"TRANSFORMER":           "generic",
	"CONDUCTOR_RAIL":        "generic",
}

// specFileFor returns the schema file for typeCode, if registered.
func specFileFor(typeCode string) (string, bool) {
	name, ok := specFileByType[typeCode]
	return name, ok
}

type specSchema struct {
	Type                 string               `json:"type"`
	AdditionalProperties *bool                `json:"additionalProperties"`
	Properties           map[string]specField `json:"properties"`
	Required             []string             `json:"required"`
}

type specField struct {
	Type    string     `json:"type"`
	Enum    []string   `json:"enum"`
	Minimum *float64   `json:"minimum"`
	Maximum *float64   `json:"maximum"`
	MaxLen  *int       `json:"maxLength"`
	Items   *specField `json:"items"`
}

// ValidTypeCode reports whether typeCode is a registered equipment type.
func ValidTypeCode(typeCode string) bool {
	_, ok := specFileFor(typeCode)
	return ok
}

// ValidateSpec validates a specifications payload against the versioned schema
// for typeCode. It rejects unknown fields, wrong types, out-of-range values,
// unknown schema versions, and unregistered type codes.
func ValidateSpec(typeCode, specVersion string, spec json.RawMessage) error {
	if specVersion != SpecSchemaVersion {
		return fmt.Errorf("%w: %s", ErrUnsupportedSpecVersion, specVersion)
	}
	name, ok := specFileFor(typeCode)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnsupportedType, typeCode)
	}
	raw, err := specFiles.ReadFile("specs/v1/" + name + ".json")
	if err != nil {
		return fmt.Errorf("load spec schema for %s: %w", typeCode, err)
	}
	var schema specSchema
	if err = json.Unmarshal(raw, &schema); err != nil {
		return fmt.Errorf("parse spec schema for %s: %w", typeCode, err)
	}
	if schema.Type != "object" {
		return fmt.Errorf("spec schema for %s is not an object", typeCode)
	}

	var obj map[string]json.RawMessage
	if err = json.Unmarshal(spec, &obj); err != nil {
		return fmt.Errorf("spec must be a JSON object: %w", err)
	}
	if obj == nil {
		return errors.New("spec must be a JSON object, not null")
	}

	// Credential / secret material is never a permitted field on any schema.
	for key := range obj {
		if isSecretField(key) {
			return fmt.Errorf("field %q is forbidden: credential/secret material never enters a BUSINESS spec", key)
		}
	}

	// additionalProperties:false => reject any field outside the schema.
	if schema.AdditionalProperties != nil && !*schema.AdditionalProperties {
		for key := range obj {
			if _, known := schema.Properties[key]; !known {
				return fmt.Errorf("unknown field %q for type %s", key, typeCode)
			}
		}
	}

	for _, req := range schema.Required {
		if _, present := obj[req]; !present {
			return fmt.Errorf("missing required field %q for type %s", req, typeCode)
		}
	}

	for key, value := range obj {
		if err = validateField(typeCode, key, schema.Properties[key], value); err != nil {
			return err
		}
	}
	return nil
}

func isSecretField(key string) bool {
	switch key {
	case "password", "secret", "credential", "privateKey", "private_key",
		"token", "apiKey", "api_key", "mqttSecret", "mqtt_secret":
		return true
	}
	return false
}

func validateField(typeCode, key string, field specField, raw json.RawMessage) error {
	switch field.Type {
	case "string":
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("field %q must be a string: %w", key, err)
		}
		if len(field.Enum) > 0 && !contains(field.Enum, v) {
			return fmt.Errorf("field %q must be one of %v", key, field.Enum)
		}
		if field.MaxLen != nil && len(v) > *field.MaxLen {
			return fmt.Errorf("field %q exceeds max length %d", key, *field.MaxLen)
		}
	case "integer":
		var v float64
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("field %q must be an integer: %w", key, err)
		}
		if v != math.Trunc(v) {
			return fmt.Errorf("field %q must be an integer", key)
		}
		if field.Minimum != nil && v < *field.Minimum {
			return fmt.Errorf("field %q below minimum %.0f", key, *field.Minimum)
		}
		if field.Maximum != nil && v > *field.Maximum {
			return fmt.Errorf("field %q above maximum %.0f", key, *field.Maximum)
		}
	case "number":
		var v float64
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("field %q must be a number: %w", key, err)
		}
		if field.Minimum != nil && v < *field.Minimum {
			return fmt.Errorf("field %q below minimum %g", key, *field.Minimum)
		}
		if field.Maximum != nil && v > *field.Maximum {
			return fmt.Errorf("field %q above maximum %g", key, *field.Maximum)
		}
	case "boolean":
		var v bool
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("field %q must be a boolean: %w", key, err)
		}
	case "array":
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return fmt.Errorf("field %q must be an array: %w", key, err)
		}
		if field.Items != nil && field.Items.Type == "string" {
			for i, item := range items {
				var s string
				if err := json.Unmarshal(item, &s); err != nil {
					return fmt.Errorf("field %q item %d must be a string", key, i)
				}
			}
		}
	default:
		return fmt.Errorf("unsupported schema field type %q for %q (%s)", field.Type, key, typeCode)
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
