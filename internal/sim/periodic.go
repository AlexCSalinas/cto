package sim

import (
	"github.com/alexcsalinas/cto/internal/fleet"
	"github.com/alexcsalinas/cto/internal/metrics"
)

func (w *World) handleControllerTick() {
	w.controllerTick()
	w.q.push(&ControllerTick{at(w.now + w.cfg.Controller.TickIntervalSec)})
}

// controllerTick retries placements and applies the controller's plan. It
// also runs when a host finishes booting so a replacement launched during a
// reclaim warning can be used before the deadline.
func (w *World) controllerTick() {
	w.placePending()
	plan := w.ctl.Tick(w.view(), w.now)
	w.applyMigrations(plan.Migrations, reasonRebalance)
	w.applyLaunches(plan.LaunchHosts)
	w.applyShutdowns(plan.ShutdownHosts)
}

// handleCheckpointTick snapshots every awake box. Sleeping boxes were
// checkpointed when they were paged out.
func (w *World) handleCheckpointTick() {
	for _, h := range w.sortedHosts() {
		for _, b := range h.SortedBoxes() {
			if b.State != fleet.BoxActive {
				continue
			}
			b.Advance(w.now, w.model.Box)
			w.m.Checkpointed(b.DirtyGB)
			b.Checkpoint(w.now)
		}
	}
	w.q.push(&CheckpointTick{at(w.now + w.cfg.CheckpointIntervalSec)})
}

// handleMetricsSample records fleet-level utilization over hosts that can
// run boxes (Running or Draining; a Booting host has no capacity yet).
func (w *World) handleMetricsSample() {
	var memCap, memUsed, cpuCap, cpuUsed float64
	running, overcommitted := 0, 0
	for _, h := range w.sortedHosts() {
		if h.State != fleet.HostRunning && h.State != fleet.HostDraining {
			continue
		}
		for _, b := range h.Boxes {
			b.Advance(w.now, w.model.Box)
		}
		used := h.ObsMemGB()
		if used > h.MemGB {
			overcommitted++
		}
		running++
		memCap += h.MemGB
		memUsed += used
		cpuCap += h.CPU
		cpuUsed += h.ObsCPU()
	}
	s := metrics.Sample{T: w.now, RunningHosts: running, CostSoFar: w.costSoFar()}
	if memCap > 0 {
		s.MemUtil = memUsed / memCap
		s.CPUUtil = cpuUsed / cpuCap
	}
	w.m.Sampled(s, overcommitted)
	w.q.push(&MetricsSample{at(w.now + w.cfg.SampleIntervalSec)})
}

func (w *World) costSoFar() float64 {
	total := 0.0
	for _, h := range w.sortedHosts() {
		total += float64(h.AliveSeconds(w.now)) / 3600 * h.PricePerHr
	}
	return total
}

func (w *World) handleSimEnd() {
	for _, h := range w.sortedHosts() {
		for _, b := range h.SortedBoxes() {
			b.Advance(w.now, w.model.Box)
		}
		w.m.HostBilled(h.Type, h.AliveSeconds(w.now), h.PricePerHr)
	}
	for _, b := range w.pendingBoxes() {
		w.m.Pending(w.now - b.PendingSince)
	}
}
