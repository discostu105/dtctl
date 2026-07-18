#!/usr/bin/env bash
# Establishes ground truth by direct queries against the tenant, using
# bin/dtctl-recipes. Writes <rundir>/<out> (default ground-truth.json).
#
#   ./ground_truth.sh <rundir> [--out ground-truth-post.json] [--sets dev,holdout]
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
RUNDIR=${1:?usage: ground_truth.sh <rundir> [--out <file>] [--sets dev,holdout]}
shift
OUT=ground-truth.json
SETS=dev,holdout
while [ $# -gt 0 ]; do
    case $1 in
        --out) OUT=$2; shift 2 ;;
        --sets) SETS=$2; shift 2 ;;
        *) echo "unknown arg: $1" >&2; exit 2 ;;
    esac
done
: "${EVAL_CONTEXT:?source env.sh first}"
: "${EVAL_TRAP_SERVICE:?source env.sh first}"
BIN=$PWD/bin/dtctl-recipes
[ -x "$BIN" ] || { echo "run ./build.sh first" >&2; exit 1; }
mkdir -p "$RUNDIR"

q() { "$BIN" --context "$EVAL_CONTEXT" query "$1" -o json --plain --no-agent 2>/dev/null; }

# All vars default empty; an unmeasured set contributes no GT keys.
T1= T2= T3= T4= T5= T5R= T5N= T6= T7= T7A= T8= T9= T10= T10N= T11= T12=
T13A= T13G= T14= T15= T16= T17= T18E= T18N= T19= T19N= T19F= T20= T20R=
T21= T22= T23= T24=
H1= H2= H3= H4= H5= H6= H7= H8S= H8L=

case ",$SETS," in *,dev,*)
    echo "ground truth: t1 error sources"
    T1=$(q 'fetch logs, from:now()-1h | filter loglevel == "ERROR" | summarize c = count(), by:{k8s.container.name} | sort c desc | limit 5')

    echo "ground truth: t2 p95 by service entity"
    T2=$(q 'fetch spans, from:now()-1h | filter isNotNull(dt.smartscape.service) | summarize p95 = percentile(duration, 95), by:{dt.smartscape.service} | sort p95 desc | limit 3 | fieldsAdd name = getNodeName(dt.smartscape.service) | fields name, p95')

    echo "ground truth: t3 RUM volume"
    T3=$(q 'fetch user.events, from:now()-24h | summarize c = count()')

    echo "ground truth: t4 GenAI tokens"
    T4=$(q 'fetch spans, from:now()-24h | filter isNotNull(gen_ai.request.model) | summarize t = sum(gen_ai.usage.input_tokens), by:{gen_ai.request.model} | sort t desc | limit 3')

    echo "ground truth: t5/t20 trap scope via raw smartscape (independent of resolve scope)"
    SVCIDS=$(q "smartscapeNodes \"SERVICE\" | filter name == \"$EVAL_TRAP_SERVICE\" | fields id" \
        | python3 -c 'import json,sys; print(", ".join("toSmartscapeId(\"%s\")" % r["id"] for r in json.load(sys.stdin)["records"]))')
    PODIDS=$(q "smartscapeEdges \"runs_on\" | filter in(source_id, {$SVCIDS}) | fieldsAdd tid = toString(target_id) | filter startsWith(tid, \"K8S_POD-\") | dedup tid | fields tid" \
        | python3 -c 'import json,sys; print(", ".join("\"%s\"" % r["tid"] for r in json.load(sys.stdin)["records"]))')
    PODNAMES=$(q "smartscapeNodes \"K8S_POD\" | fieldsAdd sid = toString(id) | filter in(sid, {$PODIDS}) | fields name" \
        | python3 -c 'import json,sys; print(", ".join("\"%s\"" % r["name"] for r in json.load(sys.stdin)["records"]))')

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

    echo "ground truth: t20 distinct traces through trap service (raw pod scope + service.name)"
    T20=$(q "fetch spans, from:now()-4h | filter in(k8s.pod.name, {$PODNAMES}) or service.name == \"$EVAL_TRAP_SERVICE\" | summarize c = countDistinct(trace.id)")

    echo "ground truth: t20 cross-check (resolve scope variant)"
    SCOPE_SPANS=$("$BIN" --context "$EVAL_CONTEXT" resolve scope "$EVAL_TRAP_SERVICE" --for spans --plain --no-agent 2>/dev/null || true)
    if [ -n "$SCOPE_SPANS" ]; then
        T20R=$(q "fetch spans, from:now()-4h | filter ($SCOPE_SPANS) or service.name == \"$EVAL_TRAP_SERVICE\" | summarize c = countDistinct(trace.id)")
    fi

    echo "ground truth: t21 pods behind trap service (runs_on hop, all instances)"
    T21=$(q "smartscapeEdges \"runs_on\" | filter in(source_id, {$SVCIDS}) | fieldsAdd tid = toString(target_id) | filter startsWith(tid, \"K8S_POD-\") | summarize c = countDistinct(tid)")

    echo "ground truth: t22 top ERROR-log namespaces"
    T22=$(q 'fetch logs, from:now()-6h | filter status == "ERROR" | summarize c = count(), by:{k8s.namespace.name} | sort c desc | limit 3')

    echo "ground truth: t23 logs in the [24h,12h] ago window"
    T23=$(q 'fetch logs, from:now()-24h, to:now()-12h | summarize c = count()')

    echo "ground truth: t24 fleet-average host CPU over 3h"
    T24=$(q 'timeseries cpu = avg(dt.host.cpu.usage), from:now()-3h | fields a = arrayAvg(cpu)')
    ;;
