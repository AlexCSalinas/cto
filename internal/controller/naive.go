package controller

import (
	"github.com/alexcsalinas/cto/internal/fleet"
)

// Naive is the baseline: first-fit by requested resources, no rebalancing,
// and a reclaim plan that moves boxes in ID order wherever they fit.
type Naive struct{}

// Name implements Controller.
func (Naive) Name() string { return "naive" }

// Init implements Controller.
func (Naive) Init(FleetView) {}

// Place implements Controller.
func (Naive) Place(v FleetView, box BoxView) fleet.HostID {
	for _, h := range v.Hosts {
		if h.State == fleet.HostRunning && fitsRequested(h, box, 0, 0) {
			return h.ID
		}
	}
	return ""
}

// Tick implements Controller: the baseline never rebalances.
func (Naive) Tick(FleetView, int64) Plan { return Plan{} }

// OnReclaimWarning implements Controller.
func (Naive) OnReclaimWarning(v FleetView, host fleet.HostID, _ int64) EvacuationPlan {
	src, ok := v.Host(host)
	if !ok {
		return EvacuationPlan{}
	}
	plan := EvacuationPlan{LaunchHosts: []string{src.Type}}
	// Requested memory already promised to each destination by this plan.
	extraMem := map[fleet.HostID]float64{}
	extraCPU := map[fleet.HostID]float64{}
	for _, b := range src.Boxes {
		if b.InFlight {
			continue
		}
		for _, h := range v.Hosts {
			if h.State != fleet.HostRunning || !fitsRequested(h, b, extraMem[h.ID], extraCPU[h.ID]) {
				continue
			}
			plan.Migrations = append(plan.Migrations, Migration{Box: b.ID, To: h.ID})
			extraMem[h.ID] += b.ReqMemGB
			extraCPU[h.ID] += b.ReqCPU
			break
		}
	}
	return plan
}

// fitsRequested checks capacity by requested resources, counting boxes
// already heading to the host and any extra the caller has promised.
func fitsRequested(h HostView, b BoxView, extraMem, extraCPU float64) bool {
	mem, cpu := h.ReqMemGB+extraMem, h.ReqCPU+extraCPU
	for _, in := range h.Incoming {
		mem += in.ReqMemGB
		cpu += in.ReqCPU
	}
	return mem+b.ReqMemGB <= h.MemGB && cpu+b.ReqCPU <= h.CPU
}
