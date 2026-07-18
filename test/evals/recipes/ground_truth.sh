#!/usr/bin/env bash
# Establishes ground truth by direct queries against the tenant, using
# bin/dtctl-recipes. Writes <rundir>/<out> (default ground-truth.json).
#
#   ./ground_truth.sh <rundir> [--out ground-truth-post.json] \
#                     [--sets dev,holdout] [--tasks "t7 t8 h4"]
#
# --tasks limits measurement to the listed tasks (default: all in the chosen
# sets). This is the scan-cost control for big tenants: full-log-scan GTs
# (t5/t14/t23/...) are prohibitively expensive on petabyte-scale log stores,
# so a batch running only census/metric tasks must not pay for them.
#
# Ground truth is measured fresh per batch because tasks use relative time
# windows. run.sh measures it BEFORE and AFTER each batch (pre/post
# envelope): score.py accepts an answer matching either measurement, which
# absorbs tenant drift during the batch without post-hoc tolerance widening.
#
# The t5/t20 trap scope is derived from RAW smartscape queries (SERVICE
# nodes -> runs_on edges -> K8S_POD names), NOT from `dtctl resolve scope` —
# the feature under eval must not define its own ground truth. The
# resolve-scope variant is still measured as a cross-check; a >10%
# disagreement prints a WARNING and is recorded in the GT file.
set -euo pipefail
cd "$(dirname "$0")"
RUNDIR=${1:?usage: ground_truth.sh <rundir> [--out <file>] [--sets dev,holdout] [--tasks "..."]}
shift
OUT=ground-truth.json
SETS=dev,holdout
TASKS_FILTER=""
while [ $# -gt 0 ]; do
    case $1 in
        --out) OUT=$2; shift 2 ;;
        --sets) SETS=$2; shift 2 ;;
        --tasks) TASKS_FILTER=$2; shift 2 ;;
        *) echo "unknown arg: $1" >&2; exit 2 ;;
    esac
done
: "${EVAL_CONTEXT:?source env.sh first}"
: "${EVAL_TRAP_SERVICE:?source env.sh first}"
BIN=$PWD/bin/dtctl-recipes
[ -x "$BIN" ] || { echo "run ./build.sh first" >&2; exit 1; }
mkdir -p "$RUNDIR"

q() { "$BIN" --context "$EVAL_CONTEXT" query "$1" -o json --plain --no-agent 2>/dev/null; }

# want <task> — is this task selected? (set membership AND --tasks filter)
want() {
    case $1 in
        t*) case ",$SETS," in *,dev,*) ;; *) return 1 ;; esac ;;
        h*) case ",$SETS," in *,holdout,*) ;; *) return 1 ;; esac ;;
    esac
    [ -z "$TASKS_FILTER" ] && return 0
    case " $TASKS_FILTER " in *" $1 "*) return 0 ;; *) return 1 ;; esac
}

# All vars default empty; an unmeasured task contributes no GT keys.
T1= T2= T3= T4= T5= T5R= T5N= T6= T7= T7A= T8= T9= T10= T10N= T11= T12=
T13A= T13G= T14= T15= T16= T17= T18E= T18N= T19= T19N= T19F= T20= T20R=
T21= T22= T23= T24=
H1= H2= H3= H4= H5= H6= H7= H8S= H8L=
SVCIDS= PODNAMES=

if want t1; then
    echo "ground truth: t1 error sources"
    T1=$(q 'fetch logs, from:now()-1h | filter loglevel == "ERROR" | summarize c = count(), by:{k8s.container.name} | sort c desc | limit 5')
fi
if want t2; then
    echo "ground truth: t2 p95 by service entity"
    T2=$(q 'fetch spans, from:now()-1h | filter isNotNull(dt.smartscape.service) | summarize p95 = percentile(duration, 95), by:{dt.smartscape.service} | sort p95 desc | limit 3 | fieldsAdd name = getNodeName(dt.smartscape.service) | fields name, p95')