esac

case ",$SETS," in *,holdout,*)
    echo "ground truth: h1 WARN-log containers 6h"
    H1=$(q 'fetch logs, from:now()-6h | filter loglevel == "WARN" and isNotNull(k8s.container.name) | summarize c = count(), by:{k8s.container.name} | sort c desc | limit 3')

    echo "ground truth: h2 distinct traces + spans 2h"
    H2=$(q 'fetch spans, from:now()-2h | summarize traces = countDistinct(trace.id), total = count()')

    echo "ground truth: h3 top RUM countries 24h"
    H3=$(q 'fetch user.events, from:now()-24h | filter isNotNull(geo.country.iso_code) | summarize c = count(), by:{geo.country.iso_code} | sort c desc | limit 3')

    echo "ground truth: h4 lowest free-disk hosts 1h"
    H4=$(q 'timeseries free = avg(dt.host.disk.free), by:{dt.smartscape.host}, from:now()-1h | fieldsAdd name = getNodeName(dt.smartscape.host), a = arrayAvg(free) | sort a asc | limit 3 | fields name, a')

    echo "ground truth: h5 longest closed problems 7d"
    H5=$(q 'fetch dt.davis.problems, from:now()-7d | filter not(dt.davis.is_duplicate) and event.status == "CLOSED" | summarize dur = takeLast(resolved_problem_duration), by:{display_id} | sort dur desc | limit 3')

    echo "ground truth: h6 p50 span duration 1h"
    H6=$(q 'fetch spans, from:now()-1h | summarize p50 = percentile(duration, 50)')

    echo "ground truth: h7 top log bucket by stored records"
    H7=$(q 'fetch dt.system.buckets | filter dt.system.table == "logs" | sort records desc | limit 3 | fields name, records, retention_days')

    echo "ground truth: h8 synthetic + lambda presence"
    H8S=$(q 'smartscapeNodes "*" | filter contains(type, "SYNTHETIC") | summarize c = count()')
    H8L=$(q 'smartscapeNodes "*" | filter type == "AWS_LAMBDA_FUNCTION" | summarize c = count()')
    ;;
esac

python3 - "$RUNDIR/$OUT" "$SETS" <<PYEOF
import json, sys, datetime

def rows(s):
    return json.loads(s)["records"] if s.strip() else []

def c0(rs):
    return int(rs[0]["c"]) if rs else 0

sets = sys.argv[2].split(",")
gt = {"at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
      "sets": sets}

