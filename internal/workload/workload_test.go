package workload

import (
	"math"
	"testing"

	"github.com/alexcsalinas/cto/internal/config"
	"github.com/alexcsalinas/cto/internal/fleet"
)

func TestGenerateIsDeterministic(t *testing.T) {
	cfg := config.Default()
	a := Generate(cfg, 7)
	b := Generate(cfg, 7)
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("len(a)=%d len(b)=%d", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].ArriveAt != b[i].ArriveAt || a[i].LifeSec != b[i].LifeSec || len(a[i].Phases) != len(b[i].Phases) {
			t.Fatalf("box %d differs between runs with the same seed", i)
		}
	}
	if c := Generate(cfg, 8); c[0].ArriveAt == a[0].ArriveAt && c[0].LifeSec == a[0].LifeSec {
		t.Fatal("different seeds should give different traces")
	}
}

func TestPhasesRespectLifeAndAlternate(t *testing.T) {
	cfg := config.Default()
	for _, b := range Generate(cfg, 3) {
		if b.ArriveAt < 0 || b.ArriveAt >= cfg.BoxArrival.StopAfterSec {
			t.Errorf("%s arrives at %d outside [0,%d)", b.ID, b.ArriveAt, cfg.BoxArrival.StopAfterSec)
		}
		total := int64(0)
		for i, p := range b.Phases {
			total += p.DurSec
			if p.DurSec < 1 {
				t.Errorf("%s phase %d has duration %d", b.ID, i, p.DurSec)
			}
			want := fleet.PhaseActive
			if i%2 == 1 {
				want = fleet.PhaseWaiting
			}
			if p.Kind != want {
				t.Errorf("%s phase %d kind = %v, want %v", b.ID, i, p.Kind, want)
			}
			if p.MemGB > b.ReqMemGB {
				t.Errorf("%s phase %d target %.1f exceeds request %.1f", b.ID, i, p.MemGB, b.ReqMemGB)
			}
		}
		if total != b.LifeSec {
			t.Errorf("%s phases sum to %d, LifeSec=%d", b.ID, total, b.LifeSec)
		}
	}
}

func TestActiveFractionMatchesArchetype(t *testing.T) {
	cfg := config.Default()
	active := map[string]float64{}
	life := map[string]float64{}
	for _, b := range Generate(cfg, 11) {
		for _, p := range b.Phases {
			if p.Kind == fleet.PhaseActive {
				active[b.Archetype] += float64(p.DurSec)
			}
			life[b.Archetype] += float64(p.DurSec)
		}
	}
	for name, a := range cfg.Archetypes {
		got := active[name] / life[name]
		if math.Abs(got-a.ActiveFrac) > 0.08 {
			t.Errorf("%s: active fraction %.2f, want ~%.2f", name, got, a.ActiveFrac)
		}
	}
}

func TestArrivalScheduleIsPiecewise(t *testing.T) {
	cfg := config.Default()
	cfg.BoxArrival = config.Arrival{
		StopAfterSec: 7200,
		Schedule:     []config.RateSegment{{UntilSec: 3600, RatePerHr: 0}, {UntilSec: 7200, RatePerHr: 600}},
	}
	boxes := Generate(cfg, 1)
	if len(boxes) < 400 {
		t.Fatalf("expected ~600 arrivals in the busy hour, got %d", len(boxes))
	}
	for _, b := range boxes {
		if b.ArriveAt < 3600 {
			t.Fatalf("%s arrived at %d during a zero-rate segment", b.ID, b.ArriveAt)
		}
	}
}

func TestPreemptorLifetimes(t *testing.T) {
	cfg := config.Default()
	p := NewPreemptor(cfg, 5)
	if _, ok := p.Lifetime("ondemand-m"); ok {
		t.Fatal("on-demand hosts must never be preempted")
	}
	sum := 0.0
	const n = 2000
	for i := 0; i < n; i++ {
		sec, ok := p.Lifetime("spot-m")
		if !ok || sec <= 0 {
			t.Fatalf("draw %d: sec=%d ok=%v", i, sec, ok)
		}
		sum += float64(sec)
	}
	meanHr := sum / n / 3600
	if want := 1 / cfg.HostTypes["spot-m"].PreemptRatePerHr; math.Abs(meanHr-want) > 0.1*want {
		t.Fatalf("mean lifetime %.1fh, want ~%.1fh", meanHr, want)
	}
}
