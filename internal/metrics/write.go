package metrics

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"text/tabwriter"
)

// WriteSummaryJSON writes the summary as indented JSON.
func WriteSummaryJSON(w io.Writer, s Summary) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// WriteSeriesCSV writes the utilization time series.
func WriteSeriesCSV(w io.Writer, series []Sample) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"t", "running_hosts", "mem_util", "cpu_util", "cost_so_far"}); err != nil {
		return err
	}
	for _, s := range series {
		row := []string{
			strconv.FormatInt(s.T, 10),
			strconv.Itoa(s.RunningHosts),
			strconv.FormatFloat(s.MemUtil, 'f', 4, 64),
			strconv.FormatFloat(s.CPUUtil, 'f', 4, 64),
			strconv.FormatFloat(s.CostSoFar, 'f', 4, 64),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// WriteTable prints a human-readable summary.
func WriteTable(w io.Writer, s Summary) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	row := func(k string, v any) { fmt.Fprintf(tw, "%s\t%v\n", k, v) }
	row("controller", s.Controller)
	row("seed", s.Seed)
	row("total_cost_usd", fmt.Sprintf("%.2f", s.TotalCostUSD))
	types := make([]string, 0, len(s.HostHours))
	for t := range s.HostHours {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		row("host_hours["+t+"]", fmt.Sprintf("%.1f", s.HostHours[t]))
	}
	row("work_done_sec", s.WorkDoneSec)
	row("lost_work_sec", s.LostWorkSec)
	row("work_per_dollar", fmt.Sprintf("%.0f", s.WorkPerDollar))
	row("boxes_arrived", s.BoxesArrived)
	row("boxes_completed", s.BoxesCompleted)
	row("boxes_lost_events", s.BoxesLostEvents)
	row("hosts_launched", s.HostsLaunched)
	row("hosts_preempted", s.HostsPreempted)
	row("migrations_total", s.MigrationsTotal)
	row("migrations_for_reclaim", s.MigrationsForReclaim)
	row("migrations_for_rebalance", s.MigrationsForRebal)
	row("migration_bytes_gb", fmt.Sprintf("%.1f", s.MigrationBytesGB))
	row("total_downtime_sec", fmt.Sprintf("%.0f", s.TotalDowntimeSec))
	row("p50/p95_downtime_sec", fmt.Sprintf("%.0f / %.0f", s.P50DowntimeSec, s.P95DowntimeSec))
	row("checkpoint_bytes_gb", fmt.Sprintf("%.0f", s.CheckpointBytesGB))
	row("mean_mem_utilization", fmt.Sprintf("%.3f", s.MeanMemUtilization))
	row("mean_cpu_utilization", fmt.Sprintf("%.3f", s.MeanCPUUtilization))
	row("overcommit_host_samples", s.OvercommitHostSamples)
	row("pending_box_seconds", s.PendingBoxSeconds)
	return tw.Flush()
}
