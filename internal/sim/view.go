package sim

import (
	"github.com/alexcsalinas/cto/internal/controller"
	"github.com/alexcsalinas/cto/internal/fleet"
)

func boxView(b *fleet.Box, inFlight bool) controller.BoxView {
	return controller.BoxView{
		ID:                     b.ID,
		HostID:                 b.HostID,
		State:                  b.State,
		Archetype:              b.Archetype,
		ReqMemGB:               b.ReqMemGB,
		ReqCPU:                 b.ReqCPU,
		ObsMemGB:               b.ObsMemGB,
		ObsCPU:                 b.ObsCPU,
		DirtyGB:                b.DirtyGB,
		WorkSinceCheckpointSec: b.WorkSinceCheckpointSec(),
		InFlight:               inFlight,
	}
}

// view snapshots the world for a controller. Boxes are advanced to now
// first so observed usage is current, and everything is copied so the
// controller cannot reach into simulator state.
func (w *World) view() controller.FleetView {
	v := controller.FleetView{
		Now:       w.now,
		Model:     w.model,
		HostTypes: w.cfg.HostTypes,
		MaxHosts:  w.cfg.MaxHosts,
		Alive:     w.aliveHosts(),
	}
	for _, h := range w.sortedHosts() {
		if !h.Alive() {
			continue
		}
		hv := controller.HostView{
			ID: h.ID, Type: h.Type, State: h.State,
			CPU: h.CPU, MemGB: h.MemGB, PricePerHr: h.PricePerHr, NetGBps: h.NetGBps,
			Preemptible: h.Preemptible, DeadlineAt: h.DeadlineAt,
		}
		for _, b := range h.SortedBoxes() {
			b.Advance(w.now, w.model.Box)
			hv.Boxes = append(hv.Boxes, boxView(b, w.migs.byBox[b.ID] != nil))
			hv.ObsMemGB += b.ObsMemGB
			hv.ObsCPU += b.ObsCPU
			hv.ReqMemGB += b.ReqMemGB
			hv.ReqCPU += b.ReqCPU
		}
		v.Hosts = append(v.Hosts, hv)
	}
	for _, b := range w.pendingBoxes() {
		v.Pending = append(v.Pending, boxView(b, false))
	}
	all := append(append([]*migration(nil), w.migs.queued...), w.migs.sortedActive()...)
	for _, m := range all {
		v.InFlight = append(v.InFlight, controller.MigrationView{Box: m.box, From: m.from, To: m.to, Running: m.running})
		b := w.boxes[m.box]
		for i := range v.Hosts {
			hv := &v.Hosts[i]
			if hv.ID == m.from || hv.ID == m.to {
				hv.InFlight++
			}
			if hv.ID == m.to {
				hv.IncomingMemGB += b.ObsMemGB
				hv.IncomingReqMem += b.ReqMemGB
			}
		}
	}
	return v
}
