package metrics

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSummaryDerivedFields(t *testing.T) {
	c := New("test", 7)
	c.HostBilled("spot-m", 3600, 0.9)
	c.HostBilled("spot-m", 1800, 0.9)
	c.HostBilled("spot-l", 3600, 1.7)
	c.Migrated(ReasonReclaim, 4, 3)
	c.Migrated(ReasonRebalance, 1, 2)
	c.Migrated(ReasonRebalance, 1, 10)
	c.Lost(120)
	c.Sampled(Sample{T: 60, MemUtil: 0.5, CPUUtil: 0.2}, 0)
	c.Sampled(Sample{T: 120, MemUtil: 0.7, CPUUtil: 0.4}, 1)
	s := c.Summary(9000)

	if want := 1.5*0.9 + 1.7; abs(s.TotalCostUSD-want) > 1e-9 {
		t.Errorf("TotalCostUSD = %v, want %v", s.TotalCostUSD, want)
	}
	if s.HostHours["spot-m"] != 1.5 || s.HostHours["spot-l"] != 1 {
		t.Errorf("HostHours = %v", s.HostHours)
	}
	if abs(s.WorkPerDollar-9000/s.TotalCostUSD) > 1e-9 {
		t.Errorf("WorkPerDollar = %v", s.WorkPerDollar)
	}
	if s.MigrationsTotal != 3 || s.MigrationsForReclaim != 1 || s.MigrationsForRebal != 2 || s.MigrationBytesGB != 6 {
		t.Errorf("migration counts wrong: %+v", s)
	}
	if s.TotalDowntimeSec != 15 || s.P50DowntimeSec != 3 || s.P95DowntimeSec != 10 {
		t.Errorf("downtime stats wrong: total=%v p50=%v p95=%v", s.TotalDowntimeSec, s.P50DowntimeSec, s.P95DowntimeSec)
	}
	if s.LostWorkSec != 120 || s.BoxesLostEvents != 1 {
		t.Errorf("loss stats wrong: %+v", s)
	}
	if abs(s.MeanMemUtilization-0.6) > 1e-9 || abs(s.MeanCPUUtilization-0.3) > 1e-9 || s.OvercommitHostSamples != 1 {
		t.Errorf("utilization stats wrong: %+v", s)
	}
}

func TestPercentile(t *testing.T) {
	tests := []struct {
		xs   []float64
		p    float64
		want float64
	}{
		{nil, 0.5, 0},
		{[]float64{5}, 0.95, 5},
		{[]float64{3, 1, 2}, 0.5, 2},
		{[]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 0.95, 10},
		{[]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 0.5, 5},
	}
	for _, tc := range tests {
		if got := percentile(tc.xs, tc.p); got != tc.want {
			t.Errorf("percentile(%v, %v) = %v, want %v", tc.xs, tc.p, got, tc.want)
		}
	}
}

func TestWriters(t *testing.T) {
	c := New("naive", 1)
	c.HostBilled("spot-m", 3600, 1)
	c.Sampled(Sample{T: 60, RunningHosts: 2, MemUtil: 0.25, CPUUtil: 0.5, CostSoFar: 0.1}, 0)
	s := c.Summary(100)

	var buf bytes.Buffer
	if err := WriteSummaryJSON(&buf, s); err != nil {
		t.Fatal(err)
	}
	var back Summary
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil || back.WorkPerDollar != 100 {
		t.Fatalf("JSON round trip failed: %v %+v", err, back)
	}

	buf.Reset()
	if err := WriteSeriesCSV(&buf, c.Series()); err != nil {
		t.Fatal(err)
	}
	if want := "t,running_hosts,mem_util,cpu_util,cost_so_far\n60,2,0.2500,0.5000,0.1000\n"; buf.String() != want {
		t.Fatalf("CSV = %q, want %q", buf.String(), want)
	}

	buf.Reset()
	if err := WriteTable(&buf, s); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "work_per_dollar") || !strings.Contains(buf.String(), "host_hours[spot-m]") {
		t.Fatalf("table missing rows:\n%s", buf.String())
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestStats(t *testing.T) {
	if s := NewStat(nil); s.Mean != 0 || s.Stddev != 0 {
		t.Errorf("empty stat = %+v", s)
	}
	if s := NewStat([]float64{4}); s.Mean != 4 || s.Stddev != 0 {
		t.Errorf("single stat = %+v", s)
	}
	s := NewStat([]float64{2, 4, 4, 4, 5, 5, 7, 9})
	if s.Mean != 5 || abs(s.Stddev-2.138089935) > 1e-6 {
		t.Errorf("stat = %+v, want mean 5 sample stddev 2.138", s)
	}
	agg := Aggregate([]Summary{{TotalCostUSD: 10, LostWorkSec: 3}, {TotalCostUSD: 20, LostWorkSec: 5}})
	if agg["total_cost_usd"].Mean != 15 || agg["lost_work_sec"].Mean != 4 {
		t.Errorf("aggregate = %+v", agg)
	}
}