fi
if want t3; then
    echo "ground truth: t3 RUM volume"
    T3=$(q 'fetch user.events, from:now()-24h | summarize c = count()')
fi
if want t4; then
    echo "ground truth: t4 GenAI tokens"
    T4=$(q 'fetch spans, from:now()-24h | filter isNotNull(gen_ai.request.model) | summarize t = sum(gen_ai.usage.input_tokens), by:{gen_ai.request.model} | sort t desc | limit 3')
fi

if want t5 || want t20 || want t21; then
    echo "ground truth: t5/t20/t21 trap scope via raw smartscape (independent of resolve scope)"
    SVCIDS=$(q "smartscapeNodes \"SERVICE\" | filter name == \"$EVAL_TRAP_SERVICE\" | fields id" \
        | python3 -c 'import json,sys; print(", ".join("toSmartscapeId(\"%s\")" % r["id"] for r in json.load(sys.stdin)["records"]))')
    PODIDS=$(q "smartscapeEdges \"runs_on\" | filter in(source_id, {$SVCIDS}) | fieldsAdd tid = toString(target_id) | filter startsWith(tid, \"K8S_POD-\") | dedup tid | fields tid" \
        | python3 -c 'import json,sys; print(", ".join("\"%s\"" % r["tid"] for r in json.load(sys.stdin)["records"]))')
    PODNAMES=$(q "smartscapeNodes \"K8S_POD\" | fieldsAdd sid = toString(id) | filter in(sid, {$PODIDS}) | fields name" \
        | python3 -c 'import json,sys; print(", ".join("\"%s\"" % r["name"] for r in json.load(sys.stdin)["records"]))')
fi

if want t5; then
    echo "ground truth: t5 complete count (raw pod scope + service.name union)"
    T5=$(q "fetch logs, from:now()-2h | filter in(k8s.pod.name, {$PODNAMES}) or service.name == \"$EVAL_TRAP_SERVICE\" | summarize c = count()")

    echo "ground truth: t5 cross-check (resolve scope variant)"
    SCOPE=$("$BIN" --context "$EVAL_CONTEXT" resolve scope "$EVAL_TRAP_SERVICE" --for logs --plain --no-agent 2>/dev/null || true)
    if [ -n "$SCOPE" ]; then
        T5R=$(q "fetch logs, from:now()-2h | filter ($SCOPE) or service.name == \"$EVAL_TRAP_SERVICE\" | summarize c = count()")
    fi

    echo "ground truth: t5 naive (entity-stamped) count"
    IDS=$(q "smartscapeNodes \"*\" | filter name == \"$EVAL_TRAP_SERVICE\" and type == \"SERVICE\" | fields id" \
        | python3 -c 'import json,sys; print(", ".join("toSmartscapeId(\"%s\")" % r["id"] for r in json.load(sys.stdin)["records"]))')
    T5N=$(q "fetch logs, from:now()-2h | filter in(dt.smartscape.service, {$IDS}) | summarize c = count()")
fi

if want t6; then
    echo "ground truth: t6 security findings"
    T6=$(q 'fetch security.events, from:now()-7d | filter in(event.type, {"DETECTION_FINDING","COMPLIANCE_FINDING"}) | summarize c = count(), by:{event.type}')
fi
if want t7; then
    echo "ground truth: t7 davis problems (distinct + active)"
    T7=$(q 'fetch dt.davis.problems, from:now()-7d | summarize c = countDistinctExact(display_id)')
    T7A=$(q 'fetch dt.davis.problems, from:now()-7d | filter not(dt.davis.is_duplicate) | summarize status = takeLast(event.status), by:{display_id} | filter status == "ACTIVE" | summarize c = count()')
