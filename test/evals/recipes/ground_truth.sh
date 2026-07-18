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

echo "ground truth: t7 davis problems (distinct + active)"
T7=$(q 'fetch dt.davis.problems, from:now()-7d | summarize c = countDistinctExact(display_id)')
T7A=$(q 'fetch dt.davis.problems, from:now()-7d | filter not(dt.davis.is_duplicate) | summarize status = takeLast(event.status), by:{display_id} | filter status == "ACTIVE" | summarize c = count()')

echo "ground truth: t8 top-CPU host"
T8=$(q 'timeseries cpu = avg(dt.host.cpu.usage), by:{dt.smartscape.host}, from:now()-1h | fieldsAdd name = getNodeName(dt.smartscape.host), a = arrayAvg(cpu) | sort a desc | limit 3 | fields name, a')

echo "ground truth: t9 OOM-killed pods 7d"
T9=$(q 'timeseries oom = sum(dt.kubernetes.container.oom_kills), by:{k8s.pod.name}, from:now()-7d | fieldsAdd t = arraySum(oom) | filter t > 0 | summarize pods = count(), total = sum(t)')

echo "ground truth: t10 open vulnerabilities (latest state) + naive event rows"
T10=$(q 'fetch security.events, from:now()-24h | filter event.type == "VULNERABILITY_STATE_REPORT_EVENT" and event.level == "VULNERABILITY" | sort timestamp asc | summarize status = takeLast(vulnerability.resolution.status), by:{vulnerability.display_id} | filter status == "OPEN" | summarize c = count()')
T10N=$(q 'fetch security.events, from:now()-24h | filter event.type == "VULNERABILITY_STATE_REPORT_EVENT" and event.level == "VULNERABILITY" | summarize c = count()')

echo "ground truth: t11 bizevents top providers"
T11=$(q 'fetch bizevents, from:now()-24h | summarize c = count(), by:{event.provider} | sort c desc | limit 3')

echo "ground truth: t12 davis event categories"
T12=$(q 'fetch dt.davis.events, from:now()-24h | filter isNotNull(event.category) | summarize c = count(), by:{event.category} | sort c desc | limit 2')

echo "ground truth: t13 azure/gcp node counts"
T13A=$(q 'smartscapeNodes "*" | filter startsWith(type, "AZURE_") | summarize c = count()')
T13G=$(q 'smartscapeNodes "*" | filter startsWith(type, "GCP_") | summarize c = count()')

echo "ground truth: t14 top log buckets"
T14=$(q 'fetch logs, from:now()-24h | summarize c = count(), by:{dt.system.bucket} | sort c desc | limit 2')

echo "ground truth: t15 hosts by OS"
T15=$(q 'smartscapeNodes "HOST" | fieldsAdd os.type | summarize c = count(), by:{os.type} | sort c desc')

echo "ground truth: t16 postgres instances"
T16=$(q 'smartscapeNodes "DB_INSTANCE_POSTGRES" | summarize c = count()')

echo "ground truth: t17 slowest root span"
T17=$(q 'fetch spans, from:now()-24h | filter request.is_root_span == true | sort duration desc | limit 1 | fieldsAdd svc = getNodeName(dt.smartscape.service) | fields svc, duration')

echo "ground truth: t18 EC2 + k8s namespaces"
T18E=$(q 'smartscapeNodes "*" | filter type == "AWS_EC2_INSTANCE" | summarize c = count()')
T18N=$(q 'smartscapeNodes "*" | filter type == "K8S_NAMESPACE" | summarize c = count()')

echo "ground truth: t19 distinct davis events + naive generic-stream rows"
T19=$(q 'fetch dt.davis.events, from:now()-24h | summarize c = countDistinct(event.id)')
T19N=$(q 'fetch events, from:now()-24h | summarize c = count()')
T19F=$(q 'fetch events, from:now()-24h | filter event.kind == "DAVIS_EVENT" | summarize c = count()')

echo "ground truth: t20 distinct traces through trap service (hop ∪ service.name)"
SCOPE_SPANS=$("$BIN" --context "$EVAL_CONTEXT" resolve scope "$EVAL_TRAP_SERVICE" --for spans --plain --no-agent 2>/dev/null)
T20=$(q "fetch spans, from:now()-4h | filter ($SCOPE_SPANS) or service.name == \"$EVAL_TRAP_SERVICE\" | summarize c = countDistinct(trace.id)")

