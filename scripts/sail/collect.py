#!/usr/bin/env python3
"""Pull observed-usage metrics for the calibration Sailboxes.

Reads SAIL_API_KEY from the environment (or ~/.sail/auth.toml), fetches
GET /v1/sailboxes/{id}/metrics for every box listed in boxes.txt and writes
one JSON file per box under scenarios/traces/. The workload log inside each
box is fetched too, so phase transitions can be lined up with the samples.

    python3 scripts/sail/collect.py --range 24h
"""
import argparse
import json
import os
import pathlib
import re
import subprocess
import sys
import urllib.request

API = "https://sailbox-api.sailresearch.com/v1"
HERE = pathlib.Path(__file__).resolve().parent
OUT = HERE.parent.parent / "scenarios" / "traces"


def api_key():
    key = os.environ.get("SAIL_API_KEY")
    if key:
        return key
    auth = pathlib.Path.home() / ".sail" / "auth.toml"
    if auth.exists():
        m = re.search(r'api_key\s*=\s*"([^"]+)"', auth.read_text())
        if m:
            return m.group(1)
    sys.exit("no SAIL_API_KEY in the environment or ~/.sail/auth.toml")


def get(path, key):
    req = urllib.request.Request(API + path, headers={"Authorization": "Bearer " + key})
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.load(r)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--range", default="24h", choices=["1h", "6h", "24h", "7d"])
    ap.add_argument("--no-log", action="store_true", help="skip fetching /workload.log from each box")
    args = ap.parse_args()
    key = api_key()
    OUT.mkdir(parents=True, exist_ok=True)
    for line in (HERE / "boxes.txt").read_text().splitlines():
        if not line.strip() or line.startswith("#"):
            continue
        name, box_id = line.split()
        metrics = get(f"/sailboxes/{box_id}/metrics?range={args.range}", key)
        info = get(f"/sailboxes/{box_id}", key)
        trace = {
            "name": name,
            "sailbox_id": box_id,
            "size": {
                "cpu_requested_vcpu": info.get("cpu_requested_vcpu"),
                "memory_requested_bytes": info.get("memory_requested_bytes"),
            },
            "range": metrics.get("range"),
            "samples": metrics.get("data", []),
            "phases": [],
        }
        if not args.no_log:
            out = subprocess.run(
                ["sail", "box", "exec", box_id, "--", "cat", "/workload.log"],
                capture_output=True, text=True, timeout=120,
            )
            trace["phases"] = [json.loads(l) for l in out.stdout.splitlines() if l.startswith("{")]
        path = OUT / f"{name}.json"
        path.write_text(json.dumps(trace, indent=1))
        print(f"{name}: {len(trace['samples'])} samples, {len(trace['phases'])} phase events -> {path}")


if __name__ == "__main__":
    main()
