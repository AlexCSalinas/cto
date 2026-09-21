package sim

import (
	"testing"

	"github.com/alexcsalinas/cto/internal/config"
	"github.com/alexcsalinas/cto/internal/controller"
	"github.com/alexcsalinas/cto/internal/fleet"
)

// scripted is a controller whose decisions are supplied by the test.
type scripted struct {
	place   func(v controller.FleetView, b controller.BoxView) fleet.HostID
	reclaim func(v controller.FleetView, host fleet.HostID, deadline int64) controller.EvacuationPlan
	tick    func(v controller.FleetView, now int64) controller.Plan
}

func (scripted) Name() string              { return "scripted" }
func (scripted) Init(controller.FleetView) {}
func (s scripted) Place(v controller.FleetView, b controller.BoxView) fleet.HostID {
	if s.place == nil {
		return ""
	}
	return s.place(v, b)
}
func (s scripted) Tick(v controller.FleetView, now int64) controller.Plan {
	if s.tick == nil {
		return controller.Plan{}
	}
	return s.tick(v, now)
}
func (s scripted) OnReclaimWarning(v controller.FleetView, h fleet.HostID, d int64) controller.EvacuationPlan {
	if s.reclaim == nil {
		return controller.EvacuationPlan{}
	}
	return s.reclaim(v, h, d)
}

// placeOn always places on the named host.
func placeOn(id fleet.HostID) func(controller.FleetView, controller.BoxView) fleet.HostID {
	return func(controller.FleetView, controller.BoxView) fleet.HostID { return id }
}

// quietScenario has no random arrivals or preemptions and a slow NIC on
// spot-m so migrations take a measurable number of seconds.
func quietScenario() config.Scenario {
	cfg := config.Default()
	cfg.DurationSec = 3600
	cfg.BoxArrival.RatePerHr = 0
	for k, h := range cfg.HostTypes {
		h.PreemptRatePerHr = 0
		if k == "spot-m" {
			h.NetGBps = 0.2 // 0.1 GB/s per slot
		}
		cfg.HostTypes[k] = h
	}
	cfg.InitialFleet = map[string]int{"spot-m": 2}
	cfg.CheckpointIntervalSec = 100000 // only when a test asks for it
	return cfg
}

// testWorld builds a world with the initial fleet up and one hand-made box
// arriving at t=0. The box has a 1000s active phase that dirties 0.05 GB/s
// toward a 20 GB target, then a long wait.
func testWorld(t *testing.T, cfg config.Scenario, ctl controller.Controller) (*World, *fleet.Box) {
	t.Helper()
	w := newWorld(cfg, ctl, 1)
	for _, typ := range cfg.SortedHostTypes() {
		for i := 0; i < cfg.InitialFleet[typ]; i++ {
			w.launchHost(typ, 0)
		}
	}
	b := &fleet.Box{
		ID: "box-00001", ReqMemGB: 32, ReqCPU: 4,
		Phases: []fleet.Phase{
			{Kind: fleet.PhaseActive, DurSec: 1000, MemGB: 20, CPU: 4, DirtyGBPerSec: 0.05},
			{Kind: fleet.PhaseWaiting, DurSec: 1000, MemGB: 20},
			{Kind: fleet.PhaseActive, DurSec: 100, MemGB: 20, CPU: 4, DirtyGBPerSec: 0.05},
		},
		PhaseLeftSec: 1000,
	}
	b.LifeSec = 2100
	b.Checkpoint(0)
	w.boxes[b.ID] = b
	w.q.push(&BoxArrive{timing: at(0), Box: b.ID})
	w.q.push(&SimEnd{at(cfg.DurationSec)})
	return w, b
}

