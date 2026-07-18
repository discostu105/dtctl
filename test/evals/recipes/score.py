#!/usr/bin/env python3
"""Score a recipes-eval batch: correctness vs ground truth + efficiency metrics.

    ./score.py <rundir>                  # score all runs found in the batch
    ./score.py <rundir> --measure-scan   # ALSO re-run each agent query with
                                         # --metadata=scannedBytes to measure
                                         # scan cost (costs Grail consumption)

Writes <rundir>/report.md and <rundir>/summary.json. Verdicts:
    PASS          answer matches ground truth within tolerance
    WRONG         answer given, does not match
    SILENT_WRONG  t5 only: answer matches the known-incomplete naive count
    UNKNOWN       agent honestly answered ANSWER: UNKNOWN
    NO_ANSWER     no parseable ANSWER line
    ERROR         run crashed / no result.json
"""
import argparse
import json
import os
import re
import subprocess
import sys

VARIANTS = ["base", "skills", "recipes", "recipes-skills"]
TASKS = ["t1", "t2", "t3", "t4", "t5", "t6"]


def close(a, b, tol):
    if b == 0:
        return a == 0
    return abs(a - b) / abs(b) <= tol


def name_match(a, b):
    a, b = a.strip().lower(), b.strip().lower()
    return a == b or a in b or b in a


def num(v):
    if isinstance(v, str):
        v = v.replace(",", "").replace("_", "").strip()
    return float(v)


def score_answer(task, ans, gt):
    """Returns (verdict, note) for a parsed ANSWER json against ground truth."""
    g = gt[task]
    try:
        if task == "t1":
            if not name_match(str(ans["top_source"]), g["top_source"]):
                return "WRONG", f'source {ans["top_source"]!r} != {g["top_source"]!r}'
            if not close(num(ans["count"]), g["count"], 0.4):
                return "WRONG", f'count {ans["count"]} vs GT {g["count"]}'
            return "PASS", ""
        if task == "t2":
            top = g["top"]
            for i, row in enumerate(top):
                if name_match(str(ans["service"]), row["service"]):
                    # rank 1 always ok; a lower rank passes only in a close race
                    if i == 0 or row["p95_ms"] >= 0.66 * top[0]["p95_ms"]:
                        if close(num(ans["p95_ms"]), row["p95_ms"], 0.5):
                            return "PASS", f"matched GT rank {i + 1}"
                        return "WRONG", f'p95 {ans["p95_ms"]} vs GT {row["p95_ms"]:.1f}'
            return "WRONG", f'service {ans["service"]!r} not in GT top {len(top)}'
        if task == "t3":
            if bool(ans["rum_present"]) != (g["events_24h"] > 0):
                return "WRONG", "rum_present mismatch"
            if g["events_24h"] and not close(num(ans["events_24h"]), g["events_24h"], 0.4):
                return "WRONG", f'events {ans["events_24h"]} vs GT {g["events_24h"]}'
            return "PASS", ""
        if task == "t4":
            if g is None:
                return "WRONG", "no GenAI data in GT"
            if not name_match(str(ans["model"]), g["model"]):
                return "WRONG", f'model {ans["model"]!r} != {g["model"]!r}'
            if not close(num(ans["input_tokens"]), g["input_tokens"], 0.3):
                return "WRONG", f'tokens {ans["input_tokens"]} vs GT {g["input_tokens"]}'
            return "PASS", ""
        if task == "t5":
            c, true, naive = num(ans["count"]), g["count"], g["naive_count"]
            if close(c, true, 0.3):
                return "PASS", ""
            # the trap: reporting the known-incomplete entity-stamped count
            if naive < 0.5 * true and close(c, naive, 0.5):
                return "SILENT_WRONG", f"{c:.0f} ≈ naive {naive} (true {true})"
            return "WRONG", f"{c:.0f} vs GT {true} (naive {naive})"
        if task == "t6":
            if bool(ans["attack_detections_present"]) != g["attack_detections_present"]:
                return "WRONG", "attack_detections_present mismatch"
            c = num(ans["compliance_findings_7d"])
            if not (0.5 * g["compliance_findings_7d"] <= c <= 2 * g["compliance_findings_7d"]):
                return "WRONG", f'compliance {c:.0f} vs GT {g["compliance_findings_7d"]}'
            return "PASS", ""
    except (KeyError, TypeError, ValueError) as e:
        return "NO_ANSWER", f"answer missing/invalid field: {e}"
    return "ERROR", f"unknown task {task}"


ANSWER_RE = re.compile(r"^ANSWER:\s*(.+?)\s*$", re.MULTILINE)


