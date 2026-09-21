package sim

import (
	"sort"

	"github.com/alexcsalinas/cto/internal/controller"
	"github.com/alexcsalinas/cto/internal/fleet"
)

func (w *World) handleBoxArrive(e *BoxArrive) {
	b := w.boxes[e.Box]
	b.State = fleet.BoxPending
	b.PendingSince = w.now
	b.LastUpdateAt = w.now
	w.m.Arrived()
	w.tryPlace(w.view(), b)
}

// pendingBoxes returns unplaced boxes oldest first so retries are fair and
// deterministic. Boxes that have not arrived yet are also Pending (the zero
// state), hence the ArriveAt filter.
func (w *World) pendingBoxes() []*fleet.Box {
	var out []*fleet.Box
	for _, b := range w.boxes {
		if b.State == fleet.BoxPending && b.ArriveAt <= w.now {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PendingSince != out[j].PendingSince {
			return out[i].PendingSince < out[j].PendingSince
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// placePending retries every waiting box. The view is only rebuilt after a
// successful placement: a refusal changes nothing, and rebuilding per box
// made a long backlog quadratic.
func (w *World) placePending() {
	v := w.view()
	for _, b := range w.pendingBoxes() {
		if w.tryPlace(v, b) {
			v = w.view()
		}
	}
}

// tryPlace asks the controller for a host. A decision the simulator cannot
// honour (unknown, not running, or physically full host) leaves the box
// Pending rather than failing the run.
func (w *World) tryPlace(v controller.FleetView, b *fleet.Box) bool {
	id := w.ctl.Place(v, boxView(b, false))
	if id == "" {
		return false
	}
	h := w.hosts[id]
	if h == nil || !h.Accepting() || h.ObsMemGB()+b.ObsMemGB > h.MemGB {
		return false
	}
	w.m.Pending(w.now - b.PendingSince)
	b.HostID = h.ID
	b.State = fleet.BoxActive
	b.ObsCPU = b.Phase().CPU
	b.LastUpdateAt = w.now
	h.Boxes[b.ID] = b
	w.scheduleTimers(b)
	return true
}

// scheduleTimers (re)arms the phase-end and auto-sleep timers for a box
// from its current phase clock, invalidating any earlier ones.
func (w *World) scheduleTimers(b *fleet.Box) {
	b.Epoch++
	if b.State == fleet.BoxSleeping && b.Phase().Kind == fleet.PhaseActive {
		// Asleep in an Active phase means the box was inside its wake delay
		// when a migration paused it; restart the delay.
		w.q.push(&BoxWake{timing: at(w.now + w.cfg.WakeDelaySec), Box: b.ID, Epoch: b.Epoch})
		return
	}
	w.q.push(&PhaseEnd{timing: at(w.now + b.PhaseLeftSec), Box: b.ID, Epoch: b.Epoch})
	if b.State == fleet.BoxActive && b.Phase().Kind == fleet.PhaseWaiting {
		if in := b.SleepIn(w.model.Box); in < b.PhaseLeftSec {
			w.q.push(&BoxSleep{timing: at(w.now + in), Box: b.ID, Epoch: b.Epoch})
		}
	}
}

func (w *World) handlePhaseEnd(e *PhaseEnd) {
	b := w.boxes[e.Box]
	if b.Epoch != e.Epoch || (b.State != fleet.BoxActive && b.State != fleet.BoxSleeping) {
		return
	}
	b.Advance(w.now, w.model.Box)
	wasAsleep := b.State == fleet.BoxSleeping
	if !b.NextPhase() {
		w.finishBox(b)
		return
	}
	if wasAsleep {
		// The inference call returned; the box needs a moment to page in.
		b.Epoch++
		w.q.push(&BoxWake{timing: at(w.now + w.cfg.WakeDelaySec), Box: b.ID, Epoch: b.Epoch})
		return
	}
	b.ObsCPU = b.Phase().CPU
	w.scheduleTimers(b)
}

func (w *World) handleBoxSleep(e *BoxSleep) {
	b := w.boxes[e.Box]
	if b.Epoch != e.Epoch || b.State != fleet.BoxActive || b.Phase().Kind != fleet.PhaseWaiting {
		return
	}
	b.Advance(w.now, w.model.Box)
	b.Sleep(w.now, w.model.Box)
}

func (w *World) handleBoxWake(e *BoxWake) {
	b := w.boxes[e.Box]
	if b.Epoch != e.Epoch || b.State != fleet.BoxSleeping {
		return
	}
	b.Advance(w.now, w.model.Box)
	b.Wake()
	w.scheduleTimers(b)
}

func (w *World) finishBox(b *fleet.Box) {
	h := w.hosts[b.HostID]
	delete(h.Boxes, b.ID)
	b.HostID = ""
	b.State = fleet.BoxDone
	w.m.Completed()
	// A box can finish while its pre-copy is still streaming; the move is
	// pointless now and must not deliver a finished box to the destination.
	w.migs.cancelWhere(w, func(m *migration) bool { return m.box == b.ID })
	w.retireIfEmpty(h)
	w.startMigrations()
}
