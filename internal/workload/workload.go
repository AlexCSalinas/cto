// Package workload turns a scenario into a concrete, seeded trace: the boxes
// that will arrive (with their full phase sequence) and the lifetimes of
// preemptible hosts. Everything is sampled up front from explicit RNG
// streams so a run is reproducible and two controllers see the same trace.
package workload

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"

	"github.com/alexcsalinas/cto/internal/config"
	"github.com/alexcsalinas/cto/internal/fleet"
)

// Stream selectors for the PCG seed's second word, so the box trace and the
// preemption draws never share an RNG.
const (
	streamBoxes    = 1
	streamPreempts = 2
)

// Generate samples the box arrivals and phase traces for a scenario.
func Generate(cfg config.Scenario, seed uint64) []*fleet.Box {
	rng := rand.New(rand.NewPCG(seed, streamBoxes))
	names := cfg.SortedArchetypes()
	cum := make([]float64, len(names))
	total := 0.0
	for i, n := range names {
		total += cfg.Archetypes[n].Weight
		cum[i] = total
	}
	var boxes []*fleet.Box
	for i, at := range arrivals(rng, cfg.BoxArrival) {
		// Float64 is in [0,1), so the search always lands inside cum.
		name := names[sort.SearchFloat64s(cum, rng.Float64()*total)]
		b := newBox(rng, fleet.BoxID(fmt.Sprintf("box-%05d", i+1)), name, cfg.Archetypes[name], at)
		boxes = append(boxes, b)
	}
	return boxes
}

// arrivals samples a piecewise-constant Poisson process. Because the process
// is memoryless, restarting at each segment boundary is exact.
func arrivals(rng *rand.Rand, a config.Arrival) []int64 {
	segs := a.Schedule
	if len(segs) == 0 {
		segs = []config.RateSegment{{UntilSec: a.StopAfterSec, RatePerHr: a.RatePerHr}}
	}
	var out []int64
	start := int64(0)
	for _, seg := range segs {
		end := min(seg.UntilSec, a.StopAfterSec)
		t := float64(start)
		for seg.RatePerHr > 0 {
			t += rng.ExpFloat64() / (seg.RatePerHr / 3600)
			if t >= float64(end) {
				break
			}
			out = append(out, int64(t))
		}
		start = end
		if start >= a.StopAfterSec {
			break
		}
	}
	return out
}

func newBox(rng *rand.Rand, id fleet.BoxID, name string, a config.Archetype, at int64) *fleet.Box {
	b := &fleet.Box{
		ID:        id,
		Archetype: name,
		ArriveAt:  at,
		ReqMemGB:  uniform(rng, a.ReqMemGB),
		ReqCPU:    uniform(rng, a.ReqCPU),
		State:     fleet.BoxPending,
	}
	life := int64(math.Ceil(uniform(rng, a.LifeHr) * 3600))
	b.Phases = phases(rng, a, b, life)
	for _, p := range b.Phases {
		b.LifeSec += p.DurSec
	}
	b.PhaseLeftSec = b.Phases[0].DurSec
	b.Checkpoint(at)
	return b
}

// phases alternates Active bursts and inference waits until the box's
// lifetime is used up. Burst lengths come straight from the archetype's
// range; wait lengths are scaled so the expected active fraction over the
// whole life equals active_frac (see DESIGN.md).
func phases(rng *rand.Rand, a config.Archetype, b *fleet.Box, life int64) []fleet.Phase {
	meanBurst := (a.ActiveBurstSec.Lo() + a.ActiveBurstSec.Hi()) / 2
	meanWait := (a.WaitSec.Lo() + a.WaitSec.Hi()) / 2
	waitScale := meanBurst * (1 - a.ActiveFrac) / a.ActiveFrac / meanWait
	var out []fleet.Phase
	remaining := life
	memTarget := 0.0
	for remaining > 0 {
		var p fleet.Phase
		if len(out)%2 == 0 {
			memTarget = uniform(rng, a.MemFracOfReq) * b.ReqMemGB
			p = fleet.Phase{
				Kind:          fleet.PhaseActive,
				DurSec:        int64(math.Ceil(uniform(rng, a.ActiveBurstSec))),
				MemGB:         memTarget,
				CPU:           uniform(rng, a.CPUFracOfReq) * b.ReqCPU,
				DirtyGBPerSec: a.DirtyGBPerSec,
			}
		} else {
			// Memory stays resident until the box is paged out, so a wait
			// phase keeps the preceding burst's target.
			p = fleet.Phase{
				Kind:   fleet.PhaseWaiting,
				DurSec: int64(math.Ceil(uniform(rng, a.WaitSec) * waitScale)),
				MemGB:  memTarget,
			}
		}
		p.DurSec = max(1, min(p.DurSec, remaining))
		remaining -= p.DurSec
		out = append(out, p)
	}
	return out
}

func uniform(rng *rand.Rand, r config.Range) float64 {
	return r.Lo() + rng.Float64()*(r.Hi()-r.Lo())
}

// Preemptor hands out preemption lifetimes for hosts as they are launched.
// The k-th launched preemptible host always gets the k-th draw, regardless
// of which controller asked for it.
type Preemptor struct {
	rng   *rand.Rand
	types map[string]config.HostType
}

// NewPreemptor creates the preemption stream for a seed.
func NewPreemptor(cfg config.Scenario, seed uint64) *Preemptor {
	return &Preemptor{rng: rand.New(rand.NewPCG(seed, streamPreempts)), types: cfg.HostTypes}
}

// Lifetime samples how many seconds after launch a host of the given type is
// preempted. ok is false for hosts that are never preempted.
func (p *Preemptor) Lifetime(hostType string) (sec int64, ok bool) {
	t := p.types[hostType]
	if !t.Preemptible || t.PreemptRatePerHr <= 0 {
		return 0, false
	}
	return int64(math.Ceil(p.rng.ExpFloat64() / (t.PreemptRatePerHr / 3600))), true
}
