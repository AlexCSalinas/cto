package config

import (
	"errors"
	"fmt"
)

// Validate checks the scenario for values that would make a run meaningless
// or crash it, and returns a descriptive error for the first problem found.
func (s Scenario) Validate() error {
	if s.DurationSec <= 0 {
		return errors.New("duration_sec must be positive")
	}
	if s.MaxHosts <= 0 {
		return errors.New("max_hosts must be positive")
	}
	if s.HostBootSec < 0 {
		return errors.New("host_boot_sec must not be negative")
	}
	if len(s.HostTypes) == 0 {
		return errors.New("host_types must not be empty")
	}
	for _, name := range s.SortedHostTypes() {
		if err := s.HostTypes[name].validate(); err != nil {
			return fmt.Errorf("host_types[%q]: %w", name, err)
		}
	}
	initial := 0
	for name, n := range s.InitialFleet {
		if _, ok := s.HostTypes[name]; !ok {
			return fmt.Errorf("initial_fleet references unknown host type %q", name)
		}
		if n < 0 {
			return fmt.Errorf("initial_fleet[%q] must not be negative", name)
		}
		initial += n
	}
	if initial == 0 {
		return errors.New("initial_fleet must contain at least one host")
	}
	if initial > s.MaxHosts {
		return fmt.Errorf("initial_fleet has %d hosts, more than max_hosts=%d", initial, s.MaxHosts)
	}
	if err := s.BoxArrival.validate(); err != nil {
		return fmt.Errorf("box_arrival: %w", err)
	}
	if len(s.Archetypes) == 0 {
		return errors.New("archetypes must not be empty")
	}
	weight := 0.0
	for _, name := range s.SortedArchetypes() {
		a := s.Archetypes[name]
		if err := a.validate(); err != nil {
			return fmt.Errorf("archetypes[%q]: %w", name, err)
		}
		weight += a.Weight
	}
	if weight <= 0 {
		return errors.New("archetype weights must sum to more than zero")
	}
	if s.SleepGraceSec < 0 || s.WakeDelaySec < 0 {
		return errors.New("sleep_grace_sec and wake_delay_sec must not be negative")
	}
	if s.ResidentFloorGB < 0 {
		return errors.New("resident_floor_gb must not be negative")
	}
	if s.MemRampGBPerSec <= 0 {
		return errors.New("mem_ramp_gb_per_sec must be positive")
	}
	if s.CheckpointIntervalSec <= 0 {
		return errors.New("checkpoint_interval_sec must be positive")
	}
	if err := s.Migration.validate(); err != nil {
		return fmt.Errorf("migration: %w", err)
	}
	if err := s.Controller.validate(); err != nil {
		return fmt.Errorf("controller: %w", err)
	}
	if s.SampleIntervalSec <= 0 {
		return errors.New("sample_interval_sec must be positive")
	}
	return nil
}

func (h HostType) validate() error {
	switch {
	case h.CPU <= 0 || h.MemGB <= 0:
		return errors.New("cpu and mem_gb must be positive")
	case h.PricePerHr < 0:
		return errors.New("price_per_hr must not be negative")
	case h.NetGBps <= 0:
		return errors.New("net_gbps must be positive")
	case h.Preemptible && h.ReclaimWarnSec <= 0:
		return errors.New("preemptible hosts need a positive reclaim_warn_sec")
	case h.Preemptible && h.PreemptRatePerHr < 0:
		return errors.New("preempt_rate_per_hr must not be negative")
	case !h.Preemptible && h.PreemptRatePerHr != 0:
		return errors.New("non-preemptible hosts must have preempt_rate_per_hr 0")
	}
	return nil
}

func (a Arrival) validate() error {
	if a.StopAfterSec < 0 {
		return errors.New("stop_after_sec must not be negative")
	}
	if len(a.Schedule) == 0 {
		if a.RatePerHr < 0 {
			return errors.New("rate_per_hr must not be negative")
		}
		return nil
	}
	prev := int64(0)
	for i, seg := range a.Schedule {
		if seg.UntilSec <= prev {
			return fmt.Errorf("rate_schedule[%d].until_sec must increase", i)
		}
		if seg.RatePerHr < 0 {
			return fmt.Errorf("rate_schedule[%d].rate_per_hr must not be negative", i)
		}
		prev = seg.UntilSec
	}
	return nil
}

func (a Archetype) validate() error {
	if a.Weight < 0 {
		return errors.New("weight must not be negative")
	}
	if a.ActiveFrac <= 0 || a.ActiveFrac > 1 {
		return errors.New("active_frac must be in (0, 1]")
	}
	if a.DirtyGBPerSec < 0 {
		return errors.New("dirty_gb_per_sec must not be negative")
	}
	ranges := []struct {
		name string
		r    Range
		min  float64
	}{
		{"life_hr", a.LifeHr, 1e-9},
		{"req_mem_gb", a.ReqMemGB, 1e-9},
		{"req_cpu", a.ReqCPU, 1e-9},
		{"active_burst_sec", a.ActiveBurstSec, 1},
		{"wait_sec", a.WaitSec, 1},
		{"mem_frac_of_req", a.MemFracOfReq, 0},
		{"cpu_frac_of_req", a.CPUFracOfReq, 0},
	}
	for _, x := range ranges {
		if x.r.Lo() < x.min || x.r.Hi() < x.r.Lo() {
			return fmt.Errorf("%s must satisfy %g <= lo <= hi, got %v", x.name, x.min, x.r)
		}
	}
	if a.MemFracOfReq.Hi() > 1 || a.CPUFracOfReq.Hi() > 1 {
		return errors.New("mem_frac_of_req and cpu_frac_of_req must not exceed 1")
	}
	return nil
}

func (m Migration) validate() error {
	switch {
	case m.FixedDowntimeSec < 0:
		return errors.New("fixed_downtime_sec must not be negative")
	case m.DowntimeFraction < 0 || m.DowntimeFraction > 1:
		return errors.New("downtime_fraction must be in [0, 1]")
	case m.MaxConcurrentPerHost <= 0:
		return errors.New("max_concurrent_per_host must be positive")
	}
	return nil
}

func (c Controller) validate() error {
	switch {
	case c.HeadroomFrac < 0:
		return errors.New("headroom_frac must not be negative")
	case c.HotThreshold <= 0 || c.HotThreshold > 1.5:
		return errors.New("hot_threshold must be in (0, 1.5]")
	case c.ColdThreshold < 0 || c.ColdThreshold >= c.HotThreshold:
		return errors.New("cold_threshold must be in [0, hot_threshold)")
	case c.TickIntervalSec <= 0:
		return errors.New("tick_interval_sec must be positive")
	}
	return nil
}
