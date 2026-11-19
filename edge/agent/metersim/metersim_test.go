package metersim

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFixtureEqualsTheOnboardingCopy(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "internal", "onboarding", "lhk_simulation_fixture.jsonl"))
	if err != nil || !bytes.Equal(b, embeddedFixture) {
		t.Fatal("edge fixture differs from internal/onboarding/lhk_simulation_fixture.jsonl")
	}
	f, err := LoadFixture()
	if err != nil || len(f.Lines) != 4 {
		t.Fatalf("fixture %d %v", len(f.Lines), err)
	}
	if n := len(f.Points()); n != 57 {
		t.Fatalf("points %d want 57 (51 equipment + 3 mechanism hours + 3 daily)", n)
	}
}

// Edge-side counting: values increase, survive a restart (persisted), never restart at zero;
// daily values restart at the local day boundary only.
func TestCountersPersistAcrossRestartAndDailyRollsOver(t *testing.T) {
	f, _ := LoadFixture()
	path := filepath.Join(t.TempDir(), "meters.json")
	s, err := Open(path, f)
	if err != nil {
		t.Fatal(err)
	}
	day1 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	first, _ := s.Step(day1, 1)
	find := func(code, metric string) float64 {
		return s.state.Values[code+"|"+metric]
	}
	c1 := find("LHK-P001", "cycles")
	if c1 != 800000 || len(first) != 57 {
		t.Fatalf("first step %v (%d samples)", c1, len(first))
	}
	s.Step(day1.Add(time.Minute), 1)
	// Restart: a new Source on the same file continues from the persisted values.
	s, _ = Open(path, f)
	s.Step(day1.Add(2*time.Minute), 1)
	if v := find("LHK-P001", "cycles"); v != 800002 {
		t.Fatalf("after restart %v want 800002", v)
	}
	tonnes := find("LHK-P053", "production_tonnes")
	s.Step(day1.Add(24*time.Hour), 1) // next local day: daily values restart, counters do not
	if v := find("LHK-P053", "production_tonnes"); v != 2 || tonnes <= 2 {
		t.Fatalf("daily rollover %v (was %v)", v, tonnes)
	}
	if v := find("LHK-P001", "cycles"); v != 800003 {
		t.Fatalf("counter after day change %v", v)
	}
}
