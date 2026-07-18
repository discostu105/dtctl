#!/usr/bin/env python3
"""Post-mortem analysis of a scored recipes-eval batch: where did calls,
tokens, and time actually go, per variant?

    ./analyze.py <rundir>          # writes <rundir>/analysis.md

Reads the per-call captures (out/*.argv|.out|.err|.dur), calls.log, and
summary.json. Produces, per variant:
  - call taxonomy: which dtctl subcommands were used, how often, error rates
  - failure taxonomy: grouped stderr signatures of errored calls
  - waste: consecutive-error chains, empty-result queries, retried queries
  - context weight: bytes of dtctl output shipped back into the model,
    split by subcommand (the input-token driver we control)
  - recipe usage: which --recipe invocations happened (recipes variants)
  - slowest calls
"""
import json
import os
import re
import sys
from collections import Counter, defaultdict

VARIANTS = ["base", "skills", "recipes", "recipes-skills"]


def read_argv(path):
    with open(path, "rb") as f:
        return [a.decode("utf-8", "replace") for a in f.read().split(b"\0") if a]


def subcommand(argv):
    """First non-flag tokens, e.g. 'query', 'recipes', 'get workflow'."""
    words = [a for a in argv if not a.startswith("-")][:2]
    if not words:
        return "(flags-only)"
    if words[0] in ("get", "describe", "resolve", "recipes", "verify"):
        return " ".join(words)
    return words[0]


def err_signature(err_text):
    """First meaningful stderr line, normalized into a bucketable signature."""
    for line in err_text.splitlines():
        line = line.strip()
        if not line:
            continue
        line = re.sub(r'"[^"]*"', '"…"', line)
        line = re.sub(r"\b\d+\b", "N", line)
        return line[:140]
    return "(empty stderr)"


def load_calls(ws):
    """Per-call records for one workspace, ordered by start time."""
    outdir = os.path.join(ws, "out")
    if not os.path.isdir(outdir):
        return []
    rc = {}
    log = os.path.join(ws, "calls.log")
    if os.path.exists(log):
        with open(log) as f:
            for line in f:
                parts = line.rstrip("\n").split("\t", 2)
                if len(parts) >= 2 and parts[0].isdigit():
                    rc[parts[0]] = parts[1]
    calls = []
    for f in sorted(os.listdir(outdir)):
        if not f.endswith(".argv"):
            continue
        ts = f[:-5]
        argv = read_argv(os.path.join(outdir, f))
        rec = {"ts": ts, "argv": argv, "sub": subcommand(argv), "rc": rc.get(ts, "0")}
        for ext, key in ((".out", "out_bytes"), (".err", "err_bytes")):
            try:
                rec[key] = os.path.getsize(os.path.join(outdir, ts + ext))
            except OSError:
                rec[key] = 0
        try:
            with open(os.path.join(outdir, ts + ".dur")) as df:
                rec["dur_ms"] = int(df.read().strip())
        except (OSError, ValueError):
            rec["dur_ms"] = 0
        if rec["rc"] != "0":
            try:
                with open(os.path.join(outdir, ts + ".err"), errors="replace") as ef:
                    rec["err_sig"] = err_signature(ef.read())
            except OSError:
                rec["err_sig"] = "(unreadable stderr)"
            if rec["err_sig"] == "(empty stderr)":
                # agent mode reports errors on stdout in the JSON envelope
                try:
                    with open(os.path.join(outdir, ts + ".out")) as of:
                        msg = (json.load(of).get("error") or {}).get("message", "")
                    if msg:
                        rec["err_sig"] = err_signature("envelope: " + msg)
                except Exception:
                    pass
        # empty-result detection (agent-envelope or raw records)
        rec["empty"] = False
        try:
            with open(os.path.join(outdir, ts + ".out")) as of:
                data = json.load(of)
            for k in ("result", "records"):
                v = data.get(k) if isinstance(data, dict) else None
                if isinstance(v, dict):
                    v = v.get("records")
                if isinstance(v, list):
                    rec["empty"] = len(v) == 0
        except Exception:
            pass
        calls.append(rec)
    return calls


VALUE_FLAGS = {"-o", "--output", "-f", "--set", "--metadata", "--context",
               "--recipe", "--jq", "--default-timeframe-start", "--default-timeframe-end"}


def dql_of(call, ws):
    argv = call["argv"]
    if "query" not in argv[:4]:
        return None
    rest = argv[argv.index("query") + 1:]
    if "-f" in rest:  # file/stdin query: read the teed .in capture if present
        try:
            path = rest[rest.index("-f") + 1]
            path = os.path.join(ws, "out", call["ts"] + ".in") if path == "-" \
                else os.path.join(ws, path)
            with open(path) as f:
                return f.read().strip() or None
        except (IndexError, OSError):
            return None
    cands = [a for i, a in enumerate(rest)
             if not a.startswith("-") and (i == 0 or rest[i - 1] not in VALUE_FLAGS)]
    return max(cands, key=len) if cands else None


