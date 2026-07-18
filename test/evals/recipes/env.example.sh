# Copy to env.sh and fill in for your environment. env.sh is git-ignored:
# it names a real tenant context and a real service — never commit those.

# dtctl context to evaluate against (must be authenticated; read access suffices).
export EVAL_CONTEXT=my-context

# A service that runs as MULTIPLE instances and whose logs carry
# dt.smartscape.service on only a minority of records (check
# facts.fieldCarriage in your recipe book). This is the t5 trap.
export EVAL_TRAP_SERVICE=my-service

# Directory containing the dynatrace-for-ai skills (dt-*).
export EVAL_SKILLS_DIR=$HOME/.agents/skills

# Model for the eval agents (any id/alias the claude CLI accepts).
export EVAL_MODEL=claude-sonnet-5

# Baseline ref for the "without recipes" binary. PIN THIS: the default
# (merge-base of HEAD and upstream/main) moves when upstream moves, which
# silently changes the control arms between batches.
#export EVAL_BASELINE_REF=<commit>


# Where run artifacts go. Kept OUTSIDE the repo on purpose: run output
# contains tenant data and must never be committed.
#export EVAL_RUNS_DIR=$HOME/.cache/dtctl-recipes-evals

# Concurrency and per-run turn cap.
#export EVAL_PARALLEL=3
#export EVAL_MAX_TURNS=50
