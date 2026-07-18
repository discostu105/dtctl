#!/usr/bin/env bash
# Runs the recipes eval matrix: variants × tasks, one fresh headless claude
# agent per cell, each in an isolated workspace with a logging dtctl wrapper.
#
#   ./run.sh                     # all 5 variants × the DEV task set
#   ./run.sh -s all              # task set: dev (default) | holdout | all
#   ./run.sh -v recipes,base     # subset of variants
#   ./run.sh -t t5,h3            # explicit task subset (overrides -s)
#   ./run.sh -n 3                # 3 trial batches (dirs <batch>-r1..-r3)
#   ./run.sh -b 20260718-1200    # reuse an existing batch dir (re-run cells)
#
# Variants:
#   base            dtctl-main,    no dynatrace-for-ai skills
#   skills          dtctl-main,    dynatrace-for-ai skills
#   recipes         dtctl-recipes, no dynatrace-for-ai skills (+ recipe book)
#   recipes-skills  dtctl-recipes, dynatrace-for-ai skills    (+ recipe book)
#   head-base       dtctl-recipes, no skills, recipe book HIDDEN via a shadow
#                   XDG_CONFIG_HOME — isolates the HEAD binary's generic
#                   ergonomics (error UX, aliases, redirects) from the book;
#                   the recipes-vs-base comparison alone conflates the two.
#
# Task sets: t1-t24 are the DEV suite (optimization mining allowed); h1-h8
# are HELD OUT (see README — never mine optimizations from holdout runs).
#
# Skill visibility is controlled per run via an isolated CLAUDE_CONFIG_DIR
# (credentials copied in; dt-* skills symlinked in only for skills variants).
# Workspaces live OUTSIDE the repo so eval agents never see this project's
# CLAUDE.md and run output (tenant data) can never be committed.
#
# Each batch dir gets: manifest.json (pinned provenance: binaries, skills
# rev, claude CLI, model, book hash), ground-truth.json (measured BEFORE the
# runs) and ground-truth-post.json (AFTER — score.py accepts either, so
# tenant drift during the batch does not force tolerance widening).
set -euo pipefail
cd "$(dirname "$0")"
EVALDIR=$PWD

[ -f env.sh ] || { echo "copy env.example.sh to env.sh and fill it in" >&2; exit 1; }
# shellcheck disable=SC1091
source ./env.sh
: "${EVAL_CONTEXT:?}" "${EVAL_TRAP_SERVICE:?}"
EVAL_SKILLS_DIR=${EVAL_SKILLS_DIR:-$HOME/.agents/skills}
EVAL_MODEL=${EVAL_MODEL:-claude-sonnet-5}
EVAL_MAX_TURNS=${EVAL_MAX_TURNS:-50}
EVAL_PARALLEL=${EVAL_PARALLEL:-3}
RUNS_ROOT=${EVAL_RUNS_DIR:-$HOME/.cache/dtctl-recipes-evals}

VARIANTS="base skills recipes recipes-skills head-base"
DEV_TASKS="t1 t2 t3 t4 t5 t6 t7 t8 t9 t10 t11 t12 t13 t14 t15 t16 t17 t18 t19 t20 t21 t22 t23 t24"
HOLDOUT_TASKS="h1 h2 h3 h4 h5 h6 h7 h8"
TASKSET=dev
TASKS=
BATCH=$(date +%Y%m%d-%H%M%S)
BATCH_GIVEN=0
TRIALS=1
while getopts "v:t:s:b:n:" o; do
    case $o in
        v) VARIANTS=${OPTARG//,/ } ;;
        t) TASKS=${OPTARG//,/ } ;;
        s) TASKSET=$OPTARG ;;
        b) BATCH=$OPTARG; BATCH_GIVEN=1 ;;
        n) TRIALS=$OPTARG ;;
        *) exit 2 ;;
    esac
