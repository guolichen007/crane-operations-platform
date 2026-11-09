package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"testing"
)

// G3-3B-R1 P1-B: exact amount input. For every accepted request the raw
// request amount, the numeric column and the document amount are equal as
// decimals; every refused request writes nothing.

func (c testClient) callRaw(method, path, body string) (int, []byte) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.origin+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if method == "POST" && path == "/api/v1/projects" {
		req.Header.Set("Idempotency-Key", randomToken())
	}
	req.Header.Set("Origin", c.origin)
	req.Header.Set("X-CSRF-Token", c.csrf)
	r, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r.StatusCode, b
}

func TestCheckAmountToken(t *testing.T) {
	for raw, want := range map[string]string{
		"null": "", "0": "", "0.00": "", "0.01": "", "0.10": "", "125000.25": "", "1.500": "", "9999999999999.99": "",
		"90071992547409.90": "", "100000000000000": "", "100000000000000.00": "", "99999999999999": "",
		"0.125": errAmountPrecision, "100.005": errAmountPrecision, "12345678.9999": errAmountPrecision,
		"90071992547409.91": errAmountLossy, "99999999999999.99": errAmountLossy,
		"100000000000000.01": errAmountRange, "-1": errAmountRange, "-0.01": errAmountRange,
		"1e3": errAmountFormat, "1E-2": errAmountFormat, `"5"`: errAmountFormat, "true": errAmountFormat, "01": errAmountFormat,
	} {
		if got := checkAmountToken(json.RawMessage(raw)); got != want {
			t.Errorf("%s: %q want %q", raw, got, want)
		}
	}
}

