// Command cto runs the CTO fleet-scheduling simulator.
//
//	cto run      -scenario scenarios/base.json -controller greedy -seed 42 [-out summary.json] [-out-series series.csv]
//	cto compare  -scenario scenarios/base.json -controllers naive,greedy -seeds 1,2,3,4,5
//	cto validate -scenario scenarios/base.json
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/alexcsalinas/cto/internal/config"
	"github.com/alexcsalinas/cto/internal/controller"
	"github.com/alexcsalinas/cto/internal/metrics"
	"github.com/alexcsalinas/cto/internal/sim"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "validate":
		err = cmdValidate(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	case "compare":
		err = cmdCompare(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cto <run|compare|validate> [flags]")
}

func cmdValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	scenario := fs.String("scenario", "scenarios/base.json", "scenario JSON file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := config.Load(*scenario)
	if err != nil {
		return err
	}
	hosts := 0
	for _, n := range s.InitialFleet {
		hosts += n
	}
	fmt.Printf("%s: ok (%d host types, %d initial hosts, %d archetypes, %.1fh)\n",
		*scenario, len(s.HostTypes), hosts, len(s.Archetypes), float64(s.DurationSec)/3600)
	return nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	scenario := fs.String("scenario", "scenarios/base.json", "scenario JSON file")
	name := fs.String("controller", "greedy", "controller name: "+fmt.Sprint(controller.Names()))
	seed := fs.Uint64("seed", 0, "RNG seed (0 = scenario's seed_default)")
	out := fs.String("out", "", "write summary JSON to this file")
	outSeries := fs.String("out-series", "", "write utilization time series CSV to this file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*scenario)
	if err != nil {
		return err
	}
	if *seed == 0 {
		*seed = cfg.SeedDefault
	}
	ctl, err := controller.New(*name, cfg.Controller)
	if err != nil {
		return err
	}
	res, err := sim.Run(cfg, ctl, *seed)
	if err != nil {
		return err
	}
	if err := metrics.WriteTable(os.Stdout, res.Summary); err != nil {
		return err
	}
	if *out != "" {
		if err := writeFile(*out, func(f *os.File) error { return metrics.WriteSummaryJSON(f, res.Summary) }); err != nil {
			return err
		}
	}
	if *outSeries != "" {
		if err := writeFile(*outSeries, func(f *os.File) error { return metrics.WriteSeriesCSV(f, res.Series) }); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, write func(*os.File) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func cmdCompare(args []string) error {
	return fmt.Errorf("compare: not implemented yet")
}
