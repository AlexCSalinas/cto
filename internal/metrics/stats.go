package metrics

import "math"

// Stat is the mean and sample standard deviation of one metric across
// seeds.
type Stat struct {
	Mean   float64
	Stddev float64
}

// NewStat summarises a set of values; a single value has zero spread.
func NewStat(xs []float64) Stat {
	if len(xs) == 0 {
		return Stat{}
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	if len(xs) < 2 {
		return Stat{Mean: mean}
	}
	ss := 0.0
	for _, x := range xs {
		ss += (x - mean) * (x - mean)
	}
	return Stat{Mean: mean, Stddev: math.Sqrt(ss / float64(len(xs)-1))}
}

// KeyMetrics are the columns of the compare table, in display order.
var KeyMetrics = []string{
	"total_cost_usd", "work_per_dollar", "lost_work_sec",
	"mean_mem_utilization", "migrations_total", "total_downtime_sec",
}

// Value returns one of the KeyMetrics from a summary.
func (s Summary) Value(key string) float64 {
	switch key {
	case "total_cost_usd":
		return s.TotalCostUSD
	case "work_per_dollar":
		return s.WorkPerDollar
	case "lost_work_sec":
		return float64(s.LostWorkSec)
	case "mean_mem_utilization":
		return s.MeanMemUtilization
	case "migrations_total":
		return float64(s.MigrationsTotal)
	case "total_downtime_sec":
		return s.TotalDowntimeSec
	}
	return math.NaN()
}

// Aggregate computes each key metric's Stat over a set of runs.
func Aggregate(runs []Summary) map[string]Stat {
	out := map[string]Stat{}
	for _, k := range KeyMetrics {
		xs := make([]float64, 0, len(runs))
		for _, r := range runs {
			xs = append(xs, r.Value(k))
		}
		out[k] = NewStat(xs)
	}
	return out
}
