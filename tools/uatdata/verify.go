package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/guolichen007/crane-operations-platform/internal/geo"
	"github.com/guolichen007/crane-operations-platform/internal/operations"
)

// Expected server-side numbers for the dataset, derived by hand from data.go (see the comments)
// and checked against the real operations.Summary at the recorded seed time with the default
// operations config (stale 2100 s, skew 120 s). The same table is the "KPI ↔ API" reference for
// UAT-B: the browser must show exactly these numbers for the same user/region/window.
type expectation struct {
	User   string
	Region *int
	Window string
	Want   map[string]any // dotted JSON path → value
}

func region(c int) *int { return &c }

// reg is one OperationsRegion row: direct child of the scope with its counts.
func reg(code int, name string, registered, connected, alarm int) map[string]any {
	return map[string]any{"adcode": code, "name": name, "registered": registered, "realConnected": connected, "realAlarmDevices": alarm}
}

// P001 as seen by uat-admin / uat-revoke (11 non-retired assets; A08 RETIRED is excluded):
//
//	registered 11 · draft 1 (A06) · outOfService 1 (A07) · noRuntime 1 (A05)
//	bound REAL 8 (A01 A02 A03 A06 A07 A09 A10 A11) · SIM 2 (A03 A04) · TEST 1 (A12)
//	real.connected 2 (A01 A02) — A03 is REAL offline + SIM online and must NOT count
//	pendingReview 2 (A06 DRAFT, A07 OUT_OF_SERVICE+alarm) · stale 2 (A10 3h old, A11 +1h future)
//	unknownTime 1 (A09 invalid lastSeenAt) · offline 1 (A03) · live 1 (A01)
//	alarm devices REAL 1 (A02) · SIM 1 (A03) · TEST 1 (A12) · all 3
//	records 24h: REAL attributed 70 (A02: 10+20+40) · REAL unattributed 2 (orphan) · SIM 5 · total 77
//	             (retired A08's 3 records are excluded; 2 future records are counted only in futureSkew)
var p001 = map[string]any{
	"scope.authorizedProjects": 1, "metrics.projects": 1,
	"metrics.assets.registered": 11, "metrics.assets.draft": 1, "metrics.assets.outOfService": 1, "metrics.assets.noRuntime": 1,
	"metrics.assets.unlocated": 0, "metrics.assets.atScopeLevel": 0,
	"metrics.bound.REAL": 8, "metrics.bound.SIMULATION": 2, "metrics.bound.TEST": 1,
	"metrics.real.connected": 2, "metrics.real.pendingReview": 2, "metrics.real.pendingReviewAlarm": 1,
	"metrics.real.stale": 2, "metrics.real.unknownTime": 1, "metrics.real.offline": 1, "metrics.real.live": 1,
	"metrics.alarm.devices.REAL": 1, "metrics.alarm.devices.SIMULATION": 1, "metrics.alarm.devices.TEST": 1, "metrics.alarm.devicesAll": 3,
	"metrics.alarm.records.cells.REAL.attributed": 70, "metrics.alarm.records.cells.REAL.unattributed": 2,
	"metrics.alarm.records.cells.SIMULATION.attributed": 5, "metrics.alarm.records.cells.TEST.attributed": 0,
	"metrics.alarm.records.total": 77, "metrics.alarm.records.assets": 2, "metrics.alarm.records.futureSkew": 2,
	"metrics.real.pendingReviewAssets": []any{
		map[string]any{"id": "UAT-A06", "assetStatus": "DRAFT", "alarm": false},
		map[string]any{"id": "UAT-A07", "assetStatus": "OUT_OF_SERVICE", "alarm": true}},
	"regions": []any{reg(330000, "浙江省", 11, 2, 1)},
}

