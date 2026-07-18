#!/usr/bin/env python3
"""Compare two scored batches variant-by-variant.

    ./compare.py <rundir-before> <rundir-after>

Prints per-variant metric deltas and per-cell verdict changes. Only tasks
present in BOTH batches enter the metric sums, so a widened task set still
compares like-for-like.
"""
import json
import os
import sys

VARIANTS = ["base", "head-base", "skills", "recipes", "recipes-skills"]
METRICS = [("calls", "calls"), ("errors", "errored"), ("empties", "empties"),
           ("turns", "turns"), ("cost_usd", "cost USD"),
           ("tokens_in_uncached", "uncached-in tok"), ("tokens_out", "out tok"),
           ("duration_s", "wall s"), ("dtctl_s", "dtctl s"), ("scan_gb", "scan GB")]


def load(rundir):
    with open(os.path.join(rundir, "summary.json")) as f:
        return {(r["variant"], r["task"]): r for r in json.load(f)["runs"]}


def main():
    a, b = load(sys.argv[1]), load(sys.argv[2])
    na, nb = (os.path.basename(p.rstrip("/")) for p in sys.argv[1:3])
    common_tasks = sorted({t for _, t in a} & {t for _, t in b},
                          key=lambda t: (t[0], int(t[1:]) if t[1:].isdigit() else 99))
    print(f"comparing {na} → {nb} over {len(common_tasks)} shared tasks\n")

    for v in VARIANTS:
        rows_a = [a[(v, t)] for t in common_tasks if (v, t) in a]
        rows_b = [b[(v, t)] for t in common_tasks if (v, t) in b]
        if not rows_a or not rows_b:
            continue
        pa = sum(r["verdict"] == "PASS" for r in rows_a)
        pb = sum(r["verdict"] == "PASS" for r in rows_b)
        print(f"== {v}:  pass {pa}/{len(rows_a)} → {pb}/{len(rows_b)}")
        for key, label in METRICS:
            sa = sum(r.get(key) or 0 for r in rows_a)
            sb = sum(r.get(key) or 0 for r in rows_b)
            if sa or sb:
                d = sb - sa
                pct = f" ({d / sa * +100:+.0f}%)" if sa else ""
                print(f"   {label:>15}: {round(sa, 2):>10} → {round(sb, 2):>10}{pct}")
        changes = []
        for t in common_tasks:
            va = a.get((v, t), {}).get("verdict")
            vb = b.get((v, t), {}).get("verdict")
            if va != vb:
                changes.append(f"{t}: {va} → {vb}")
        if changes:
            print("   verdict changes: " + "; ".join(changes))
        print()


if __name__ == "__main__":
    main()
