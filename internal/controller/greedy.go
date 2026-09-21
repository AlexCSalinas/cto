package controller

import (
	"math"
	"sort"

	"github.com/alexcsalinas/cto/internal/fleet"
)

// Greedy packs by observed usage plus headroom, keeps hosts between the
// cold and hot thresholds by migrating or draining, and evacuates a
// reclaimed host most-valuable-work-first within its deadline.
type Greedy struct {
	cfg Config
	// ratio is the mean observed/requested memory of awake boxes per
	// archetype, learned from the fleet. It sizes boxes that have never run,
	// for which observed usage is still zero.
	ratio map[string]float64
}

// NewGreedy builds the greedy controller.
func NewGreedy(cfg Config) *Greedy {
	return &Greedy{cfg: cfg, ratio: map[string]float64{}}
}

// Name implements Controller.
func (g *Greedy) Name() string { return "greedy" }

// Init implements Controller.
func (g *Greedy) Init(v FleetView) { g.learn(v) }

// learn refreshes the per-archetype memory ratios from awake boxes.
func (g *Greedy) learn(v FleetView) {
	sum, n := map[string]float64{}, map[string]int{}
	for _, h := range v.Hosts {
		for _, b := range h.Boxes {
			if b.State == fleet.BoxActive && b.ObsMemGB > 0 {
				sum[b.Archetype] += b.ObsMemGB / b.ReqMemGB
				n[b.Archetype]++
			}
		}
	}
	for a, s := range sum {
		g.ratio[a] = s / float64(n[a])
	}
}

// demand is how much of a host the controller reserves for a box: what it
// observes now (sleeping boxes sit at the resident floor) plus headroom on
// the request. A box that has never run is sized by its archetype's ratio.
func (g *Greedy) demand(b BoxView) (mem, cpu float64) {
	mem, cpu = b.ObsMemGB, b.ObsCPU
	if b.State == fleet.BoxPending {
		mem = g.ratio[b.Archetype] * b.ReqMemGB
	}
	return mem + g.cfg.HeadroomFrac*b.ReqMemGB, cpu + g.cfg.HeadroomFrac*b.ReqCPU
}

// room is a host's spare capacity after every resident and incoming box's
// demand. It is updated as a plan reserves space.
type room struct{ mem, cpu float64 }

func (g *Greedy) rooms(v FleetView) map[fleet.HostID]*room {
	out := map[fleet.HostID]*room{}
	for _, h := range v.Hosts {
		r := &room{mem: h.MemGB, cpu: h.CPU}
		for _, b := range append(append([]BoxView(nil), h.Boxes...), h.Incoming...) {
			m, c := g.demand(b)
			r.mem -= m
			r.cpu -= c
		}
		out[h.ID] = r
	}
	return out
}

func (g *Greedy) fits(r *room, b BoxView) bool {
	m, c := g.demand(b)
	return m <= r.mem && c <= r.cpu
}

func (g *Greedy) reserve(r *room, b BoxView) {
	m, c := g.demand(b)
	r.mem -= m
	r.cpu -= c
}

// pick chooses a destination for b among running hosts other than exclude.
// tightest=true is best-fit (smallest room left); false picks the host with
// the most room, which spreads load off a hot host.
func (g *Greedy) pick(v FleetView, rooms map[fleet.HostID]*room, b BoxView, exclude fleet.HostID, tightest bool) (HostView, bool) {
	var best HostView
	found := false
	better := func(h HostView) bool {
		r, br := rooms[h.ID], rooms[best.ID]
		if r.mem != br.mem {
			return (r.mem < br.mem) == tightest
		}
		if h.PricePerHr != best.PricePerHr {
			return h.PricePerHr < best.PricePerHr
		}
		return h.ID < best.ID
	}
	for _, h := range v.Hosts {
		if h.State != fleet.HostRunning || h.ID == exclude || !g.fits(rooms[h.ID], b) {
			continue
		}
		if !found || better(h) {
			best, found = h, true
		}
	}
	return best, found
}

// Place implements Controller: best-fit among running hosts, so hosts fill
// up tightly and empty ones can be drained.
func (g *Greedy) Place(v FleetView, box BoxView) fleet.HostID {
	h, ok := g.pick(v, g.rooms(v), box, "", true)
	if !ok {
		return ""
	}
	return h.ID
}

