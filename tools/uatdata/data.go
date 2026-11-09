package main

import (
	"fmt"
	"time"
)

// The H3 synthetic acceptance dataset. Every row lives in a dedicated namespace so seed and
// cleanup can never touch anything else:
//
//	projects / assets / runtimes / locations : id prefix "UAT-"
//	users                                    : id prefix "uat-"
//
// No production personal data: names are generic, emails are never stored, the only identity
// input is the Keycloak `sub` of a user that already exists in the Preview realm.
const (
	projectPrefix = "UAT-"
	userPrefix    = "uat-"
	seedMarker    = "uat-seeded-at="
)

type project struct {
	ID, Name  string
	AdminCode *int
	Label     string
}

type user struct {
	ID, Display string
	Grants      map[string]string // project -> role
}

// runtime: one runtime identity bound to an asset (Asset == "" → orphan identity).
type runtime struct {
	ID, Project, Asset, Source string
	Conn                       string // MQTT_CONNECTED | OFFLINE
	LastSeen                   string // "fresh" | "offline" | "stale" | "future" | "invalid" | "sim-fresh"
	Alarm, Op                  string
}

type assetRow struct {
	ID, Project, Name, Status string
}

type alarmRow struct {
	ID, Device string
	At         time.Time
}

func code(c int) *int { return &c }

var projects = []project{
	{"UAT-P001", "UAT 浙江海宁项目", code(330481), "区县级（海宁市）"},
	{"UAT-P002", "UAT 广东深圳项目", code(440300), "市级（深圳市）"},
	{"UAT-P003", "UAT 浙江省级项目", code(330000), "省级（浙江省本级）"},
	{"UAT-P004", "UAT 无空间位置项目", nil, "无行政区编码"},
	{"UAT-P005", "UAT 浙江杭州项目", code(330100), "市级（杭州市）"},
	{"UAT-P006", "UAT 待复核批量项目", code(330481), "区县级（海宁市），130 条待复核"},
}

// Identities. uat-admin is platform_admin but only on UAT-P001: it must NOT see anything else
// (platform_admin is not a global bypass). uat-revoke is the subject of the live revocation test.
var users = []user{
	{"uat-admin", "UAT 管理员（仅 P001）", map[string]string{"UAT-P001": "platform_admin"}},
	{"uat-biz", "UAT 业务用户（仅 P002）", map[string]string{"UAT-P002": "operator"}},
	{"uat-zero", "UAT 零授权用户", map[string]string{}},
	{"uat-geo", "UAT 多层级查看者", map[string]string{"UAT-P001": "viewer", "UAT-P003": "viewer", "UAT-P004": "viewer", "UAT-P005": "viewer"}},
	{"uat-bulk", "UAT 待复核查看者", map[string]string{"UAT-P006": "viewer"}},
	{"uat-revoke", "UAT 撤销测试用户", map[string]string{"UAT-P001": "viewer"}},
}

const bulkReview = 130

