package sim

import (
	"fmt"
	"sort"

	"github.com/alexcsalinas/cto/internal/fleet"
)

func sortHosts(hs []*fleet.Host) {
	sort.Slice(hs, func(i, j int) bool { return hs[i].ID < hs[j].ID })
}

func (w *World) aliveHosts() int {
	n := 0
	for _, h := range w.hosts {
		if h.Alive() {
			n++
		}
	}
	return n
}

// launchHost adds a host of the given type, honouring max_hosts. Its
// preemption time is drawn now so the draw order is fixed by launch order.
func (w *World) launchHost(typ string, bootSec int64) bool {
	ht, ok := w.cfg.HostTypes[typ]
	if !ok || w.aliveHosts() >= w.cfg.MaxHosts {
		return false
	}
	w.hostSeq++
	h := &fleet.Host{
		ID:             fleet.HostID(fmt.Sprintf("%s-%03d", typ, w.hostSeq)),
		Type:           typ,
		CPU:            ht.CPU,
		MemGB:          ht.MemGB,
		PricePerHr:     ht.PricePerHr,
		Preemptible:    ht.Preemptible,
		ReclaimWarnSec: ht.ReclaimWarnSec,
		NetGBps:        ht.NetGBps,
		State:          fleet.HostBooting,
		Boxes:          map[fleet.BoxID]*fleet.Box{},
		LaunchedAt:     w.now,
	}
	w.hosts[h.ID] = h
	w.m.HostLaunched()
	if bootSec <= 0 {
		h.State = fleet.HostRunning
	} else {
		w.q.push(&HostBooted{timing: at(w.now + bootSec), Host: h.ID})
	}
	if life, ok := w.preempt.Lifetime(typ); ok {
		dieAt := w.now + life
		w.q.push(&ReclaimWarning{timing: at(max(w.now, dieAt-ht.ReclaimWarnSec)), Host: h.ID, Deadline: dieAt})
		w.q.push(&HostDead{timing: at(dieAt), Host: h.ID})
	}
	return true
}

func (w *World) applyLaunches(types []string) {
	for _, typ := range types {
		w.launchHost(typ, w.cfg.HostBootSec)
	}
}

// applyShutdowns drains hosts the controller no longer wants. A draining
// host dies as soon as it is empty; the controller is responsible for
// moving its boxes.
func (w *World) applyShutdowns(ids []fleet.HostID) {
	for _, id := range ids {
		h := w.hosts[id]
		if h == nil || h.State != fleet.HostRunning || w.migs.inflightTo(id) > 0 {
			continue
		}
		h.State = fleet.HostDraining
		w.retireIfEmpty(h)
	}
}

func (w *World) handleHostBooted(e *HostBooted) {
	h := w.hosts[e.Host]
	if h.State == fleet.HostBooting {
		h.State = fleet.HostRunning
	}
	w.controllerTick()
	w.startMigrations()
}

func (w *World) handleReclaimWarning(e *ReclaimWarning) {
	h := w.hosts[e.Host]
	if !h.Alive() {
		return
	}
	h.State = fleet.HostDraining
	h.DeadlineAt = e.Deadline
	w.m.HostPreempted()
	w.migs.cancelIncoming(w, h.ID)
	// The controller hears about every reclaim, even of an empty host, so
	// it can decide whether the lost capacity needs replacing.
	plan := w.ctl.OnReclaimWarning(w.view(), h.ID, e.Deadline)
	w.applyMigrations(plan.Migrations, reasonReclaim)
	w.applyLaunches(plan.LaunchHosts)
	w.retireIfEmpty(h)
}

func (w *World) handleHostDead(e *HostDead) {
	if h := w.hosts[e.Host]; h.Alive() {
		w.killHost(h)
	}
}

// killHost is the provider pulling the plug: in-flight migrations touching
// the host are cancelled and every box still on it loses its uncheckpointed
// work.
func (w *World) killHost(h *fleet.Host) {
	h.State = fleet.HostDead
	h.DeadAt = w.now
	w.migs.cancelTouching(w, h.ID)
	for _, b := range h.SortedBoxes() {
		b.Advance(w.now, w.model.Box)
		delete(h.Boxes, b.ID)
		w.m.Lost(b.Lose(w.now))
	}
	w.placePending()
}

// retireIfEmpty stops billing a draining host once nothing runs on it. A
// reclaimed host is terminated early for the same reason: no point paying
// for a doomed, empty machine.
func (w *World) retireIfEmpty(h *fleet.Host) bool {
	if h.State != fleet.HostDraining || len(h.Boxes) > 0 || w.migs.inflightTo(h.ID) > 0 {
		return false
	}
	h.State = fleet.HostDead
	h.DeadAt = w.now
	return true
}