func TestBoxLostWhenSourceDiesMidMigration(t *testing.T) {
	cfg := quietScenario()
	ctl := scripted{
		place: func(v controller.FleetView, b controller.BoxView) fleet.HostID {
			// First placement on host 1; after it dies, host 2 is the only choice.
			for _, h := range v.Hosts {
				if h.State == fleet.HostRunning {
					return h.ID
				}
			}
			return ""
		},
		reclaim: func(v controller.FleetView, host fleet.HostID, _ int64) controller.EvacuationPlan {
			return controller.EvacuationPlan{Migrations: []controller.Migration{{Box: "box-00001", To: "spot-m-002"}}}
		},
	}
	w, b := testWorld(t, cfg, ctl)
	// At t=500 the box has ~20 GB dirty; at 0.1 GB/s that is 200s of copy,
	// far more than the 60s warning.
	w.q.push(&ReclaimWarning{timing: at(500), Host: "spot-m-001", Deadline: 560})
	w.q.push(&HostDead{timing: at(560), Host: "spot-m-001"})
	w.loop()

	s := w.result().Summary
	if s.BoxesLostEvents != 1 || s.MigrationsTotal != 0 {
		t.Fatalf("expected one loss and no completed migration, got %+v", s)
	}
	if b.LostWorkSec != 560 {
		t.Fatalf("LostWorkSec = %d, want 560 (no checkpoint since t=0)", b.LostWorkSec)
	}
	// Re-placed on the survivor immediately, resumed from t=0 and finished.
	if s.PendingBoxSeconds != 0 || b.State != fleet.BoxDone || b.WorkDoneSec != 1100 {
		t.Fatalf("pending=%d state=%v work=%d, want 0 Done 1100", s.PendingBoxSeconds, b.State, b.WorkDoneSec)
	}
	if w.hosts["spot-m-001"].AliveSeconds(w.now) != 560 {
		t.Fatalf("dead host billed for %d s, want 560", w.hosts["spot-m-001"].AliveSeconds(w.now))
	}
}

func TestMigrationCompletesAndPausesWork(t *testing.T) {
	cfg := quietScenario()
	cfg.DurationSec = 1500 // stop while the box is asleep in its wait phase
	w, b := testWorld(t, cfg, scripted{place: placeOn("spot-m-001")})
	// 10 s in: dirty = 0.5 GB + 0.25 floor = 0.75 GB at 0.1 GB/s = 7.5 s copy.
	w.q.push(&ControllerTick{at(10)})
	w.ctl = scripted{
		place: placeOn("spot-m-001"),
		tick: func(v controller.FleetView, now int64) controller.Plan {
			if now != 10 {
				return controller.Plan{}
			}
			return controller.Plan{Migrations: []controller.Migration{{Box: b.ID, To: "spot-m-002"}}}
		},
	}
	w.loop()
	s := w.result().Summary
	if s.MigrationsTotal != 1 || s.MigrationsForRebal != 1 {
		t.Fatalf("expected one rebalance migration, got %+v", s)
	}
	if b.HostID != "spot-m-002" || len(w.hosts["spot-m-001"].Boxes) != 0 {
		t.Fatalf("box should live on host 2 now: host=%s", b.HostID)
	}
	// run=6s, downtime=2+1.5=3.5 -> stop at 16, done at 20: 4 s of downtime.
	if s.TotalDowntimeSec != 4 {
		t.Fatalf("downtime = %v, want 4", s.TotalDowntimeSec)
	}
	// The phase clock paused during downtime, so the active phase still
	// delivered its full 1000 s of work (ending at t=1004 instead of 1000).
	if b.WorkDoneSec != 1000 || b.State != fleet.BoxSleeping {
		t.Fatalf("WorkDoneSec=%d state=%v, want 1000 Sleeping", b.WorkDoneSec, b.State)
	}
}

func TestSleepingBoxMigratesCheaply(t *testing.T) {
	cfg := quietScenario()
	w, b := testWorld(t, cfg, scripted{})
	w.ctl = scripted{
		place: placeOn("spot-m-001"),
		tick: func(v controller.FleetView, now int64) controller.Plan {
			if now != 1200 {
				return controller.Plan{}
			}
			return controller.Plan{Migrations: []controller.Migration{{Box: b.ID, To: "spot-m-002"}}}
		},
	}
	w.q.push(&ControllerTick{at(1200)}) // box is asleep in its wait phase
	w.loop()
	s := w.result().Summary
	if s.MigrationsTotal != 1 || s.MigrationBytesGB != cfg.ResidentFloorGB {
		t.Fatalf("sleeping box should move only the floor: %+v", s)
	}
	if b.State != fleet.BoxDone || b.WorkDoneSec != 1100 {
		t.Fatalf("box should still complete: state=%v work=%d", b.State, b.WorkDoneSec)
	}
}

