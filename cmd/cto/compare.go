package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/alexcsalinas/cto/internal/config"
	"github.com/alexcsalinas/cto/internal/controller"
	"github.com/alexcsalinas/cto/internal/metrics"
	"github.com/alexcsalinas/cto/internal/sim"
)

func cmdCompare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	scenario := fs.String("scenario", "scenarios/base.json", "scenario JSON file")
	names := fs.String("controllers", "naive,greedy", "comma-separated controller names; the first is the baseline")
	seeds := fs.String("seeds", "1,2,3,4,5", "comma-separated seeds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*scenario)
	if err != nil {
		return err
	}
	seedList, err := parseSeeds(*seeds)
	if err != nil {
		return err
	}
	ctlNames := strings.Split(*names, ",")
	results := map[string]map[string]metrics.Stat{}
	for _, name := range ctlNames {
		var runs []metrics.Summary
		for _, seed := range seedList {
			ctl, err := controller.New(name, cfg.Controller)
			if err != nil {
				return err
			}
			res, err := sim.Run(cfg, ctl, seed)
			if err != nil {
				return fmt.Errorf("%s seed %d: %w", name, seed, err)
			}
			runs = append(runs, res.Summary)
		}
		results[name] = metrics.Aggregate(runs)
	}
	printComparison(ctlNames, results)
	return nil
}

func parseSeeds(s string) ([]uint64, error) {
	var out []uint64
	for _, f := range strings.Split(s, ",") {
		n, err := strconv.ParseUint(strings.TrimSpace(f), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bad seed %q: %w", f, err)
		}
		out = append(out, n)
	}
	return out, nil
}

// printComparison prints mean ± stddev per controller and a verdict for
// each controller against the first (baseline) one.
func printComparison(names []string, results map[string]map[string]metrics.Stat) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "metric")
	for _, n := range names {
		fmt.Fprintf(tw, "\t%s", n)
	}
	fmt.Fprintln(tw)
	for _, k := range metrics.KeyMetrics {
		fmt.Fprint(tw, k)
		for _, n := range names {
			s := results[n][k]
			fmt.Fprintf(tw, "\t%s ± %s", fmtNum(s.Mean), fmtNum(s.Stddev))
		}
		fmt.Fprintln(tw)
	}
	tw.Flush()

	base := results[names[0]]
	for _, n := range names[1:] {
		r := results[n]
		fmt.Printf("%s: %.1fx work_per_dollar vs %s, %s lost work, %s cost\n",
			n, r["work_per_dollar"].Mean/base["work_per_dollar"].Mean, names[0],
			pctChange(base["lost_work_sec"].Mean, r["lost_work_sec"].Mean),
			pctChange(base["total_cost_usd"].Mean, r["total_cost_usd"].Mean))
	}
}

func fmtNum(x float64) string {
	if x != 0 && x < 10 {
		return strconv.FormatFloat(x, 'f', 3, 64)
	}
	return strconv.FormatFloat(x, 'f', 0, 64)
}

func pctChange(from, to float64) string {
	if from == 0 {
		return "n/a"
	}
	p := (to - from) / from * 100
	if p < 0 {
		return fmt.Sprintf("%.0f%% less", -p)
	}
	return fmt.Sprintf("%.0f%% more", p)
}