fi
if want t8; then
    echo "ground truth: t8 top-CPU host"
    T8=$(q 'timeseries cpu = avg(dt.host.cpu.usage), by:{dt.smartscape.host}, from:now()-1h | fieldsAdd name = getNodeName(dt.smartscape.host), a = arrayAvg(cpu) | sort a desc | limit 3 | fields name, a')
fi
if want t9; then
    echo "ground truth: t9 OOM-killed pods 7d"
    T9=$(q 'timeseries oom = sum(dt.kubernetes.container.oom_kills), by:{k8s.pod.name}, from:now()-7d | fieldsAdd t = arraySum(oom) | filter t > 0 | summarize pods = count(), total = sum(t)')
fi
if want t10; then
    echo "ground truth: t10 open vulnerabilities (latest state) + naive event rows"
    T10=$(q 'fetch security.events, from:now()-24h | filter event.type == "VULNERABILITY_STATE_REPORT_EVENT" and event.level == "VULNERABILITY" | sort timestamp asc | summarize status = takeLast(vulnerability.resolution.status), by:{vulnerability.display_id} | filter status == "OPEN" | summarize c = count()')
    T10N=$(q 'fetch security.events, from:now()-24h | filter event.type == "VULNERABILITY_STATE_REPORT_EVENT" and event.level == "VULNERABILITY" | summarize c = count()')
fi
if want t11; then
    echo "ground truth: t11 bizevents top providers"
    T11=$(q 'fetch bizevents, from:now()-24h | summarize c = count(), by:{event.provider} | sort c desc | limit 3')
fi
if want t12; then
    echo "ground truth: t12 davis event categories"
    T12=$(q 'fetch dt.davis.events, from:now()-24h | filter isNotNull(event.category) | summarize c = count(), by:{event.category} | sort c desc | limit 2')
fi
if want t13; then
    echo "ground truth: t13 azure/gcp node counts"
    T13A=$(q 'smartscapeNodes "*" | filter startsWith(type, "AZURE_") | summarize c = count()')
    T13G=$(q 'smartscapeNodes "*" | filter startsWith(type, "GCP_") | summarize c = count()')
fi
if want t14; then
    echo "ground truth: t14 top log buckets"
    T14=$(q 'fetch logs, from:now()-24h | summarize c = count(), by:{dt.system.bucket} | sort c desc | limit 2')
fi
if want t15; then
    echo "ground truth: t15 hosts by OS"
    T15=$(q 'smartscapeNodes "HOST" | fieldsAdd os.type | summarize c = count(), by:{os.type} | sort c desc')
fi
if want t16; then
    echo "ground truth: t16 postgres instances"
    T16=$(q 'smartscapeNodes "DB_INSTANCE_POSTGRES" | summarize c = count()')
fi
if want t17; then
    echo "ground truth: t17 slowest root span"
    T17=$(q 'fetch spans, from:now()-24h | filter request.is_root_span == true | sort duration desc | limit 1 | fieldsAdd svc = getNodeName(dt.smartscape.service) | fields svc, duration')
fi
if want t18; then
    echo "ground truth: t18 EC2 + k8s namespaces"
    T18E=$(q 'smartscapeNodes "*" | filter type == "AWS_EC2_INSTANCE" | summarize c = count()')
    T18N=$(q 'smartscapeNodes "*" | filter type == "K8S_NAMESPACE" | summarize c = count()')
fi
if want t19; then
    echo "ground truth: t19 distinct davis events + naive generic-stream rows"
    T19=$(q 'fetch dt.davis.events, from:now()-24h | summarize c = countDistinct(event.id)')
    T19N=$(q 'fetch events, from:now()-24h | summarize c = count()')
    T19F=$(q 'fetch events, from:now()-24h | filter event.kind == "DAVIS_EVENT" | summarize c = count()')
