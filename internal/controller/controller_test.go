package controller

import (
	"testing"

	"github.com/alexcsalinas/cto/internal/config"
	"github.com/alexcsalinas/cto/internal/fleet"
)

func testModel() fleet.Model {
	return fleet.Model{
		Box:       fleet.Params{ResidentFloorGB: 0.25},
		Migration: fleet.MigrationParams{FixedDowntimeSec: 2, DowntimeFraction: 0.2, MaxConcurrentPerHost: 2},
	}
}

func host(id string, mem float64, price float64, boxes ...BoxView) HostView {
	h := HostView{ID: fleet.HostID(id), Type: "spot-m", State: fleet.HostRunning, CPU: 32, MemGB: mem, PricePerHr: price, NetGBps: 10, Preemptible: true}
	for _, b := range boxes {
		b.HostID = h.ID
		if b.State == fleet.BoxPending {
			b.State = fleet.BoxActive
		}
		h.Boxes = append(h.Boxes, b)
		h.ObsMemGB += b.ObsMemGB
		h.ObsCPU += b.ObsCPU
		h.ReqMemGB += b.ReqMemGB
		h.ReqCPU += b.ReqCPU
	}
	return h
}

func box(id string, req, obs, dirty float64, work int64) BoxView {
	return BoxView{ID: fleet.BoxID(id), Archetype: "coding-agent", ReqMemGB: req, ReqCPU: 4, ObsMemGB: obs, ObsCPU: 1, DirtyGB: dirty, WorkSinceCheckpointSec: work}
}

func view(hosts ...HostView) FleetView {
	return FleetView{Now: 1000, Hosts: hosts, Model: testModel(), HostTypes: config.Default().HostTypes, MaxHosts: 20, Alive: len(hosts)}
}

func TestRegistry(t *testing.T) {
	for _, name := range []string{"naive", "greedy", "lp"} {
		c, err := New(name, config.Default().Controller)
		if err != nil || c.Name() != name {
			t.Errorf("New(%q) = %v, %v", name, c, err)
		}
	}
	if _, err := New("nope", Config{}); err == nil {
		t.Error("unknown controller should error")
	}
}

func TestNaivePlacesFirstFitByRequested(t *testing.T) {
	v := view(
		host("spot-m-001", 128, 0.9, box("box-1", 100, 1, 0, 0)), // 28 GB requested left
		host("spot-m-002", 128, 0.9),
	)
	n := Naive{}
	if got := n.Place(v, box("new", 32, 0, 0, 0)); got != "spot-m-002" {
		t.Errorf("Place = %s, want spot-m-002 (host 1 lacks requested room despite low observed use)", got)
	}
	if got := n.Place(v, box("new", 20, 0, 0, 0)); got != "spot-m-001" {
		t.Errorf("Place = %s, want first fit spot-m-001", got)
	}
}

func TestGreedyPlacesBestFitByObserved(t *testing.T) {
	g := NewGreedy(config.Default().Controller)
	v := view(
		host("spot-m-001", 128, 0.9, box("box-1", 100, 10, 0, 0)), // demand 30 -> 98 free
		host("spot-m-002", 128, 0.9, box("box-2", 40, 50, 0, 0)),  // demand 58 -> 70 free
		host("spot-m-003", 128, 0.9),                              // 128 free
	)
	// New box demand: unknown archetype ratio -> 0.2*32 = 6.4. Tightest fit
	// is host 2, not the emptiest one and not the requested-space-poor one.
	if got := g.Place(v, box("new", 32, 0, 0, 0)); got != "spot-m-002" {
		t.Errorf("Place = %s, want best-fit spot-m-002", got)
	}
	big := box("big", 200, 0, 0, 0)
	big.ObsMemGB = 100
	big.State = fleet.BoxActive // an observed box, e.g. re-queued: demand 140
	if got := g.Place(v, big); got != "" {
		t.Errorf("Place = %s, want \"\" when nothing fits", got)
	}
}

func TestGreedyLearnsArchetypeRatio(t *testing.T) {
	g := NewGreedy(config.Default().Controller)
	v := view(host("spot-m-001", 128, 0.9, box("box-1", 20, 10, 0, 0), box("box-2", 40, 30, 0, 0)))
	g.Init(v)
	if r := g.ratio["coding-agent"]; r != 0.625 {
		t.Fatalf("ratio = %v, want mean(0.5, 0.75)", r)
	}
	pending := box("new", 32, 0, 0, 0)
	if mem, _ := g.demand(pending); mem != 0.625*32+0.2*32 {
		t.Fatalf("pending demand = %v", mem)
	}
}

