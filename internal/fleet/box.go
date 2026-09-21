package fleet

import "math"

// BoxID identifies a box. IDs are zero-padded so they sort in arrival order.
type BoxID string

// BoxState is the lifecycle state of a box.
type BoxState int

// Box lifecycle states. Active means "awake on a host"; whether the box is
// doing useful work depends on the kind of its current phase. A box whose
// host dies is Lost only for an instant: it is re-queued as Pending and the
// loss is recorded in LostWorkSec and the metrics.
const (
	BoxPending BoxState = iota
	BoxActive
	BoxSleeping
	BoxMigrating
	BoxDone
)

func (s BoxState) String() string {
	return [...]string{"Pending", "Active", "Sleeping", "Migrating", "Done"}[s]
}

// PhaseKind distinguishes useful work from waiting on an inference call.
type PhaseKind int

// Phase kinds.
const (
	PhaseActive PhaseKind = iota
	PhaseWaiting
)

// Phase is one segment of a box's trace-driven lifetime.
type Phase struct {
	Kind          PhaseKind
	DurSec        int64
	MemGB         float64 // observed-memory target the box ramps toward
	CPU           float64
	DirtyGBPerSec float64 // Active only
}

// Params are the box-dynamics tunables taken from the scenario.
type Params struct {
	SleepGraceSec   int64
	WakeDelaySec    int64
	ResidentFloorGB float64
	MemRampGBPerSec float64
}

// Box is an agent sandbox VM. Observed usage is integrated lazily: Advance
// moves the box from LastUpdateAt to now, so nothing ticks every second.
type Box struct {
	ID        BoxID
	Archetype string
	ArriveAt  int64
	LifeSec   int64
	ReqMemGB  float64
	ReqCPU    float64
	Phases    []Phase

	HostID           HostID
	State            BoxState
	ObsMemGB         float64
	ObsCPU           float64
	DirtyGB          float64
	LastCheckpointAt int64
	WorkDoneSec      int64
	LostWorkSec      int64

	// Phase clock. PhaseLeftSec counts down while the box is awake, or while
	// it is asleep inside a Waiting phase; it pauses during migration
	// downtime so a migrated box finishes the same amount of work.
	PhaseIdx     int
	PhaseLeftSec int64
	LastUpdateAt int64
	PendingSince int64

	// Epoch invalidates timer events (PhaseEnd, BoxSleep, BoxWake) that were
	// scheduled before the box's timeline was rewritten.
	Epoch uint64

	// Checkpoint state restored when the box is Lost.
	ckptPhaseIdx     int
	ckptPhaseLeftSec int64
	ckptWorkDoneSec  int64

	// State the box returns to after migration downtime.
	preMigrateState BoxState
}

// Phase returns the box's current phase.
func (b *Box) Phase() Phase { return b.Phases[b.PhaseIdx] }

// Working reports whether the box is Active and inside an Active phase, i.e.
// accumulating useful work.
func (b *Box) Working() bool {
	return b.State == BoxActive && b.Phase().Kind == PhaseActive
}

// WorkSinceCheckpointSec is the useful work that would be lost if the box's
// host died right now.
func (b *Box) WorkSinceCheckpointSec() int64 { return b.WorkDoneSec - b.ckptWorkDoneSec }

// Advance integrates observed memory, dirty pages, work and the phase clock
// from LastUpdateAt to now.
func (b *Box) Advance(now int64, p Params) {
	dt := now - b.LastUpdateAt
	b.LastUpdateAt = now
	if dt <= 0 {
		return
	}
	switch b.State {
	case BoxActive:
		ph := b.Phase()
		b.ObsMemGB = ramp(b.ObsMemGB, ph.MemGB, p.MemRampGBPerSec*float64(dt))
		b.ObsCPU = ph.CPU
		if ph.Kind == PhaseActive {
			b.WorkDoneSec += dt
			// A box cannot dirty more memory than it has resident.
			b.DirtyGB = math.Min(b.DirtyGB+ph.DirtyGBPerSec*float64(dt), b.ObsMemGB)
		}
		b.PhaseLeftSec -= dt
	case BoxSleeping:
		if b.Phase().Kind == PhaseWaiting {
			b.PhaseLeftSec -= dt
		}
	}
}

// ramp moves cur toward target by at most step.
func ramp(cur, target, step float64) float64 {
	switch {
	case cur < target:
		return math.Min(cur+step, target)
	case cur > target:
		return math.Max(cur-step, target)
	}
	return cur
}

// SleepIn returns how many seconds until the box should auto-sleep in its
// current Waiting phase (0 if the grace period has already elapsed).
func (b *Box) SleepIn(p Params) int64 {
	elapsed := b.Phase().DurSec - b.PhaseLeftSec
	if elapsed >= p.SleepGraceSec {
		return 0
	}
	return p.SleepGraceSec - elapsed
}

// Sleep pages the box out. Paging out writes the full image to durable
// storage, so it doubles as a checkpoint: this is why sleeping boxes migrate
// for the price of the resident floor.
func (b *Box) Sleep(now int64, p Params) {
	b.State = BoxSleeping
	b.ObsMemGB = math.Min(b.ObsMemGB, p.ResidentFloorGB)
	b.ObsCPU = 0
	b.Checkpoint(now)
}

// Wake makes the box Active again; memory ramps back up from the floor.
func (b *Box) Wake() {
	b.State = BoxActive
	b.ObsCPU = b.Phase().CPU
}

// Checkpoint records a durable snapshot of the box at now.
func (b *Box) Checkpoint(now int64) {
	b.DirtyGB = 0
	b.LastCheckpointAt = now
	b.ckptPhaseIdx = b.PhaseIdx
	b.ckptPhaseLeftSec = b.PhaseLeftSec
	b.ckptWorkDoneSec = b.WorkDoneSec
}

// NextPhase advances to the next phase and reports false when the box has
// finished its last one.
func (b *Box) NextPhase() bool {
	b.PhaseIdx++
	if b.PhaseIdx >= len(b.Phases) {
		return false
	}
	b.PhaseLeftSec = b.Phase().DurSec
	return true
}

// Lose handles the box's host dying under it: work since the last checkpoint
// is lost, and the box is re-queued to resume from that checkpoint.
func (b *Box) Lose(now int64) int64 {
	lost := b.WorkSinceCheckpointSec()
	b.LostWorkSec += lost
	b.WorkDoneSec = b.ckptWorkDoneSec
	b.PhaseIdx = b.ckptPhaseIdx
	b.PhaseLeftSec = b.ckptPhaseLeftSec
	b.DirtyGB = 0
	b.ObsMemGB = 0
	b.ObsCPU = 0
	b.HostID = ""
	b.State = BoxPending
	b.PendingSince = now
	b.LastUpdateAt = now
	b.Epoch++
	return lost
}

// StartMigrationDowntime stops the box for the final copy.
func (b *Box) StartMigrationDowntime() {
	b.preMigrateState = b.State
	b.State = BoxMigrating
}

// EndMigrationDowntime resumes the box on its new host with a clean image.
func (b *Box) EndMigrationDowntime(now int64, to HostID) {
	b.State = b.preMigrateState
	b.HostID = to
	b.Checkpoint(now)
}

// TransferGB is the amount of memory a migration must move right now.
func (b *Box) TransferGB(floorGB float64) float64 { return b.DirtyGB + floorGB }

// AbortMigrationDowntime resumes the box on its source host after the
// destination disappeared mid-copy.
func (b *Box) AbortMigrationDowntime() { b.State = b.preMigrateState }
