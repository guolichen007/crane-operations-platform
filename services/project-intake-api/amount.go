package main

import (
	"encoding/json"
	"math/big"
)

// G3-3B trusted project amount. The persisted amount is the numeric(18,2)
// column intake_projects.estimated_amount (yuan). document.estimatedAmount and
// document.knowledge.estimatedAmount carry the value the API accepted and its
// knowledge state (G1 rules, knowledgeState()). A project's amount counts as a
// trusted KNOWN amount only when the knowledge state is valid under those
// rules AND the column equals the document value exactly. Anything else is
// INVALID: reported, never summed, never rewritten.

const (
	amountKnown   = "KNOWN"
	amountUnknown = "UNKNOWN"
	amountLegacy  = "LEGACY_UNCONFIRMED"
	amountInvalid = "INVALID"
	amountMax     = "100000000000000" // validateInput: badNumber(..., 1e14)
)

// amountClassSQL is the SQL form of amountClass (alias p). It never casts
// unless jsonb_typeof is checked first, so malformed documents classify as
// INVALID instead of failing the whole query.
func amountClassSQL(p string) string {
	doc := p + `.document`
	v := `(CASE WHEN jsonb_typeof(` + doc + `->'estimatedAmount')='number' THEN (` + doc + `->>'estimatedAmount')::numeric END)`
	kt := `jsonb_typeof(` + doc + `->'knowledge')`
	st := `jsonb_typeof(` + doc + `->'knowledge'->'estimatedAmount')`
	k := `(` + doc + `->'knowledge'->>'estimatedAmount')`
	col := p + `.estimated_amount`
	return `(CASE
 WHEN coalesce(jsonb_typeof(` + doc + `->'estimatedAmount'),'null') NOT IN ('null','number') THEN 'INVALID'
 WHEN coalesce(` + kt + `,'null') NOT IN ('object','null') THEN 'INVALID'
 WHEN ` + kt + `='object' AND EXISTS(SELECT 1 FROM jsonb_object_keys(` + doc + `->'knowledge') AS kk(key) WHERE kk.key<>'estimatedAmount') THEN 'INVALID'
 WHEN coalesce(` + st + `,'null') NOT IN ('string','null') THEN 'INVALID'
 WHEN ` + v + `<0 OR ` + v + `>` + amountMax + ` THEN 'INVALID'
 WHEN coalesce(` + k + `,'')='' AND ` + v + ` IS NULL THEN CASE WHEN ` + col + ` IS NULL THEN 'UNKNOWN' ELSE 'INVALID' END
 WHEN coalesce(` + k + `,'')='' AND ` + v + `=0 THEN CASE WHEN ` + col + `=0 THEN 'LEGACY_UNCONFIRMED' ELSE 'INVALID' END
 WHEN coalesce(` + k + `,'')='' THEN CASE WHEN ` + col + `=` + v + ` THEN 'KNOWN' ELSE 'INVALID' END
 WHEN ` + k + `='UNKNOWN' AND ` + v + ` IS NULL AND ` + col + ` IS NULL THEN 'UNKNOWN'
 WHEN ` + k + `='KNOWN' AND ` + v + ` IS NOT NULL AND ` + col + `=` + v + ` THEN 'KNOWN'
 WHEN ` + k + `='LEGACY_UNCONFIRMED' AND ` + v + `=0 AND ` + col + `=0 THEN 'LEGACY_UNCONFIRMED'
 ELSE 'INVALID' END)`
}

// amountClass is the Go reference used to prove amountClassSQL: it decodes
// with the real Input model and knowledge rules (inputKnowledge) and compares
// the column with the document value as exact decimals. column is the
// numeric column as text, nil for NULL.
func amountClass(document []byte, column *string) string {
	var raw struct {
		Amount    json.RawMessage            `json:"estimatedAmount"`
		Knowledge map[string]json.RawMessage `json:"knowledge"`
	}
	if json.Unmarshal(document, &raw) != nil {
		return amountInvalid
	}
	var in struct {
		EstimatedAmount *float64          `json:"estimatedAmount"`
		Knowledge       map[string]string `json:"knowledge"`
	}
	if json.Unmarshal(document, &in) != nil {
		return amountInvalid
	}
	var exact *big.Rat // the document number as written, not its float64 rounding
	if len(raw.Amount) > 0 && string(raw.Amount) != "null" {
		r, ok := new(big.Rat).SetString(string(raw.Amount))
		if !ok {
			return amountInvalid
		}
		exact = r
		max, _ := new(big.Rat).SetString(amountMax)
		if r.Sign() < 0 || r.Cmp(max) > 0 {
			return amountInvalid
		}
	}
	ins := Input{EstimatedAmount: in.EstimatedAmount, Knowledge: in.Knowledge}
	if numericKnowledge(map[string]*float64{"estimatedAmount": ins.EstimatedAmount}, &ins.Knowledge) != nil {
		return amountInvalid
	}
	state := ins.Knowledge["estimatedAmount"]
	var col *big.Rat
	if column != nil {
		c, ok := new(big.Rat).SetString(*column)
		if !ok {
			return amountInvalid
		}
		col = c
	}
	switch state {
	case amountUnknown:
		if col == nil {
			return amountUnknown
		}
	case amountLegacy:
		if col != nil && col.Sign() == 0 {
			return amountLegacy
		}
	case amountKnown:
		if col != nil && exact != nil && col.Cmp(exact) == 0 {
			return amountKnown
		}
	}
	return amountInvalid
}
