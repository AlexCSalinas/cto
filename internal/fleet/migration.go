package fleet

import "math"

// MigrationParams are the live-migration tunables taken from the scenario.
type MigrationParams struct {
	FixedDowntimeSec     float64
	DowntimeFraction     float64
	MaxConcurrentPerHost int
}

// Model bundles every tunable the fleet equations need so controllers and
// the simulator compute identical estimates.
type Model struct {
	Box       Params
	Migration MigrationParams
}

// MigrationCost is the outcome of the migration equations for one transfer.
type MigrationCost struct {
	TransferGB  float64
	TransferSec float64 // wall time of the memory copy
	RunSec      float64 // pre-copy: the box keeps running on the source
	DowntimeSec float64 // final copy: the box is stopped
}

// TotalSec is the time from migration start to completion.
func (c MigrationCost) TotalSec() float64 { return c.RunSec + c.DowntimeSec }

// EstimateMigration applies the migration equations (see DESIGN.md §3):
//
//	bw           = min(fromNet, toNet) / max_concurrent_per_host
//	transfer_sec = transfer_gb / bw
//	downtime_sec = fixed_downtime_sec + transfer_sec * downtime_fraction
//
// Each concurrent migration slot is given a fixed, equal share of the host
// NIC, so an estimate made by a controller matches what the simulator does.
func (m Model) EstimateMigration(transferGB, fromNet, toNet float64) MigrationCost {
	bw := math.Min(fromNet, toNet) / float64(m.Migration.MaxConcurrentPerHost)
	transfer := transferGB / bw
	return MigrationCost{
		TransferGB:  transferGB,
		TransferSec: transfer,
		RunSec:      transfer * (1 - m.Migration.DowntimeFraction),
		DowntimeSec: m.Migration.FixedDowntimeSec + transfer*m.Migration.DowntimeFraction,
	}
}
