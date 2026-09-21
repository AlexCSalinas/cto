// Package controller defines the scheduling policy boundary of the
// simulator and the built-in policies. A Controller only ever sees
// read-only snapshots (FleetView) and answers with plans; the simulator
// decides what actually happens.
package controller

import (
	"github.com/alexcsalinas/cto/internal/config"
	"github.com/alexcsalinas/cto/internal/fleet"
)

// Controller is a scheduling policy.
type Controller interface {
	// Name is the registry name of the policy.
	Name() string
	// Init is called once with the initial fleet before the run starts.
	Init(view FleetView)
	// Place chooses a host for a newly arrived or re-queued box. Returning ""
	// leaves the box Pending; it is retried on the next ControllerTick and
	// whenever a host finishes booting.
	Place(view FleetView, box BoxView) fleet.HostID
	// Tick is the periodic rebalance hook.
	Tick(view FleetView, now int64) Plan
	// OnReclaimWarning is called when a host starts draining; deadline is the
	// absolute simulation time at which it dies.
	OnReclaimWarning(view FleetView, host fleet.HostID, deadline int64) EvacuationPlan
}

// Migration asks the simulator to move Box to host To.
type Migration struct {
	Box fleet.BoxID
	To  fleet.HostID
}

// Plan is what Tick returns. The simulator honours launches up to the fleet
// cap and shuts hosts down once they are empty.
type Plan struct {
	Migrations    []Migration
	LaunchHosts   []string
	ShutdownHosts []fleet.HostID
}

// EvacuationPlan is what OnReclaimWarning returns. Migrations are executed
// in order, subject to the per-host concurrency limit.
type EvacuationPlan struct {
	Migrations  []Migration
	LaunchHosts []string
}

// Config is the controller section of the scenario.
type Config = config.Controller

// BoxView is a read-only snapshot of a box.
type BoxView struct {
	ID                     fleet.BoxID
	HostID                 fleet.HostID
	State                  fleet.BoxState
	Archetype              string
	ReqMemGB               float64
	ReqCPU                 float64
	ObsMemGB               float64
	ObsCPU                 float64
	DirtyGB                float64
	WorkSinceCheckpointSec int64
	InFlight               bool // a migration of this box is queued or running
}

// HostView is a read-only snapshot of a host and the boxes on it.
type HostView struct {
	ID          fleet.HostID
	Type        string
	State       fleet.HostState
	CPU         float64
	MemGB       float64
	PricePerHr  float64
	NetGBps     float64
	Preemptible bool
	DeadlineAt  int64
	Boxes       []BoxView // sorted by ID
	ObsMemGB    float64
	ObsCPU      float64
	ReqMemGB    float64
	ReqCPU      float64
	// Migrations queued or running with this host as an endpoint, and the
	// observed memory of boxes heading here.
	InFlight       int
	IncomingMemGB  float64
	IncomingReqMem float64
}

// MigrationView is a queued or running migration.
type MigrationView struct {
	Box     fleet.BoxID
	From    fleet.HostID
	To      fleet.HostID
	Running bool
}

// FleetView is a read-only snapshot of the world at Now. Slices are sorted
// by ID so any iteration over them is deterministic.
type FleetView struct {
	Now       int64
	Hosts     []HostView
	Pending   []BoxView // boxes waiting for placement, oldest first
	InFlight  []MigrationView
	Model     fleet.Model
	HostTypes map[string]config.HostType // shared, do not mutate
	MaxHosts  int
	Alive     int // hosts currently billed (Booting, Running, Draining)
}

// Host looks up a host by ID.
func (v FleetView) Host(id fleet.HostID) (HostView, bool) {
	for _, h := range v.Hosts {
		if h.ID == id {
			return h, true
		}
	}
	return HostView{}, false
}

// EstimateMigrationSec is the time from start to completion of moving box
// from one host to another, using the simulator's own equations.
func (v FleetView) EstimateMigrationSec(box BoxView, from, to HostView) float64 {
	gb := box.DirtyGB + v.Model.Box.ResidentFloorGB
	return v.Model.EstimateMigration(gb, from.NetGBps, to.NetGBps).TotalSec()
}