fi
if want t20; then
    echo "ground truth: t20 distinct traces through trap service (raw pod scope + service.name)"
    T20=$(q "fetch spans, from:now()-4h | filter in(k8s.pod.name, {$PODNAMES}) or service.name == \"$EVAL_TRAP_SERVICE\" | summarize c = countDistinct(trace.id)")

    echo "ground truth: t20 cross-check (resolve scope variant)"
    SCOPE_SPANS=$("$BIN" --context "$EVAL_CONTEXT" resolve scope "$EVAL_TRAP_SERVICE" --for spans --plain --no-agent 2>/dev/null || true)
    if [ -n "$SCOPE_SPANS" ]; then
        T20R=$(q "fetch spans, from:now()-4h | filter ($SCOPE_SPANS) or service.name == \"$EVAL_TRAP_SERVICE\" | summarize c = countDistinct(trace.id)")
    fi
fi
if want t21; then
    echo "ground truth: t21 pods behind trap service (runs_on hop, liveness-filtered)"
    # Smartscape retains superseded pods for a while (a rollout leaves the old
    # replicaset's pods as nodes AND edge targets), so a pure edge/node count
    # answers "recently backing", not "currently backing" (observed live: 10
    # smartscape pods, 6 actually running). Liveness = the pod emitted
    # container-CPU datapoints in the last 30m — an independent metric stream.
    T21=$(q "timeseries cpu = avg(dt.kubernetes.container.cpu_usage), by:{k8s.pod.name}, from:now()-30m | filter in(k8s.pod.name, {$PODNAMES}) | summarize c = count()")
fi
if want t22; then
    echo "ground truth: t22 top ERROR-log namespaces"
    T22=$(q 'fetch logs, from:now()-6h | filter status == "ERROR" | summarize c = count(), by:{k8s.namespace.name} | sort c desc | limit 3')
fi
if want t23; then
    echo "ground truth: t23 logs in the [24h,12h] ago window"
    T23=$(q 'fetch logs, from:now()-24h, to:now()-12h | summarize c = count()')
fi
if want t24; then
    echo "ground truth: t24 fleet-average host CPU over 3h"
    T24=$(q 'timeseries cpu = avg(dt.host.cpu.usage), from:now()-3h | fields a = arrayAvg(cpu)')
fi

if want h1; then
    echo "ground truth: h1 WARN-log containers 6h"
    H1=$(q 'fetch logs, from:now()-6h | filter loglevel == "WARN" and isNotNull(k8s.container.name) | summarize c = count(), by:{k8s.container.name} | sort c desc | limit 3')
fi
if want h2; then
    echo "ground truth: h2 distinct traces + spans 2h"
    H2=$(q 'fetch spans, from:now()-2h | summarize traces = countDistinct(trace.id), total = count()')
fi
if want h3; then
    echo "ground truth: h3 top RUM countries 24h"
    H3=$(q 'fetch user.events, from:now()-24h | filter isNotNull(geo.country.iso_code) | summarize c = count(), by:{geo.country.iso_code} | sort c desc | limit 3')
fi
if want h4; then
    echo "ground truth: h4 lowest free-disk hosts 1h"
    H4=$(q 'timeseries free = avg(dt.host.disk.free), by:{dt.smartscape.host}, from:now()-1h | fieldsAdd name = getNodeName(dt.smartscape.host), a = arrayAvg(free) | sort a asc | limit 3 | fields name, a')
fi
if want h5; then
    echo "ground truth: h5 longest closed problems 7d"
    H5=$(q 'fetch dt.davis.problems, from:now()-7d | filter not(dt.davis.is_duplicate) and event.status == "CLOSED" | summarize dur = takeLast(resolved_problem_duration), by:{display_id} | sort dur desc | limit 3')
fi
if want h6; then
    echo "ground truth: h6 p50 span duration 1h"
    H6=$(q 'fetch spans, from:now()-1h | summarize p50 = percentile(duration, 50)')
fi
if want h7; then
    echo "ground truth: h7 top log bucket by stored records"
    H7=$(q 'fetch dt.system.buckets | filter dt.system.table == "logs" | sort records desc | limit 3 | fields name, records, retention_days')
