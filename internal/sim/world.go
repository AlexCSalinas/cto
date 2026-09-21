package sim

import (
	"fmt"

	"github.com/alexcsalinas/cto/internal/config"
	"github.com/alexcsalinas/cto/internal/controller"
	"github.com/alexcsalinas/cto/internal/fleet"
	"github.com/alexcsalinas/cto/internal/metrics"
	"github.com/alexcsalinas/cto/internal/workload"
)

// Result is what a run produces.
type Result struct {
	Summary metrics.Summary
	Series  []metrics.Sample
}

// World is the complete simulation state. Handlers mutate it; nothing else
// does.
type World struct {
	cfg     config.Scenario
	model   fleet.Model
	ctl     controller.Controller
	m       *metrics.Collector
	preempt *workload.Preemptor

	now     int64
	q       queue
	hosts   map[fleet.HostID]*fleet.Host
	boxes   map[fleet.BoxID]*fleet.Box
	hostSeq int
	migs    migrations
}

func newWorld(cfg config.Scenario, ctl controller.Controller, seed uint64) *World {
	return &World{
		cfg: cfg,
		model: fleet.Model{
			Box: fleet.Params{
				SleepGraceSec:   cfg.SleepGraceSec,
				WakeDelaySec:    cfg.WakeDelaySec,
				ResidentFloorGB: cfg.ResidentFloorGB,
				MemRampGBPerSec: cfg.MemRampGBPerSec,
			},
			Migration: fleet.MigrationParams{
				FixedDowntimeSec:     cfg.Migration.FixedDowntimeSec,
				DowntimeFraction:     cfg.Migration.DowntimeFraction,
				MaxConcurrentPerHost: cfg.Migration.MaxConcurrentPerHost,
			},
		},
		ctl:     ctl,
		m:       metrics.New(ctl.Name(), seed),
		preempt: workload.NewPreemptor(cfg, seed),
		hosts:   map[fleet.HostID]*fleet.Host{},
		boxes:   map[fleet.BoxID]*fleet.Box{},
		migs:    newMigrations(),
	}
}

// Run simulates one scenario with one controller and one seed. It never
// prints; the caller decides how to present the Result.
func Run(cfg config.Scenario, ctl controller.Controller, seed uint64) (Result, error) {
	if err := cfg.Validate(); err != nil {
		return Result{}, err
	}
	w := newWorld(cfg, ctl, seed)
	for _, b := range workload.Generate(cfg, seed) {
		w.boxes[b.ID] = b
		w.q.push(&BoxArrive{timing: at(b.ArriveAt), Box: b.ID})
	}
	// The initial fleet is up at t=0 so every controller starts from the
	// same capacity.
	for _, typ := range cfg.SortedHostTypes() {
		for i := 0; i < cfg.InitialFleet[typ]; i++ {
			if !w.launchHost(typ, 0) {
				return Result{}, fmt.Errorf("initial fleet exceeds max_hosts")
			}
		}
	}
	w.q.push(&CheckpointTick{at(cfg.CheckpointIntervalSec)})
	w.q.push(&ControllerTick{at(cfg.Controller.TickIntervalSec)})
	w.q.push(&MetricsSample{at(cfg.SampleIntervalSec)})
	w.q.push(&SimEnd{at(cfg.DurationSec)})
	ctl.Init(w.view())
	w.loop()
	return w.result(), nil
}

// loop drains the queue until SimEnd.
func (w *World) loop() {
	for w.q.len() > 0 {
		ev := w.q.pop()
		w.now = ev.At()
		if w.dispatch(ev) {
			return
		}
	}
}

// dispatch routes one event to its handler and reports whether the run is
// over.
func (w *World) dispatch(ev Event) bool {
	switch e := ev.(type) {
	case *BoxArrive:
		w.handleBoxArrive(e)
	case *PhaseEnd:
		w.handlePhaseEnd(e)
	case *BoxSleep:
		w.handleBoxSleep(e)
	case *BoxWake:
		w.handleBoxWake(e)
	case *MigrationStop:
		w.handleMigrationStop(e)
	case *MigrationDone:
		w.handleMigrationDone(e)
	case *CheckpointTick:
		w.handleCheckpointTick()
	case *ReclaimWarning:
		w.handleReclaimWarning(e)
	case *HostDead:
		w.handleHostDead(e)
	case *HostBooted:
		w.handleHostBooted(e)
	case *ControllerTick:
		w.handleControllerTick()
	case *MetricsSample:
		w.handleMetricsSample()
	case *SimEnd:
		w.handleSimEnd()
		return true
	}
	return false
}

// sortedHosts returns hosts by ID; every loop over the fleet uses it so map
// order never leaks into results.
func (w *World) sortedHosts() []*fleet.Host {
	out := make([]*fleet.Host, 0, len(w.hosts))
	for _, h := range w.hosts {
		out = append(out, h)
	}
	sortHosts(out)
	return out
}

func (w *World) result() Result {
	work := int64(0)
	for _, b := range w.boxes {
		work += b.WorkDoneSec
	}
	return Result{Summary: w.m.Summary(work), Series: w.m.Series()}
}
