// Package operations implements the read-only operations summary (Homepage H2-1C):
// statistics over the caller's authorized Inventory projects, narrowed by an optional
// administrative region, computed by ONE PostgreSQL statement (summary.sql) so every
// number comes from the same snapshot.
//
// Contract: contracts/model.json OperationsSummary (generated Go/TS/JSON Schema/OpenAPI);
// design and evidence: docs/04-前端与可视化/Homepage-H2-0-运营数据契约.md, Homepage-H2-1B-*.md.
package operations

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/geo"
	"github.com/guolichen007/crane-operations-platform/internal/identity"
)

//go:embed summary.sql
var summarySQL string

// Windows accepted by the API (alarmWindow query parameter).
var Windows = map[string]time.Duration{"PT1H": time.Hour, "PT6H": 6 * time.Hour, "PT24H": 24 * time.Hour, "P7D": 7 * 24 * time.Hour}

const (
	DefaultStaleAfter    = 2100 * time.Second
	DefaultMaxFutureSkew = 120 * time.Second
	MaxReviewAssets      = 100
)

// Config is validated once at startup (LoadConfig); out-of-range values refuse to start.
type Config struct {
	StaleAfter    time.Duration
	MaxFutureSkew time.Duration
}

func (c Config) Validate() error {
	if c.StaleAfter < 300*time.Second || c.StaleAfter > 7200*time.Second || c.StaleAfter%time.Second != 0 {
		return fmt.Errorf("OPS_STALE_AFTER_SECONDS must be an integer in 300..7200, got %v", c.StaleAfter)
	}
	if c.MaxFutureSkew < 0 || c.MaxFutureSkew > 600*time.Second || c.MaxFutureSkew%time.Second != 0 {
		return fmt.Errorf("OPS_MAX_FUTURE_SKEW_SECONDS must be an integer in 0..600, got %v", c.MaxFutureSkew)
	}
	return nil
}

// Request is one summary query. User is the authenticated user id; AllProjects is only
// for the development token mode (no web session, no project memberships).
type Request struct {
	User        string
	AllProjects bool
	Region      *int   // nil = whole authorized scope
	Window      string // key of Windows
}

var (
	ErrInvalidWindow = errors.New("invalid alarm window")
	// ErrRegionNotFound: the code is well-formed but not in the administrative index (HTTP 404).
	ErrRegionNotFound = geo.ErrNotFound
	// ErrUnavailable: a required step failed (index, authorization or statistics query) — HTTP 503, never partial data.
	ErrUnavailable = errors.New("operations summary unavailable")
)

// readRoles: project roles that grant read, derived from the capability model (no role ordering).
func readRoles() []string {
	out := []string{}
	for _, role := range []string{"viewer", "operator", "maintainer", "platform_admin"} {
		if identity.HasCapability(role, identity.CapRead) {
			out = append(out, role)
		}
	}
	return out
}

// regionMap returns (code[], child[]) for the requested scope: every indexed code at the
// country scope, or the region's descendants otherwise; child = the direct child of the
// scope that contains the code (0 for the scope region itself).
func regionMap(ix *geo.Index, scope int) ([]int64, []int64, error) {
	codes, err := ix.Descendants(scope)
	if err != nil {
		return nil, nil, err
	}
	cs := make([]int64, 0, len(codes))
	ch := make([]int64, 0, len(codes))
	for _, c := range codes {
		child := 0
		if c != scope {
			child, _ = ix.ChildOf(scope, c)
		}
		cs = append(cs, int64(c))
		ch = append(ch, int64(child))
	}
	return cs, ch, nil
}

