package sim

import (
	"encoding/json"
	"testing"

	"github.com/alexcsalinas/cto/internal/config"
	"github.com/alexcsalinas/cto/internal/controller"
)

// shortScenario is the default scenario shrunk to a few simulated hours so
// the whole-run tests stay fast.
func shortScenario() config.Scenario {
	cfg := config.Default()
	cfg.DurationSec = 4 * 3600
	cfg.BoxArrival.StopAfterSec = 2 * 3600
	return cfg
}

func TestRunIsDeterministic(t *testing.T) {
	cfg := shortScenario()
	run := func() []byte {
		r, err := Run(cfg, controller.Naive{}, 42)
		if err != nil {
			t.Fatal(err)
		}
		out, _ := json.Marshal(r)
		return out
	}
	a, b := run(), run()
	if string(a) != string(b) {
		t.Fatalf("two runs with the same seed differ:\n%s\n%s", a, b)
	}
	if r, _ := Run(cfg, controller.Naive{}, 43); r.Summary.WorkDoneSec == 0 {
		t.Fatal("run produced no work")
	}
}

func TestRunAccountsWorkAndCost(t *testing.T) {
	r, err := Run(shortScenario(), controller.Naive{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	s := r.Summary
	if s.BoxesArrived == 0 || s.TotalCostUSD <= 0 || s.WorkPerDollar <= 0 {
		t.Fatalf("implausible summary: %+v", s)
	}
	if s.MeanMemUtilization <= 0 || s.MeanMemUtilization > 1.5 {
		t.Fatalf("mean mem util %.3f out of range", s.MeanMemUtilization)
	}
	if len(r.Series) == 0 || r.Series[len(r.Series)-1].CostSoFar > s.TotalCostUSD+1e-9 {
		t.Fatalf("series cost must not exceed final cost")
	}
}
