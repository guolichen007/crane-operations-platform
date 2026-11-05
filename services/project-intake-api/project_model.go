package main

import (
	"encoding/json"
	"errors"
	"math"
)

func pointer[T any](v T) *T { return &v }
func badNumber(v *float64, max float64) bool {
	return v != nil && (*v < 0 || *v > max || math.IsNaN(*v) || math.IsInf(*v, 0))
}
func knowledgeState(state string, value *float64) (string, error) {
	if state == "" {
		if value == nil {
			return "UNKNOWN", nil
		}
		if *value == 0 {
			return "LEGACY_UNCONFIRMED", nil
		}
		return "KNOWN", nil
	}
	if state == "UNKNOWN" && value == nil || state == "KNOWN" && value != nil || state == "LEGACY_UNCONFIRMED" && value != nil && *value == 0 {
		return state, nil
	}
	return "", errors.New("数值及已知性标记不一致")
}
func numericKnowledge(values map[string]*float64, k *map[string]string) error {
	if *k == nil {
		*k = map[string]string{}
	}
	for name := range *k {
		if _, ok := values[name]; !ok {
			return errors.New("数值已知性字段不正确")
		}
	}
	for name, value := range values {
		state, err := knowledgeState((*k)[name], value)
		if err != nil {
			return err
		}
		(*k)[name] = state
	}
	return nil
}
func inputKnowledge(p *Input) error {
	if err := numericKnowledge(map[string]*float64{"estimatedAmount": p.EstimatedAmount}, &p.Knowledge); err != nil {
		return err
	}
	for i := range p.PlannedEquipment {
		e := &p.PlannedEquipment[i]
		var quantity *float64
		if e.Quantity != nil {
			quantity = pointer(float64(*e.Quantity))
		}
		if err := numericKnowledge(map[string]*float64{"quantity": quantity, "capacityTons": e.CapacityTons, "spanM": e.SpanM, "liftHeightM": e.LiftHeightM}, &e.Knowledge); err != nil {
			return err
		}
		for _, set := range []struct{ values, allowed []string }{{e.EnvironmentFlags, []string{"DUST", "HIGH_TEMPERATURE", "EXPLOSIVE", "OUTDOOR"}}, {e.AutomationGoals, requirements}} {
			seen := map[string]bool{}
			for _, v := range set.values {
				if !contains(set.allowed, v) || seen[v] {
					return errors.New("设备环境或自动化目标不正确")
				}
				seen[v] = true
			}
		}
	}
	return nil
}

// Internal empty strings are never exposed as a fictional owner or team.
func (p Project) MarshalJSON() ([]byte, error) {
	if p.Actions == nil {
		p.Actions = map[string]ActionHint{}
	}
	if p.AssignmentStatus == "" {
		p.AssignmentStatus = "ASSIGNED"
		if p.OwnerSubject == "" {
			p.AssignmentStatus = "UNASSIGNED_DRAFT"
		}
	}
	type plain Project
	body, err := json.Marshal(plain(p))
	if err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err = json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	if p.OwnerSubject == "" {
		obj["ownerSubject"] = json.RawMessage("null")
		obj["teamId"] = json.RawMessage("null")
	}
	return json.Marshal(obj)
}
