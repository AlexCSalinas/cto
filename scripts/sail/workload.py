#!/usr/bin/env python3
"""Synthetic agent workload for calibrating CTO against real Sailboxes.

Runs inside a Sailbox. Alternates Active bursts (allocate memory, keep
touching it, burn CPU) with Waiting phases (hold memory, sit idle) using the
same archetype shape as scenarios/base.json, scaled to a small box. The
point is not the work itself but what the platform observes: how memory
ramps and is billed, whether autosleep kicks in during waits, and how
quickly the box resumes. Phase transitions are logged as JSON lines so they
can be lined up with the platform's metrics afterwards.
"""
import argparse
import json
import random
import sys
import time

CHUNK = 64 << 20  # 64 MiB

# Memory targets are in GiB and deliberately small; the ratios and phase
# timings mirror the simulator's archetypes.
ARCHETYPES = {
    "coding-agent": dict(mem=(1.5, 5.0), active_frac=0.4, burst=(60, 900), wait=(20, 300), touch_frac=0.5),
    "deep-research": dict(mem=(0.5, 2.0), active_frac=0.15, burst=(30, 300), wait=(120, 1800), touch_frac=0.2),
    "build-heavy": dict(mem=(6.0, 14.0), active_frac=0.8, burst=(600, 3600), wait=(10, 60), touch_frac=0.8),
}


def log(**fields):
    fields["t"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    fields["meminfo_total_gib"] = meminfo("MemTotal")
    fields["meminfo_avail_gib"] = meminfo("MemAvailable")
    print(json.dumps(fields), flush=True)


def meminfo(key):
    try:
        with open("/proc/meminfo") as f:
            for line in f:
                if line.startswith(key + ":"):
                    return round(int(line.split()[1]) / (1 << 20), 3)
    except OSError:
        pass
    return None


def resize(chunks, target_gib):
    """Grow or shrink the resident set to target_gib, touching new pages."""
    want = int(target_gib * (1 << 30) / CHUNK)
    while len(chunks) > want:
        chunks.pop()
    while len(chunks) < want:
        c = bytearray(CHUNK)
        c[::4096] = b"x" * len(c[::4096])  # fault every page in
        chunks.append(c)


def burst(chunks, seconds, touch_frac):
    """Keep the CPU busy and rewrite touch_frac of the resident set per
    minute, which is what makes pages dirty for a checkpoint."""
    end = time.time() + seconds
    i = 0
    per_sec = max(1, int(len(chunks) * touch_frac / 60))
    while time.time() < end:
        for _ in range(per_sec):
            c = chunks[i % len(chunks)]
            c[::4096] = bytes([i & 0xFF]) * len(c[::4096])
            i += 1
        # Cheap CPU burn so cpu_used_vcpu is visibly nonzero.
        x = 0
        for k in range(200_000):
            x ^= k * k
    return i


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--archetype", required=True, choices=sorted(ARCHETYPES))
    ap.add_argument("--hours", type=float, default=6)
    ap.add_argument("--seed", type=int, default=1)
    args = ap.parse_args()
    a = ARCHETYPES[args.archetype]
    rng = random.Random(args.seed)
    # Scale waits so the expected active fraction matches, as the simulator does.
    mean_burst = sum(a["burst"]) / 2
    mean_wait = sum(a["wait"]) / 2
    wait_scale = mean_burst * (1 - a["active_frac"]) / a["active_frac"] / mean_wait

    chunks = []
    deadline = time.time() + args.hours * 3600
    log(event="start", archetype=args.archetype, hours=args.hours, wait_scale=round(wait_scale, 2))
    while time.time() < deadline:
        target = rng.uniform(*a["mem"])
        dur = rng.uniform(*a["burst"])
        log(event="active", target_gib=round(target, 2), dur_s=int(dur))
        resize(chunks, target)
        burst(chunks, dur, a["touch_frac"])
        dur = rng.uniform(*a["wait"]) * wait_scale
        log(event="wait", dur_s=int(dur), held_gib=round(len(chunks) * CHUNK / (1 << 30), 2))
        time.sleep(dur)
    log(event="done")


if __name__ == "__main__":
    sys.exit(main())