func assets() []assetRow {
	a := []assetRow{
		{"UAT-A01", "UAT-P001", "UAT-1号行车 REAL在线", "ACTIVE"},
		{"UAT-A02", "UAT-P001", "UAT-2号行车 REAL告警+历史告警", "ACTIVE"},
		{"UAT-A03", "UAT-P001", "UAT-3号行车 REAL离线+SIM在线", "ACTIVE"},
		{"UAT-A04", "UAT-P001", "UAT-4号行车 仅SIM在线", "ACTIVE"},
		{"UAT-A05", "UAT-P001", "UAT-5号行车 无运行身份", "ACTIVE"},
		{"UAT-A06", "UAT-P001", "UAT-6号行车 草稿但REAL在线", "DRAFT"},
		{"UAT-A07", "UAT-P001", "UAT-7号行车 停用但REAL告警", "OUT_OF_SERVICE"},
		{"UAT-A08", "UAT-P001", "UAT-8号行车 已退役", "RETIRED"},
		{"UAT-A09", "UAT-P001", "UAT-9号行车 无效观测时间", "ACTIVE"},
		{"UAT-A10", "UAT-P001", "UAT-10号行车 过期数据", "ACTIVE"},
		{"UAT-A11", "UAT-P001", "UAT-11号行车 未来观测时间", "ACTIVE"},
		{"UAT-A12", "UAT-P001", "UAT-12号行车 TEST在线告警", "ACTIVE"},
		// P002 deliberately reuses "1号行车"-style names: a leak would be visible, not silently merged.
		{"UAT-B01", "UAT-P002", "UAT-1号行车 REAL告警", "ACTIVE"},
		{"UAT-B02", "UAT-P002", "UAT-2号行车 REAL在线", "ACTIVE"},
		{"UAT-B03", "UAT-P002", "UAT-3号行车 草稿无运行身份", "DRAFT"},
		{"UAT-G01", "UAT-P003", "UAT-省本级行车", "ACTIVE"},
		{"UAT-G02", "UAT-P004", "UAT-无位置行车", "ACTIVE"},
		{"UAT-G03", "UAT-P005", "UAT-杭州行车", "ACTIVE"},
	}
	for i := 1; i <= bulkReview; i++ {
		a = append(a, assetRow{fmt.Sprintf("UAT-R%03d", i), "UAT-P006", fmt.Sprintf("UAT-待复核%03d", i), "DRAFT"})
	}
	return a
}

func runtimes() []runtime {
	r := []runtime{
		{"UAT-EDGE-A01", "UAT-P001", "UAT-A01", "REAL", "MQTT_CONNECTED", "fresh", "NONE", "LIVE"},
		{"UAT-EDGE-A02", "UAT-P001", "UAT-A02", "REAL", "MQTT_CONNECTED", "fresh", "ACTIVE", "SILENT"},
		{"UAT-EDGE-A03", "UAT-P001", "UAT-A03", "REAL", "OFFLINE", "offline", "NONE", "DISCONNECTED"},
		{"UAT-SIM-A03", "UAT-P001", "UAT-A03", "SIMULATION", "MQTT_CONNECTED", "sim-fresh", "ACTIVE", "LIVE"},
		{"UAT-SIM-A04", "UAT-P001", "UAT-A04", "SIMULATION", "MQTT_CONNECTED", "sim-fresh", "NONE", "SILENT"},
		{"UAT-EDGE-A06", "UAT-P001", "UAT-A06", "REAL", "MQTT_CONNECTED", "fresh", "NONE", "SILENT"},
		{"UAT-EDGE-A07", "UAT-P001", "UAT-A07", "REAL", "MQTT_CONNECTED", "fresh", "ACTIVE", "SILENT"},
		{"UAT-EDGE-A08", "UAT-P001", "UAT-A08", "REAL", "MQTT_CONNECTED", "fresh", "ACTIVE", "SILENT"},
		{"UAT-EDGE-A09", "UAT-P001", "UAT-A09", "REAL", "MQTT_CONNECTED", "invalid", "ACTIVE", "SILENT"},
		{"UAT-EDGE-A10", "UAT-P001", "UAT-A10", "REAL", "MQTT_CONNECTED", "stale", "NONE", "SILENT"},
		{"UAT-EDGE-A11", "UAT-P001", "UAT-A11", "REAL", "MQTT_CONNECTED", "future", "ACTIVE", "SILENT"},
		{"UAT-TEST-A12", "UAT-P001", "UAT-A12", "TEST", "MQTT_CONNECTED", "fresh", "ACTIVE", "SILENT"},
		{"UAT-ORPH-P001", "UAT-P001", "", "REAL", "MQTT_CONNECTED", "fresh", "NONE", "SILENT"},
		{"UAT-EDGE-B01", "UAT-P002", "UAT-B01", "REAL", "MQTT_CONNECTED", "fresh", "ACTIVE", "SILENT"},
		{"UAT-EDGE-B02", "UAT-P002", "UAT-B02", "REAL", "MQTT_CONNECTED", "fresh", "NONE", "SILENT"},
		{"UAT-EDGE-G01", "UAT-P003", "UAT-G01", "REAL", "MQTT_CONNECTED", "fresh", "NONE", "SILENT"},
		{"UAT-EDGE-G03", "UAT-P005", "UAT-G03", "REAL", "MQTT_CONNECTED", "fresh", "NONE", "SILENT"},
	}
	for i := 1; i <= bulkReview; i++ {
		id := fmt.Sprintf("UAT-R%03d", i)
		r = append(r, runtime{"UAT-EDGE-" + id[4:], "UAT-P006", id, "REAL", "MQTT_CONNECTED", "fresh", "NONE", "SILENT"})
	}
	return r
}

