# CTO — Chief Token Officer

CTO is a discrete-event simulator for scheduling long-running AI agent
sandboxes ("boxes") across a fleet of cheap, preemptible hosts. Agent
sandboxes are VMs that live for hours, spend most of that time asleep
waiting on an inference call, and can be live-migrated. Spot hosts are
half the price of on-demand but disappear with two minutes' notice. The
money in agent infrastructure is made or lost at the fleet level: how
tightly you pack observed (not requested) usage, how little you pay for the
capacity that is idle, and how much agent progress you throw away when a
host is reclaimed. CTO exists to compare scheduling controllers fairly on
exactly that trade-off, under identical seeded workloads, with one number
to argue about: **useful work-seconds per dollar**.

```
                         ┌──────────────────────────────────────────┐
   seeded workload ───▶  │  event queue  (container/heap, (At,Seq)) │
   boxes + phases        │  BoxArrive PhaseEnd BoxSleep BoxWake     │
   host lifetimes        │  MigrationStop/Done CheckpointTick       │
                         │  ReclaimWarning HostDead HostBooted      │
                         │  ControllerTick MetricsSample SimEnd     │
                         └───────────────┬──────────────────────────┘
                                         │ pop, w.now = At
                                         ▼
                         ┌──────────────────────────────────────────┐
                         │  World: hosts, boxes, migrations, clock  │
                         │  lazy integration of memory/dirty/work   │
                         └───────┬───────────────────────▲──────────┘
                    read-only    │                        │ Plan /
                    FleetView    │                        │ EvacuationPlan /
                    (copies)     ▼                        │ HostID
                         ┌──────────────────────────────────────────┐
                         │  Controller: Place · Tick · OnReclaim    │
                         │  naive | greedy | lp (stub)              │
                         └──────────────────────────────────────────┘
```

The controller only ever sees snapshots and answers with plans. The
simulator decides what actually happens, validates every decision, and
records the metrics.

## Quickstart

```
make            # go build ./...
make test
make run        # one run: greedy on scenarios/base.json, seed 42
make compare    # naive vs greedy, 5 seeds, on every scenario

go run ./cmd/cto run      -scenario scenarios/base.json -controller greedy -seed 42 -out summary.json -out-series series.csv
go run ./cmd/cto compare  -scenario scenarios/high-preemption.json -controllers naive,greedy,lp -seeds 1,2,3
go run ./cmd/cto validate -scenario scenarios/bursty.json
```

Go 1.22+, standard library only. Same scenario + same seed ⇒ byte-identical
output.

## The model

- **Boxes** arrive as a Poisson process and belong to an archetype
  (`coding-agent`, `deep-research`, `build-heavy`) that fixes their
  lifetime, requested memory, and a trace of alternating *Active* and
  *WaitingOnInference* phases generated up front.
- Observed memory ramps toward each phase's target at a bounded rate
  (0.5 GB/s); only Active phases dirty memory and count as useful work.
- A box auto-sleeps 5 s into a wait: CPU drops to 0, memory to a 0.25 GB
  resident floor, and paging out doubles as a checkpoint. Waking takes 2 s.
- Every 600 s each awake box checkpoints in the background (dirty pages → 0).
- **Hosts** are spot capacity with a per-hour preemption hazard. A reclaim
  gives 120 s (60 s in `high-preemption`) of warning, then the host dies.
  Boots take 60 s. Billing is per second while alive.
- **Migration** cost depends on dirty pages since the last checkpoint:

      transfer_gb  = DirtyGB + resident_floor
      transfer_sec = transfer_gb / (min(NetA, NetB) / max_concurrent_per_host)
      downtime_sec = fixed_downtime (2 s) + transfer_sec × downtime_fraction (0.2)

  The box keeps running for the rest of the copy. Each host has two
  migration slots; a migration needs one on each end.
- A box on a host that dies loses the work since its last checkpoint and is
  re-queued to resume from that checkpoint.
- **Objective:** `work_per_dollar = Σ useful Active seconds / Σ host cost`.
  In a real system the numerator is tokens or completed tasks per dollar.

## Results (`make compare`, `scenarios/base.json`, seeds 1–5)

```
metric                naive          greedy
total_cost_usd        423 ± 0.146    804 ± 44
work_per_dollar       8542 ± 54      14221 ± 632
lost_work_sec         9303 ± 3288    93 ± 186
mean_mem_utilization  0.262 ± 0.015  0.403 ± 0.014
migrations_total      6.600 ± 6.877  667 ± 138
total_downtime_sec    21 ± 22        2041 ± 411
greedy: 1.7x work_per_dollar vs naive, 99% less lost work, 90% more cost
```

Naive spends less in absolute terms only because it never grows the fleet:
it packs by *requested* memory, so its eight hosts are "full" at 26%
observed utilization and most boxes queue for hours. Greedy runs about
three times the work for less than twice the money. On
`high-preemption.json` (0.25 reclaims per host-hour, 60 s warning) greedy
gets 2.0× work per dollar with 82% less lost work; on `bursty.json` 1.5×
and 98% less.

## What greedy does

1. **Placement** is best-fit on *observed* memory plus a 20% headroom on
   the request, so hosts fill tightly and empties can be drained. A box
   that has never run is sized by the mean observed/requested ratio of its
   archetype, learned from the fleet.
2. **Every 60 s** it moves one box off any host above 90% observed memory,
   choosing the box that frees the most GB per second of migration and
   sending it to the host with the most room.
3. It **drains** a host below 25% when its boxes all fit elsewhere, and
   shuts it down once empty.
4. It **keeps an evacuation reserve**: enough free room across the fleet to
   re-home the fullest spot host, launching ahead of time because a
   reclaim warning is shorter than a boot.
5. On a **reclaim warning** it orders boxes by uncheckpointed work per
   second of migration, packs them onto the host's migration slots, and
   skips any box that cannot finish before the deadline so it does not
   delay the ones that can.

## What the LP version would do

`lp` currently behaves like greedy; `internal/controller/lp.go` holds the
formulation for the rebalance step, solved once per tick:

    minimise   Σ_h y[h]·price[h]·T  +  λ·Σ_b m[b]·(DirtyGB[b] + floor)/bw[b]
    subject to Σ_h x[b][h] = 1                       every live box on one host
               Σ_b x[b][h]·demand[b] ≤ y[h]·cap[h]   observed + headroom fits
               x[b][h] ≤ y[h]                        no box on a dead host
               m[b] ≥ x[b][h]  for h ≠ current(b)    a move is a migration
               per-host migration slots

with `x[b][h]`, `y[h]`, `m[b]` binary. Greedy is the one-move-per-tick
heuristic for this; the ILP would decide consolidation and relief moves
jointly and price migrations against host cost explicitly.

## Limitations

- Single region, no network topology, no disk or KV-cache model.
- Preemptions are independent per host; real spot reclaims come in
  correlated waves, which would make the evacuation reserve less effective.
- Bandwidth is modelled as fixed per-slot shares; background checkpoints do
  not compete with migrations.
- No page-fault modelling: memory ramps linearly, and a box that outgrows
  its host is only reported (`overcommit_host_samples`), not OOM-killed.
- The simulator trusts the controller to keep boxes off draining hosts; a
  controller can strand boxes.
- Controllers cannot address a host they have just asked to launch.

See `DESIGN.md` for every assumption, equation and tie-break rule.