func TestCheckpointResetsDirtyAndBoundsLoss(t *testing.T) {
	cfg := quietScenario()
	cfg.CheckpointIntervalSec = 300
	w, b := testWorld(t, cfg, scripted{place: placeOn("spot-m-001")})
	w.q.push(&CheckpointTick{at(300)})
	w.q.push(&HostDead{timing: at(500), Host: "spot-m-001"})
	w.loop()
	if b.LostWorkSec != 200 {
		t.Fatalf("LostWorkSec = %d, want 200 (work since the t=300 checkpoint)", b.LostWorkSec)
	}
	s := w.result().Summary
	if s.CheckpointBytesGB != 15 { // 300 s * 0.05 GB/s, all resident (20 GB target)
		t.Fatalf("CheckpointBytesGB = %v, want 15", s.CheckpointBytesGB)
	}
}

func TestPendingBoxPlacedWhenReplacementBoots(t *testing.T) {
	cfg := quietScenario()
	cfg.InitialFleet = map[string]int{"spot-m": 1}
	cfg.DurationSec = 1000 // stop while the box is still running
	w, b := testWorld(t, cfg, scripted{})
	w.ctl = scripted{
		place: func(v controller.FleetView, _ controller.BoxView) fleet.HostID {
			for _, h := range v.Hosts {
				if h.State == fleet.HostRunning && h.ID != "spot-m-001" {
					return h.ID
				}
			}
			return ""
		},
		reclaim: func(controller.FleetView, fleet.HostID, int64) controller.EvacuationPlan {
			return controller.EvacuationPlan{LaunchHosts: []string{"spot-m"}}
		},
	}
	w.q.push(&ReclaimWarning{timing: at(100), Host: "spot-m-001", Deadline: 130})
	w.q.push(&HostDead{timing: at(130), Host: "spot-m-001"})
	w.loop()
	s := w.result().Summary
	// Pending from 0 until the replacement is up at 100+60=160.
	if b.HostID != "spot-m-002" || s.PendingBoxSeconds != 160 || s.HostsLaunched != 2 {
		t.Fatalf("host=%s pending=%d launched=%d", b.HostID, s.PendingBoxSeconds, s.HostsLaunched)
	}
}

func TestBoxFinishingDuringPreCopyCancelsMigration(t *testing.T) {
	cfg := quietScenario()
	cfg.DurationSec = 3000
	w, b := testWorld(t, cfg, scripted{place: placeOn("spot-m-001")})
	// One 1000 s active phase; a migration started at 995 s has ~50 GB to
	// copy at 0.1 GB/s, so the box completes long before the final copy.
	b.Phases = b.Phases[:1]
	b.LifeSec = 1000
	w.ctl = scripted{
		place: placeOn("spot-m-001"),
		tick: func(v controller.FleetView, now int64) controller.Plan {
			if now != 995 {
				return controller.Plan{}
			}
			return controller.Plan{Migrations: []controller.Migration{{Box: b.ID, To: "spot-m-002"}}}
		},
	}
	w.q.push(&ControllerTick{at(995)})
	w.q.push(&HostDead{timing: at(2000), Host: "spot-m-002"})
	w.loop()
	s := w.result().Summary
	if b.State != fleet.BoxDone || b.WorkDoneSec != 1000 || s.MigrationsTotal != 0 || s.BoxesLostEvents != 0 {
		t.Fatalf("state=%v work=%d summary=%+v", b.State, b.WorkDoneSec, s)
	}
	if len(w.hosts["spot-m-002"].Boxes) != 0 {
		t.Fatal("finished box must not be delivered to the destination")
	}
}

func TestMigrationDuringWakeDelayStillWakes(t *testing.T) {
	cfg := quietScenario()
	cfg.DurationSec = 2050 // stop inside the final 100 s active phase
	w, b := testWorld(t, cfg, scripted{place: placeOn("spot-m-001")})
	// The wait phase ends at t=2000 (1000 active + 1000 wait); the wake
	// delay runs 2000-2002. A sleeping box migrates in ~5 s, so a move
	// submitted at 2000 pauses the box inside its wake delay.
	w.ctl = scripted{
		place: placeOn("spot-m-001"),
		tick: func(v controller.FleetView, now int64) controller.Plan {
			if now != 2000 {
				return controller.Plan{}
			}
			return controller.Plan{Migrations: []controller.Migration{{Box: b.ID, To: "spot-m-002"}}}
		},
	}
	w.q.push(&ControllerTick{at(2000)})
	w.loop()
	if b.State != fleet.BoxActive || b.HostID != "spot-m-002" || b.WorkDoneSec <= 1000 {
		t.Fatalf("box should be awake and working on host 2: state=%v host=%s work=%d", b.State, b.HostID, b.WorkDoneSec)
	}
}