func TestGreedyHotHostMovesBestRatio(t *testing.T) {
	g := NewGreedy(config.Default().Controller)
	// Host 1 is at 120/128. box-a frees 30 GB but drags 30 GB of dirty
	// pages (~8 s); box-c frees 65 GB but 60 GB dirty (~14 s); box-b frees
	// 25 GB in ~2 s, the best GB per second.
	v := view(
		host("spot-m-001", 128, 0.9,
			box("box-a", 40, 30, 30, 100),
			box("box-b", 40, 25, 0.5, 100),
			box("box-c", 80, 65, 60, 100)),
		host("spot-m-002", 128, 0.9, box("box-d", 40, 60, 0, 0)),
		host("spot-m-003", 128, 0.9, box("box-e", 40, 40, 0, 0)),
	)
	plan := g.Tick(v, 1000)
	if len(plan.Migrations) != 1 || plan.Migrations[0].Box != "box-b" || plan.Migrations[0].To != "spot-m-003" {
		t.Fatalf("plan = %+v, want box-b -> spot-m-003 (most room)", plan)
	}
	// Room (0+60+80) is below the reserve needed to evacuate host 1 (152),
	// so the tick also asks for the cheapest host per GB.
	if len(plan.ShutdownHosts) != 0 || len(plan.LaunchHosts) != 1 || plan.LaunchHosts[0] != "spot-l" {
		t.Fatalf("expected a reserve launch and no drain: %+v", plan)
	}
}

func TestGreedyDrainsColdHostAndLaunchesForPending(t *testing.T) {
	g := NewGreedy(config.Default().Controller)
	v := view(
		host("spot-l-001", 256, 1.7, box("box-a", 20, 60, 0, 0)), // 192 GB room
		host("spot-m-002", 128, 0.9, box("box-b", 20, 5, 0, 0)),  // 4% used
	)
	plan := g.Tick(v, 1000)
	if len(plan.ShutdownHosts) != 1 || plan.ShutdownHosts[0] != "spot-m-002" {
		t.Fatalf("expected spot-m-002 to be drained, got %+v", plan)
	}
	if len(plan.Migrations) != 1 || plan.Migrations[0].Box != "box-b" || plan.Migrations[0].To != "spot-l-001" {
		t.Fatalf("expected box-b moved to spot-l-001, got %+v", plan.Migrations)
	}

	// With a box pending that fits nowhere, no draining and a launch instead.
	v.Pending = []BoxView{box("huge", 100, 0, 0, 0)}
	v.Pending[0].ObsMemGB = 200
	v.Pending[0].State = fleet.BoxActive
	plan = g.Tick(v, 1000)
	if len(plan.ShutdownHosts) != 0 || len(plan.LaunchHosts) != 1 || plan.LaunchHosts[0] != "spot-l" {
		t.Fatalf("expected a spot-l launch and no drain, got %+v", plan)
	}
}

func TestGreedyEvacuationRespectsDeadline(t *testing.T) {
	g := NewGreedy(config.Default().Controller)
	// spot-m NIC 10 GB/s / 2 slots = 5 GB/s. 100 GB dirty takes 23 s;
	// 1 GB takes 3 s. Deadline is 25 s away.
	src := host("spot-m-001", 128, 0.9,
		box("box-fat", 40, 100, 100, 500),  // slot 0, done at +23: high value
		box("box-slim", 20, 10, 1, 50),     // slot 1, done at +3
		box("box-fat2", 40, 100, 100, 400), // slot 1 would finish at +26: skipped
		box("box-idle", 20, 0.25, 0, 0),    // sleeping-like, zero value: last
	)
	src.State = fleet.HostDraining
	src.DeadlineAt = 1025
	v := view(src,
		host("spot-m-002", 256, 1.7),
		host("spot-m-003", 256, 1.7),
	)
	plan := g.OnReclaimWarning(v, src.ID, 1025)
	var order []fleet.BoxID
	for _, m := range plan.Migrations {
		order = append(order, m.Box)
	}
	want := []fleet.BoxID{"box-fat", "box-slim", "box-idle"}
	if len(order) != len(want) {
		t.Fatalf("migrations = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("migrations = %v, want %v", order, want)
		}
	}
	if len(plan.LaunchHosts) != 0 {
		t.Fatalf("no launch needed when everything fits elsewhere: %+v", plan)
	}
}

func TestGreedyEvacuationLaunchesWhenNoRoom(t *testing.T) {
	g := NewGreedy(config.Default().Controller)
	src := host("spot-m-001", 128, 0.9, box("box-a", 40, 50, 1, 100))
	src.State = fleet.HostDraining
	v := view(src, host("spot-m-002", 128, 0.9, box("box-b", 40, 100, 0, 0)))
	plan := g.OnReclaimWarning(v, src.ID, 1120)
	if len(plan.Migrations) != 0 || len(plan.LaunchHosts) != 1 || plan.LaunchHosts[0] != "spot-m" {
		t.Fatalf("expected only a replacement launch, got %+v", plan)
	}
}
