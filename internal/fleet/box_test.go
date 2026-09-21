package fleet

import (
	"math"
	"testing"
)

var testParams = Params{SleepGraceSec: 5, WakeDelaySec: 2, ResidentFloorGB: 0.25, MemRampGBPerSec: 0.5}

func newTestBox() *Box {
	b := &Box{
		ID:       "box-1",
		ReqMemGB: 32,
		Phases: []Phase{
			{Kind: PhaseActive, DurSec: 100, MemGB: 10, CPU: 4, DirtyGBPerSec: 0.05},
			{Kind: PhaseWaiting, DurSec: 60, MemGB: 10},
			{Kind: PhaseActive, DurSec: 50, MemGB: 20, CPU: 4, DirtyGBPerSec: 0.05},
		},
		State:        BoxActive,
		PhaseLeftSec: 100,
	}
	b.Checkpoint(0)
	return b
}

func TestAdvanceRampsMemoryAndDirtiesAtBoundedRate(t *testing.T) {
	b := newTestBox()
	b.Advance(4, testParams)
	if want := 2.0; b.ObsMemGB != want {
		t.Fatalf("after 4s ObsMemGB = %.2f, want %.2f (ramp 0.5 GB/s)", b.ObsMemGB, want)
	}
	// Dirty pages cannot exceed what is resident: 0.05*4 = 0.2 < 2.0.
	if want := 0.2; math.Abs(b.DirtyGB-want) > 1e-9 {
		t.Fatalf("DirtyGB = %.3f, want %.3f", b.DirtyGB, want)
	}
	b.Advance(100, testParams)
	if b.ObsMemGB != 10 {
		t.Fatalf("ObsMemGB should saturate at the phase target, got %.2f", b.ObsMemGB)
	}
	if b.WorkDoneSec != 100 || b.PhaseLeftSec != 0 {
		t.Fatalf("WorkDoneSec=%d PhaseLeftSec=%d, want 100 and 0", b.WorkDoneSec, b.PhaseLeftSec)
	}
	if want := 5.0; math.Abs(b.DirtyGB-want) > 1e-9 {
		t.Fatalf("DirtyGB = %.3f, want %.3f", b.DirtyGB, want)
	}
}

func TestSleepAndWake(t *testing.T) {
	b := newTestBox()
	b.Advance(100, testParams)
	b.NextPhase() // into the Waiting phase
	if b.Phase().Kind != PhaseWaiting || b.PhaseLeftSec != 60 {
		t.Fatalf("expected waiting phase of 60s, got %+v left=%d", b.Phase(), b.PhaseLeftSec)
	}
	if in := b.SleepIn(testParams); in != 5 {
		t.Fatalf("SleepIn = %d, want grace of 5", in)
	}
	b.Advance(105, testParams)
	b.Sleep(105, testParams)
	if b.State != BoxSleeping || b.ObsMemGB != 0.25 || b.ObsCPU != 0 || b.DirtyGB != 0 {
		t.Fatalf("sleeping box should sit at the resident floor with no dirty pages: %+v", b)
	}
	if b.TransferGB(0.25) != 0.25 {
		t.Fatalf("sleeping box should migrate for the floor only, got %.2f GB", b.TransferGB(0.25))
	}
	b.Advance(160, testParams)
	if b.WorkDoneSec != 100 {
		t.Fatalf("no work should accrue while waiting, got %d", b.WorkDoneSec)
	}
	if b.PhaseLeftSec != 0 {
		t.Fatalf("waiting phase clock should keep running while asleep, left=%d", b.PhaseLeftSec)
	}
	b.NextPhase()
	b.Advance(162, testParams) // wake delay: active-phase clock must not tick while asleep
	if b.PhaseLeftSec != 50 {
		t.Fatalf("PhaseLeftSec = %d during wake delay, want 50", b.PhaseLeftSec)
	}
	b.Wake()
	b.Advance(172, testParams)
	if b.ObsMemGB != 5.25 || b.WorkDoneSec != 110 {
		t.Fatalf("after wake ObsMemGB=%.2f WorkDoneSec=%d, want 5.25 and 110", b.ObsMemGB, b.WorkDoneSec)
	}
}

