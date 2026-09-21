// Package config defines the scenario configuration for a CTO run: host
// types, initial fleet, workload archetypes and the tunables of the box,
// migration and controller models. A scenario is a JSON file that overrides
// fields of Default().
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Range is an inclusive [lo, hi] interval sampled uniformly by the workload
// generator.
type Range [2]float64

// Lo returns the lower bound.
func (r Range) Lo() float64 { return r[0] }

// Hi returns the upper bound.
func (r Range) Hi() float64 { return r[1] }

// HostType describes one class of host the fleet can be built from.
type HostType struct {
	CPU              float64 `json:"cpu"`
	MemGB            float64 `json:"mem_gb"`
	PricePerHr       float64 `json:"price_per_hr"`
	Preemptible      bool    `json:"preemptible"`
	ReclaimWarnSec   int64   `json:"reclaim_warn_sec"`
	PreemptRatePerHr float64 `json:"preempt_rate_per_hr"`
	NetGBps          float64 `json:"net_gbps"`
}

// Archetype describes a family of boxes with similar lifecycle behaviour.
// Every Range is sampled once per box (life, request) or once per phase
// (burst, wait, memory fraction).
type Archetype struct {
	Weight         float64 `json:"weight"`
	LifeHr         Range   `json:"life_hr"`
	ReqMemGB       Range   `json:"req_mem_gb"`
	ReqCPU         Range   `json:"req_cpu"`
	ActiveFrac     float64 `json:"active_frac"`
	ActiveBurstSec Range   `json:"active_burst_sec"`
	WaitSec        Range   `json:"wait_sec"`
	MemFracOfReq   Range   `json:"mem_frac_of_req"`
	CPUFracOfReq   Range   `json:"cpu_frac_of_req"`
	DirtyGBPerSec  float64 `json:"dirty_gb_per_sec"`
}

// RateSegment is one piece of a piecewise-constant arrival rate: the rate
// applies from the previous segment's UntilSec (or 0) up to UntilSec.
type RateSegment struct {
	UntilSec  int64   `json:"until_sec"`
	RatePerHr float64 `json:"rate_per_hr"`
}

// Arrival configures the Poisson arrival process for boxes. If Schedule is
// non-empty it replaces the constant RatePerHr; arrivals always stop at
// StopAfterSec.
type Arrival struct {
	RatePerHr    float64       `json:"rate_per_hr"`
	StopAfterSec int64         `json:"stop_after_sec"`
	Schedule     []RateSegment `json:"rate_schedule,omitempty"`
}

// Migration holds the live-migration model parameters (see DESIGN.md).
type Migration struct {
	FixedDowntimeSec     float64 `json:"fixed_downtime_sec"`
	DowntimeFraction     float64 `json:"downtime_fraction"`
	MaxConcurrentPerHost int     `json:"max_concurrent_per_host"`
}

// Controller holds the tunables shared by the built-in controllers.
type Controller struct {
	HeadroomFrac    float64 `json:"headroom_frac"`
	HotThreshold    float64 `json:"hot_threshold"`
	ColdThreshold   float64 `json:"cold_threshold"`
	TickIntervalSec int64   `json:"tick_interval_sec"`
}

// Scenario is the full configuration of one simulation run.
type Scenario struct {
	SeedDefault           uint64               `json:"seed_default"`
	DurationSec           int64                `json:"duration_sec"`
	HostTypes             map[string]HostType  `json:"host_types"`
	InitialFleet          map[string]int       `json:"initial_fleet"`
	MaxHosts              int                  `json:"max_hosts"`
	HostBootSec           int64                `json:"host_boot_sec"`
	BoxArrival            Arrival              `json:"box_arrival"`
	Archetypes            map[string]Archetype `json:"archetypes"`
	SleepGraceSec         int64                `json:"sleep_grace_sec"`
	WakeDelaySec          int64                `json:"wake_delay_sec"`
	ResidentFloorGB       float64              `json:"resident_floor_gb"`
	MemRampGBPerSec       float64              `json:"mem_ramp_gb_per_sec"`
	CheckpointIntervalSec int64                `json:"checkpoint_interval_sec"`
	Migration             Migration            `json:"migration"`
	Controller            Controller           `json:"controller"`
	SampleIntervalSec     int64                `json:"sample_interval_sec"`
}