// writeState is everything a create/PATCH may write.
func writeState(t *testing.T, f g1Fixture) string {
	t.Helper()
	var s string
	err := f.db.QueryRow(context.Background(), `SELECT concat_ws('|',
 (SELECT count(*) FROM intake_projects),(SELECT coalesce(sum(version),0) FROM intake_projects),
 (SELECT count(*) FROM audit_events),(SELECT count(*) FROM project_participations),
 (SELECT count(*) FROM project_activities),(SELECT count(*) FROM project_parties),(SELECT count(*) FROM planned_equipment),
 (SELECT count(*) FROM project_create_requests),(SELECT string_agg(coalesce(estimated_amount::text,'∅'),',' ORDER BY id) FROM intake_projects))`).Scan(&s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func projectBody(name, amount, knowledge string) string {
	in := map[string]any{}
	_ = json.Unmarshal(mustJSON(sample(name)), &in)
	in["customer"] = name + "客户"
	delete(in, "estimatedAmount")
	delete(in, "knowledge")
	delete(in, "version")
	b, _ := json.Marshal(in)
	s := strings.TrimSuffix(string(b), "}")
	if amount != "" {
		s += `,"estimatedAmount":` + amount
	}
	if knowledge != "" {
		s += `,"knowledge":{"estimatedAmount":"` + knowledge + `"}`
	}
	return s + "}"
}

func storedAmount(t *testing.T, f g1Fixture, id string) (col, doc, k *string) {
	t.Helper()
	err := f.db.QueryRow(context.Background(), `SELECT estimated_amount::text, document->>'estimatedAmount', document->'knowledge'->>'estimatedAmount' FROM intake_projects WHERE id=$1`, id).Scan(&col, &doc, &k)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func decEqual(a string, b *string) bool {
	if b == nil {
		return a == "null"
	}
	x, ok1 := new(big.Rat).SetString(a)
	y, ok2 := new(big.Rat).SetString(*b)
	return ok1 && ok2 && x.Cmp(y) == 0
}

func TestDatabaseG33BR1AmountInput(t *testing.T) {
	f := g1FixtureFor(t)
	report := &strings.Builder{}
	report.WriteString("| 请求金额 | knowledge | HTTP | 列 | 文档 | 规范化 knowledge | 结果 |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	type tc struct{ amount, knowledge, wantK, wantErr string }
	cases := []tc{
		{"null", "", "UNKNOWN", ""}, {"null", "UNKNOWN", "UNKNOWN", ""},
		{"0", "KNOWN", "KNOWN", ""}, {"0", "", "LEGACY_UNCONFIRMED", ""}, {"0", "LEGACY_UNCONFIRMED", "LEGACY_UNCONFIRMED", ""},
		{"0.01", "KNOWN", "KNOWN", ""}, {"0.10", "KNOWN", "KNOWN", ""}, {"125000.25", "KNOWN", "KNOWN", ""},
		{"125000.25", "", "KNOWN", ""}, // old client without knowledge
		{"9999999999999.99", "KNOWN", "KNOWN", ""}, {"90071992547409.90", "KNOWN", "KNOWN", ""},
		{"100000000000000", "KNOWN", "KNOWN", ""}, {"100000000000000.00", "KNOWN", "KNOWN", ""},
		{"0.125", "KNOWN", "", errAmountPrecision}, {"100.005", "KNOWN", "", errAmountPrecision}, {"12345678.9999", "KNOWN", "", errAmountPrecision},
		{"90071992547409.91", "KNOWN", "", errAmountLossy}, {"99999999999999.99", "KNOWN", "", errAmountLossy},
		{"100000000000000.01", "KNOWN", "", errAmountRange}, {"-1", "KNOWN", "", errAmountRange},
		{"1e3", "KNOWN", "", errAmountFormat}, {`"125000.25"`, "KNOWN", "", errAmountFormat},
		{"5", "UNKNOWN", "", "数值及已知性标记不一致"}, // G1 rule still applies after the token check
	}
	for i, c := range cases {
		name := fmt.Sprintf("合成金额写入%02d", i)
		before := writeState(t, f)
		s, b := f.sales.callRaw("POST", "/api/v1/projects", projectBody(name, c.amount, c.knowledge))
		row := fmt.Sprintf("| %s | %s | %d |", c.amount, c.knowledge, s)
		if c.wantErr != "" {
			if s != 400 || !strings.Contains(string(b), "INVALID_INPUT") || !strings.Contains(string(b), c.wantErr) {
				t.Fatalf("%s: HTTP %d %s want 400 %s", c.amount, s, b, c.wantErr)
			}
			if after := writeState(t, f); after != before {
				t.Fatalf("%s: refused create wrote data", c.amount)
			}
			fmt.Fprintf(report, "%s — | — | — | 拒绝：%s |\n", row, c.wantErr)
			continue
		}
		p := projectFrom(t, s, b)
		col, doc, k := storedAmount(t, f, p.ID)
		if !decEqual(c.amount, col) || !decEqual(c.amount, doc) || k == nil || *k != c.wantK {
			t.Fatalf("%s: column %v document %v knowledge %v", c.amount, col, doc, k)
		}
		var docRaw []byte
		_ = f.db.QueryRow(context.Background(), `SELECT document::text FROM intake_projects WHERE id=$1`, p.ID).Scan(&docRaw)
		cls := amountClass(docRaw, col)
		wantCls := map[string]string{"KNOWN": amountKnown, "UNKNOWN": amountUnknown, "LEGACY_UNCONFIRMED": amountLegacy}[c.wantK]
		if cls != wantCls {
			t.Fatalf("%s: accepted write classified %s", c.amount, cls)
		}
		show := func(x *string) string {
			if x == nil {
				return "NULL"
			}
			return *x
		}
		fmt.Fprintf(report, "%s %s | %s | %s | 接受，三者十进制相等，统计类别 %s |\n", row, show(col), show(doc), *k, cls)
	}

	t.Run("patch-edit-reject-and-409", func(t *testing.T) {
		s, b := f.sales.callRaw("POST", "/api/v1/projects", projectBody("合成金额编辑", "1000.50", "KNOWN"))
		p := projectFrom(t, s, b)
		patch := func(c testClient, version int, amount, knowledge string) (int, []byte) {
			body := projectBody("合成金额编辑", amount, knowledge)
			body = strings.Replace(body, "{", fmt.Sprintf(`{"version":%d,`, version), 1)
			return c.callRaw("PATCH", "/api/v1/projects/"+p.ID, body)
		}
		// Change to another legal amount.
		s, b = patch(f.sales, p.Version, "2500.75", "KNOWN")
		p = projectFrom(t, s, b)
		if col, doc, _ := storedAmount(t, f, p.ID); !decEqual("2500.75", col) || !decEqual("2500.75", doc) {
			t.Fatal("edit", col, doc)
		}
		// Refused edits change nothing: precision, loss, range, format.
		for _, bad := range []string{"123.456", "90071992547409.91", "-5", "1e2"} {
			before := writeState(t, f)
			s, b = patch(f.sales, p.Version, bad, "KNOWN")
			expect(t, s, 400, b)
			if writeState(t, f) != before {
				t.Fatal("refused edit wrote data", bad)
			}
		}
		// Two sessions with the same version: one 200, one 409 (no partial write).
		s1, _ := patch(f.sales, p.Version, "3000.00", "KNOWN")
		before := writeState(t, f)
		s2, b2 := patch(f.sales, p.Version, "4000.00", "KNOWN")
		if s1 != 200 || s2 != 409 || writeState(t, f) != before {
			t.Fatal("concurrent", s1, s2, string(b2))
		}
		if col, _, _ := storedAmount(t, f, p.ID); !decEqual("3000.00", col) {
			t.Fatal("winner", col)
		}
		// UNKNOWN and explicit 0 still differ.
		p = projectFromGet(t, f.sales, p.ID)
		s, b = patch(f.sales, p.Version, "null", "UNKNOWN")
		p = projectFrom(t, s, b)
		s, b = patch(f.sales, p.Version, "0", "KNOWN")
		p = projectFrom(t, s, b)
		if col, _, k := storedAmount(t, f, p.ID); !decEqual("0", col) || *k != "KNOWN" {
			t.Fatal("known zero", col, *k)
		}
	})

	t.Run("historical-invalid-has-a-recovery-path", func(t *testing.T) {
		// A record written by the pre-R1 API: column rounded, document kept the
		// original (synthetic; same bytes the old API produced).
		s, b := f.sales.callRaw("POST", "/api/v1/projects", projectBody("合成历史异常金额", "1.00", "KNOWN"))
		p := projectFrom(t, s, b)
		g1Exec(t, f.db, `UPDATE intake_projects SET estimated_amount=12345679.00, document=jsonb_set(document,'{estimatedAmount}','12345678.9999') WHERE id=$1`, p.ID)
		var docRaw []byte
		var col *string
		_ = f.db.QueryRow(context.Background(), `SELECT document::text, estimated_amount::text FROM intake_projects WHERE id=$1`, p.ID).Scan(&docRaw, &col)
		if amountClass(docRaw, col) != amountInvalid {
			t.Fatal("fixture should be INVALID")
		}
		// The project stays readable; stage changes (no amount input) still work.
		cur := projectFromGet(t, f.sales, p.ID)
		if cur.EstimatedAmount == nil || *cur.EstimatedAmount != 12345678.9999 {
			t.Fatal("historical value must be shown as stored", cur.EstimatedAmount)
		}
		s, b = f.sales.call("POST", "/api/v1/projects/"+p.ID+"/stage-change", map[string]any{"version": cur.Version, "salesStage": "L2_TECHNICAL", "reason": "合成推进"})
		cur = projectFrom(t, s, b)
		// Re-saving the stored amount unchanged is refused with an explicit message …
		body := strings.Replace(projectBody("合成历史异常金额", "12345678.9999", "KNOWN"), "{", fmt.Sprintf(`{"version":%d,`, cur.Version), 1)
		body = strings.Replace(body, `"salesStage":"L1_REQUIREMENT"`, `"salesStage":"L2_TECHNICAL"`, 1)
		s, b = f.sales.callRaw("PATCH", "/api/v1/projects/"+p.ID, body)
		if s != 400 || !strings.Contains(string(b), errAmountPrecision) {
			t.Fatal("historical re-save", s, string(b))
		}
		// … and the user corrects it (or marks it unknown): accepted, now KNOWN.
		body = strings.Replace(projectBody("合成历史异常金额", "12345679.00", "KNOWN"), "{", fmt.Sprintf(`{"version":%d,`, cur.Version), 1)
		body = strings.Replace(body, `"salesStage":"L1_REQUIREMENT"`, `"salesStage":"L2_TECHNICAL"`, 1)
		s, b = f.sales.callRaw("PATCH", "/api/v1/projects/"+p.ID, body)
		p = projectFrom(t, s, b)
		_ = f.db.QueryRow(context.Background(), `SELECT document::text, estimated_amount::text FROM intake_projects WHERE id=$1`, p.ID).Scan(&docRaw, &col)
		if amountClass(docRaw, col) != amountKnown {
			t.Fatal("corrected record should be KNOWN")
		}
	})
	if path := os.Getenv("BAOBEI_R1_INPUT_REPORT"); path != "" {
		_ = os.WriteFile(path, []byte(report.String()), 0o600)
	}
}
