package sim

import (
	"math"
	"sort"

	"github.com/alexcsalinas/cto/internal/controller"
	"github.com/alexcsalinas/cto/internal/fleet"
	"github.com/alexcsalinas/cto/internal/metrics"
)

const (
	reasonReclaim   = metrics.ReasonReclaim
	reasonRebalance = metrics.ReasonRebalance
)

// migration is one requested box move. It waits in the queue until both
// endpoints have a free slot, then runs to completion or is cancelled by a
// host death.
type migration struct {
	id      uint64
	box     fleet.BoxID
	from    fleet.HostID
	to      fleet.HostID
	reason  metrics.Reason
	running bool
	cost    fleet.MigrationCost
	stopAt  int64
	doneAt  int64
}

// migrations is the per-run migration bookkeeping. Bandwidth is modelled as
// max_concurrent_per_host slots per host, each with a fixed share of the
// NIC; a migration needs a slot on both hosts. Queued requests are scanned
// in submission order, so a blocked request never delays one behind it.
type migrations struct {
	next   uint64
	queued []*migration
	active map[uint64]*migration
	byBox  map[fleet.BoxID]*migration
}

func newMigrations() migrations {
	return migrations{active: map[uint64]*migration{}, byBox: map[fleet.BoxID]*migration{}}
}

func (ms *migrations) inflightTo(h fleet.HostID) int {
	n := 0
	for _, m := range ms.queued {
		if m.to == h {
			n++
		}
	}
	for _, m := range ms.active {
		if m.to == h {
			n++
		}
	}
	return n
}

func (ms *migrations) activeOn(h fleet.HostID) int {
	n := 0
	for _, m := range ms.active {
		if m.from == h || m.to == h {
			n++
		}
	}
	return n
}

func (ms *migrations) sortedActive() []*migration {
	out := make([]*migration, 0, len(ms.active))
	for _, m := range ms.active {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func (w *World) applyMigrations(reqs []controller.Migration, reason metrics.Reason) {
	for _, r := range reqs {
		w.submitMigration(r.Box, r.To, reason)
	}
	w.startMigrations()
}

// submitMigration queues a move if it makes sense right now: the box must be
// running or asleep on a host, not already moving, and the destination must
// be up or booting.
func (w *World) submitMigration(id fleet.BoxID, to fleet.HostID, reason metrics.Reason) bool {
	b := w.boxes[id]
	dst := w.hosts[to]
	switch {
	case b == nil || dst == nil:
		return false
	case b.State != fleet.BoxActive && b.State != fleet.BoxSleeping:
		return false
	case w.migs.byBox[id] != nil || b.HostID == to:
		return false
	case dst.State != fleet.HostRunning && dst.State != fleet.HostBooting:
		return false
	}
	w.migs.next++
	m := &migration{id: w.migs.next, box: id, from: b.HostID, to: to, reason: reason}
	w.migs.queued = append(w.migs.queued, m)
	w.migs.byBox[id] = m
	return true
}

// startMigrations launches every queued migration that has slots on both
// ends and drops the ones that stopped making sense.
func (w *World) startMigrations() {
	keep := w.migs.queued[:0]
	for _, m := range w.migs.queued {
		b := w.boxes[m.box]
		dst := w.hosts[m.to]
		switch {
		case b.HostID != m.from || (b.State != fleet.BoxActive && b.State != fleet.BoxSleeping):
			delete(w.migs.byBox, m.box)
		case dst.State == fleet.HostBooting:
			keep = append(keep, m)
		case dst.State != fleet.HostRunning:
			delete(w.migs.byBox, m.box)
		case w.migs.activeOn(m.from) >= w.cfg.Migration.MaxConcurrentPerHost ||
			w.migs.activeOn(m.to) >= w.cfg.Migration.MaxConcurrentPerHost:
			keep = append(keep, m)
		default:
			w.startMigration(m, b)
		}
	}
	for i := len(keep); i < len(w.migs.queued); i++ {
		w.migs.queued[i] = nil
	}
	w.migs.queued = keep
}

func (w *World) startMigration(m *migration, b *fleet.Box) {
	b.Advance(w.now, w.model.Box)
	src, dst := w.hosts[m.from], w.hosts[m.to]
	m.cost = w.model.EstimateMigration(b.TransferGB(w.model.Box.ResidentFloorGB), src.NetGBps, dst.NetGBps)
	m.stopAt = w.now + int64(math.Floor(m.cost.RunSec))
	m.doneAt = w.now + int64(math.Ceil(m.cost.TotalSec()))
	m.running = true
	w.migs.active[m.id] = m
	w.q.push(&MigrationStop{timing: at(m.stopAt), ID: m.id})
	w.q.push(&MigrationDone{timing: at(m.doneAt), ID: m.id})
}

func (w *World) handleMigrationStop(e *MigrationStop) {
	m := w.migs.active[e.ID]
	if m == nil {
		return
	}
	b := w.boxes[m.box]
	if b.HostID != m.from || (b.State != fleet.BoxActive && b.State != fleet.BoxSleeping) {
		w.migs.cancelWhere(w, func(x *migration) bool { return x == m })
		return
	}
	b.Advance(w.now, w.model.Box)
	b.StartMigrationDowntime()
	b.Epoch++ // pause the phase clock; timers are re-armed on completion
}

func (w *World) handleMigrationDone(e *MigrationDone) {
	m := w.migs.active[e.ID]
	if m == nil {
		return
	}
	b := w.boxes[m.box]
	src, dst := w.hosts[m.from], w.hosts[m.to]
	b.Advance(w.now, w.model.Box)
	delete(src.Boxes, b.ID)
	dst.Boxes[b.ID] = b
	b.EndMigrationDowntime(w.now, dst.ID)
	w.scheduleTimers(b)
	delete(w.migs.active, m.id)
	delete(w.migs.byBox, m.box)
	w.m.Migrated(m.reason, m.cost.TransferGB, float64(m.doneAt-m.stopAt))
	w.retireIfEmpty(src)
	w.startMigrations()
}

// cancelTouching aborts every migration with host h as an endpoint. A box
// whose source died is about to be Lost by the caller.
func (ms *migrations) cancelTouching(w *World, h fleet.HostID) {
	ms.cancelWhere(w, func(m *migration) bool { return m.from == h || m.to == h })
}

// cancelIncoming aborts migrations heading into h, used when h starts
// draining: landing a box on a doomed host only sets it up to be lost.
func (ms *migrations) cancelIncoming(w *World, h fleet.HostID) {
	ms.cancelWhere(w, func(m *migration) bool { return m.to == h })
}

// cancelWhere drops matching migrations. A box that was already paused for
// its final copy resumes where it was.
func (ms *migrations) cancelWhere(w *World, match func(*migration) bool) {
	keep := ms.queued[:0]
	for _, m := range ms.queued {
		if match(m) {
			delete(ms.byBox, m.box)
			continue
		}
		keep = append(keep, m)
	}
	ms.queued = keep
	for _, m := range ms.sortedActive() {
		if !match(m) {
			continue
		}
		delete(ms.active, m.id)
		delete(ms.byBox, m.box)
		if b := w.boxes[m.box]; b.State == fleet.BoxMigrating {
			b.AbortMigrationDowntime()
			w.scheduleTimers(b)
		}
	}
}
