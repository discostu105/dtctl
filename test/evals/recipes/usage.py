#!/usr/bin/env python3
"""Reports how each arm actually used its knowledge sources in a batch.

Two questions score.py cannot answer:
  1. How often was the recipe machinery used (briefing / --recipe /
     describe recipe / resolve scope), per arm and per cell?
  2. Were the dynatrace-for-ai skills actually LOADED (Skill tool invoked),
     or did the agent run on the one-line skill descriptions alone?

Recipe usage comes from the logging wrapper's argv records; skill loads come
from the headless agents' own transcripts under the batch's isolated
CLAUDE_CONFIG_DIR (cfg-*/projects/*/<session>.jsonl).

  ./usage.py ~/.cache/dtctl-recipes-evals/<batch>
"""
import glob
import json
import os
import sys
from collections import Counter, defaultdict

VARIANTS = ["base", "skills", "recipes", "recipes-skills"]


def read_argv(path):
    return open(path, "rb").read().decode("utf-8", "replace").split("\0")[:-1]


def recipe_usage(rundir):
    """Per-variant Counters of dtctl call categories, plus per-cell flags."""
    per_variant = defaultdict(Counter)
    cells = defaultdict(dict)  # variant -> task -> Counter
    for out in sorted(glob.glob(f"{rundir}/*/t*/out")):
        variant, task = out.split("/")[-3], out.split("/")[-2]
        if variant not in VARIANTS:
            continue
        c = Counter()
        for f in glob.glob(out + "/*.argv"):
            av = read_argv(f)
            c["calls"] += 1
            if av[:1] == ["recipes"]:
                c["briefing"] += 1
            if av[:2] == ["describe", "recipe"]:
                c["describe recipe"] += 1
            if "--recipe" in av:
                c["--recipe"] += 1
            if av[:2] == ["resolve", "scope"]:
                c["resolve scope"] += 1
            if av[:1] == ["commands"]:
                c["commands"] += 1
        per_variant[variant].update(c)
        cells[variant][task] = c
    return per_variant, cells


def skill_loads(rundir):
    """variant -> task -> [skill names loaded via the Skill tool]."""
    loads = defaultdict(lambda: defaultdict(list))
    denied = defaultdict(int)
    batch = os.path.basename(os.path.normpath(rundir))
    for f in glob.glob(f"{rundir}/cfg-*/projects/*/*.jsonl"):
        cell = os.path.basename(os.path.dirname(f))
        # project dir name: ...-<batch>-<variant>-<task>
        tail = cell.split(f"{batch}-", 1)
        if len(tail) != 2:
            continue
        variant, _, task = tail[1].rpartition("-")
        for line in open(f):
            try:
                rec = json.loads(line)
            except json.JSONDecodeError:
                continue
            content = (rec.get("message") or {}).get("content")
            if not isinstance(content, list):
                continue
            for c in content:
                if not isinstance(c, dict):
                    continue
                if c.get("type") == "tool_use" and c.get("name") == "Skill":
                    skill = (c.get("input") or {}).get("skill")
                    loads[variant][task].append(skill or "?")
                if c.get("type") == "tool_result" and c.get("is_error"):
                    text = json.dumps(c)
                    if "requested permissions" in text:
                        denied[variant] += 1
    return loads, denied


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    rundir = sys.argv[1].rstrip("/")
    per_variant, cells = recipe_usage(rundir)
    loads, denied = skill_loads(rundir)

    ncells = {v: len(cells.get(v, {})) for v in VARIANTS}
    print(f"batch: {rundir}")
    print(f"{'':16s} {'cells':>5} {'calls':>5} {'brief':>5} {'--rec':>5} "
          f"{'descr':>5} {'scope':>5} {'cmds':>5} {'skill-cells':>11} {'loads':>5} {'denied':>6}")
    for v in VARIANTS:
        if not ncells[v]:
            continue
        c = per_variant[v]
        skill_cells = sum(1 for t in loads.get(v, {}) if loads[v][t])
        nloads = sum(len(x) for x in loads.get(v, {}).values())
        print(f"{v:16s} {ncells[v]:5d} {c['calls']:5d} {c['briefing']:5d} "
              f"{c['--recipe']:5d} {c['describe recipe']:5d} {c['resolve scope']:5d} "
              f"{c['commands']:5d} {skill_cells:11d} {nloads:5d} {denied.get(v, 0):6d}")

    # Which skills, and which recipe-arm cells never touched the machinery.
    for v in VARIANTS:
        names = Counter()
        for t, ls in loads.get(v, {}).items():
            names.update(ls)
        if names:
            top = ", ".join(f"{k}×{n}" for k, n in names.most_common())
            print(f"{v}: skills loaded: {top}")
    for v in ("recipes", "recipes-skills"):
        untouched = [t for t, c in cells.get(v, {}).items()
                     if not (c["briefing"] or c["--recipe"] or c["describe recipe"] or c["resolve scope"])]
        if untouched:
            print(f"{v}: cells with NO recipe machinery: {sorted(untouched)}")


if __name__ == "__main__":
    main()
