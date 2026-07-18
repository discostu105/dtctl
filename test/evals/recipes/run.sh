#!/usr/bin/env bash
# Runs the recipes eval matrix: variants × tasks, one fresh headless claude
# agent per cell, each in an isolated workspace with a logging dtctl wrapper.
#
#   ./run.sh                     # all 4 variants × all 6 tasks
#   ./run.sh -v recipes,base     # subset of variants
#   ./run.sh -t t5,t6            # subset of tasks
#   ./run.sh -b 20260718-1200    # reuse an existing batch dir (re-run cells)
#
# Variants:
#   base            dtctl-main,    no dynatrace-for-ai skills
#   skills          dtctl-main,    dynatrace-for-ai skills
#   recipes         dtctl-recipes, no dynatrace-for-ai skills (+ recipe book)
#   recipes-skills  dtctl-recipes, dynatrace-for-ai skills    (+ recipe book)
#
# Skill visibility is controlled per run via an isolated CLAUDE_CONFIG_DIR
# (credentials copied in; dt-* skills symlinked in only for skills variants).
# Workspaces live OUTSIDE the repo so eval agents never see this project's
# CLAUDE.md and run output (tenant data) can never be committed.
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

VARIANTS="base skills recipes recipes-skills"
TASKS="t1 t2 t3 t4 t5 t6 t7 t8 t9 t10 t11 t12 t13 t14 t15 t16 t17 t18 t19 t20 t21 t22 t23 t24"
BATCH=$(date +%Y%m%d-%H%M%S)
while getopts "v:t:b:" o; do
    case $o in
        v) VARIANTS=${OPTARG//,/ } ;;
        t) TASKS=${OPTARG//,/ } ;;
        b) BATCH=$OPTARG ;;
        *) exit 2 ;;
    esac
done

[ -x bin/dtctl-recipes ] && [ -x bin/dtctl-main ] || { echo "run ./build.sh first" >&2; exit 1; }
RUNDIR=$RUNS_ROOT/$BATCH
mkdir -p "$RUNDIR"
cp bin/BUILDINFO "$RUNDIR/" 2>/dev/null || true

# The recipes variants need a generated book for the context.
BOOK=${XDG_CONFIG_HOME:-$HOME/.config}/dtctl/recipes/$EVAL_CONTEXT.yaml
case " $VARIANTS " in *" recipes"*)
    if [ ! -f "$BOOK" ]; then
        echo "no recipe book at $BOOK — generate one first:" >&2
        echo "  bin/dtctl-recipes --context $EVAL_CONTEXT recipes discover --pack <pack.yaml>" >&2
        exit 1
    fi ;;
esac

# Per-batch isolated claude config dirs: skills on / off.
for mode in skills noskills; do
    CD=$RUNDIR/cfg-$mode
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

if [ ! -f "$RUNDIR/ground-truth.json" ]; then
    ./ground_truth.sh "$RUNDIR"
fi

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

run_one() { # $1 = variant, $2 = task
    local v=$1 t=$2
    local ws=$RUNDIR/$v/$t
    mkdir -p "$ws/bin" "$ws/out"

    local real=$EVALDIR/bin/dtctl-main cfg=$RUNDIR/cfg-noskills
    case $v in recipes*) real=$EVALDIR/bin/dtctl-recipes ;; esac
    case $v in *skills*) cfg=$RUNDIR/cfg-skills ;; esac

    cat > "$ws/bin/dtctl" <<WRAP
#!/usr/bin/env bash
d="\$(cd "\$(dirname "\$0")/.." && pwd)"
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

    (
        cd "$ws"
        CLAUDE_CONFIG_DIR=$cfg PATH=$ws/bin:$PATH \
        claude -p "$(cat prompt.md)" \
            --model "$EVAL_MODEL" \
            --output-format json \
            --max-turns "$EVAL_MAX_TURNS" \
            --allowedTools "Bash" \
            --disallowedTools "WebSearch,WebFetch,Task,Agent" \
            < /dev/null > result.json 2> claude.err
    ) || echo "  $v/$t: claude exited non-zero (see $ws/claude.err)" >&2
    echo "done: $v/$t  (calls: $(wc -l < "$ws/calls.log" 2>/dev/null || echo 0))"
}

echo "batch $BATCH → $RUNDIR"
echo "variants: $VARIANTS | tasks: $TASKS | model: $EVAL_MODEL | parallel: $EVAL_PARALLEL"
jobs_running=0
for v in $VARIANTS; do
    for t in $TASKS; do
        run_one "$v" "$t" &
        jobs_running=$((jobs_running + 1))
        if [ "$jobs_running" -ge "$EVAL_PARALLEL" ]; then
            wait -n
            jobs_running=$((jobs_running - 1))
        fi
    done
done
wait
echo "batch complete. Score it:"
echo "  ./score.py $RUNDIR"