fi
if want h8; then
    echo "ground truth: h8 synthetic + lambda presence"
    H8S=$(q 'smartscapeNodes "*" | filter contains(type, "SYNTHETIC") | summarize c = count()')
    H8L=$(q 'smartscapeNodes "*" | filter type == "AWS_LAMBDA_FUNCTION" | summarize c = count()')
fi

python3 - "$RUNDIR/$OUT" "$SETS" "$TASKS_FILTER" <<PYEOF
import json, sys, datetime

def rows(s):
    return json.loads(s)["records"] if s.strip() else []

def c0(rs):
    return int(rs[0]["c"]) if rs else 0

sets = sys.argv[2].split(",")
tasks_filter = sys.argv[3].split()

def want(task):
    if task.startswith("t") and "dev" not in sets:
        return False
    if task.startswith("h") and "holdout" not in sets:
        return False
    return not tasks_filter or task in tasks_filter

gt = {"at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
      "sets": sets, "tasks_filter": tasks_filter or None}

if want("t1"):
    t1 = [r for r in rows('''$T1''') if r.get("k8s.container.name")]
    gt["t1"] = {"top_source": t1[0]["k8s.container.name"], "count": int(t1[0]["c"]),
                "sources": [{"name": r["k8s.container.name"], "count": int(r["c"])} for r in t1]} if t1 else None
if want("t2"):
    t2 = rows('''$T2''')
    gt["t2"] = {"top": [{"service": r["name"], "p95_ms": int(r["p95"]) / 1e6} for r in t2]}
if want("t3"):
    t3 = rows('''$T3''')
    gt["t3"] = {"events_24h": int(t3[0]["c"]) if t3 else 0}
if want("t4"):
    t4 = rows('''$T4''')
    gt["t4"] = {"model": t4[0]["gen_ai.request.model"], "input_tokens": int(t4[0]["t"])} if t4 else None
if want("t5"):
    t5, t5r, t5n = rows('''$T5'''), rows('''$T5R'''), rows('''$T5N''')
    gt["t5"] = {"count": c0(t5), "naive_count": c0(t5n),
                "resolve_count": int(t5r[0]["c"]) if t5r else None}
if want("t6"):
    t6 = rows('''$T6''')
    det = sum(int(r["c"]) for r in t6 if r["event.type"] == "DETECTION_FINDING")
    comp = sum(int(r["c"]) for r in t6 if r["event.type"] == "COMPLIANCE_FINDING")
    gt["t6"] = {"attack_detections_present": det > 0, "detection_count": det,
                "compliance_findings_7d": comp}
if want("t7"):
    gt["t7"] = {"problems_7d": c0(rows('''$T7''')), "active_now": c0(rows('''$T7A'''))}
if want("t8"):
    gt["t8"] = {"top": [{"host": r["name"], "cpu": float(r["a"])} for r in rows('''$T8''')]}
if want("t9"):
    t9 = rows('''$T9''')
    gt["t9"] = {"pods": int(t9[0]["pods"]) if t9 else 0,
                "total": int(t9[0]["total"]) if t9 else 0}
if want("t10"):
    gt["t10"] = {"open": c0(rows('''$T10''')), "naive": c0(rows('''$T10N'''))}
if want("t11"):
    gt["t11"] = {"top": [{"provider": r["event.provider"], "count": int(r["c"])} for r in rows('''$T11''')]}
if want("t12"):
    gt["t12"] = {"top": [{"category": r["event.category"], "count": int(r["c"])} for r in rows('''$T12''')]}
if want("t13"):
    gt["t13"] = {"azure_nodes": c0(rows('''$T13A''')), "gcp_nodes": c0(rows('''$T13G'''))}
if want("t14"):
    gt["t14"] = {"top": [{"bucket": r["dt.system.bucket"], "count": int(r["c"])} for r in rows('''$T14''')]}