func TestCheckpointAndLose(t *testing.T) {
	b := newTestBox()
	b.Advance(40, testParams)
	b.Checkpoint(40)
	if b.DirtyGB != 0 || b.LastCheckpointAt != 40 {
		t.Fatalf("checkpoint should clear dirty pages: %+v", b)
	}
	b.Advance(90, testParams)
	if b.WorkSinceCheckpointSec() != 50 {
		t.Fatalf("WorkSinceCheckpointSec = %d, want 50", b.WorkSinceCheckpointSec())
	}
	lost := b.Lose(90)
	if lost != 50 || b.LostWorkSec != 50 || b.WorkDoneSec != 40 {
		t.Fatalf("lost=%d LostWorkSec=%d WorkDoneSec=%d, want 50/50/40", lost, b.LostWorkSec, b.WorkDoneSec)
	}
	if b.State != BoxPending || b.PhaseLeftSec != 60 || b.ObsMemGB != 0 || b.HostID != "" {
		t.Fatalf("lost box should resume from its checkpoint as Pending: %+v", b)
	}
}

func TestMigrationDowntimePausesPhaseClock(t *testing.T) {
	b := newTestBox()
	b.Advance(30, testParams)
	b.StartMigrationDowntime()
	b.Advance(35, testParams)
	if b.PhaseLeftSec != 70 || b.WorkDoneSec != 30 {
		t.Fatalf("clock must pause during downtime: left=%d work=%d", b.PhaseLeftSec, b.WorkDoneSec)
	}
	b.EndMigrationDowntime(35, "host-b")
	if b.State != BoxActive || b.HostID != "host-b" || b.DirtyGB != 0 {
		t.Fatalf("after migration: %+v", b)
	}
}

func TestEstimateMigration(t *testing.T) {
	m := Model{Migration: MigrationParams{FixedDowntimeSec: 2, DowntimeFraction: 0.2, MaxConcurrentPerHost: 2}}
	tests := []struct {
		gb, from, to            float64
		transfer, run, downtime float64
	}{
		{10, 10, 25, 2, 1.6, 2.4},          // bottleneck is the 10 GB/s side, halved by 2 slots
		{0.25, 25, 25, 0.02, 0.016, 2.004}, // sleeping box: floor only
		{0, 10, 10, 0, 0, 2},
	}
	for _, tc := range tests {
		c := m.EstimateMigration(tc.gb, tc.from, tc.to)
		if math.Abs(c.TransferSec-tc.transfer) > 1e-9 || math.Abs(c.RunSec-tc.run) > 1e-9 || math.Abs(c.DowntimeSec-tc.downtime) > 1e-9 {
			t.Errorf("EstimateMigration(%v) = %+v, want transfer=%v run=%v downtime=%v", tc, c, tc.transfer, tc.run, tc.downtime)
		}
		if math.Abs(c.TotalSec()-(tc.run+tc.downtime)) > 1e-9 {
			t.Errorf("TotalSec = %v, want %v", c.TotalSec(), tc.run+tc.downtime)
		}
	}
}

func TestHostHelpers(t *testing.T) {
	h := &Host{ID: "h", MemGB: 100, State: HostRunning, Boxes: map[BoxID]*Box{}, LaunchedAt: 100}
	h.Boxes["b"] = &Box{ID: "b", ObsMemGB: 3, ObsCPU: 1}
	h.Boxes["a"] = &Box{ID: "a", ObsMemGB: 4, ObsCPU: 2}
	if h.ObsMemGB() != 7 || h.ObsCPU() != 3 {
		t.Fatalf("sums wrong: mem=%v cpu=%v", h.ObsMemGB(), h.ObsCPU())
	}
	if bs := h.SortedBoxes(); bs[0].ID != "a" || bs[1].ID != "b" {
		t.Fatal("SortedBoxes must order by ID")
	}
	if h.AliveSeconds(160) != 60 {
		t.Fatalf("AliveSeconds = %d, want 60", h.AliveSeconds(160))
	}
	h.State, h.DeadAt = HostDead, 130
	if h.AliveSeconds(160) != 30 || h.Alive() {
		t.Fatalf("dead host billed to DeadAt only: %d", h.AliveSeconds(160))
	}
}
