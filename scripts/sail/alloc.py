#!/usr/bin/env python3
"""alloc.py <single|stepped> <GiB>  -- allocate and fault memory, logging /proc/meminfo."""
import ctypes, sys, threading, time
GIB = 1 << 30
mode, gib = sys.argv[1], float(sys.argv[2])
N = int(gib * GIB)
t0 = time.monotonic()

def mi(key):
    with open("/proc/meminfo") as f:
        for l in f:
            if l.startswith(key + ":"):
                return int(l.split()[1]) / (1 << 20)

def log(tag, held):
    print(f"t={time.monotonic()-t0:6.1f}s held={held/GIB:5.2f}GiB MemTotal={mi('MemTotal'):.3f} MemAvailable={mi('MemAvailable'):.3f} {tag}", flush=True)

held = [0]
stop = False
def watcher():
    while not stop:
        log("tick", held[0]); time.sleep(0.25)

log("start", 0)
threading.Thread(target=watcher, daemon=True).start()
if mode == "single":
    buf = ctypes.create_string_buffer(N)      # one mmap of the whole size
    ctypes.memset(buf, 0x41, N)               # fault every page as fast as libc runs
    held[0] = N
else:
    CHUNK, STEP, PAUSE = 64 << 20, 4, 0.5     # 256 MiB per step, same as workload.py
    bufs = []
    while held[0] < N:
        for _ in range(STEP):
            if held[0] >= N: break
            b = ctypes.create_string_buffer(CHUNK); ctypes.memset(b, 0x41, CHUNK)
            bufs.append(b); held[0] += CHUNK
        time.sleep(PAUSE)
stop = True
log("done", held[0])
