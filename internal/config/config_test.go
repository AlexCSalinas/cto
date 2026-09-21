package config

import (
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default() invalid: %v", err)
	}
}

func TestParseOverridesAndReplacesMaps(t *testing.T) {
	s, err := Parse([]byte(`{
		"duration_sec": 3600,
		"host_types": {"only": {"cpu": 4, "mem_gb": 16, "price_per_hr": 1, "net_gbps": 1}},
		"initial_fleet": {"only": 1},
		"max_hosts": 3
	}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.DurationSec != 3600 {
		t.Errorf("duration_sec = %d, want 3600", s.DurationSec)
	}
	if len(s.HostTypes) != 1 {
		t.Errorf("host_types should be replaced, got %d entries", len(s.HostTypes))
	}
	if s.SleepGraceSec != Default().SleepGraceSec {
		t.Errorf("untouched fields must keep defaults")
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Scenario)
		want   string
	}{
		{"negative duration", func(s *Scenario) { s.DurationSec = -1 }, "duration_sec"},
		{"unknown fleet type", func(s *Scenario) { s.InitialFleet = map[string]int{"nope": 1} }, "unknown host type"},
		{"fleet over cap", func(s *Scenario) { s.MaxHosts = 1 }, "max_hosts"},
		{"empty fleet", func(s *Scenario) { s.InitialFleet = map[string]int{} }, "at least one host"},
		{"zero weights", func(s *Scenario) {
			for k, a := range s.Archetypes {
				a.Weight = 0
				s.Archetypes[k] = a
			}
		}, "weights"},
		{"bad range", func(s *Scenario) {
			a := s.Archetypes["coding-agent"]
			a.LifeHr = Range{5, 2}
			s.Archetypes["coding-agent"] = a
		}, "life_hr"},
		{"preemptible without warning", func(s *Scenario) {
			h := s.HostTypes["spot-m"]
			h.ReclaimWarnSec = 0
			s.HostTypes["spot-m"] = h
		}, "reclaim_warn_sec"},
		{"cold above hot", func(s *Scenario) { s.Controller.ColdThreshold = 0.95 }, "cold_threshold"},
		{"bad schedule", func(s *Scenario) {
			s.BoxArrival.Schedule = []RateSegment{{UntilSec: 10, RatePerHr: 1}, {UntilSec: 5, RatePerHr: 1}}
		}, "rate_schedule[1]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := Default()
			tc.mutate(&s)
			err := s.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestParseRejectsMalformedJSON(t *testing.T) {
	if _, err := Parse([]byte(`{"duration_sec": `)); err == nil {
		t.Fatal("expected parse error")
	}
}
