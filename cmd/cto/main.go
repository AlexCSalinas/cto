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
	return fmt.Errorf("run: not implemented yet")
}

func cmdCompare(args []string) error {
	return fmt.Errorf("compare: not implemented yet")
}
