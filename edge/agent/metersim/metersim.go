// Package metersim is the SIMULATION / TEST edge-side meter source for the first real asset
// (R9 B3/B7). It mirrors what a real edge does for the 老河口 point list: counting and duration
// accumulation happen HERE (edge side), the cumulative values are persisted locally (restart keeps
// them), and a batch is published as one METER message through the critical spool (back-filled in
// order after an outage, removed only after the cloud application ACK).
//
// It never runs for a REAL identity and never claims REAL: the batch carries the runtime's own
// sourceMode, which the cloud cross-checks against the registered runtime.
package metersim

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"math"
	"os"
	"sort"
	"time"

	contract "github.com/guolichen007/crane-operations-platform/contracts/go"
	"github.com/guolichen007/crane-operations-platform/internal/platform/atomicfile"
)

// Point is one simulated meter source (point code + metric).
type Point struct {
	Code   string  `json:"code"`
	Metric string  `json:"metric"`
	Rate   float64 `json:"rate"` // per simulated minute (cycles / hours / tonnes / jobs)
	Daily  bool    `json:"daily"`
}

// Fixture is the reviewed SIMULATION fixture (deploy copy of internal/onboarding/lhk_simulation_fixture.jsonl).
type Fixture struct {
	Lines []FixtureLine
}

type FixtureLine struct {
	FixtureFormat   string `json:"fixture_format"`
	RuntimeID       string `json:"runtime_id"`
	SourceMode      string `json:"source_mode"`
	TestRunID       string `json:"test_run_id"`
	RelativeSeconds int    `json:"relative_seconds"`
	Sequence        int    `json:"sequence"`
	Note            string `json:"note"`
	Points          []struct {
		PointCode string   `json:"point_code"`
		Metric    string   `json:"metric"`
		Unit      string   `json:"unit"`
		Value     *float64 `json:"value"`
		Quality   string   `json:"quality"`
		Source    string   `json:"source"`
		TestOnly  bool     `json:"test_only"`
	} `json:"points"`
	FaultEvents []struct {
		PointCode string `json:"point_code"`
		EventID   string `json:"event_id"`
		Code      string `json:"code"`
		Text      string `json:"text"`
		Source    string `json:"source"`
	} `json:"fault_events"`
}

//go:embed lhk_simulation_fixture.jsonl
var embeddedFixture []byte

// LoadFixture parses the embedded fixture (closed schema; SIMULATION only).
func LoadFixture() (Fixture, error) {
	var f Fixture
	sc := bufio.NewScanner(bytes.NewReader(embeddedFixture))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var l FixtureLine
		dec := json.NewDecoder(bytes.NewReader(sc.Bytes()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&l); err != nil {
			return f, err
		}
		if l.SourceMode != "SIMULATION" || l.FixtureFormat != "lhk-normalized-simulation-v1" {
			return f, errors.New("fixture must be lhk-normalized-simulation-v1 SIMULATION")
		}
		f.Lines = append(f.Lines, l)
	}
	return f, sc.Err()
}

// Points returns the meter sources of the fixture with a plausible simulated rate per metric
// (normal operation: counters advance slowly; values are synthetic, never real usage).
func (f Fixture) Points() []Point {
	seen := map[string]bool{}
	out := []Point{}
	if len(f.Lines) == 0 {
		return out
	}
	for _, p := range f.Lines[0].Points {
		key := p.PointCode + "|" + p.Metric
		if seen[key] {
			continue
		}
		seen[key] = true
		pt := Point{Code: p.PointCode, Metric: p.Metric}
		switch p.Metric {
		case "cycles":
			pt.Rate = 1
		case "hours", "mechanism_running_hours":
			pt.Rate = 1.0 / 60
		case "daily_auto_hours":
			pt.Rate, pt.Daily = 1.0/60, true
		case "production_tonnes":
			pt.Rate, pt.Daily = 2, true
		case "daily_job_count":
			pt.Rate, pt.Daily = 1, true
		}
		out = append(out, pt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code+out[i].Metric < out[j].Code+out[j].Metric })
	return out
}

// State is the edge-persisted counter state (survives restarts: values never restart at zero
// unless the operator resets the simulator explicitly).
type State struct {
	Values map[string]float64 `json:"values"`
	Day    string             `json:"day"`
	Faults int                `json:"faults"`
}

// Source accumulates simulated values at the edge.
type Source struct {
	path   string
	points []Point
	start  map[string]float64
	state  State
}

// Open loads (or initializes from the fixture's first line) the persisted counters.
func Open(path string, f Fixture) (*Source, error) {
	s := &Source{path: path, points: f.Points(), start: map[string]float64{}, state: State{Values: map[string]float64{}}}
	for _, p := range f.Lines[0].Points {
		if p.Value != nil {
			s.start[p.PointCode+"|"+p.Metric] = *p.Value
		}
	}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err = json.Unmarshal(b, &s.state); err != nil {
			return nil, err
		}
	case os.IsNotExist(err):
		for k, v := range s.start {
			s.state.Values[k] = v
		}
	default:
		return nil, err
	}
	return s, nil
}

// Step advances every counter by `minutes` of simulated normal operation and returns the batch
// samples; daily values restart at the day boundary (edge local time).
func (s *Source) Step(now time.Time, minutes float64) ([]contract.MeterSample, error) {
	day := now.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
	newDay := s.state.Day != "" && s.state.Day != day
	s.state.Day = day
	out := make([]contract.MeterSample, 0, len(s.points))
	for _, p := range s.points {
		key := p.Code + "|" + p.Metric
		v := s.state.Values[key]
		if p.Daily && newDay {
			v = 0
		}
		v += p.Rate * minutes
		v = math.Round(v*1e6) / 1e6
		s.state.Values[key] = v
		val := v
		if p.Metric == "cycles" || p.Metric == "daily_job_count" {
			val = math.Floor(v) // an edge counter publishes whole counts; the fraction stays local
		}
		out = append(out, contract.MeterSample{PointCode: p.Code, Metric: p.Metric, Value: &val, Quality: "GOOD"})
	}
	b, err := json.Marshal(s.state)
	if err != nil {
		return nil, err
	}
	return out, atomicfile.Write(s.path, b)
}
