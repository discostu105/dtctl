# Recipes eval harness

Repeatable with-vs-without evaluation of the Recipes feature
([RECIPES_CONCEPT.md](../../../docs/dev/RECIPES_CONCEPT.md) §6.1): does a
per-environment recipe book make an AI agent measurably better at
investigating a Dynatrace environment — and how does that interact with the
generic dynatrace-for-ai skills?

## The matrix

Five variants, one fresh headless `claude` agent per cell:

| Variant | dtctl binary | dynatrace-for-ai skills (`dt-*`) | recipe book |
|---|---|---|---|
| `base` | built from the frozen baseline (no recipes feature) | ✗ | — |
| `head-base` | built from this branch | ✗ | **hidden** (shadow `XDG_CONFIG_HOME`) |
| `skills` | frozen baseline | ✓ | — |
| `recipes` | built from this branch | ✗ | ✓ |
| `recipes-skills` | built from this branch | ✓ | ✓ |

`head-base` exists to de-confound the comparison: the HEAD binary carries
generic ergonomics (rich DQL errors, `--dql`/verb redirects, window-trap
advice, heavy-scan warnings) that would help agents even with no book.
`recipes − head-base` isolates the book's marginal value; `head-base − base`
isolates the ergonomics that ship to every dtctl user.

## Task suites: dev vs HELD-OUT

- **`t1`–`t24` are the DEV suite.** Optimization mining is allowed — and has
  happened, three cycles of it. That makes them a development set: dtctl has
  been tuned against their observed failures (window-trap advice ← t23,
  canonical-stream notes ← t12/t15, absence evidence ← t13, …). Dev-suite
  pass rates measure regression, not generalization.
- **`h1`–`h8` are HELD OUT.** They exist to answer "does the win
  generalize?" — new task families (ratio arithmetic, unit conversion,
  system-table joins) plus fresh instances of known families (top-k,
  absence proofs). The discipline that keeps them meaningful:
  1. **Never mine optimizations from a holdout failure.** Report holdout
     verdicts; do not read holdout transcripts to derive dtctl fixes.
  2. If a holdout task's failure *is* eventually mined, **retire it into
     the dev suite** and author a replacement holdout task.
  3. Prefer reporting dev and holdout pass rates separately; never blend
     them into one headline number.

Dev tasks (in `tasks/`): (t1) top ERROR-log source, (t2) highest-p95
service, (t3) RUM presence + volume, (t4) GenAI token consumption, (t5)
**trap** — complete log count for a multi-instance service whose logs are
entity-stamped on only a minority of records, (t6) security posture with
absent attack detections, (t7) distinct Davis problems + active, (t8)
top-CPU host, (t9) OOM-killed pods, (t10) **trap** — open vulnerabilities
(latest-state dedup), (t11) top bizevents producer, (t12) top Davis event
category, (t13) Azure/GCP absence proof, (t14) top log retention bucket,
(t15) host census + dominant OS, (t16) PostgreSQL instance count, (t17)
slowest root span, (t18) EC2 + k8s-namespace census, (t19) **trap** —
distinct Davis events vs generic-stream row counting, (t20) distinct traces
through the multi-instance service, (t21) pods backing it, (t22) top
ERROR-log namespace, (t23) explicit historical window (24h→12h ago), (t24)
fleet-average host CPU.

Holdout tasks: (h1) top WARN-log container, (h2) distinct traces +
spans-per-trace ratio, (h3) top RUM country, (h4) least-free-disk host,
(h5) longest closed Davis problem in minutes, (h6) **trap** — p50 span
duration in ms (ns/µs unit error scores SILENT_WRONG), (h7) top log bucket
by stored records + its retention, (h8) synthetic-absence + Lambda count.

## How it works

- **Agent runs are headless**: `claude -p` with `--output-format json`,
  `--max-turns`, and `Bash,Skill,Read` allowed. Web access and subagents are
  disallowed so the only knowledge sources are the model, the skills, and
  dtctl itself.