def parse_answer(text):
    """Last ANSWER: line → (dict|'UNKNOWN'|None, raw)."""
    matches = ANSWER_RE.findall(text or "")
    if not matches:
        return None, ""
    raw = matches[-1]
    if raw.strip().upper().startswith("UNKNOWN"):
        return "UNKNOWN", raw
    # tolerate markdown fences/backticks around the json
    raw_json = raw.strip().strip("`")
    try:
        return json.loads(raw_json), raw
    except json.JSONDecodeError:
        return None, raw


def transcript_answer(rundir, session_id):
    """Fallback: scan the session transcript for ANSWER lines in ALL assistant
    messages — the agent's final message can be post-answer chatter (e.g. a
    stray background-task notification reply)."""
    import glob
    best = None
    for path in glob.glob(os.path.join(rundir, "cfg-*", "projects", "*", session_id + ".jsonl")):
        with open(path) as f:
            for line in f:
                try:
                    ev = json.loads(line)
                except json.JSONDecodeError:
                    continue
                msg = ev.get("message") or {}
                if ev.get("type") != "assistant":
                    continue
                for block in msg.get("content") or []:
                    if isinstance(block, dict) and block.get("type") == "text":
                        ans, raw = parse_answer(block.get("text", ""))
                        if ans is not None:
                            best = (ans, raw)
    return best


def out_is_empty(path):
    """Best-effort: did this dtctl call return an empty result set?"""
    try:
        with open(path) as f:
            data = json.load(f)
    except (json.JSONDecodeError, OSError, UnicodeDecodeError):
        return False
    for key in ("result", "records"):
        v = data.get(key) if isinstance(data, dict) else None
        if isinstance(v, dict):
            v = v.get("records")
        if isinstance(v, list):
            return len(v) == 0
    return False


def call_metrics(ws):
    # One .argv file per dtctl invocation is the authoritative call count
    # (calls.log lines can span multiple physical lines for multiline DQL).
    rc = {}
    log = os.path.join(ws, "calls.log")
    if os.path.exists(log):
        with open(log) as f:
            for line in f:
                parts = line.rstrip("\n").split("\t", 2)
                if len(parts) >= 2 and parts[0].isdigit():
                    rc[parts[0]] = parts[1]
    outdir = os.path.join(ws, "out")
    ts = [f[:-5] for f in os.listdir(outdir) if f.endswith(".argv")] if os.path.isdir(outdir) else []
    calls = len(ts)
    errors = sum(1 for t in ts if rc.get(t, "0") != "0")
    empties = sum(1 for t in ts if out_is_empty(os.path.join(outdir, t + ".out")))
    return calls, errors, empties


def extract_query_dqls(ws):
    """Best-effort DQL extraction from the per-call argv records."""
    dqls = []
    outdir = os.path.join(ws, "out")
    if not os.path.isdir(outdir):
        return dqls
    for f in sorted(os.listdir(outdir)):
        if not f.endswith(".argv"):
            continue
        with open(os.path.join(outdir, f), "rb") as fh:
            argv = [a.decode("utf-8", "replace") for a in fh.read().split(b"\0") if a]
        if "query" not in argv[:3]:
            continue
        qi = argv.index("query")
        rest = argv[qi + 1:]
        if "-f" in rest:  # query from file or stdin ('-': read the teed .in capture)
            try:
                path = rest[rest.index("-f") + 1]
                if path == "-":
                    path = os.path.join(outdir, f[:-5] + ".in")
                else:
                    path = os.path.join(ws, path)
                with open(path) as qf:
                    dql = qf.read().strip()
                    if dql:
                        dqls.append(dql)
            except (IndexError, OSError):
                pass
            continue
        candidates = [a for a in rest if not a.startswith("-")]
        if candidates:
            dqls.append(max(candidates, key=len))
    return dqls


