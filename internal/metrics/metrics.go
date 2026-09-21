// Package metrics accumulates what happened during a run and turns it into
// a Summary plus a utilization time series.
package metrics

import (
	"math"
	"sort"
)

// Sample is one row of the utilization time series.
type Sample struct {
	T            int64
	RunningHosts int
	MemUtil      float64
	CPUUtil      float64
	CostSoFar    float64
}

// Summary is the end-of-run report. Field names match the JSON keys used by
// the CLI and README.
type Summary struct {
	Controller            string             `json:"controller"`
	Seed                  uint64             `json:"seed"`
	TotalCostUSD          float64            `json:"total_cost_usd"`
	HostHours             map[string]float64 `json:"host_hours"`
	WorkDoneSec           int64              `json:"work_done_sec"`
	LostWorkSec           int64              `json:"lost_work_sec"`
	WorkPerDollar         float64            `json:"work_per_dollar"`
	BoxesArrived          int                `json:"boxes_arrived"`
	BoxesCompleted        int                `json:"boxes_completed"`
	BoxesLostEvents       int                `json:"boxes_lost_events"`
	MigrationsTotal       int                `json:"migrations_total"`
	MigrationsForReclaim  int                `json:"migrations_for_reclaim"`
	MigrationsForRebal    int                `json:"migrations_for_rebalance"`
	MigrationBytesGB      float64            `json:"migration_bytes_gb"`
	TotalDowntimeSec      float64            `json:"total_downtime_sec"`
	P50DowntimeSec        float64            `json:"p50_downtime_sec"`
	P95DowntimeSec        float64            `json:"p95_downtime_sec"`
	CheckpointBytesGB     float64            `json:"checkpoint_bytes_gb"`
	MeanMemUtilization    float64            `json:"mean_mem_utilization"`
	MeanCPUUtilization    float64            `json:"mean_cpu_utilization"`
	OvercommitHostSamples int                `json:"overcommit_host_samples"`
	PendingBoxSeconds     int64              `json:"pending_box_seconds"`
	HostsLaunched         int                `json:"hosts_launched"`
	HostsPreempted        int                `json:"hosts_preempted"`
}

// Reason tags why a migration happened.
type Reason int

// Migration reasons.
const (
	ReasonReclaim Reason = iota
	ReasonRebalance
)

// Collector receives events from the simulator. It is the only mutable
// metrics state; Summary() is a pure function of it.
type Collector struct {
	s         Summary
	downtimes []float64
	samples   []Sample
	memSum    float64
	cpuSum    float64
}

// New creates a collector for one run.
func New(controller string, seed uint64) *Collector {
	return &Collector{s: Summary{Controller: controller, Seed: seed, HostHours: map[string]float64{}}}
}

// Arrived counts a box entering the system.
func (c *Collector) Arrived() { c.s.BoxesArrived++ }

// Completed counts a box finishing its last phase.
func (c *Collector) Completed() { c.s.BoxesCompleted++ }

// Lost records a box losing lostSec of work to a dead host.
func (c *Collector) Lost(lostSec int64) {
	c.s.BoxesLostEvents++
	c.s.LostWorkSec += lostSec
}

// Migrated records a completed migration.
func (c *Collector) Migrated(reason Reason, gb, downtimeSec float64) {
	c.s.MigrationsTotal++
	if reason == ReasonReclaim {
		c.s.MigrationsForReclaim++
	} else {
		c.s.MigrationsForRebal++
	}
	c.s.MigrationBytesGB += gb
	c.s.TotalDowntimeSec += downtimeSec
	c.downtimes = append(c.downtimes, downtimeSec)
}

// Checkpointed records background checkpoint traffic.
func (c *Collector) Checkpointed(gb float64) { c.s.CheckpointBytesGB += gb }

// Pending records time a box spent waiting for placement.
func (c *Collector) Pending(sec int64) { c.s.PendingBoxSeconds += sec }

// HostLaunched counts a host entering the fleet.
func (c *Collector) HostLaunched() { c.s.HostsLaunched++ }

// HostPreempted counts a reclaim warning.
func (c *Collector) HostPreempted() { c.s.HostsPreempted++ }

// HostBilled records the final bill for one host.
func (c *Collector) HostBilled(hostType string, sec int64, pricePerHr float64) {
	hours := float64(sec) / 3600
	c.s.HostHours[hostType] += hours
	c.s.TotalCostUSD += hours * pricePerHr
}

// Sampled appends a utilization sample. overcommitted is the number of
// running hosts whose observed memory exceeded capacity at this instant.
func (c *Collector) Sampled(s Sample, overcommitted int) {
	c.samples = append(c.samples, s)
	c.memSum += s.MemUtil
	c.cpuSum += s.CPUUtil
	c.s.OvercommitHostSamples += overcommitted
}

// Series returns the utilization time series.
func (c *Collector) Series() []Sample { return c.samples }

// Summary finalizes the derived metrics. workDoneSec is summed by the
// simulator over all boxes at the end of the run.
func (c *Collector) Summary(workDoneSec int64) Summary {
	s := c.s
	s.WorkDoneSec = workDoneSec
	if s.TotalCostUSD > 0 {
		s.WorkPerDollar = float64(workDoneSec) / s.TotalCostUSD
	}
	if n := float64(len(c.samples)); n > 0 {
		s.MeanMemUtilization = c.memSum / n
		s.MeanCPUUtilization = c.cpuSum / n
	}
	s.P50DowntimeSec = percentile(c.downtimes, 0.5)
	s.P95DowntimeSec = percentile(c.downtimes, 0.95)
	return s
}

// percentile uses nearest-rank on a sorted copy; it is only for reporting.
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	rank := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[max(0, min(rank, len(sorted)-1))]
}