done
if [ -z "$TASKS" ]; then
    case $TASKSET in
        dev) TASKS=$DEV_TASKS ;;
        holdout) TASKS=$HOLDOUT_TASKS ;;
        all) TASKS="$DEV_TASKS $HOLDOUT_TASKS" ;;
        *) echo "unknown task set: $TASKSET (dev|holdout|all)" >&2; exit 2 ;;
    esac
fi
if [ "$TRIALS" -gt 1 ] && [ "$BATCH_GIVEN" = 1 ]; then
    echo "-n and -b are incompatible" >&2; exit 2
fi

# Which GT sets do the selected tasks need?
GTSETS=""
case " $TASKS " in *" t"*) GTSETS=dev ;; esac
case " $TASKS " in *" h"*) GTSETS=${GTSETS:+$GTSETS,}holdout ;; esac

[ -x bin/dtctl-recipes ] && [ -x bin/dtctl-main ] || { echo "run ./build.sh first" >&2; exit 1; }

# The recipes variants need a generated book for the context.
BOOK=${XDG_CONFIG_HOME:-$HOME/.config}/dtctl/recipes/$EVAL_CONTEXT.yaml
case " $VARIANTS " in *" recipes"*)
    if [ ! -f "$BOOK" ]; then
        echo "no recipe book at $BOOK — generate one first:" >&2
        echo "  bin/dtctl-recipes --context $EVAL_CONTEXT recipes discover --pack <pack.yaml>" >&2
        exit 1
    fi ;;
esac

write_manifest() { # $1 = rundir
    RUNDIR=$1 BOOK=$BOOK EVALDIR=$EVALDIR EVAL_SKILLS_DIR=$EVAL_SKILLS_DIR \
    EVAL_CONTEXT=$EVAL_CONTEXT EVAL_MODEL=$EVAL_MODEL EVAL_MAX_TURNS=$EVAL_MAX_TURNS \
    EVAL_PARALLEL=$EVAL_PARALLEL M_VARIANTS=$VARIANTS M_TASKS=$TASKS \
    M_SKILLS_REV=$(git -C "$EVAL_SKILLS_DIR" rev-parse HEAD 2>/dev/null || echo "not-a-git-repo") \
    M_HARNESS_REV=$(git rev-parse HEAD 2>/dev/null || echo unknown) \
    M_CLAUDE=$(claude --version 2>/dev/null | head -1 || echo unknown) \
    python3 - <<'PY'
import hashlib, json, os, subprocess, datetime

def sha(path):
    try:
        return hashlib.sha256(open(path, "rb").read()).hexdigest()
    except OSError:
        return None

buildinfo = {}
try:
    for line in open(os.path.join(os.environ["EVALDIR"], "bin", "BUILDINFO")):
        k, _, v = line.partition(":")
        buildinfo[k.strip()] = v.strip()
except OSError:
    pass

m = {
    "created": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "context": os.environ["EVAL_CONTEXT"],
    "model_alias": os.environ["EVAL_MODEL"],
    "max_turns": int(os.environ["EVAL_MAX_TURNS"]),
    "parallel": int(os.environ["EVAL_PARALLEL"]),
    "variants": os.environ["M_VARIANTS"].split(),
    "tasks": os.environ["M_TASKS"].split(),
    "dtctl_recipes_commit": buildinfo.get("dtctl-recipes"),
    "dtctl_main_commit": buildinfo.get("dtctl-main"),
    "built": buildinfo.get("built"),
    "harness_commit": os.environ["M_HARNESS_REV"],
    "skills_dir": os.environ["EVAL_SKILLS_DIR"],
    "skills_commit": os.environ["M_SKILLS_REV"],
    "claude_cli": os.environ["M_CLAUDE"],
    "book_path": os.environ["BOOK"],
    "book_sha256": sha(os.environ["BOOK"]),
}
path = os.path.join(os.environ["RUNDIR"], "manifest.json")
with open(path, "w") as f:
    json.dump(m, f, indent=2)
print("wrote", path)
PY
}

