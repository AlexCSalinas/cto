# CTO design notes

Everything here is either an assumption the spec left open, an equation the
code implements, or a rule that keeps runs byte-for-byte reproducible.

## 1. Simulation core

- Time is `int64` seconds from t=0. Sub-second migration durations are
  rounded: the pre-copy (box still running) is `floor`ed and the total is
  `ceil`ed, so recorded downtime is a whole number of seconds and never
  shorter than the model says.
- Events are ordered by `(At, Seq)`. `Seq` is assigned when the event is
  pushed, so two events at the same time fire in the order they were
  scheduled. This is the only tie-break rule the queue needs.
- Box state is integrated lazily (`Box.Advance`) from `LastUpdateAt` to now
  whenever an event touches the box, a view is built, or a metrics sample
  is taken. Nothing ticks every second.
- Stale timers are dropped with an epoch: every box carries `Epoch`, bumped
  whenever its timeline is rewritten (placement, migration downtime, loss).
  `PhaseEnd`, `BoxSleep` and `BoxWake` events carry the epoch they were
  scheduled under and are ignored if it no longer matches. Migration events
  are validated against the set of active migrations instead.
- Every loop over hosts or boxes runs in sorted ID order. This is not only
  about controller fairness: float sums over Go map iteration made two
  identical runs differ in the fourth decimal of the utilization series.
- Host IDs are `<type>-<n>` with a run-wide launch counter; box IDs are
  `box-<n>` zero-padded so lexical order is arrival order.

## 2. Workload

- Two independent PCG streams derived from the seed: `(seed, 1)` for the box
  trace, `(seed, 2)` for host lifetimes. The box trace is therefore
  identical across controllers, and the k-th launched preemptible host
  always receives the k-th lifetime draw. Different controllers launch hosts
  in different orders, so their preemption times differ; this is inherent.
- Arrivals are a Poisson process, piecewise-constant when `rate_schedule`
  is given (the `bursty` scenario). Restarting the exponential clock at each
  segment boundary is exact because the process is memoryless.
- Phases strictly alternate `Active`, `Waiting`, `Active`, ... starting with
  `Active`. Burst lengths are sampled from `active_burst_sec`. Wait lengths
  are sampled from `wait_sec` and multiplied by
  `E[burst]·(1−active_frac) / (active_frac·E[wait])` so the expected active
  fraction over a box's life equals `active_frac`; otherwise the two config
  fields would contradict each other.
- The last phase is truncated so phases sum to the sampled lifetime.
- Each Active phase samples its own memory target (`mem_frac_of_req · req`)
  and CPU (`cpu_frac_of_req · req_cpu`, a field added to the spec). A
  Waiting phase keeps the preceding target: memory stays resident until the
  box is paged out.

## 3. Box dynamics

Per second of `Active` state (awake on a host), with phase `p`:

    ObsMemGB   → ramps toward p.MemGB by at most mem_ramp_gb_per_sec
    ObsCPU     = p.CPU
    DirtyGB   += p.DirtyGBPerSec       (Active phases only, capped at ObsMemGB)
    WorkDone  += 1                     (Active phases only)

- Sleep happens `sleep_grace_sec` into a Waiting phase (never if the wait is
  shorter than the grace). Sleeping drops observed memory to
  `resident_floor_gb` and CPU to 0 **and counts as a checkpoint**: paging
  out writes the full image to durable storage, so `DirtyGB = 0`. This is
  the mechanism behind "sleeping boxes migrate cheaply".
- Waking takes `wake_delay_sec`, during which the box is still `Sleeping`
  and the next phase's clock does not run. Memory ramps back up from the
  floor.
- The phase clock (`PhaseLeftSec`) pauses during migration downtime, so a
  migrated box still completes all of its work; downtime shows up as
  elapsed time and in `total_downtime_sec`, not as lost work.

## 4. Migration

    transfer_gb  = DirtyGB + resident_floor_gb
    bw           = min(from.NetGBps, to.NetGBps) / max_concurrent_per_host
    transfer_sec = transfer_gb / bw
    downtime_sec = fixed_downtime_sec + transfer_sec · downtime_fraction
    run_sec      = transfer_sec · (1 − downtime_fraction)   box keeps running
    total_sec    = run_sec + downtime_sec

**Shared bandwidth.** Each host has `max_concurrent_per_host` migration
slots and every slot is given a fixed `1/max_concurrent` share of the NIC.
A migration needs a slot on both hosts; otherwise it waits in a queue that
is scanned in submission order (a blocked request never delays the ones
behind it). The fixed share is pessimistic when a host runs a single
migration, but it means a controller's `EstimateMigrationSec` is exactly
what the simulator will do, which is what makes deadline-aware evacuation
plans trustworthy.