echo "ground truth: t21 pods behind trap service (runs_on hop, all instances)"
SVCIDS=$(q "smartscapeNodes \"SERVICE\" | filter name == \"$EVAL_TRAP_SERVICE\" | fields id" \
    | python3 -c 'import json,sys; print(", ".join("toSmartscapeId(\"%s\")" % r["id"] for r in json.load(sys.stdin)["records"]))')
T21=$(q "smartscapeEdges \"runs_on\" | filter in(source_id, {$SVCIDS}) | fieldsAdd tid = toString(target_id) | filter startsWith(tid, \"K8S_POD-\") | summarize c = countDistinct(tid)")

echo "ground truth: t22 top ERROR-log namespaces"
T22=$(q 'fetch logs, from:now()-6h | filter status == "ERROR" | summarize c = count(), by:{k8s.namespace.name} | sort c desc | limit 3')

echo "ground truth: t23 logs in the [24h,12h] ago window"
T23=$(q 'fetch logs, from:now()-24h, to:now()-12h | summarize c = count()')

echo "ground truth: t24 fleet-average host CPU over 3h"
T24=$(q 'timeseries cpu = avg(dt.host.cpu.usage), from:now()-3h | fields a = arrayAvg(cpu)')

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
t7 = rows('''$T7''')
t7a = rows('''$T7A''')
t8 = rows('''$T8''')
t9 = rows('''$T9''')
t10 = rows('''$T10''')
t10n = rows('''$T10N''')
t11 = rows('''$T11''')
t12 = rows('''$T12''')
t13a = rows('''$T13A''')
t13g = rows('''$T13G''')
t14 = rows('''$T14''')
t15 = rows('''$T15''')
t16 = rows('''$T16''')
t17 = rows('''$T17''')
t18e = rows('''$T18E''')
t18n = rows('''$T18N''')
t19 = rows('''$T19''')
t19n = rows('''$T19N''')
t19f = rows('''$T19F''')
t20 = rows('''$T20''')
t21 = rows('''$T21''')
t22 = [r for r in rows('''$T22''') if r.get("k8s.namespace.name")]
t23 = rows('''$T23''')
t24 = rows('''$T24''')

def c0(rs):
    return int(rs[0]["c"]) if rs else 0

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
    "t7": {"problems_7d": c0(t7), "active_now": c0(t7a)},
    "t8": {"top": [{"host": r["name"], "cpu": float(r["a"])} for r in t8]},
    "t9": {"pods": int(t9[0]["pods"]) if t9 else 0,
           "total": int(t9[0]["total"]) if t9 else 0},
    "t10": {"open": c0(t10), "naive": c0(t10n)},
    "t11": {"top": [{"provider": r["event.provider"], "count": int(r["c"])} for r in t11]},
    "t12": {"top": [{"category": r["event.category"], "count": int(r["c"])} for r in t12]},
    "t13": {"azure_nodes": c0(t13a), "gcp_nodes": c0(t13g)},
    "t14": {"top": [{"bucket": r["dt.system.bucket"], "count": int(r["c"])} for r in t14]},
    "t15": {"host_count": sum(int(r["c"]) for r in t15),
            "dominant_os": t15[0]["os.type"] if t15 else ""},
    "t16": {"count": c0(t16)},
    "t17": {"duration_ms": int(t17[0]["duration"]) / 1e6 if t17 else 0,
            "service": t17[0]["svc"] if t17 else ""},
    "t18": {"ec2": c0(t18e), "namespaces": c0(t18n)},
    "t19": {"events": c0(t19), "naive_rows": c0(t19n), "naive_filtered": c0(t19f)},
    "t20": {"traces": c0(t20)},
    "t21": {"pods": c0(t21)},
    "t22": {"top": [{"namespace": r["k8s.namespace.name"], "count": int(r["c"])} for r in t22]},
    "t23": {"count": c0(t23)},
    "t24": {"cpu": float(t24[0]["a"]) if t24 else 0.0},
}
path = sys.argv[1] + "/ground-truth.json"
with open(path, "w") as f:
    json.dump(gt, f, indent=2)
print("wrote", path)
print(json.dumps(gt, indent=2))
PYEOF