- **Skill visibility is isolation, not installation**: each run gets an
  isolated `CLAUDE_CONFIG_DIR` (credentials copied in). Skills variants get
  the `dt-*` skills symlinked into `<config>/skills/`; the others see none.
- **Every dtctl call is logged**: workspaces put a wrapper `dtctl` first on
  PATH that records argv, exit code, and full output per call, and pins
  `--context $EVAL_CONTEXT`.
- **Workspaces live outside the repo** (`~/.cache/dtctl-recipes-evals/`), so
  eval agents never see this project's CLAUDE.md and run artifacts — which
  contain tenant data — can never be committed.
- **Ground truth is measured BEFORE and AFTER each batch** by
  `ground_truth.sh` (→ `ground-truth.json` / `ground-truth-post.json`).
  Scoring accepts an answer matching either measurement — the drift
  envelope — so volatile metrics don't force post-hoc tolerance widening.
  The t5/t20 trap scope is derived from **raw smartscape queries**, not
  from `dtctl resolve scope` (the feature under eval must not define its
  own ground truth); the resolve-scope variant is still measured as a
  cross-check and a >10% disagreement warns loudly.
- **Provenance is pinned per batch** in `manifest.json`: both binary
  commits, the skills checkout rev, `claude --version`, the model alias,
  and the recipe-book sha256. `score.py` adds its own hash and the resolved
  model ids (from `modelUsage`) to `summary.json`.

## Running

```bash
cd test/evals/recipes
cp env.example.sh env.sh   # fill in context, trap service, skills dir
./build.sh                 # bin/dtctl-recipes (HEAD) + bin/dtctl-main (EVAL_BASELINE_REF)

# recipes variants need a generated book for the context:
#   bin/dtctl-recipes --context <ctx> recipes discover --pack <pack.yaml>

./run.sh                   # 5 variants × dev suite
./run.sh -s all            # + holdout suite   (-s dev|holdout|all)
./run.sh -n 3              # 3 trial batches (<batch>-r1..-r3) for stats
./run.sh -v recipes,base -t t5,h3   # any subset
./score.py ~/.cache/dtctl-recipes-evals/<batch>       # report.md + summary.json
./stats.py ~/.cache/dtctl-recipes-evals/<batch>-r*    # pooled Wilson +
                                                      #   task-clustered CIs, McNemar
./usage.py <batch>         # knowledge-source usage per arm
./analyze.py <batch>       # per-batch forensics (analysis.md)
./score.py <batch> --measure-scan   # + scan-cost column (re-runs agent
                                    #   queries; costs consumption)
```

Requirements: `claude` CLI (≥ 2.x) authenticated on this machine, Go
toolchain, python3, an authenticated dtctl context, and the
dynatrace-for-ai skills checked out locally.

**Pin `EVAL_BASELINE_REF`** in `env.sh`. The default (merge-base with
upstream/main) moves when upstream moves; all published batches froze the
baseline at the same commit — a silent baseline change would make control
arms incomparable across batches.

## Cost & caveats

- Each batch spawns `variants × tasks` live agent sessions (Claude API
  cost, reported per run in `report.md`) and runs real Grail queries
  (consumption; the pre+post GT measurement includes two heavy log scans).
- **Single-batch deltas on the control arms are mostly noise** (observed
  ±2 verdicts, ±50% wall between identical batches). Headline claims come
  from `stats.py` over `-n` trials: quote the task-clustered interval, and
  treat arm differences with McNemar p ≥ 0.05 as unproven.
- Recipes variants share the context's real recipe book, and `--recipe`
  runs update its stamps (the feature's designed living-cache behavior —
  deliberately not isolated).
- `score.py --measure-scan` re-executes the agents' queries later than the
  agents did; relative time windows make the re-measured scan sizes
  comparable, not identical.
- The next validity step beyond this harness: a second tenant
  (OneAgent-only / mixed-era) — `EVAL_CONTEXT` already parameterizes it.

Results: [docs/dev/RECIPES_EVAL.md](../../../docs/dev/RECIPES_EVAL.md).