- After a successful migration the box is checkpointed (`DirtyGB = 0`).
- If the source dies first, the box is Lost. If the destination dies first,
  or receives a reclaim warning, the migration is aborted and the box
  resumes on its source (it never stopped running during pre-copy; if it
  was in downtime it is unpaused).
- A box that finishes its last phase during pre-copy cancels its migration.
- Migrations may target a `Booting` host; they start once it is `Running`.

## 5. Checkpoints and loss

- `CheckpointTick` every `checkpoint_interval_sec` snapshots every awake
  box: `DirtyGB = 0` and the checkpoint records `(phase index, phase time
  left, work done)`. Checkpoint traffic is counted in `checkpoint_bytes_gb`
  but does not compete with migrations for bandwidth (simplification).
- When a host dies, every box still on it loses `WorkDoneSec −
  work_at_checkpoint` (added to `LostWorkSec`, subtracted from
  `WorkDoneSec`) and is re-queued `Pending` **resuming from the checkpoint**
  rather than restarting the phase from scratch. This keeps the "lost work"
  accounting exact: the box re-executes precisely what was lost. `Lost` is
  therefore an event, not a lasting state.
- Its memory is restored on the new host from zero and ramps up again.

## 6. Hosts and billing

- The initial fleet is `Running` at t=0 with no boot delay, so every
  controller starts from identical capacity. Launched hosts boot for
  `host_boot_sec` and are billed from launch.
- Preemption lifetime is drawn at launch from `Exp(preempt_rate_per_hr)`.
  `ReclaimWarning` fires at `max(launch, death − reclaim_warn_sec)`.
- A host in `Draining` (reclaimed or being retired by the controller) is
  terminated, and billing stops, as soon as it has no boxes and no incoming
  migrations. A reclaimed host that is fully evacuated is therefore not paid
  for until its deadline.
- The controller hears about every reclaim, including of an empty host.
- Host cost is `alive_seconds · price_per_hr / 3600` over
  `Booting | Running | Draining`.
- The simulator does not enforce a physical memory limit after placement.
  A placement is refused only if the host is not `Running` or observed
  memory would exceed capacity at that instant. Hosts whose observed memory
  later exceeds capacity are counted in `overcommit_host_samples` so a
  controller that over-packs is visible in the report.
- Controller decisions the simulator cannot honour (unknown host, host not
  running, box not movable) are silently dropped; the box stays `Pending`
  or in place.
- `ControllerTick` also runs whenever a host finishes booting, so a
  replacement launched during a reclaim warning can be used before the
  deadline. Pending boxes are retried on every tick and boot.

## 7. Controllers

**naive**: first-fit by requested memory and CPU over hosts in ID order,
counting boxes already heading to a host. Never rebalances. On reclaim it
moves boxes in ID order to the first host with requested room and always
launches one replacement of the same type.

**greedy** (see README for the plain-English version):

- `demand(box) = observed + headroom_frac · requested` for memory and CPU.
  Sleeping boxes contribute their resident floor. A box that has never run
  (observed = 0) uses `ratio[archetype] · requested` instead of observed,
  where `ratio` is the mean observed/requested of awake boxes of that
  archetype, refreshed every tick. Without this, every new host became hot
  within minutes.
- Placement is best-fit (least room left after placement) among `Running`
  hosts; ties go to the cheaper host, then the lower ID.
- Tick order: learn ratios → launch decision → stranded boxes on draining
  hosts → one relief migration per hot host → at most one cold drain.
- Launch decision, one per tick and only when nothing is booting: if a
  pending box fits nowhere, or if total room across running hosts is below
  the **evacuation reserve** (the summed demand of the fullest running
  preemptible host). Launched type is the cheapest per GB that can hold the
  box. The reserve exists because a reclaim warning is shorter than a boot.
- Hot host (`observed > hot_threshold · capacity`): move the box with the
  highest `observed_gb / EstimateMigrationSec` to the running host with the
  most room. With a 2 s fixed downtime a large box with modest dirty pages
  often beats a small clean one; that is intended.
- Cold host (`observed < cold_threshold · capacity`): drain it and mark it
  for shutdown if all its boxes fit elsewhere, nothing is pending or
  booting, no other host is already being retired, it has no migrations in
  flight or planned this tick, it has at most `max_concurrent_per_host`
  boxes, and removing it leaves room ≥ reserve (a host removal costs exactly
  its capacity in room).