type row struct {
	StaleAfterSeconds   int  `json:"staleAfterSeconds"`
	MaxFutureSkew       int  `json:"maxFutureSkewSeconds"`
	AuthorizedProjects  *int `json:"authorizedProjects"`
	Projects            *int `json:"projects"`
	Registered          *int `json:"registered"`
	Draft               *int `json:"draft"`
	OutOfService        *int `json:"outOfService"`
	NoRuntime           *int `json:"noRuntime"`
	Unlocated           *int `json:"unlocated"`
	AtScopeLevel        *int `json:"atScopeLevel"`
	BoundREAL           *int `json:"boundREAL"`
	BoundSIMULATION     *int `json:"boundSIMULATION"`
	BoundTEST           *int `json:"boundTEST"`
	RealConnected       *int `json:"realConnected"`
	RealPendingReview   *int `json:"realPendingReview"`
	PendingReviewAlarm  *int `json:"pendingReviewAlarm"`
	PendingReviewAssets []struct {
		ID          string `json:"id"`
		AssetStatus string `json:"assetStatus"`
		Alarm       bool   `json:"alarm"`
	} `json:"pendingReviewAssets"`
	RealStale        *int `json:"realStale"`
	RealUnknownTime  *int `json:"realUnknownTime"`
	RealOffline      *int `json:"realOffline"`
	RealLive         *int `json:"realLive"`
	AlarmDevicesReal *int `json:"alarmDevicesReal"`
	AlarmDevicesSim  *int `json:"alarmDevicesSim"`
	AlarmDevicesTest *int `json:"alarmDevicesTest"`
	AlarmDevicesAll  *int `json:"alarmDevicesAll"`
	Records          map[string]struct {
		Attributed   *int `json:"attributed"`
		Unattributed *int `json:"unattributed"`
	} `json:"records"`
	RecordsTotal      *int      `json:"recordsTotal"`
	RecordsAssets     *int      `json:"recordsAssets"`
	RecordsFutureSkew *int      `json:"recordsFutureSkew"`
	Regions           [][4]*int `json:"regions"`
}