// Tick implements Controller.
func (g *Greedy) Tick(v FleetView, now int64) Plan {
	g.learn(v)
	rooms := g.rooms(v)
	budget := map[fleet.HostID]int{}
	for _, h := range v.Hosts {
		budget[h.ID] = v.Model.Migration.MaxConcurrentPerHost - h.InFlight
	}
	var plan Plan
	move := func(b BoxView, from, to HostView) {
		plan.Migrations = append(plan.Migrations, Migration{Box: b.ID, To: to.ID})
		g.reserve(rooms[to.ID], b)
		budget[from.ID]--
		budget[to.ID]--
	}
	booting := 0
	for _, h := range v.Hosts {
		if h.State == fleet.HostBooting {
			booting++
		}
	}
	// Boxes that fit nowhere need a new host; one launch per tick is enough
	// because the next tick sees it booting.
	if len(v.Pending) > 0 && booting == 0 {
		if _, ok := g.pick(v, rooms, v.Pending[0], "", true); !ok {
			plan.LaunchHosts = append(plan.LaunchHosts, g.launchType(v, v.Pending[0]))
		}
	}
	for _, h := range v.Hosts {
		switch {
		case h.State == fleet.HostDraining:
			// Boxes stranded on a dying or retiring host (e.g. landed after
			// its evacuation plan was made) get another chance.
			for _, b := range h.Boxes {
				if b.InFlight || budget[h.ID] <= 0 {
					continue
				}
				if to, ok := g.pick(v, rooms, b, h.ID, false); ok && budget[to.ID] > 0 {
					move(b, h, to)
				}
			}
		case h.State == fleet.HostRunning && h.ObsMemGB > g.cfg.HotThreshold*h.MemGB && budget[h.ID] > 0:
			if b, to, ok := g.relief(v, rooms, h, budget); ok {
				move(b, h, to)
			}
		}
	}
	if h, ok := g.coldHost(v, rooms, budget, booting); ok {
		for _, b := range h.Boxes {
			to, _ := g.pick(v, rooms, b, h.ID, true)
			move(b, h, to)
		}
		plan.ShutdownHosts = append(plan.ShutdownHosts, h.ID)
	}
	return plan
}

// relief picks the box on a hot host that frees the most memory per second
// of migration, sent to the host with the most room.
func (g *Greedy) relief(v FleetView, rooms map[fleet.HostID]*room, h HostView, budget map[fleet.HostID]int) (BoxView, HostView, bool) {
	var best BoxView
	var dest HostView
	bestScore := -1.0
	for _, b := range h.Boxes {
		if b.InFlight || b.ObsMemGB <= 0 {
			continue
		}
		to, ok := g.pick(v, rooms, b, h.ID, false)
		if !ok || budget[to.ID] <= 0 {
			continue
		}
		if score := b.ObsMemGB / v.EstimateMigrationSec(b, h, to); score > bestScore {
			best, dest, bestScore = b, to, score
		}
	}
	return best, dest, bestScore > 0
}

// coldHost finds one under-used running host whose boxes all fit elsewhere,
// provided nothing is pending or booting and no other host is already being
// retired, so consolidation never races against a scale-up. Hosts with
// migrations in flight or planned this tick are left alone.
func (g *Greedy) coldHost(v FleetView, rooms map[fleet.HostID]*room, budget map[fleet.HostID]int, booting int) (HostView, bool) {
	running := 0
	for _, h := range v.Hosts {
		if h.State == fleet.HostDraining && h.DeadlineAt == 0 {
			return HostView{}, false
		}
		if h.State == fleet.HostRunning {
			running++
		}
	}
	if len(v.Pending) > 0 || booting > 0 || running <= 1 {
		return HostView{}, false
	}
	for _, h := range v.Hosts {
		if h.State != fleet.HostRunning || h.ObsMemGB >= g.cfg.ColdThreshold*h.MemGB {
			continue
		}
		if budget[h.ID] != v.Model.Migration.MaxConcurrentPerHost {
			continue
		}
		if len(h.Boxes) > v.Model.Migration.MaxConcurrentPerHost {
			continue // drain in a couple of steps, not by queueing a flood
		}
		trial := map[fleet.HostID]*room{}
		for id, r := range rooms {
			c := *r
			trial[id] = &c
		}
		ok := true
		for _, b := range h.Boxes {
			to, found := g.pick(v, trial, b, h.ID, true)
			if !found {
				ok = false
				break
			}
			g.reserve(trial[to.ID], b)
		}
		if ok {
			return h, true
		}
	}
	return HostView{}, false
}

// launchType picks the cheapest host type per GB that can hold the box.
func (g *Greedy) launchType(v FleetView, b BoxView) string {
	mem, _ := g.demand(b)
	best, bestCost := "", math.Inf(1)
	names := make([]string, 0, len(v.HostTypes))
	for n := range v.HostTypes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		t := v.HostTypes[n]
		if t.MemGB < mem {
			continue
		}
		if cost := t.PricePerHr / t.MemGB; cost < bestCost {
			best, bestCost = n, cost
		}
	}
	return best
}