render() { # expand ${EVAL_*} placeholders
    python3 -c 'import os,sys; sys.stdout.write(os.path.expandvars(sys.stdin.read()))'
}

preamble() { # $1 = variant
    cat <<'EOF'
You are investigating a live Dynatrace environment using the dtctl CLI via the
Bash tool. dtctl is on PATH and already authenticated against the right
environment — just run `dtctl <verb> <resource> ...`.

Rules:
- Use at most 12 dtctl invocations. Work efficiently.
- Only dtctl gives you data about this environment; never report a value you
  have not measured.
EOF
    case $1 in
    recipes*)
        cat <<'EOF'
- If unsure of dtctl's commands, `dtctl commands` prints the catalog. This
  environment has a per-environment recipe book of verified queries — start
  with `dtctl recipes`. The book is freshly generated; do NOT run
  `dtctl recipes discover` or `dtctl recipes refresh`.
EOF
        ;;
    *)
        cat <<'EOF'
- If unsure of dtctl's commands, `dtctl commands` prints the catalog.
EOF
        ;;
    esac
    cat <<'EOF'

When done, report your findings. The LAST line of your final message must be
exactly one line:
ANSWER: <json matching the schema in the task below>
If you cannot establish an answer you can defend, end with: ANSWER: UNKNOWN

# Task

EOF
}

run_one() { # $1 = rundir, $2 = variant, $3 = task
    local rundir=$1 v=$2 t=$3
    local ws=$rundir/$v/$t
    mkdir -p "$ws/bin" "$ws/out"

    local real=$EVALDIR/bin/dtctl-main cfg=$rundir/cfg-noskills xdg_line=""
    case $v in recipes*|head-base) real=$EVALDIR/bin/dtctl-recipes ;; esac
    case $v in *skills*) cfg=$rundir/cfg-skills ;; esac
    # head-base: hide the recipe book (and packs) behind a shadow config home;
    # auth is unaffected (tokens live in the keyring, the refresh lock in /tmp).
    case $v in head-base) xdg_line="export XDG_CONFIG_HOME=$rundir/xdg-nobook" ;; esac

    cat > "$ws/bin/dtctl" <<WRAP
#!/usr/bin/env bash
d="\$(cd "\$(dirname "\$0")/.." && pwd)"
$xdg_line
n=\$(date +%s%N)
printf '%s\0' "\$@" > "\$d/out/\$n.argv"
# tee stdin so queries passed via '-f -' are recoverable for scan measurement
tee "\$d/out/\$n.in" 2>/dev/null | "$real" --context "$EVAL_CONTEXT" "\$@" >"\$d/out/\$n.out" 2>"\$d/out/\$n.err"
rc=\$?
echo \$(( ( \$(date +%s%N) - n ) / 1000000 )) > "\$d/out/\$n.dur"
{ printf '%s\t%s\t' "\$n" "\$rc"; printf '%s' "\$*" | tr '\n\t' '  '; printf '\n'; } >> "\$d/calls.log"
cat "\$d/out/\$n.out"
cat "\$d/out/\$n.err" >&2
exit \$rc
WRAP
    chmod +x "$ws/bin/dtctl"

    { preamble "$v"; render < "tasks/$t.md"; } > "$ws/prompt.md"
    printf '{"variant":"%s","task":"%s","model":"%s","started":"%s"}\n' \
        "$v" "$t" "$EVAL_MODEL" "$(date -Is)" > "$ws/meta.json"

    # Skill + Read are allowed since matrix-7: earlier batches denied Reads of
    # skill reference files (beyond SKILL.md), silently weakening skills arms.
    (
        cd "$ws"
        CLAUDE_CONFIG_DIR=$cfg PATH=$ws/bin:$PATH \
        claude -p "$(cat prompt.md)" \
            --model "$EVAL_MODEL" \
            --output-format json \
            --max-turns "$EVAL_MAX_TURNS" \
            --allowedTools "Bash,Skill,Read" \
            --disallowedTools "WebSearch,WebFetch,Task,Agent" \
            < /dev/null > result.json 2> claude.err
    ) || echo "  $v/$t: claude exited non-zero (see $ws/claude.err)" >&2
    if grep -q '"result":"Failed to authenticate' "$ws/result.json" 2>/dev/null; then
        echo "AUTH FAILURE: $v/$t — claude credentials expired/stranded; re-login and re-run this arm" >&2
    fi
    echo "done: $v/$t  (calls: $(wc -l < "$ws/calls.log" 2>/dev/null || echo 0))"
}

