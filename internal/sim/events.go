package sim

import "github.com/alexcsalinas/cto/internal/fleet"

// Timer events on boxes carry the box Epoch they were scheduled under; the
// box drops them if its timeline has been rewritten since (migration
// downtime, loss).

// BoxArrive is a box entering the system.
type BoxArrive struct {
	timing
	Box fleet.BoxID
}

// PhaseEnd is the end of a box's current phase.
type PhaseEnd struct {
	timing
	Box   fleet.BoxID
	Epoch uint64
}

// BoxSleep is the sleep-grace timer expiring inside a Waiting phase.
type BoxSleep struct {
	timing
	Box   fleet.BoxID
	Epoch uint64
}

// BoxWake is the wake delay expiring after a Waiting phase ended.
type BoxWake struct {
	timing
	Box   fleet.BoxID
	Epoch uint64
}

// MigrationStop is the point in a migration where the box is paused for
// the final copy.
type MigrationStop struct {
	timing
	ID uint64
}

// MigrationDone is the completion of a migration.
type MigrationDone struct {
	timing
	ID uint64
}

// CheckpointTick is the periodic background checkpoint of every awake box.
type CheckpointTick struct{ timing }

// ReclaimWarning is the provider announcing that a host will be killed at
// Deadline.
type ReclaimWarning struct {
	timing
	Host     fleet.HostID
	Deadline int64
}

// HostDead is a host being killed by the provider.
type HostDead struct {
	timing
	Host fleet.HostID
}

// HostBooted is a launched host becoming usable.
type HostBooted struct {
	timing
	Host fleet.HostID
}

// ControllerTick is the periodic rebalance hook.
type ControllerTick struct{ timing }

// MetricsSample is the periodic utilization sample.
type MetricsSample struct{ timing }

// SimEnd stops the run.
type SimEnd struct{ timing }
