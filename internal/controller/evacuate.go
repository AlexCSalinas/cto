package controller

import (
	"math"
	"sort"

	"github.com/alexcsalinas/cto/internal/fleet"
)

// OnReclaimWarning implements Controller. Boxes are ordered by uncheckpointed
// work per second of migration and scheduled onto the source's migration
// slots; a box that could not finish before the deadline is skipped so it
// does not delay the ones that can.
func (g *Greedy) OnReclaimWarning(v FleetView, host fleet.HostID, deadline int64) EvacuationPlan {
	src, ok := v.Host(host)
	if !ok {
		return EvacuationPlan{}
	}
	rooms := g.rooms(v)
	boxes := make([]BoxView, 0, len(src.Boxes))
	for _, b := range src.Boxes {
		if !b.InFlight {
			boxes = append(boxes, b)
		}
	}
	// Value per second uses the source NIC on both ends as a proxy; the
	// real destination is only known once chosen below.
	value := func(b BoxView) float64 {
		return float64(b.WorkSinceCheckpointSec) / v.EstimateMigrationSec(b, src, src)
	}
	sort.SliceStable(boxes, func(i, j int) bool {
		vi, vj := value(boxes[i]), value(boxes[j])
		if vi != vj {
			return vi > vj
		}
		return boxes[i].ID < boxes[j].ID
	})

	var plan EvacuationPlan
	slots := make([]int64, v.Model.Migration.MaxConcurrentPerHost)
	for i := range slots {
		slots[i] = v.Now
	}
	stranded := 0
	for _, b := range boxes {
		to, ok := g.pick(v, rooms, b, src.ID, false)
		if !ok {
			stranded++
			continue
		}
		s := 0
		for i := range slots {
			if slots[i] < slots[s] {
				s = i
			}
		}
		finish := slots[s] + int64(math.Ceil(v.EstimateMigrationSec(b, src, to)))
		if finish > deadline {
			continue
		}
		plan.Migrations = append(plan.Migrations, Migration{Box: b.ID, To: to.ID})
		g.reserve(rooms[to.ID], b)
		slots[s] = finish
	}
	if stranded > 0 {
		plan.LaunchHosts = append(plan.LaunchHosts, src.Type)
	}
	return plan
}