- Reclaim: candidates sorted by `work_since_checkpoint / EstimateMigrationSec`
  (source NIC on both ends as the proxy), ties by ID. Boxes are assigned to
  the earliest-free source slot; a box whose finish time exceeds the deadline
  is skipped so it cannot delay one that would make it. Destination slot
  contention is ignored in the estimate. If any box had no destination at
  all, one replacement of the source's type is launched.
- Migration budget per tick: `max_concurrent_per_host − in_flight` per host.

**lp**: embeds greedy; the intended ILP is written out in `lp.go`.

## 8. Metrics

- `mean_mem_utilization` is the mean over samples of
  `Σ observed / Σ capacity` across `Running | Draining` hosts.
- `p50/p95_downtime_sec` use nearest-rank on the per-migration downtimes.
- `pending_box_seconds` counts from arrival (or loss) to placement, plus
  any remaining wait at the end of the run.
- `work_per_dollar = Σ WorkDoneSec / total_cost_usd`, with lost work
  already subtracted.

## 9. Known limitations

- Single region, no network topology, no disk or KV-cache model.
- Preemptions are independent per host; real spot markets reclaim in
  correlated waves.
- Bandwidth is slot-based, not truly shared; checkpoints do not consume it.
- No page-fault model: memory ramps at a constant rate.
- Migration destinations are validated by observed memory only.
- Controllers cannot name a host they have just asked to launch.
- The project is about 3,000 lines of non-test Go plus 1,000 lines of tests,
  somewhat above the 2–3k target; config validation and the two controllers
  are where the bulk is.

## 10. Calibration against real Sailboxes (2026-09-22)

Three Sailboxes (app `cto`) ran `scripts/sail/workload.py` for 8 h; the
platform's own metrics and phase logs are in `scenarios/traces/`. Total
spend for the exercise was $1.00. What was measured, and what it changes:

- **Elastic memory is real and hot-plugged.** A size-s box boots with
  1.9 GiB of guest RAM under a 16 GiB cap and grows on demand (the build
  box reached 16.9 GiB guest MemTotal). The platform reports used vs
  requested exactly as the model assumes. Growth lags allocation: the
  deep-research box was OOM-killed by the guest kernel 62 s in when it
  allocated 1.93 GiB against 1.93 GiB of MemTotal. The simulator's bounded
  ramp (`mem_ramp_gb_per_sec`) is the right shape, but it does not model
  the failure mode when a box outruns it. The workload now grows in
  256 MiB steps.
- **Ramp rate.** At 60 s resolution (the finest the 6h metrics window
  gives) a 1.7–1.9 GiB allocation completes within one sample, so the real
  ramp is at least 0.03 GB/s and consistent with the 0.5 GB/s default; the
  data cannot pin it tighter.
- **Resident floor.** After the workload exited, used memory settled at
  0.12–0.13 GiB (size s and m alike); the model's 0.25 GB floor is
  conservative by 2×.
- **CPU tracks phases one-for-one.** Per-sample `cpu_used_vcpu` equalled
  the logged active fraction of that interval (0.98–0.99 vCPU during
  bursts, 0.00 during waits). The measured active fractions were 0.45
  (coding, target 0.40) and 0.81 (build, target 0.80), which validates the
  wait-scaling arithmetic, not the archetypes themselves: the workload is
  synthetic.
- **Autosleep did not engage during waits, and the reason matters.** Sail
  defines idle as no CPU use, no process waiting on a timer, and no open
  connection other than an outbound request awaiting its reply. The first
  run's waits were `time.sleep()`, a timer wait, so the boxes stayed awake
  and were billed for resident memory through every wait (both boxes were
  sampled continuously; used memory stayed flat at the burst's peak). A
  real agent blocked on an inference HTTP request is explicitly allowed to
  sleep, which is the case the simulator models. The workload's waits are
  now wall-clock alarms by default. A 180 s alarm wait fired on time but the
  box was still awake 105 s in and slept only after the process exited:
  inconclusive, since an empty box also needed ~2 min to be seen asleep.
  A wait blocked on a real outbound HTTP request is the test that would
  settle it.
- **Sleeping is not billed, and the empty box slept in ~2 min** (the smoke
  box before its workload started; the default idle timeout is 30 s).
- **Sampling.** `metrics?range=24h` returns 15-minute peaks; `6h` returns
  1-minute samples. The spend endpoint settles per box on termination.
- **Not observable from outside:** live migrations and host preemption.
  Sail states migrations happen a few times a day and are invisible to the
  agent; nothing in the metrics or lifecycle API exposes them, so the
  lost-work side of the model remains unvalidated.