run_batch() { # $1 = rundir
    local rundir=$1
    mkdir -p "$rundir"
    cp bin/BUILDINFO "$rundir/" 2>/dev/null || true
    write_manifest "$rundir"

    # Per-batch isolated claude config dirs: skills on / off.
    # HAZARD: the two dirs get independent COPIES of the credentials. OAuth
    # refresh tokens rotate — if the token expires mid-batch, the first config
    # dir to refresh strands the other copy ("could not be refreshed"), and
    # every remaining cell of the stranded arms dies at turn 1 with an auth
    # error (observed live in matrix-10: all 64 skills-arm cells). Log in
    # freshly (`claude` → /login) before long batches; run_one warns loudly
    # when a cell hits this so a wedged batch is visible immediately.
    local mode CD s found
    for mode in skills noskills; do
        CD=$rundir/cfg-$mode
        mkdir -p "$CD"
        cp "$HOME/.claude/.credentials.json" "$CD/" 2>/dev/null \
            || echo "warning: no ~/.claude/.credentials.json — headless claude may not authenticate" >&2
        if [ "$mode" = skills ]; then
            mkdir -p "$CD/skills"
            found=0
            for s in "$EVAL_SKILLS_DIR"/dt-*; do
                [ -d "$s" ] && ln -sfn "$s" "$CD/skills/$(basename "$s")" && found=1
            done
            [ "$found" = 1 ] || { echo "no dt-* skills found in $EVAL_SKILLS_DIR" >&2; exit 1; }
        fi
    done

    # head-base shadow config home: dtctl config only — no recipes/, no packs/.
    case " $VARIANTS " in *" head-base "*)
        mkdir -p "$rundir/xdg-nobook/dtctl"
        cp "${XDG_CONFIG_HOME:-$HOME/.config}/dtctl/config" "$rundir/xdg-nobook/dtctl/" ;;
    esac

    if [ ! -f "$rundir/ground-truth.json" ]; then
        ./ground_truth.sh "$rundir" --sets "$GTSETS"
    fi

    echo "batch $(basename "$rundir") → $rundir"
    echo "variants: $VARIANTS | tasks: $TASKS | model: $EVAL_MODEL | parallel: $EVAL_PARALLEL"
    local jobs_running=0 v t
    for v in $VARIANTS; do
        for t in $TASKS; do
            run_one "$rundir" "$v" "$t" &
            jobs_running=$((jobs_running + 1))
            if [ "$jobs_running" -ge "$EVAL_PARALLEL" ]; then
                wait -n
                jobs_running=$((jobs_running - 1))
            fi
        done
    done
    wait

    # Post-batch GT: the second edge of the drift envelope.
    if [ -z "${EVAL_SKIP_POST_GT:-}" ]; then
        ./ground_truth.sh "$rundir" --out ground-truth-post.json --sets "$GTSETS"
    fi
    echo "batch complete. Score it:"
    echo "  ./score.py $rundir"
}

if [ "$TRIALS" -gt 1 ]; then
    for k in $(seq 1 "$TRIALS"); do
        echo "=== trial $k/$TRIALS ==="
        run_batch "$RUNS_ROOT/$BATCH-r$k"
    done
    echo "all trials done. Aggregate:"
    echo "  ./stats.py $RUNS_ROOT/$BATCH-r*"
else
    run_batch "$RUNS_ROOT/$BATCH"
fi