// Default returns the base scenario. Scenario files only need to specify the
// fields they change.
func Default() Scenario {
	return Scenario{
		SeedDefault: 42,
		DurationSec: 48 * 3600,
		HostTypes: map[string]HostType{
			"spot-m":     {CPU: 32, MemGB: 128, PricePerHr: 0.90, Preemptible: true, ReclaimWarnSec: 120, PreemptRatePerHr: 0.05, NetGBps: 10},
			"spot-l":     {CPU: 64, MemGB: 256, PricePerHr: 1.70, Preemptible: true, ReclaimWarnSec: 120, PreemptRatePerHr: 0.05, NetGBps: 25},
			"ondemand-m": {CPU: 32, MemGB: 128, PricePerHr: 2.60, NetGBps: 10},
		},
		InitialFleet: map[string]int{"spot-m": 6, "spot-l": 2},
		MaxHosts:     20,
		HostBootSec:  60,
		BoxArrival:   Arrival{RatePerHr: 30, StopAfterSec: 36 * 3600},
		Archetypes: map[string]Archetype{
			"coding-agent":  {Weight: 0.5, LifeHr: Range{2, 12}, ReqMemGB: Range{8, 32}, ReqCPU: Range{2, 8}, ActiveFrac: 0.4, ActiveBurstSec: Range{60, 900}, WaitSec: Range{20, 300}, MemFracOfReq: Range{0.3, 0.7}, CPUFracOfReq: Range{0.3, 0.8}, DirtyGBPerSec: 0.02},
			"deep-research": {Weight: 0.3, LifeHr: Range{6, 48}, ReqMemGB: Range{4, 16}, ReqCPU: Range{1, 4}, ActiveFrac: 0.15, ActiveBurstSec: Range{30, 300}, WaitSec: Range{120, 1800}, MemFracOfReq: Range{0.2, 0.5}, CPUFracOfReq: Range{0.2, 0.6}, DirtyGBPerSec: 0.005},
			"build-heavy":   {Weight: 0.2, LifeHr: Range{1, 6}, ReqMemGB: Range{32, 96}, ReqCPU: Range{8, 16}, ActiveFrac: 0.8, ActiveBurstSec: Range{600, 3600}, WaitSec: Range{10, 60}, MemFracOfReq: Range{0.6, 0.95}, CPUFracOfReq: Range{0.5, 1.0}, DirtyGBPerSec: 0.08},
		},
		SleepGraceSec:         5,
		WakeDelaySec:          2,
		ResidentFloorGB:       0.25,
		MemRampGBPerSec:       0.5,
		CheckpointIntervalSec: 600,
		Migration:             Migration{FixedDowntimeSec: 2, DowntimeFraction: 0.2, MaxConcurrentPerHost: 2},
		Controller:            Controller{HeadroomFrac: 0.2, HotThreshold: 0.9, ColdThreshold: 0.25, TickIntervalSec: 60},
		SampleIntervalSec:     60,
	}
}

// Load reads a scenario file on top of Default() and validates the result.
// Map-valued fields (host_types, initial_fleet, archetypes) present in the
// file replace the defaults wholesale rather than merging key by key, so a
// scenario can define a fleet with fewer host types than the default.
func Load(path string) (Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, err
	}
	return Parse(data)
}

// Parse is Load for in-memory JSON.
func Parse(data []byte) (Scenario, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Scenario{}, fmt.Errorf("parse scenario: %w", err)
	}
	s := Default()
	if _, ok := raw["host_types"]; ok {
		s.HostTypes = nil
	}
	if _, ok := raw["initial_fleet"]; ok {
		s.InitialFleet = nil
	}
	if _, ok := raw["archetypes"]; ok {
		s.Archetypes = nil
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Scenario{}, fmt.Errorf("parse scenario: %w", err)
	}
	if err := s.Validate(); err != nil {
		return Scenario{}, err
	}
	return s, nil
}

// SortedHostTypes returns host type names in sorted order so callers that
// iterate the map produce deterministic output.
func (s Scenario) SortedHostTypes() []string {
	names := make([]string, 0, len(s.HostTypes))
	for n := range s.HostTypes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// SortedArchetypes returns archetype names in sorted order.
func (s Scenario) SortedArchetypes() []string {
	names := make([]string, 0, len(s.Archetypes))
	for n := range s.Archetypes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
