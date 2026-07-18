#!/usr/bin/env python3
"""Aggregate statistics over one or more SCORED batches (summary.json).

    ./stats.py <rundir> [<rundir> ...]

Single-batch verdict counts overstate certainty: 24/24 vs 21/24 is only
three discordant tasks (exact McNemar p = 0.25). This tool pools trials and
reports what the evidence actually supports:

  - per-arm pooled pass rate with a Wilson 95% interval (treats cells as
    independent — an upper bound on certainty),
  - a task-CLUSTERED bootstrap interval (resamples tasks, keeping all
    trials of a task together — the honest interval, since the same task
    repeatedly failing across trials is one phenomenon, not many),
  - pairwise arm comparisons on shared (task, batch) cells: discordant
    counts and an exact two-sided McNemar (binomial) p-value,
  - per-arm task instability: tasks whose verdict flips between trials.

PASS counts as success; everything else (WRONG, SILENT_WRONG, UNKNOWN,
NO_ANSWER, ERROR) as failure — an honest UNKNOWN is still a task the agent
could not answer.
"""
import json
import math
import os
import random
import sys
from collections import defaultdict

VARIANTS = ["base", "head-base", "skills", "recipes", "recipes-skills"]


def load(rundirs):
    """-> {(variant, task, batch): passed(bool)}"""
    cells = {}
    for rd in rundirs:
        batch = os.path.basename(rd.rstrip("/"))
        with open(os.path.join(rd, "summary.json")) as f:
            for r in json.load(f)["runs"]:
                cells[(r["variant"], r["task"], batch)] = r["verdict"] == "PASS"
    return cells


def wilson(k, n, z=1.96):
    if n == 0:
        return 0.0, 0.0
    p = k / n
    d = 1 + z * z / n
    c = (p + z * z / (2 * n)) / d
    h = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n)) / d
    return max(0.0, c - h), min(1.0, c + h)


def cluster_bootstrap(by_task, iters=10000, seed=7):
    """by_task: {task: [bool, ...]} -> (lo, hi) 95% CI of the pooled pass
    rate, resampling TASKS with replacement (all trials of a task travel
    together)."""
    rng = random.Random(seed)
    tasks = sorted(by_task)
    if not tasks:
        return 0.0, 0.0
    rates = []
    for _ in range(iters):
        picked = [by_task[rng.choice(tasks)] for _ in tasks]
        n = sum(len(v) for v in picked)
        k = sum(sum(v) for v in picked)
        rates.append(k / n if n else 0.0)
    rates.sort()
    return rates[int(0.025 * iters)], rates[int(0.975 * iters) - 1]


def mcnemar_exact(b, c):
    """Two-sided exact McNemar on discordant counts (binomial, p=0.5)."""
    n = b + c
    if n == 0:
        return 1.0
    k = min(b, c)
    tail = sum(math.comb(n, i) for i in range(k + 1)) / 2 ** n
    return min(1.0, 2 * tail)


def main():
    rundirs = sys.argv[1:]
    if not rundirs:
        sys.exit(__doc__)
    cells = load(rundirs)
    batches = sorted({b for _, _, b in cells})
    print(f"pooling {len(batches)} batch(es): {', '.join(batches)}\n")

    variants = [v for v in VARIANTS if any(k[0] == v for k in cells)]
    print(f"{'arm':>15} {'pass':>9} {'rate':>6}  {'Wilson95':>13}  {'task-clustered95':>16}")
    for v in variants:
        vk = {k: p for k, p in cells.items() if k[0] == v}
        n, k = len(vk), sum(vk.values())
        lo, hi = wilson(k, n)
        by_task = defaultdict(list)
        for (_, t, _), p in vk.items():
            by_task[t].append(p)
        blo, bhi = cluster_bootstrap(by_task)
        print(f"{v:>15} {k:>4}/{n:<4} {k / n if n else 0:6.1%}  "
              f"[{lo:5.1%},{hi:5.1%}]  [{blo:5.1%},{bhi:5.1%}]")

    print("\npairwise (shared task+batch cells; b = only left passes, c = only right):")
    for i, a in enumerate(variants):
        for bb in variants[i + 1:]:
            shared = [(t, ba) for (v, t, ba) in cells if v == a
                      and (bb, t, ba) in cells]
            bcnt = sum(1 for t, ba in shared if cells[(a, t, ba)] and not cells[(bb, t, ba)])
            ccnt = sum(1 for t, ba in shared if not cells[(a, t, ba)] and cells[(bb, t, ba)])
            if not shared:
                continue
            p = mcnemar_exact(bcnt, ccnt)
            sig = " *" if p < 0.05 else ""
            print(f"  {a:>15} vs {bb:<15} n={len(shared):<4} b={bcnt:<3} c={ccnt:<3} "
                  f"McNemar p={p:.3f}{sig}")

    if len(batches) > 1:
        print("\nper-arm task instability (verdict flips between trials):")
        for v in variants:
            by_task = defaultdict(set)
            for (vv, t, ba), p in cells.items():
                if vv == v:
                    by_task[t].add(p)
            unstable = sorted(t for t, s in by_task.items() if len(s) > 1)
            if unstable:
                print(f"  {v}: {', '.join(unstable)}")
        print("\n(headline claims should quote the task-clustered interval; "
              "single-batch deltas inside it are noise)")


if __name__ == "__main__":
    main()