func with(base map[string]any, over map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

var zero = map[string]any{
	"scope.authorizedProjects": 0, "metrics.projects": 0, "metrics.assets.registered": 0, "metrics.real.connected": 0,
	"metrics.alarm.devicesAll": 0, "metrics.alarm.records.total": 0, "metrics.real.pendingReviewAssets": []any{}, "regions": []any{},
}

func expectations() []expectation {
	return []expectation{
		// UAT-A / UAT-B: isolation between P001 and P002.
		{"uat-admin", nil, "PT24H", p001},
		{"uat-revoke", nil, "PT24H", p001},
		{"uat-admin", region(440000), "PT24H", with(zero, map[string]any{"scope.authorizedProjects": 1})},
		// Alarm windows: 1h = 10+5+2, 6h = 30+5+2, 7d = 130+5+2 (records older than 7 days excluded).
		{"uat-admin", nil, "PT1H", map[string]any{"metrics.alarm.records.total": 17, "metrics.alarm.records.cells.REAL.attributed": 10, "metrics.alarm.devicesAll": 3}},
		{"uat-admin", nil, "PT6H", map[string]any{"metrics.alarm.records.total": 37, "metrics.alarm.records.cells.REAL.attributed": 30}},
		{"uat-admin", nil, "P7D", map[string]any{"metrics.alarm.records.total": 137, "metrics.alarm.records.cells.REAL.attributed": 130}},
		// uat-biz: P002 only (B01 alarm, B02 online, B03 draft without runtime).
		{"uat-biz", nil, "PT24H", map[string]any{
			"scope.authorizedProjects": 1, "metrics.projects": 1, "metrics.assets.registered": 3, "metrics.assets.draft": 1,
			"metrics.assets.noRuntime": 1, "metrics.bound.REAL": 2, "metrics.real.connected": 2, "metrics.real.pendingReview": 0,
			"metrics.alarm.devices.REAL": 1, "metrics.alarm.devicesAll": 1, "metrics.alarm.records.total": 4,
			"metrics.alarm.records.cells.REAL.attributed": 4, "regions": []any{reg(440000, "广东省", 3, 2, 1)}}},
		{"uat-biz", region(330000), "PT24H", with(zero, map[string]any{"scope.authorizedProjects": 1})},
		// Zero grants: a legal 0, never an error.
		{"uat-zero", nil, "PT24H", zero},
		// Geography: county (P001 海宁), province-level code (P003), unlocated (P004), prefecture (P005 杭州).
		{"uat-geo", nil, "PT24H", map[string]any{
			"scope.authorizedProjects": 4, "metrics.projects": 4, "metrics.assets.registered": 14, "metrics.assets.noRuntime": 2,
			"metrics.assets.unlocated": 1, "metrics.assets.atScopeLevel": 0, "metrics.real.connected": 4,
			"regions": []any{reg(330000, "浙江省", 13, 4, 1)}}},
		{"uat-geo", region(330000), "PT24H", map[string]any{
			"scope.authorizedProjects": 4, "metrics.projects": 3, "metrics.assets.registered": 13, "metrics.assets.unlocated": 0,
			"metrics.assets.atScopeLevel": 1, "metrics.real.connected": 4,
			"regions": []any{reg(330100, "杭州市", 1, 1, 0), reg(330400, "嘉兴市", 11, 2, 1)}}},
		{"uat-geo", region(330400), "PT24H", map[string]any{
			"metrics.projects": 1, "metrics.assets.registered": 11, "metrics.assets.atScopeLevel": 0, "regions": []any{reg(330481, "海宁市", 11, 2, 1)}}},
		{"uat-geo", region(440000), "PT24H", with(zero, map[string]any{"scope.authorizedProjects": 4})},
		// Pending review: 130 assets, the detail list is capped at 100.
		{"uat-bulk", nil, "PT24H", map[string]any{
			"metrics.assets.registered": 130, "metrics.assets.draft": 130, "metrics.real.pendingReview": 130,
			"metrics.real.connected": 0, "regions": []any{reg(330000, "浙江省", 130, 0, 0)}}},
	}
}

func lookup(doc map[string]any, path string) (any, bool) {
	var cur any = doc
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// normalize round-trips through JSON so expected Go literals compare with decoded documents.
func normalize(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

type verifyResult struct {
	Checks   int      `json:"checks"`
	Failures []string `json:"failures"`
	Anchor   string   `json:"seedAnchor"`
}

func verify(ctx context.Context, db *sql.DB) (verifyResult, error) {
	res := verifyResult{Failures: []string{}}
	anchor, err := seedAnchor(ctx, db)
	if err != nil {
		return res, err
	}
	res.Anchor = anchor.Format(time.RFC3339Nano)
	ix, err := geo.Default()
	if err != nil {
		return res, err
	}
	cfg := operations.Config{StaleAfter: operations.DefaultStaleAfter, MaxFutureSkew: operations.DefaultMaxFutureSkew}
	now := func() time.Time { return anchor }
	for _, e := range expectations() {
		s, err := operations.Summary(ctx, db, ix, cfg, operations.Request{User: e.User, Region: e.Region, Window: e.Window}, now)
		label := fmt.Sprintf("%s region=%v %s", e.User, deref(e.Region), e.Window)
		if err != nil {
			res.Failures = append(res.Failures, label+": "+err.Error())
			continue
		}
		var doc map[string]any
		b, _ := json.Marshal(s)
		_ = json.Unmarshal(b, &doc)
		keys := make([]string, 0, len(e.Want))
		for k := range e.Want {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			res.Checks++
			got, ok := lookup(doc, k)
			want := normalize(e.Want[k])
			if !ok || !reflect.DeepEqual(normalize(got), want) {
				gb, _ := json.Marshal(got)
				wb, _ := json.Marshal(want)
				res.Failures = append(res.Failures, fmt.Sprintf("%s %s: got %s want %s", label, k, gb, wb))
			}
		}
		if e.User == "uat-bulk" {
			res.Checks++
			list := s.Metrics.Real.PendingReviewAssets
			if len(list) != operations.MaxReviewAssets || list[0].Id != "UAT-R001" || list[len(list)-1].Id != "UAT-R100" {
				res.Failures = append(res.Failures, fmt.Sprintf("%s pendingReviewAssets cap: len=%d", label, len(list)))
			}
		}
	}
	return res, nil
}

func deref(p *int) any {
	if p == nil {
		return "all"
	}
	return *p
}
