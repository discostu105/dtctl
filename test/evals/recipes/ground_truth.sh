#!/usr/bin/env bash
# Establishes ground truth for all six tasks by direct queries against the
# tenant, using bin/dtctl-recipes. Writes <rundir>/ground-truth.json.
#
# Ground truth is measured fresh per batch because the tasks use relative time
# windows — yesterday's numbers are not today's. Scoring tolerances in
# score.py absorb the drift between this measurement and the agent runs.
set -euo pipefail
cd "$(dirname "$0")"
RUNDIR=${1:?usage: ground_truth.sh <rundir>}
: "${EVAL_CONTEXT:?source env.sh first}"
: "${EVAL_TRAP_SERVICE:?source env.sh first}"
BIN=$PWD/bin/dtctl-recipes
[ -x "$BIN" ] || { echo "run ./build.sh first" >&2; exit 1; }

q() { "$BIN" --context "$EVAL_CONTEXT" query "$1" -o json --plain --no-agent 2>/dev/null; }

echo "ground truth: t1 error sources"
T1=$(q 'fetch logs, from:now()-1h | filter loglevel == "ERROR" | summarize c = count(), by:{k8s.container.name} | sort c desc | limit 5')

echo "ground truth: t2 p95 by service entity"
T2=$(q 'fetch spans, from:now()-1h | filter isNotNull(dt.smartscape.service) | summarize p95 = percentile(duration, 95), by:{dt.smartscape.service} | sort p95 desc | limit 3 | fieldsAdd name = getNodeName(dt.smartscape.service) | fields name, p95')

echo "ground truth: t3 RUM volume"
T3=$(q 'fetch user.events, from:now()-24h | summarize c = count()')

echo "ground truth: t4 GenAI tokens"
T4=$(q 'fetch spans, from:now()-24h | filter isNotNull(gen_ai.request.model) | summarize t = sum(gen_ai.usage.input_tokens), by:{gen_ai.request.model} | sort t desc | limit 3')

echo "ground truth: t5 complete count (resolve scope + service.name union)"
SCOPE=$("$BIN" --context "$EVAL_CONTEXT" resolve scope "$EVAL_TRAP_SERVICE" --for logs --plain --no-agent 2>/dev/null)
T5=$(q "fetch logs, from:now()-2h | filter ($SCOPE) or service.name == \"$EVAL_TRAP_SERVICE\" | summarize c = count()")

echo "ground truth: t5 naive (entity-stamped) count"
IDS=$(q "smartscapeNodes \"*\" | filter name == \"$EVAL_TRAP_SERVICE\" and type == \"SERVICE\" | fields id" \
    | python3 -c 'import json,sys; print(", ".join("toSmartscapeId(\"%s\")" % r["id"] for r in json.load(sys.stdin)["records"]))')
T5N=$(q "fetch logs, from:now()-2h | filter in(dt.smartscape.service, {$IDS}) | summarize c = count()")

echo "ground truth: t6 security findings"
T6=$(q 'fetch security.events, from:now()-7d | filter in(event.type, {"DETECTION_FINDING","COMPLIANCE_FINDING"}) | summarize c = count(), by:{event.type}')

python3 - "$RUNDIR" <<PYEOF
import json, sys, datetime

def rows(s):
    return json.loads(s)["records"] if s.strip() else []

t1 = rows('''$T1''')
t1 = [r for r in t1 if r.get("k8s.container.name")]
t2 = rows('''$T2''')
t3 = rows('''$T3''')
t4 = rows('''$T4''')
t5 = rows('''$T5''')
t5n = rows('''$T5N''')
t6 = rows('''$T6''')

det = sum(int(r["c"]) for r in t6 if r["event.type"] == "DETECTION_FINDING")
comp = sum(int(r["c"]) for r in t6 if r["event.type"] == "COMPLIANCE_FINDING")

gt = {
    "at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "t1": {"top_source": t1[0]["k8s.container.name"], "count": int(t1[0]["c"]),
           "sources": [{"name": r["k8s.container.name"], "count": int(r["c"])} for r in t1]},
    "t2": {"top": [{"service": r["name"], "p95_ms": int(r["p95"]) / 1e6} for r in t2]},
    "t3": {"events_24h": int(t3[0]["c"]) if t3 else 0},
    "t4": {"model": t4[0]["gen_ai.request.model"], "input_tokens": int(t4[0]["t"])} if t4 else None,
    "t5": {"count": int(t5[0]["c"]), "naive_count": int(t5n[0]["c"]) if t5n else 0},
    "t6": {"attack_detections_present": det > 0, "detection_count": det,
           "compliance_findings_7d": comp},
}
path = sys.argv[1] + "/ground-truth.json"
with open(path, "w") as f:
    json.dump(gt, f, indent=2)
print("wrote", path)
print(json.dumps(gt, indent=2))
PYEOF
