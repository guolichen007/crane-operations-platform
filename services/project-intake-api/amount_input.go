package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
)

// G3-3B-R1 P1-B: exact estimated amounts on write.
//
// The API model keeps estimatedAmount as *float64 (G1 contract, no client
// change). Before decoding, POST /projects and PATCH /projects/{id} inspect
// the raw JSON number token and reject — 400 INVALID_INPUT — any amount that
// could not be stored exactly in both places (numeric(18,2) column and the
// document):
//   - negative, above 1e14, or not a plain decimal literal;
//   - more than two decimal places (0.125, 12345678.9999): the column would
//     round while the document keeps the original;
//   - a cent value that float64 cannot carry (90071992547409.91 →
//     …409.90): the server would otherwise silently store another amount.
// Every cent value up to 9,999,999,999,999.99 and every whole-yuan value up to
// 1e14 is accepted unchanged; only some cent values ≥ 1e13 are refused with an
// explicit message. null and an explicit 0 keep their G1 meanings.

const (
	errAmountFormat    = "预估金额格式不正确：请输入不小于 0 的普通十进制数字"
	errAmountRange     = "预估金额超出允许范围（0 至 100,000,000,000,000 元）"
	errAmountPrecision = "预估金额最多保留两位小数（精确到分）"
	errAmountLossy     = "预估金额位数过多，无法精确保存到分；请改为整元或较小的金额"
)

// plain decimal JSON number: no sign other than '-', no exponent.
var plainDecimal = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// checkAmountToken validates one raw JSON value of estimatedAmount. It returns
// "" when the value is acceptable (including null/absent).
func checkAmountToken(raw json.RawMessage) string {
	text := string(bytes.TrimSpace(raw))
	if len(raw) == 0 || text == "null" {
		return ""
	}
	if !plainDecimal.MatchString(text) {
		return errAmountFormat
	}
	exact, ok := new(big.Rat).SetString(text)
	if !ok {
		return errAmountFormat
	}
	max, _ := new(big.Rat).SetString(amountMax)
	if exact.Sign() < 0 || exact.Cmp(max) > 0 {
		return errAmountRange
	}
	if i := bytes.IndexByte([]byte(text), '.'); i >= 0 && len(text)-i-1 > 2 {
		// 1.50 / 1.500 are fine only if the extra digits are zeros.
		cents := new(big.Rat).Mul(exact, big.NewRat(100, 1))
		if !cents.IsInt() {
			return errAmountPrecision
		}
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return errAmountFormat
	}
	back, _ := new(big.Rat).SetString(strconv.FormatFloat(f, 'f', -1, 64))
	if back == nil || back.Cmp(exact) != 0 {
		return errAmountLossy
	}
	return ""
}

// decodeProjectWrite reads the body once, validates the raw amount token and
// then decodes it with the normal strict decoder.
func decodeProjectWrite(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		fail(w, 415, "CONTENT_TYPE", "请使用 JSON 请求")
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
	if err != nil {
		fail(w, 400, "INVALID_INPUT", "请求字段不正确")
		return false
	}
	var raw struct {
		Amount json.RawMessage `json:"estimatedAmount"`
	}
	// Malformed JSON is reported by decode() below with its usual message.
	if json.Unmarshal(body, &raw) == nil {
		if msg := checkAmountToken(raw.Amount); msg != "" {
			fail(w, 400, "INVALID_INPUT", msg)
			return false
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return decode(w, r, v)
}
