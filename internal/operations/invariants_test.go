package operations

import (
	"errors"
	"testing"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
)

// Pure (no database) proof that every runtime invariant of checkInvariants rejects the defect it
// guards against. Each case starts from one consistent summary and breaks exactly one rule.
func consistentSummary() contract.OperationsSummary {
	var s contract.OperationsSummary
	m := &s.Metrics
	// 10 assets = 6 in Zhejiang + 2 in Beijing + 1 unlocated + 1 at scope level.
	m.Assets = contract.OperationsAssetCounts{Registered: 10, Unlocated: 1, AtScopeLevel: 1}
	s.Regions = []contract.OperationsRegion{
		{Adcode: 330000, Name: "浙江省", Registered: 6, RealConnected: 3, RealAlarmDevices: 1},
		{Adcode: 110000, Name: "北京市", Registered: 2, RealConnected: 1},
	}
	// 3 assets have both REAL and SIMULATION identities: each counted once per source, never above registered.
	m.Bound = contract.RuntimeSourceCounts{REAL: 7, SIMULATION: 5, TEST: 1}
	m.Real = contract.OperationsRealCounts{Connected: 4, PendingReview: 2, PendingReviewAssets: []contract.OperationsReviewAsset{{Id: "A1"}, {Id: "A2"}}}
	m.Alarm.Devices = contract.RuntimeSourceCounts{REAL: 1, SIMULATION: 2}
	m.Alarm.DevicesAll = 2 // one asset alarms on both REAL and SIMULATION
	c := &m.Alarm.Records.Cells
	c.REAL.Attributed, c.REAL.Unattributed, c.SIMULATION.Attributed, c.TEST.Unattributed = 3, 1, 5, 2
	m.Alarm.Records.Total = 11
	return s
}

func TestSummaryRuntimeInvariants(t *testing.T) {
	if err := checkInvariants(consistentSummary()); err != nil {
		t.Fatalf("consistent summary rejected: %v", err)
	}
	cases := map[string]func(*contract.OperationsSummary){
		"region partition short (asset lost)":    func(s *contract.OperationsSummary) { s.Regions[0].Registered-- },
		"region partition over (asset in two)":   func(s *contract.OperationsSummary) { s.Metrics.Assets.AtScopeLevel++ },
		"unlocated counted twice":                func(s *contract.OperationsSummary) { s.Metrics.Assets.Unlocated++ },
		"duplicate region code":                  func(s *contract.OperationsSummary) { s.Regions[1].Adcode = 330000 },
		"region connected above its assets":      func(s *contract.OperationsSummary) { s.Regions[1].RealConnected = 3 },
		"alarm cells sum != total":               func(s *contract.OperationsSummary) { s.Metrics.Alarm.Records.Cells.SIMULATION.Unattributed++ },
		"alarm total without cells":              func(s *contract.OperationsSummary) { s.Metrics.Alarm.Records.Total++ },
		"devicesAll above per-source sum":        func(s *contract.OperationsSummary) { s.Metrics.Alarm.DevicesAll = 4 },
		"REAL bound double-counts dual identity": func(s *contract.OperationsSummary) { s.Metrics.Bound.REAL = 11 },
		"SIMULATION bound double-counts":         func(s *contract.OperationsSummary) { s.Metrics.Bound.SIMULATION = 11 },
		"TEST bound above registered":            func(s *contract.OperationsSummary) { s.Metrics.Bound.TEST = 11 },
		"REAL alarm devices above registered":    func(s *contract.OperationsSummary) { s.Metrics.Alarm.Devices.REAL = 11 },
		"connected + review above bound REAL":    func(s *contract.OperationsSummary) { s.Metrics.Real.Connected = 6 },
		"review list longer than review total":   func(s *contract.OperationsSummary) { s.Metrics.Real.PendingReview = 1 },
		"review list longer than MaxReviewAssets": func(s *contract.OperationsSummary) {
			s.Metrics.Assets.Registered, s.Metrics.Assets.AtScopeLevel = 300, 291
			s.Metrics.Bound.REAL, s.Metrics.Real.PendingReview = 250, 200
			s.Metrics.Real.PendingReviewAssets = make([]contract.OperationsReviewAsset, MaxReviewAssets+1)
		},
	}
	for name, breakIt := range cases {
		s := consistentSummary()
		breakIt(&s)
		if err := checkInvariants(s); !errors.Is(err, ErrInvariant) {
			t.Errorf("%s: err = %v, want ErrInvariant", name, err)
		}
	}
}