if want("t15"):
    t15 = rows('''$T15''')
    gt["t15"] = {"host_count": sum(int(r["c"]) for r in t15),
                 "dominant_os": t15[0]["os.type"] if t15 else ""}
if want("t16"):
    gt["t16"] = {"count": c0(rows('''$T16'''))}
if want("t17"):
    t17 = rows('''$T17''')
    gt["t17"] = {"duration_ms": int(t17[0]["duration"]) / 1e6 if t17 else 0,
                 "service": t17[0]["svc"] if t17 else ""}
if want("t18"):
    gt["t18"] = {"ec2": c0(rows('''$T18E''')), "namespaces": c0(rows('''$T18N'''))}
if want("t19"):
    gt["t19"] = {"events": c0(rows('''$T19''')), "naive_rows": c0(rows('''$T19N''')),
                 "naive_filtered": c0(rows('''$T19F'''))}
if want("t20"):
    t20, t20r = rows('''$T20'''), rows('''$T20R''')
    gt["t20"] = {"traces": c0(t20), "resolve_traces": int(t20r[0]["c"]) if t20r else None}
if want("t21"):
    gt["t21"] = {"pods": c0(rows('''$T21'''))}
if want("t22"):
    t22 = [r for r in rows('''$T22''') if r.get("k8s.namespace.name")]
    gt["t22"] = {"top": [{"namespace": r["k8s.namespace.name"], "count": int(r["c"])} for r in t22]}
if want("t23"):
    gt["t23"] = {"count": c0(rows('''$T23'''))}
if want("t24"):
    t24 = rows('''$T24''')
    gt["t24"] = {"cpu": float(t24[0]["a"]) if t24 else 0.0}

# Cross-check: raw-smartscape GT vs the resolve-scope-derived variant.
for task, key_a, key_b in (("t5", "count", "resolve_count"), ("t20", "traces", "resolve_traces")):
    if gt.get(task):
        a, b = gt[task][key_a], gt[task][key_b]
        if b is None:
            print(f"WARNING: {task} resolve-scope cross-check unavailable")
        elif abs(a - b) > 0.1 * max(a, b, 1):
            print(f"WARNING: {task} GT disagreement: raw-smartscape {a} vs resolve-scope {b} "
                  f"— investigate before trusting {task} verdicts")

if want("h1"):
    gt["h1"] = {"top": [{"container": r["k8s.container.name"], "count": int(r["c"])} for r in rows('''$H1''')]}
if want("h2"):
    h2 = rows('''$H2''')
    gt["h2"] = {"traces": int(h2[0]["traces"]) if h2 else 0,
                "spans": int(h2[0]["total"]) if h2 else 0}
if want("h3"):
    gt["h3"] = {"top": [{"country": r["geo.country.iso_code"], "count": int(r["c"])} for r in rows('''$H3''')]}
if want("h4"):
    gt["h4"] = {"top": [{"host": r["name"], "free": float(r["a"])} for r in rows('''$H4''')]}
if want("h5"):
    gt["h5"] = {"top": [{"problem": r["display_id"], "minutes": int(r["dur"]) / 6e10} for r in rows('''$H5''')]}
if want("h6"):
    h6 = rows('''$H6''')
    gt["h6"] = {"p50_ms": int(h6[0]["p50"]) / 1e6 if h6 else 0}
if want("h7"):
    gt["h7"] = {"top": [{"bucket": r["name"], "records": int(r["records"]),
                         "retention_days": int(r["retention_days"])} for r in rows('''$H7''')]}
if want("h8"):
    gt["h8"] = {"synthetic_nodes": c0(rows('''$H8S''')), "lambda_functions": c0(rows('''$H8L'''))}

path = sys.argv[1]
with open(path, "w") as f:
    json.dump(gt, f, indent=2)
print("wrote", path)
PYEOF
