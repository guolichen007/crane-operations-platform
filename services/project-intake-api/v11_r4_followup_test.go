package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// V1.1 R4 (AC-18): follow-up center windows in business time, disjoint,
// equal to the project list with the same parameters, no rows created by
// reading, ownership changes reflected at once.

func followupsOf(t *testing.T, c testClient, query string) FollowupCenter {
	t.Helper()
	s, b := c.call("GET", "/api/v1/followups?"+query, nil)
	expect(t, s, 200, b)
	var out FollowupCenter
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBusinessDayBoundaries(t *testing.T) {
	cst := time.FixedZone("CST", 8*3600)
	for _, tc := range []struct{ at, today string }{
		{"2026-11-17T00:00:00+08:00", "2026-11-17"},
		{"2026-11-17T23:59:59+08:00", "2026-11-17"},
		{"2026-11-16T16:00:00Z", "2026-11-17"}, // 00:00 CST
		{"2026-11-16T15:59:59Z", "2026-11-16"}, // 23:59:59 CST previous day
		{"2026-12-31T20:00:00Z", "2027-01-01"},
	} {
		at, _ := time.Parse(time.RFC3339, tc.at)
		d := businessDay(at)
		if got := d.Today.In(cst).Format("2006-01-02"); got != tc.today {
			t.Fatal(tc.at, got)
		}
		if d.Tomorrow.Sub(d.Today) != 24*time.Hour || d.WeekEnd.Sub(d.Tomorrow) != 7*24*time.Hour {
			t.Fatal("window lengths")
		}
	}
}

func TestDatabaseV11FollowupCenter(t *testing.T) {
	f := g1FixtureFor(t)
	ctx := context.Background()
	cst := time.FixedZone("CST", 8*3600)
	// Fixed clock: 2026-11-17 09:30 CST.
	now := time.Date(2026, 11, 17, 9, 30, 0, 0, cst)
	f.app.now = func() time.Time { return now }
	tag := "V11跟进" + randomToken()[:6]
	mk := func(c testClient, name string, at *time.Time, stage string) Project {
		in := sample(tag + name)
		in.Customer = tag + name + "客户"
		in.SalesStage = stage
		in.NextFollowupAt = at
		in.NextAction = "下一步" + name
		s, b := c.call("POST", "/api/v1/projects", in)
		return projectFrom(t, s, b)
	}
	at := func(s string) *time.Time { v, _ := time.ParseInLocation("2006-01-02 15:04:05", s, cst); return &v }
	ps := map[string]Project{
		"逾期昨天":   mk(f.sales, "逾期昨天", at("2026-11-16 23:59:59"), "L1_REQUIREMENT"),
		"逾期刚过":   mk(f.sales, "逾期刚过", at("2026-11-17 09:29:59"), "L1_REQUIREMENT"),
		"今天稍后":   mk(f.sales, "今天稍后", at("2026-11-17 09:30:00"), "L2_TECHNICAL"),
		"今天最后一秒": mk(f.sales, "今天最后一秒", at("2026-11-17 23:59:59"), "L2_TECHNICAL"),
		"明天零点":   mk(f.sales, "明天零点", at("2026-11-18 00:00:00"), "L3_SITE_SURVEY"),
		"第七天末":   mk(f.sales, "第七天末", at("2026-11-24 23:59:59"), "L3_SITE_SURVEY"),
		"第八天":    mk(f.sales, "第八天", at("2026-11-25 00:00:00"), "L3_SITE_SURVEY"),
		"未安排":    mk(f.sales, "未安排", nil, "L0_LEAD"),
		"已赢单":    mk(f.sales, "已赢单", at("2026-11-10 10:00:00"), "WON"),
		"同事B":    mk(f.other, "同事B", at("2026-11-17 15:00:00"), "L1_REQUIREMENT"),
		"主管本人":   mk(f.manager, "主管本人", at("2026-11-17 11:00:00"), "L1_REQUIREMENT"),
	}
	before := countRows(t, f.db, `SELECT count(*) FROM project_activities`) + countRows(t, f.db, `SELECT count(*) FROM audit_events`)
	want := map[string][]string{
		"overdue":     {"逾期昨天", "逾期刚过"},
		"today":       {"今天稍后", "今天最后一秒"},
		"week":        {"明天零点", "第七天末"},
		"unscheduled": {"未安排"},
	}
	names := func(items []Project) string {
		out := []string{}
		for _, p := range items {
			out = append(out, strings.TrimPrefix(p.ProjectName, tag))
		}
		return strings.Join(out, ",")
	}
	for w, exp := range want {
		got := followupsOf(t, f.sales, "window="+w+"&q="+tag)
		if names(got.Items) != strings.Join(exp, ",") || got.Total != len(exp) || got.Counts[w] != len(exp) {
			t.Fatalf("%s: got %s (%d) counts %v", w, names(got.Items), got.Total, got.Counts)
		}
		// Same membership as GET /projects with the same parameters.
		if l := listOf(t, f.sales, "followup="+w+"&lifecycle=open&q="+tag+"&pageSize=1"); l.Total != len(exp) {
			t.Fatal(w, "list", l.Total)
		}
		if got.Day.Today.In(cst).Format("2006-01-02 15:04") != "2026-11-17 00:00" || got.Day.Timezone != "Asia/Shanghai" {
			t.Fatal("business day", got.Day)
		}
	}
	// Reading created nothing.
	if after := countRows(t, f.db, `SELECT count(*) FROM project_activities`) + countRows(t, f.db, `SELECT count(*) FROM audit_events`); after != before {
		t.Fatal("reading the follow-up center created rows", before, after)
	}
	// Sales see only their own; the supervisor chooses own/team/member.
	if c := followupsOf(t, f.sales, "window=today&q="+tag); strings.Contains(names(c.Items), "同事B") {
		t.Fatal("other sales leaked")
	}
	if c := followupsOf(t, f.manager, "window=today&q="+tag); c.Counts["today"] != 4 {
		t.Fatal("team today", c.Counts)
	}
	if c := followupsOf(t, f.manager, "window=today&scope=mine&q="+tag); names(c.Items) != "主管本人" {
		t.Fatal("supervisor mine", names(c.Items))
	}
	if c := followupsOf(t, f.manager, "window=today&scope=member&memberId="+staffIDOf(t, f.db, "synthetic-b")+"&q="+tag); names(c.Items) != "同事B" {
		t.Fatal("supervisor member", names(c.Items))
	}
	mb := login(t, f.server, "synthetic-manager-other")
	s, b := mb.call("GET", "/api/v1/followups?window=today&scope=member&memberId="+staffIDOf(t, f.db, "synthetic-a"), nil)
	expect(t, s, 404, b)
	if c := followupsOf(t, mb, "window=today&q="+tag); c.Total != 0 {
		t.Fatal("other team sees follow-ups")
	}
	for _, bad := range []string{"window=soon", "window=today&stage=L0_LEAD", "window=today&pageSize=101"} {
		s, b = f.sales.call("GET", "/api/v1/followups?"+bad, nil)
		expect(t, s, 400, b)
	}
	// The supervisor reads but cannot record a follow-up on a member's project.
	sp := g1Submit(t, f.other, ps["同事B"])
	s, b = f.manager.call("POST", "/api/v1/projects/"+sp.ID+"/activities", map[string]any{"version": sp.Version, "type": "PHONE", "occurredAt": "2026-11-17T01:00:00Z", "summary": "代记", "result": "", "nextAction": ""})
	expect(t, s, 403, b)
	// Recording a follow-up moves the project between windows at once and
	// keeps every earlier activity.
	p := g1Submit(t, f.sales, ps["今天稍后"])
	s, b = f.sales.call("POST", "/api/v1/projects/"+p.ID+"/activities", map[string]any{"version": p.Version, "type": "VISIT", "occurredAt": "2026-11-17T01:30:00Z", "summary": "拜访", "result": "确认", "nextAction": "发方案", "nextFollowupAt": "2026-11-20T02:00:00Z"})
	p = projectFrom(t, s, b)
	if c := followupsOf(t, f.sales, "window=week&q="+tag); !strings.Contains(names(c.Items), "今天稍后") {
		t.Fatal("activity did not move the project to week")
	}
	s, b = f.sales.call("POST", "/api/v1/projects/"+p.ID+"/activities", map[string]any{"version": p.Version, "type": "PHONE", "occurredAt": "2026-11-17T02:00:00Z", "summary": "电话", "result": "", "nextAction": "", "nextFollowupAt": ""})
	p = projectFrom(t, s, b)
	if c := followupsOf(t, f.sales, "window=unscheduled&q="+tag); !strings.Contains(names(c.Items), "今天稍后") {
		t.Fatal("cleared plan not unscheduled")
	}
	if n := countRows(t, f.db, `SELECT count(*) FROM project_activities WHERE project_id='`+p.ID+`'`); n != 2 {
		t.Fatal("activity history", n)
	}
	// Idempotent against replay: an old version is a conflict, no second row.
	s, b = f.sales.call("POST", "/api/v1/projects/"+p.ID+"/activities", map[string]any{"version": p.Version - 1, "type": "PHONE", "occurredAt": "2026-11-17T02:00:00Z", "summary": "重放", "result": "", "nextAction": ""})
	expect(t, s, 409, b)
	// Transfer: the follow-up leaves A1's center and appears in A2's.
	tp := g1Submit(t, f.sales, ps["今天最后一秒"])
	s, b = g1Decision(f.manager, v11Get(t, f.manager, tp.ID), "ownership-transfer", map[string]any{"targetSubject": "synthetic-b"})
	expect(t, s, 200, b)
	if c := followupsOf(t, f.sales, "window=today&q="+tag); strings.Contains(names(c.Items), "今天最后一秒") {
		t.Fatal("transferred project still in old owner's center")
	}
	if c := followupsOf(t, f.other, "window=today&q="+tag); !strings.Contains(names(c.Items), "今天最后一秒") {
		t.Fatal("transferred project missing in new owner's center")
	}
	// Midnight: at 00:00 the next day, yesterday's "today" items are overdue.
	now = time.Date(2026, 11, 18, 0, 0, 0, 0, cst)
	c := followupsOf(t, f.other, "window=overdue&q="+tag)
	if !strings.Contains(names(c.Items), "今天最后一秒") {
		t.Fatal("midnight rollover", names(c.Items))
	}
	// Workspace summary uses the same clock.
	s, b = f.sales.call("GET", "/api/v1/workspace-summary", nil)
	expect(t, s, 200, b)
	_ = fmt.Sprint(ctx)
}