def main():
    rundir = sys.argv[1]
    summary = {}
    try:
        with open(os.path.join(rundir, "summary.json")) as f:
            summary = {(r["variant"], r["task"]): r for r in json.load(f)["runs"]}
    except (OSError, json.JSONDecodeError):
        pass

    tasks = sorted({t for _, t in summary} | {
        t for v in VARIANTS if os.path.isdir(os.path.join(rundir, v))
        for t in os.listdir(os.path.join(rundir, v))},
        key=lambda t: int(t[1:]) if t[1:].isdigit() else 99)

    lines = [f"# Batch analysis: `{os.path.basename(rundir.rstrip('/'))}`", ""]
    for v in VARIANTS:
        vdir = os.path.join(rundir, v)
        if not os.path.isdir(vdir):
            continue
        allcalls = []
        for t in tasks:
            ws = os.path.join(vdir, t)
            if os.path.isdir(ws):
                for c in load_calls(ws):
                    c["task"] = t
                    allcalls.append(c)
        if not allcalls:
            continue
        lines += [f"## {v}", "",
                  f"{len(allcalls)} dtctl calls, "
                  f"{sum(1 for c in allcalls if c['rc'] != '0')} errored, "
                  f"{sum(1 for c in allcalls if c['empty'])} empty-result, "
                  f"{sum(c['out_bytes'] for c in allcalls) / 1024:.0f} KiB stdout shipped to model", ""]

        sub = defaultdict(lambda: [0, 0, 0, 0])  # calls, errs, out_bytes, ms
        for c in allcalls:
            s = sub[c["sub"]]
            s[0] += 1
            s[1] += c["rc"] != "0"
            s[2] += c["out_bytes"]
            s[3] += c["dur_ms"]
        lines += ["| subcommand | calls | errors | stdout KiB | dtctl s |", "|---|---|---|---|---|"]
        for name, (n, e, b, ms) in sorted(sub.items(), key=lambda kv: -kv[1][0]):
            lines.append(f"| {name} | {n} | {e} | {b / 1024:.0f} | {ms / 1000:.1f} |")
        lines.append("")

        sigs = Counter(f'{c["err_sig"]}' for c in allcalls if c.get("err_sig"))
        if sigs:
            lines += ["Failure signatures:", ""]
            for sig, n in sigs.most_common(8):
                lines.append(f"- {n}× `{sig}`")
            lines.append("")

        # repeated identical queries (retries / flailing)
        dqls = Counter()
        for c in allcalls:
            d = dql_of(c, os.path.join(vdir, c["task"]))
            if d:
                dqls[(c["task"], d.strip())] += 1
        rep = {k: n for k, n in dqls.items() if n > 1}
        if rep:
            lines += ["Repeated identical queries:", ""]
            for (t, d), n in sorted(rep.items(), key=lambda kv: -kv[1])[:6]:
                lines.append(f"- {t}: {n}× `{d[:110]}`")
            lines.append("")

        big = sorted(allcalls, key=lambda c: -c["out_bytes"])[:5]
        lines += ["Largest outputs (context weight):", ""]
        for c in big:
            lines.append(f"- {c['task']} `{' '.join(c['argv'][:4])[:80]}` → {c['out_bytes'] / 1024:.0f} KiB")
        slow = sorted(allcalls, key=lambda c: -c["dur_ms"])[:5]
        lines += ["", "Slowest calls:", ""]
        for c in slow:
            lines.append(f"- {c['task']} `{' '.join(c['argv'][:4])[:80]}` → {c['dur_ms'] / 1000:.1f}s")
        lines.append("")

    # cross-variant verdict × efficiency roll-up from summary.json
    if summary:
        lines += ["## Roll-up (from summary.json)", "",
                  "| variant | pass | fail | calls | empties | uncached-in | out-tok | wall s |",
                  "|---|---|---|---|---|---|---|---|"]
        for v in VARIANTS:
            rows = [r for (vv, _), r in summary.items() if vv == v]
            if not rows:
                continue
            npass = sum(r["verdict"] == "PASS" for r in rows)
            lines.append(f"| {v} | {npass}/{len(rows)} | "
                         f"{', '.join(sorted(r['task'] for r in rows if r['verdict'] != 'PASS')) or '—'} | "
                         f"{sum(r.get('calls', 0) for r in rows)} | "
                         f"{sum(r.get('empties', 0) for r in rows)} | "
                         f"{sum(r.get('tokens_in_uncached', 0) for r in rows)} | "
                         f"{sum(r.get('tokens_out', 0) for r in rows)} | "
                         f"{sum(r.get('duration_s', 0) for r in rows)} |")
        lines.append("")

    path = os.path.join(rundir, "analysis.md")
    with open(path, "w") as f:
        f.write("\n".join(lines) + "\n")
    print("wrote", path)


if __name__ == "__main__":
    main()
