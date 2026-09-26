#!/usr/bin/env python3
"""Render the README and docs charts as SVG from docs/results/*.json and
scenarios/traces/. Standard library only; GitHub renders the SVGs
inline, with a light and a dark variant selected by <picture>.

    python3 scripts/charts.py
"""
import datetime as dt
import json
import pathlib
import re

ROOT = pathlib.Path(__file__).resolve().parent.parent
RESULTS = ROOT / "docs" / "results"
TRACES = ROOT / "scenarios" / "traces"
OUT = ROOT / "docs"

THEMES = {
    "light": dict(text="#0b0b0b", muted="#52514e", axis="#898781", grid="#e6e5e0",
                  naive="#2a78d6", greedy="#eb6834", shade="#eb6834", surface="none", surface_ring="#ffffff"),
    "dark": dict(text="#ffffff", muted="#c3c2b7", axis="#898781", grid="#33332f",
                 naive="#3987e5", greedy="#d95926", shade="#d95926", surface="none", surface_ring="#0d1117"),
}
FONT = "font-family='-apple-system,Segoe UI,Helvetica,Arial,sans-serif'"


def nice_max(v, ticks=4):
    """Smallest 'round' axis maximum >= v such that ticks land on round values."""
    import math
    raw = v / ticks
    mag = 10 ** math.floor(math.log10(raw))
    for m in (1, 2, 2.5, 5, 10):
        if m * mag >= raw:
            return m * mag * ticks
    return raw * ticks


def text(x, y, s, size=12, fill="#000", anchor="start", weight="normal"):
    return (f"<text x='{x:.1f}' y='{y:.1f}' font-size='{size}' fill='{fill}' "
            f"text-anchor='{anchor}' font-weight='{weight}' {FONT}>{s}</text>")


def compare_chart(theme):
    """Two panels, one per metric: grouped bars per scenario, naive vs greedy,
    with ±1 stddev whiskers and direct value labels."""
    t = THEMES[theme]
    scenarios = ["base", "high-preemption", "bursty"]
    data = {s: json.load(open(RESULTS / f"{s}.json")) for s in scenarios}
    panels = [("work_per_dollar", "Useful work per dollar (work-seconds / $)", 0),
              ("lost_work_sec", "Lost work (seconds, lower is better)", 0)]
    W, PH, top, left, gap = 760, 230, 60, 60, 40
    H = top + len(panels) * (PH + gap)
    out = [f"<svg xmlns='http://www.w3.org/2000/svg' width='{W}' height='{H}' viewBox='0 0 {W} {H}'>"]
    out.append(text(left, 24, "naive vs greedy, five seeds each, 48 simulated hours", 15, t["text"], weight="600"))
    # Legend
    lx = left
    for name, col in (("naive", t["naive"]), ("greedy", t["greedy"])):
        out.append(f"<rect x='{lx}' y='36' width='12' height='12' rx='2' fill='{col}'/>")
        out.append(text(lx + 18, 46, name, 12, t["muted"]))
        lx += 80
    for pi, (key, title, _) in enumerate(panels):
        y0 = top + pi * (PH + gap)
        plot_h = PH - 40
        base_y = y0 + 20 + plot_h
        vmax = nice_max(1.1 * max(data[s][c][key]["Mean"] + data[s][c][key]["Stddev"]
                                  for s in scenarios for c in ("naive", "greedy")))
        out.append(text(left, y0 + 8, title, 13, t["text"], weight="600"))
        # Gridlines at 4 ticks
        for k in range(1, 5):
            v = vmax * k / 4
            gy = base_y - plot_h * k / 4
            out.append(f"<line x1='{left}' y1='{gy:.1f}' x2='{W - 20}' y2='{gy:.1f}' stroke='{t['grid']}' stroke-width='1'/>")
            out.append(text(left - 6, gy + 4, f"{v:,.0f}", 10, t["axis"], anchor="end"))
        out.append(f"<line x1='{left}' y1='{base_y}' x2='{W - 20}' y2='{base_y}' stroke='{t['axis']}' stroke-width='1'/>")
        group_w = (W - 20 - left) / len(scenarios)
        bar_w = 56
        for si, s in enumerate(scenarios):
            gx = left + group_w * si + group_w / 2
            out.append(text(gx, base_y + 16, s, 12, t["muted"], anchor="middle"))
            for ci, c in enumerate(("naive", "greedy")):
                m, sd = data[s][c][key]["Mean"], data[s][c][key]["Stddev"]
                h = plot_h * m / vmax
                x = gx - bar_w - 1 + ci * (bar_w + 2)
                y = base_y - h
                out.append(f"<rect x='{x:.1f}' y='{y:.1f}' width='{bar_w}' height='{h:.1f}' fill='{t[c]}' rx='3'/>")
                out.append(f"<rect x='{x:.1f}' y='{base_y - 2}' width='{bar_w}' height='2' fill='{t[c]}'/>")
                if sd > 0:
                    e = plot_h * sd / vmax
                    cx = x + bar_w / 2
                    out.append(f"<line x1='{cx:.1f}' y1='{y - e:.1f}' x2='{cx:.1f}' y2='{y + e:.1f}' stroke='{t['text']}' stroke-width='1.5'/>")
                label = f"{m:,.0f}"
                out.append(text(x + bar_w / 2, max(y - 8 - (plot_h * sd / vmax), y0 + 22), label, 11, t["text"], anchor="middle"))
    out.append("</svg>")
    return "\n".join(out)


