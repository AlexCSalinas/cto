// Package fleet holds the domain model: hosts, boxes, their phases and the
// equations that move observed resource usage and migration cost. It knows
// nothing about events or controllers.
package fleet

import "sort"

// HostID identifies a host. IDs are "<type>-<n>" and sort lexicographically.
type HostID string

// HostState is the lifecycle state of a host.
type HostState int

// Host lifecycle states.
const (
	HostBooting HostState = iota
	HostRunning
	HostDraining
	HostDead
)

func (s HostState) String() string {
	return [...]string{"Booting", "Running", "Draining", "Dead"}[s]
}

// Host is a machine that runs boxes. Capacity fields are immutable; the
// runtime fields are owned by the simulator.
type Host struct {
	ID             HostID
	Type           string
	CPU            float64
	MemGB          float64
	PricePerHr     float64
	Preemptible    bool
	ReclaimWarnSec int64
	NetGBps        float64

	State      HostState
	Boxes      map[BoxID]*Box
	LaunchedAt int64 // billing starts here (boot time is billed)
	DeadAt     int64 // billing stops here; meaningful only when Dead
	DeadlineAt int64 // absolute time the host will be killed; 0 if none
}

// Alive reports whether the host is being billed (anything but Dead).
func (h *Host) Alive() bool { return h.State != HostDead }

// Accepting reports whether new boxes may be placed on the host.
func (h *Host) Accepting() bool { return h.State == HostRunning }

// ObsMemGB is the observed memory used by all boxes on the host. Sums run
// in ID order: float addition is not associative, so summing in map order
// would make results differ between identical runs.
func (h *Host) ObsMemGB() float64 {
	total := 0.0
	for _, b := range h.SortedBoxes() {
		total += b.ObsMemGB
	}
	return total
}

// ObsCPU is the observed CPU used by all boxes on the host.
func (h *Host) ObsCPU() float64 {
	total := 0.0
	for _, b := range h.SortedBoxes() {
		total += b.ObsCPU
	}
	return total
}

// SortedBoxes returns the host's boxes ordered by ID so iteration is
// deterministic.
func (h *Host) SortedBoxes() []*Box {
	out := make([]*Box, 0, len(h.Boxes))
	for _, b := range h.Boxes {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// AliveSeconds is how long the host has been billed as of now.
func (h *Host) AliveSeconds(now int64) int64 {
	end := now
	if h.State == HostDead && h.DeadAt < now {
		end = h.DeadAt
	}
	return end - h.LaunchedAt
}