// lastSeen renders presence.lastSeenAt relative to the seed time. The offsets are chosen so the
// classification holds for EVERY valid operations config (stale 300..7200 s, skew 0..600 s):
// fresh = 60 s old, stale = 3 h old, future = +1 h.
func lastSeen(kind string, now time.Time) (string, bool) {
	switch kind {
	case "fresh":
		return now.Add(-60 * time.Second).Format(time.RFC3339), true
	case "sim-fresh":
		return now.Add(-30 * time.Second).Format(time.RFC3339), true
	case "offline":
		return now.Add(-10 * time.Minute).Format(time.RFC3339), true
	case "stale":
		return now.Add(-3 * time.Hour).Format(time.RFC3339), true
	case "future":
		return now.Add(time.Hour).Format(time.RFC3339), true
	case "invalid":
		return "not-a-timestamp", true
	}
	return "", false
}

// alarms: >100 historical REAL records on UAT-A02 spread so every window differs
// (1h:10, 6h:30, 24h:70, 7d:130, older:20 → 150 total), plus SIM, orphan, retired-asset,
// P002 and future-skew records.
func alarms(now time.Time) []alarmRow {
	out := []alarmRow{}
	add := func(prefix, device string, n int, at func(k int) time.Time) {
		for k := 1; k <= n; k++ {
			out = append(out, alarmRow{fmt.Sprintf("UAT-%s-%03d", prefix, k), device, at(k)})
		}
	}
	add("H1", "UAT-EDGE-A02", 10, func(k int) time.Time { return now.Add(-time.Duration(k) * 5 * time.Minute) })
	add("H6", "UAT-EDGE-A02", 20, func(k int) time.Time { return now.Add(-time.Hour - time.Duration(k)*10*time.Minute) })
	add("H24", "UAT-EDGE-A02", 40, func(k int) time.Time { return now.Add(-6*time.Hour - time.Duration(k)*25*time.Minute) })
	add("D7", "UAT-EDGE-A02", 60, func(k int) time.Time { return now.Add(-24*time.Hour - time.Duration(k)*2*time.Hour) })
	add("OLD", "UAT-EDGE-A02", 20, func(k int) time.Time { return now.Add(-8*24*time.Hour - time.Duration(k)*time.Hour) })
	add("FUT", "UAT-EDGE-A02", 2, func(k int) time.Time { return now.Add(20*time.Minute + time.Duration(k)*time.Minute) })
	add("SIM", "UAT-SIM-A03", 5, func(k int) time.Time { return now.Add(-time.Duration(k) * 7 * time.Minute) })
	add("ORPH", "UAT-ORPH-P001", 2, func(k int) time.Time { return now.Add(-time.Duration(k) * 11 * time.Minute) })
	add("RET", "UAT-EDGE-A08", 3, func(k int) time.Time { return now.Add(-time.Duration(k) * 13 * time.Minute) })
	add("B01", "UAT-EDGE-B01", 4, func(k int) time.Time { return now.Add(-time.Duration(k) * 9 * time.Minute) })
	return out
}