def ts(s):
    return dt.datetime.strptime(s, "%Y-%m-%dT%H:%M:%SZ")


def trace_chart(theme):
    """Real Sailbox trace (coding-agent, 1-minute samples): memory used and
    CPU used in two stacked panels sharing time, with active phases shaded."""
    t = THEMES[theme]
    samples = json.load(open(TRACES / "coding-agent-6h.json"))["data"]
    phases = json.load(open(TRACES / "coding-agent.json"))["phases"]
    done = ts(next(p["t"] for p in phases if p["event"] == "done"))
    samples = [s for s in samples if ts(s["timestamp"]) <= done + dt.timedelta(minutes=2)]
    t0, t1 = ts(samples[0]["timestamp"]), ts(samples[-1]["timestamp"])
    span = (t1 - t0).total_seconds()
    W, left, right, top = 760, 60, 20, 60
    PH, gap = 150, 50
    H = top + 2 * PH + gap + 30
    X = lambda tm: left + (W - left - right) * (tm - t0).total_seconds() / span
    out = [f"<svg xmlns='http://www.w3.org/2000/svg' width='{W}' height='{H}' viewBox='0 0 {W} {H}'>"]
    out.append(text(left, 24, "A real Sailbox running the coding-agent workload (last 100 minutes, 1-minute samples)", 15, t["text"], weight="600"))
    out.append(f"<rect x='{left}' y='36' width='12' height='12' fill='{t['shade']}' fill-opacity='0.18'/>")
    out.append(text(left + 18, 46, "active phase (box is working)", 12, t["muted"]))
    # Active phase intervals from the log.
    active = []
    ev = [p for p in phases if p["event"] in ("active", "wait", "done")]
    for i, p in enumerate(ev[:-1]):
        if p["event"] == "active":
            active.append((ts(p["t"]), ts(ev[i + 1]["t"])))
    panels = [("memory_used_bytes", lambda v: v / 2**30, "Memory used (GiB), what Sail bills", 5.0, 5),
              ("cpu_used_vcpu", lambda v: v, "CPU used (of 1 vCPU)", 1.0, 4)]
    for pi, (key, f, title, vmax, nticks) in enumerate(panels):
        y0 = top + pi * (PH + gap)
        plot_h = PH - 30
        base_y = y0 + 20 + plot_h
        Y = lambda v: base_y - plot_h * min(v, vmax) / vmax
        out.append(text(left, y0 + 8, title, 13, t["text"], weight="600"))
        for a, b in active:
            xa, xb = max(X(a), left), min(X(b), W - right)
            if xb > xa:
                out.append(f"<rect x='{xa:.1f}' y='{y0 + 20}' width='{xb - xa:.1f}' height='{plot_h}' fill='{t['shade']}' fill-opacity='0.18'/>")
        for k in range(1, nticks + 1):
            gy = Y(vmax * k / nticks)
            out.append(f"<line x1='{left}' y1='{gy:.1f}' x2='{W - right}' y2='{gy:.1f}' stroke='{t['grid']}'/>")
            out.append(text(left - 6, gy + 4, f"{vmax * k / nticks:g}", 10, t["axis"], anchor="end"))
        out.append(f"<line x1='{left}' y1='{base_y}' x2='{W - right}' y2='{base_y}' stroke='{t['axis']}'/>")
        pts = " ".join(f"{X(ts(s['timestamp'])):.1f},{Y(f(s[key])):.1f}" for s in samples)
        out.append(f"<polyline points='{pts}' fill='none' stroke='{t['naive']}' stroke-width='2' stroke-linejoin='round'/>")
        if pi == 1:
            for m in range(0, int(span // 60) + 1, 20):
                tm = t0 + dt.timedelta(minutes=m)
                out.append(text(X(tm), base_y + 16, tm.strftime("%H:%M"), 10, t["axis"], anchor="middle"))
            out.append(text((left + W - right) / 2, base_y + 30, "UTC, 2026-09-22", 10, t["muted"], anchor="middle"))
    out.append("</svg>")
    return "\n".join(out)


def oom_chart(theme):
    """The 2026-09-26 OOM reproduction. Left: the stepped 6 GiB allocation,
    process-held memory against guest MemTotal, with the two hot-plug jumps.
    Right: the two single-shot attempts, guest memory at start against
    resident set at the kill. One shared GiB scale with the 16 GiB ceiling."""
    t = THEMES[theme]
    D = TRACES / "oom-2026-09-26"
    runs = json.load(open(D / "runs.json"))
    rows = []
    for line in (D / "stepped-6gib.log").read_text().splitlines():
        m = re.match(r"t=\s*([\d.]+)s held=\s*([\d.]+)GiB MemTotal=([\d.]+) MemAvailable=([\d.]+)", line)
        if m:
            rows.append(tuple(float(v) for v in m.groups()))
    kills = [r for r in runs["runs"] if r["mode"] == "single"]
    ceiling = runs["ceiling_gib"]
    W, H, top = 760, 380, 84
    L0, L1 = 50, 500          # left panel x-range
    R0, R1 = 560, 740         # right panel x-range
    plot_h = H - top - 46
    base_y = top + plot_h
    vmax = ceiling
    Y = lambda v: base_y - plot_h * v / vmax
    tmax = rows[-1][0]
    X = lambda tm: L0 + (L1 - L0) * tm / tmax
    out = [f"<svg xmlns='http://www.w3.org/2000/svg' width='{W}' height='{H}' viewBox='0 0 {W} {H}'>"]
    out.append(text(L0, 24, "Same box, same 6 GiB: paced allocation grows the guest, a single burst is killed", 15, t["text"], weight="600"))
    lx = L0
    for name, col in (("guest MemTotal (what the kernel can hand out)", t["naive"]), ("held by the process", t["greedy"])):
        out.append(f"<rect x='{lx}' y='36' width='12' height='12' rx='2' fill='{col}'/>")
        out.append(text(lx + 18, 46, name, 12, t["muted"]))
        lx += 290
    # Shared gridlines and y labels across both panels.
    for v in range(0, ceiling + 1, 4):
        gy = Y(v)
        for a, b in ((L0, L1), (R0, R1)):
            out.append(f"<line x1='{a}' y1='{gy:.1f}' x2='{b}' y2='{gy:.1f}' stroke='{t['grid']}'/>")
        out.append(text(L0 - 6, gy + 4, f"{v}", 10, t["axis"], anchor="end"))
    out.append(text(L0 - 6, top - 10, "GiB", 10, t["axis"], anchor="end"))
    # Ceiling: the one dashed line, because it is a threshold.
    out.append(f"<line x1='{L0}' y1='{Y(ceiling):.1f}' x2='{R1}' y2='{Y(ceiling):.1f}' stroke='{t['axis']}' stroke-dasharray='4 4'/>")
    out.append(text(R1, Y(ceiling) + 14, "size-s ceiling, 16 GiB", 10, t["muted"], anchor="end"))
    for a, b in ((L0, L1), (R0, R1)):
        out.append(f"<line x1='{a}' y1='{base_y}' x2='{b}' y2='{base_y}' stroke='{t['axis']}'/>")
    # Left panel: stepped run.
    out.append(text(L0, top - 10, "Stepped: 256 MiB every 0.5 s", 13, t["text"], weight="600"))
    total_pts, held_pts = [], []
    prev_total = None
    for tm, held, total, avail in rows:
        if prev_total is not None and total != prev_total:
            total_pts.append(f"{X(tm):.1f},{Y(prev_total):.1f}")
        total_pts.append(f"{X(tm):.1f},{Y(total):.1f}")
        held_pts.append(f"{X(tm):.1f},{Y(held):.1f}")
        prev_total = total
    out.append(f"<polyline points='{' '.join(total_pts)}' fill='none' stroke='{t['naive']}' stroke-width='2' stroke-linejoin='round'/>")
    out.append(f"<polyline points='{' '.join(held_pts)}' fill='none' stroke='{t['greedy']}' stroke-width='2' stroke-linejoin='round'/>")
    # Annotate each hot-plug jump with how little was left when it landed.
    prev = rows[0]
    for r in rows[1:]:
        if r[2] > prev[2]:
            tm, total_after = r[0], r[2]
            grew = total_after - prev[2]
            out.append(f"<circle cx='{X(tm):.1f}' cy='{Y(total_after):.1f}' r='4' fill='{t['naive']}' stroke='{t['surface_ring']}' stroke-width='2'/>")
            out.append(text(X(tm) + 8, Y(total_after) - 6, f"+{grew:.1f} GiB plugged at {tm:.0f} s", 11, t["text"]))
            out.append(text(X(tm) + 8, Y(total_after) + 8, f"with {prev[3]:.2f} GiB left", 11, t["muted"]))
        prev = r
    end = rows[-1]
    last_jump = max(r[0] for a, r in zip(rows, rows[1:]) if r[2] > a[2])
    out.append(text(X(last_jump) + 10, Y(end[1]) - 10, "done, exit 0", 11, t["text"]))
    for s_ in range(0, int(tmax) + 1, 5):
        out.append(text(X(s_), base_y + 16, f"{s_}", 10, t["axis"], anchor="middle"))
    out.append(text((L0 + L1) / 2, base_y + 32, "seconds since the allocation started", 10, t["muted"], anchor="middle"))
    # Right panel: single-shot attempts.
    out.append(text(R0, top - 10, "Single mmap + memset", 13, t["text"], weight="600"))
    slot = (R1 - R0) / len(kills)
    bw = 34
    for i, k in enumerate(kills):
        cx = R0 + slot * i + slot / 2
        start = k["memtotal_start_gib"]
        rss = k["anon_rss_kib_at_kill"] / 2**20
        out.append(f"<rect x='{cx - bw - 1:.1f}' y='{Y(start):.1f}' width='{bw}' height='{base_y - Y(start):.1f}' fill='{t['naive']}' rx='3'/>")
        out.append(f"<rect x='{cx + 1:.1f}' y='{Y(rss):.1f}' width='{bw}' height='{base_y - Y(rss):.1f}' fill='{t['greedy']}' rx='3'/>")
        out.append(text(cx, Y(max(start, rss)) - 20, "killed", 11, t["text"], anchor="middle", weight="600"))
        out.append(text(cx, Y(max(start, rss)) - 8, f"at {rss:.1f} GiB, exit 137", 10, t["muted"], anchor="middle"))
        out.append(text(cx, base_y + 16, f"box at {start:.1f} GiB", 10, t["axis"], anchor="middle"))
    out.append(text((R0 + R1) / 2, base_y + 32, "wanted 6 GiB, both times", 10, t["muted"], anchor="middle"))
    out.append("</svg>")
    return "\n".join(out)


def main():
    OUT.mkdir(exist_ok=True)
    for theme in THEMES:
        (OUT / f"compare-{theme}.svg").write_text(compare_chart(theme))
        (OUT / f"trace-{theme}.svg").write_text(trace_chart(theme))
        (OUT / f"oom-{theme}.svg").write_text(oom_chart(theme))
        print(f"wrote docs/compare-{theme}.svg docs/trace-{theme}.svg docs/oom-{theme}.svg")


if __name__ == "__main__":
    main()
