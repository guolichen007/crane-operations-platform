package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func ValidID(s string) bool { return identifier.MatchString(s) }
func Topic(tenant, project, device, suffix string) (string, error) {
	if !ValidID(tenant) || !ValidID(project) || !ValidID(device) {
		return "", errors.New("invalid topic identity")
	}
	if _, ok := Topics[suffix]; !ok {
		return "", errors.New("unknown topic suffix")
	}
	return "crane/v1/" + tenant + "/" + project + "/" + device + "/" + suffix, nil
}
func ParseTopic(topic string) (tenant, project, device, suffix string, err error) {
	p := strings.Split(topic, "/")
	if len(p) < 6 || p[0] != "crane" || p[1] != "v1" {
		err = errors.New("invalid topic")
		return
	}
	tenant, project, device, suffix = p[2], p[3], p[4], strings.Join(p[5:], "/")
	_, err = Topic(tenant, project, device, suffix)
	return
}

// Payload returns a concrete wire payload. Validate/Decode must run at the trust boundary.
func Payload[T any](m Envelope) T                  { var v T; _ = json.Unmarshal(m.Payload, &v); return v }
func WithPayload(m Envelope, payload any) Envelope { m.Payload, _ = json.Marshal(payload); return m }

type fieldSchema struct {
	Type      string   `json:"type"`
	Ref       string   `json:"$ref"`
	Format    string   `json:"format"`
	Pattern   string   `json:"pattern"`
	Enum      []string `json:"enum"`
	Const     *float64 `json:"const"`
	Minimum   *float64 `json:"minimum"`
	Maximum   *float64 `json:"maximum"`
	MinLength int      `json:"minLength"`
	MaxLength *int     `json:"maxLength"`
}
type objectSchema struct {
	Properties map[string]fieldSchema `json:"properties"`
}

var wireSchemas = func() map[string]objectSchema {
	var v map[string]objectSchema
	if err := json.Unmarshal([]byte(schemaJSON), &v); err != nil {
		panic(err)
	}
	return v
}()

// validateObject implements only the closed, required-field object vocabulary emitted
// by our generator. The same model produces the standard JSON Schemas parsed in CI.
func validateObject(name string, raw []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return errors.New("expected object: " + name)
	}
	schema := wireSchemas[name]
	if len(fields) != len(schema.Properties) {
		return errors.New("missing or unknown fields: " + name)
	}
	for key, rule := range schema.Properties {
		b, ok := fields[key]
		if !ok || bytes.Equal(b, []byte("null")) {
			return fmt.Errorf("required %s.%s", name, key)
		}
		if rule.Ref != "" {
			if err := validateObject(strings.TrimPrefix(rule.Ref, "#/$defs/"), b); err != nil {
				return err
			}
			continue
		}
		if rule.Type == "string" {
			var v string
			if json.Unmarshal(b, &v) != nil {
				return fmt.Errorf("string required: %s", key)
			}
			length := utf8.RuneCountInString(v)
			if length < rule.MinLength || (rule.MaxLength != nil && length > *rule.MaxLength) || (len(rule.Enum) > 0 && !slices.Contains(rule.Enum, v)) {
				return fmt.Errorf("invalid string: %s", key)
			}
			if rule.Pattern != "" && !regexp.MustCompile(rule.Pattern).MatchString(v) {
				return fmt.Errorf("invalid identifier: %s", key)
			}
			if rule.Format == "date-time" {
				if _, err := time.Parse(time.RFC3339Nano, v); err != nil {
					return fmt.Errorf("invalid time: %s", key)
				}
			}
		} else {
			var v float64
			if json.Unmarshal(b, &v) != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("number required: %s", key)
			}
			if (rule.Type == "integer" && math.Trunc(v) != v) || (rule.Const != nil && v != *rule.Const) || (rule.Minimum != nil && v < *rule.Minimum) || (rule.Maximum != nil && v > *rule.Maximum) {
				return fmt.Errorf("invalid number: %s", key)
			}
		}
	}
	return nil
}
func Validate(m Envelope) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(b, &fields)
	delete(fields, "payload")
	meta, _ := json.Marshal(fields)
	if err = validateObject("Envelope", meta); err != nil {
		return err
	}
	name, ok := KindPayloads[m.Kind]
	if !ok {
		return errors.New("unknown message kind")
	}
	return validateObject(name, m.Payload)
}

// Decode enforces topic identity, kind and the corresponding concrete payload together.
func Decode(topic string, raw []byte) (Envelope, error) {
	var m Envelope
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, err
	}
	// Validate the original object too, so omitted zero-valued fields cannot be hidden by unmarshalling.
	var original map[string]json.RawMessage
	if err := json.Unmarshal(raw, &original); err != nil {
		return m, err
	}
	delete(original, "payload")
	meta, _ := json.Marshal(original)
	if err := validateObject("Envelope", meta); err != nil {
		return m, err
	}
	t, p, id, suffix, err := ParseTopic(topic)
	if err != nil {
		return m, err
	}
	if t != m.TenantId || p != m.ProjectId || id != m.DeviceId || !slices.Contains(TopicKinds[suffix], m.Kind) {
		return m, errors.New("topic/kind/identity mismatch")
	}
	return m, Validate(m)
}