def measure_scan(ws, binary, context):
    """Re-run each query with --metadata=scannedBytes; returns (bytes, n, skipped)."""
    total = measured = skipped = 0
    for dql in extract_query_dqls(ws):
        try:
            out = subprocess.run(
                [binary, "--context", context, "query", dql, "-o", "json",
                 "--plain", "--no-agent", "--metadata=scannedBytes"],
                capture_output=True, text=True, timeout=120)
            sb = json.loads(out.stdout).get("metadata", {}).get("scannedBytes", 0)
            total += int(sb)
            measured += 1
        except Exception:
            skipped += 1
    return total, measured, skipped


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("rundir")
    ap.add_argument("--measure-scan", action="store_true",
                    help="re-run agent queries to measure scanned bytes (costs consumption)")
    ap.add_argument("--binary", default=os.path.join(os.path.dirname(__file__), "bin", "dtctl-recipes"))
    ap.add_argument("--context", default=os.environ.get("EVAL_CONTEXT", ""))
    args = ap.parse_args()

    with open(os.path.join(args.rundir, "ground-truth.json")) as f:
        gt = json.load(f)

    runs = []
    for v in VARIANTS:
        for t in TASKS:
            ws = os.path.join(args.rundir, v, t)
            if not os.path.isdir(ws):
                continue
            row = {"variant": v, "task": t}
            calls, errors, empties = call_metrics(ws)
            row.update(calls=calls, errors=errors, empties=empties)
            try:
                with open(os.path.join(ws, "result.json")) as f:
                    res = json.load(f)
                row.update(turns=res.get("num_turns"), cost_usd=round(res.get("total_cost_usd", 0), 4),
                           duration_s=round(res.get("duration_ms", 0) / 1000))
                if res.get("subtype") == "error_max_turns":
                    row.update(verdict="NO_ANSWER", note="hit max turns", answer="")
                else:
                    ans, raw = parse_answer(res.get("result", ""))
                    if ans is None and res.get("session_id"):
                        fb = transcript_answer(args.rundir, res["session_id"])
                        if fb:
                            ans, raw = fb
                    row["answer"] = raw
                    if ans == "UNKNOWN":
                        row.update(verdict="UNKNOWN", note="honest unknown")
                    elif ans is None:
                        row.update(verdict="NO_ANSWER", note="no parseable ANSWER line")
                    else:
                        verdict, note = score_answer(t, ans, gt)
                        row.update(verdict=verdict, note=note)
            except (OSError, json.JSONDecodeError) as e:
                row.update(verdict="ERROR", note=str(e), answer="")
            if args.measure_scan and args.context:
                b, n, sk = measure_scan(ws, args.binary, args.context)
                row.update(scan_gb=round(b / 1e9, 3), scan_measured=n, scan_skipped=sk)
            runs.append(row)
            print(f'{v:>15}/{t}: {row["verdict"]:<13} calls={calls} {row.get("note", "")}')

    with open(os.path.join(args.rundir, "summary.json"), "w") as f:
        json.dump({"ground_truth": gt, "runs": runs}, f, indent=2)

    # report.md
    variants = [v for v in VARIANTS if any(r["variant"] == v for r in runs)]
    tasks = [t for t in TASKS if any(r["task"] == t for r in runs)]
    by = {(r["variant"], r["task"]): r for r in runs}
    icon = {"PASS": "✅", "WRONG": "❌", "SILENT_WRONG": "🚨", "UNKNOWN": "⚠️ UNK",
            "NO_ANSWER": "∅", "ERROR": "💥"}
    lines = ["# Recipes eval report", "", f'Batch: `{os.path.basename(args.rundir.rstrip("/"))}` · '
             f'GT at {gt["at"]}', "", "## Correctness", "",
             "| Task | " + " | ".join(variants) + " |",
             "|---|" + "---|" * len(variants)]
    for t in tasks:
        cells = [icon.get(by[(v, t)]["verdict"], "?") if (v, t) in by else "—" for v in variants]
        lines.append(f"| {t} | " + " | ".join(cells) + " |")
    lines += ["", "## Metrics (per variant, summed over tasks)", "",
              "| Metric | " + " | ".join(variants) + " |",
              "|---|" + "---|" * len(variants)]
    metrics = [("dtctl calls", "calls"), ("errored calls", "errors"), ("empty results", "empties"),
               ("agent turns", "turns"), ("cost USD", "cost_usd"), ("wall seconds", "duration_s")]
    if args.measure_scan:
        metrics.append(("scanned GB", "scan_gb"))
    for label, key in metrics:
        cells = []
        for v in variants:
            vals = [by[(v, t)].get(key) for t in tasks if (v, t) in by]
            vals = [x for x in vals if isinstance(x, (int, float))]
            cells.append(str(round(sum(vals), 3)) if vals else "—")
        lines.append(f"| {label} | " + " | ".join(cells) + " |")
    lines += ["", "## Per-run detail", ""]
    for r in runs:
        lines.append(f'- **{r["variant"]}/{r["task"]}** {r["verdict"]}'
                     + (f' ({r["note"]})' if r.get("note") else "")
                     + f' — calls {r["calls"]}, errors {r["errors"]}, empties {r["empties"]}'
                     + (f', turns {r["turns"]}' if r.get("turns") is not None else "")
                     + (f', ${r["cost_usd"]}' if r.get("cost_usd") is not None else "")
                     + (f', scan {r["scan_gb"]} GB' if "scan_gb" in r else "")
                     + (f'\n  `{r["answer"]}`' if r.get("answer") else ""))
    report = os.path.join(args.rundir, "report.md")
    with open(report, "w") as f:
        f.write("\n".join(lines) + "\n")
    print("\nwrote", report)


if __name__ == "__main__":
    main()