if "dev" in sets:
    t1 = [r for r in rows('''$T1''') if r.get("k8s.container.name")]
    t2 = rows('''$T2''')
    t3 = rows('''$T3''')
    t4 = rows('''$T4''')
    t5 = rows('''$T5''')
    t5r = rows('''$T5R''')
    t5n = rows('''$T5N''')
    t6 = rows('''$T6''')
    t7, t7a = rows('''$T7'''), rows('''$T7A''')
    t8 = rows('''$T8''')
    t9 = rows('''$T9''')
    t10, t10n = rows('''$T10'''), rows('''$T10N''')
    t11 = rows('''$T11''')
    t12 = rows('''$T12''')
    t13a, t13g = rows('''$T13A'''), rows('''$T13G''')
    t14 = rows('''$T14''')
    t15 = rows('''$T15''')
    t16 = rows('''$T16''')
    t17 = rows('''$T17''')
    t18e, t18n = rows('''$T18E'''), rows('''$T18N''')
    t19, t19n, t19f = rows('''$T19'''), rows('''$T19N'''), rows('''$T19F''')
    t20, t20r = rows('''$T20'''), rows('''$T20R''')
    t21 = rows('''$T21''')
    t22 = [r for r in rows('''$T22''') if r.get("k8s.namespace.name")]
    t23 = rows('''$T23''')
    t24 = rows('''$T24''')

    det = sum(int(r["c"]) for r in t6 if r["event.type"] == "DETECTION_FINDING")
    comp = sum(int(r["c"]) for r in t6 if r["event.type"] == "COMPLIANCE_FINDING")

    gt.update({
        "t1": {"top_source": t1[0]["k8s.container.name"], "count": int(t1[0]["c"]),
               "sources": [{"name": r["k8s.container.name"], "count": int(r["c"])} for r in t1]},
        "t2": {"top": [{"service": r["name"], "p95_ms": int(r["p95"]) / 1e6} for r in t2]},
        "t3": {"events_24h": int(t3[0]["c"]) if t3 else 0},
        "t4": {"model": t4[0]["gen_ai.request.model"], "input_tokens": int(t4[0]["t"])} if t4 else None,
        "t5": {"count": int(t5[0]["c"]), "naive_count": int(t5n[0]["c"]) if t5n else 0,
               "resolve_count": int(t5r[0]["c"]) if t5r else None},
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
        "t20": {"traces": c0(t20), "resolve_traces": int(t20r[0]["c"]) if t20r else None},
        "t21": {"pods": c0(t21)},
        "t22": {"top": [{"namespace": r["k8s.namespace.name"], "count": int(r["c"])} for r in t22]},
        "t23": {"count": c0(t23)},
        "t24": {"cpu": float(t24[0]["a"]) if t24 else 0.0},
    })

    # Cross-check: raw-smartscape GT vs the resolve-scope-derived variant.
    for task, a, b in (("t5", gt["t5"]["count"], gt["t5"]["resolve_count"]),
                       ("t20", gt["t20"]["traces"], gt["t20"]["resolve_traces"])):
        if b is None:
            print(f"WARNING: {task} resolve-scope cross-check unavailable")
        elif abs(a - b) > 0.1 * max(a, b, 1):
            print(f"WARNING: {task} GT disagreement: raw-smartscape {a} vs resolve-scope {b} "
                  f"— investigate before trusting {task} verdicts")

if "holdout" in sets:
    h1 = rows('''$H1''')
    h2 = rows('''$H2''')
    h3 = rows('''$H3''')
    h4 = rows('''$H4''')
    h5 = rows('''$H5''')
    h6 = rows('''$H6''')
    h7 = rows('''$H7''')
    h8s, h8l = rows('''$H8S'''), rows('''$H8L''')

    gt.update({
        "h1": {"top": [{"container": r["k8s.container.name"], "count": int(r["c"])} for r in h1]},
        "h2": {"traces": int(h2[0]["traces"]) if h2 else 0,
               "spans": int(h2[0]["total"]) if h2 else 0},
        "h3": {"top": [{"country": r["geo.country.iso_code"], "count": int(r["c"])} for r in h3]},
        "h4": {"top": [{"host": r["name"], "free": float(r["a"])} for r in h4]},
        "h5": {"top": [{"problem": r["display_id"], "minutes": int(r["dur"]) / 6e10} for r in h5]},
        "h6": {"p50_ms": int(h6[0]["p50"]) / 1e6 if h6 else 0},
        "h7": {"top": [{"bucket": r["name"], "records": int(r["records"]),
                        "retention_days": int(r["retention_days"])} for r in h7]},
        "h8": {"synthetic_nodes": c0(h8s), "lambda_functions": c0(h8l)},
    })

path = sys.argv[1]
with open(path, "w") as f:
    json.dump(gt, f, indent=2)
print("wrote", path)
PYEOF