// Summary runs the single statement and maps it onto the generated contract type. Any
// missing number in the statement's JSON is an error (ErrUnavailable), never a 0.
// now is injected so tests can pin sampledAt; production passes time.Now.
func Summary(ctx context.Context, db *sql.DB, ix *geo.Index, cfg Config, req Request, now func() time.Time) (contract.OperationsSummary, error) {
	var out contract.OperationsSummary
	window, ok := Windows[req.Window]
	if !ok {
		return out, ErrInvalidWindow
	}
	if ix == nil {
		return out, fmt.Errorf("%w: administrative index", ErrUnavailable)
	}
	scope, regionName := geo.Country, "全部授权范围"
	if req.Region != nil {
		r, ok := ix.Region(*req.Region)
		if !ok {
			return out, ErrRegionNotFound
		}
		scope, regionName = r.Code, r.Name
	}
	codes, children, err := regionMap(ix, scope)
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	sampledAt := now().UTC().Truncate(time.Microsecond) // PostgreSQL timestamptz precision
	windowStart := sampledAt.Add(-window)
	raw, err := query(ctx, db, req, windowStart, sampledAt, cfg, scope == geo.Country, codes, children)
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	generatedAt := now().UTC()
	var r row
	if err = json.Unmarshal(raw, &r); err != nil {
		return out, fmt.Errorf("%w: decode: %v", ErrUnavailable, err)
	}
	if err = r.complete(); err != nil {
		return out, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	out = contract.OperationsSummary{
		SchemaVersion: 1,
		SampledAt:     sampledAt,
		GeneratedAt:   generatedAt,
		Scope:         contract.OperationsScope{Region: req.Region, RegionName: regionName, AuthorizedProjects: *r.AuthorizedProjects, GeoIndexVersion: ix.Version()},
		Freshness: contract.OperationsFreshness{StaleAfterSeconds: r.StaleAfterSeconds, MaxFutureSkewSeconds: r.MaxFutureSkew, AlarmWindow: req.Window,
			WindowStart: windowStart, WindowEnd: sampledAt},
		Metrics: contract.OperationsMetrics{
			Projects: *r.Projects,
			Assets: contract.OperationsAssetCounts{Registered: *r.Registered, Draft: *r.Draft, OutOfService: *r.OutOfService, NoRuntime: *r.NoRuntime,
				Unlocated: *r.Unlocated, AtScopeLevel: *r.AtScopeLevel},
			Bound: contract.RuntimeSourceCounts{REAL: *r.BoundREAL, SIMULATION: *r.BoundSIMULATION, TEST: *r.BoundTEST},
			Real: contract.OperationsRealCounts{Connected: *r.RealConnected, PendingReview: *r.RealPendingReview, PendingReviewAlarm: *r.PendingReviewAlarm,
				PendingReviewAssets: []contract.OperationsReviewAsset{}, Stale: *r.RealStale, UnknownTime: *r.RealUnknownTime, Offline: *r.RealOffline, Live: *r.RealLive},
			Operation: contract.NotAvailable{Status: "NOT_AVAILABLE"},
			Alarm: contract.OperationsAlarmCounts{
				Devices:    contract.RuntimeSourceCounts{REAL: *r.AlarmDevicesReal, SIMULATION: *r.AlarmDevicesSim, TEST: *r.AlarmDevicesTest},
				DevicesAll: *r.AlarmDevicesAll,
				Records: contract.OperationsAlarmRecords{
					Cells: contract.OperationsAlarmCells{
						REAL:       contract.AlarmAttributionCell{Attributed: *r.Records["REAL"].Attributed, Unattributed: *r.Records["REAL"].Unattributed},
						SIMULATION: contract.AlarmAttributionCell{Attributed: *r.Records["SIMULATION"].Attributed, Unattributed: *r.Records["SIMULATION"].Unattributed},
						TEST:       contract.AlarmAttributionCell{Attributed: *r.Records["TEST"].Attributed, Unattributed: *r.Records["TEST"].Unattributed},
					},
					Total: *r.RecordsTotal, Assets: *r.RecordsAssets, FutureSkew: *r.RecordsFutureSkew,
				},
			},
			PendingEvents: contract.NotAvailable{Status: "NOT_AVAILABLE"},
		},
		Regions: []contract.OperationsRegion{},
	}
	for _, a := range r.PendingReviewAssets {
		out.Metrics.Real.PendingReviewAssets = append(out.Metrics.Real.PendingReviewAssets, contract.OperationsReviewAsset{Id: a.ID, AssetStatus: a.AssetStatus, Alarm: a.Alarm})
	}
	for _, g := range r.Regions {
		reg, ok := ix.Region(*g[0])
		if !ok {
			return contract.OperationsSummary{}, fmt.Errorf("%w: region %d not in index", ErrUnavailable, *g[0])
		}
		out.Regions = append(out.Regions, contract.OperationsRegion{Adcode: reg.Code, Name: reg.Name, Registered: *g[1], RealConnected: *g[2], RealAlarmDevices: *g[3]})
	}
	// Internal invariants of one snapshot; a violation means a defective query, not a user state.
	cells := out.Metrics.Alarm.Records.Cells
	if cells.REAL.Attributed+cells.REAL.Unattributed+cells.SIMULATION.Attributed+cells.SIMULATION.Unattributed+cells.TEST.Attributed+cells.TEST.Unattributed != out.Metrics.Alarm.Records.Total ||
		out.Metrics.Alarm.DevicesAll > out.Metrics.Alarm.Devices.REAL+out.Metrics.Alarm.Devices.SIMULATION+out.Metrics.Alarm.Devices.TEST ||
		out.Metrics.Bound.REAL > out.Metrics.Assets.Registered || len(out.Metrics.Real.PendingReviewAssets) > MaxReviewAssets {
		return contract.OperationsSummary{}, fmt.Errorf("%w: summary invariant violated", ErrUnavailable)
	}
	if err = contract.ValidateDocument("OperationsSummary", out); err != nil {
		return contract.OperationsSummary{}, fmt.Errorf("%w: contract: %v", ErrUnavailable, err)
	}
	return out, nil
}

// query runs the single statement inside a READ ONLY transaction so that `SET LOCAL jit = off`
// is scoped to this transaction and can never survive on a pooled connection. JIT is disabled
// because the planner's cost for the region unnest join crosses jit_above_cost while the query
// itself runs in milliseconds; on PostgreSQL 17 JIT compilation alone took ~45 ms per request
// (H2-1C benchmark). The statement remains one snapshot; the transaction adds no isolation change.
func query(ctx context.Context, db *sql.DB, req Request, windowStart, sampledAt time.Time, cfg Config, country bool, codes, children []int64) ([]byte, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL jit = off`); err != nil {
		return nil, err
	}
	var raw []byte
	err = tx.QueryRowContext(ctx, summarySQL, req.User, readRoles(), req.AllProjects, windowStart, sampledAt,
		int(cfg.StaleAfter/time.Second), int(cfg.MaxFutureSkew/time.Second), country, codes, children).Scan(&raw)
	if err != nil {
		return nil, err
	}
	return raw, tx.Commit()
}

func (r row) complete() error {
	for name, v := range map[string]*int{"authorizedProjects": r.AuthorizedProjects, "projects": r.Projects, "registered": r.Registered, "draft": r.Draft,
		"outOfService": r.OutOfService, "noRuntime": r.NoRuntime, "unlocated": r.Unlocated, "atScopeLevel": r.AtScopeLevel, "boundREAL": r.BoundREAL,
		"boundSIMULATION": r.BoundSIMULATION, "boundTEST": r.BoundTEST, "realConnected": r.RealConnected, "realPendingReview": r.RealPendingReview,
		"pendingReviewAlarm": r.PendingReviewAlarm, "realStale": r.RealStale, "realUnknownTime": r.RealUnknownTime, "realOffline": r.RealOffline,
		"realLive": r.RealLive, "alarmDevicesReal": r.AlarmDevicesReal, "alarmDevicesSim": r.AlarmDevicesSim, "alarmDevicesTest": r.AlarmDevicesTest,
		"alarmDevicesAll": r.AlarmDevicesAll, "recordsTotal": r.RecordsTotal, "recordsAssets": r.RecordsAssets, "recordsFutureSkew": r.RecordsFutureSkew} {
		if v == nil {
			return fmt.Errorf("missing %s", name)
		}
	}
	for _, src := range []string{"REAL", "SIMULATION", "TEST"} {
		c, ok := r.Records[src]
		if !ok || c.Attributed == nil || c.Unattributed == nil {
			return fmt.Errorf("missing records.%s", src)
		}
	}
	if r.PendingReviewAssets == nil || r.Regions == nil {
		return errors.New("missing list")
	}
	for _, g := range r.Regions {
		for _, v := range g {
			if v == nil {
				return errors.New("missing region value")
			}
		}
	}
	return nil
}

// LoadConfig reads OPS_STALE_AFTER_SECONDS (default 2100, 300..7200) and
// OPS_MAX_FUTURE_SKEW_SECONDS (default 120, 0..600). A malformed or out-of-range value is a
// startup error — never silently replaced by the default.
func LoadConfig(lookup func(string) (string, bool)) (Config, error) {
	c := Config{StaleAfter: DefaultStaleAfter, MaxFutureSkew: DefaultMaxFutureSkew}
	for key, target := range map[string]*time.Duration{"OPS_STALE_AFTER_SECONDS": &c.StaleAfter, "OPS_MAX_FUTURE_SKEW_SECONDS": &c.MaxFutureSkew} {
		v, ok := lookup(key)
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return Config{}, fmt.Errorf("%s must be an integer number of seconds: %q", key, v)
		}
		*target = time.Duration(n) * time.Second
	}
	return c, c.Validate()
}
