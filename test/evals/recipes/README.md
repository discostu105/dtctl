# Recipes eval harness

Repeatable with-vs-without evaluation of the Recipes feature
([RECIPES_CONCEPT.md](../../../docs/dev/RECIPES_CONCEPT.md) §6.1): does a
per-environment recipe book make an AI agent measurably better at
investigating a Dynatrace environment — and how does that interact with the
generic dynatrace-for-ai skills?

## The matrix

Two independent dimensions, four variants, eighteen tasks, one fresh headless
`claude` agent per cell:

| Variant | dtctl binary | dynatrace-for-ai skills (`dt-*`) | recipe book |
|---|---|---|---|
| `base` | built from main (no recipes feature) | ✗ | — |
| `skills` | built from main | ✓ | — |
| `recipes` | built from this branch | ✗ | ✓ |
| `recipes-skills` | built from this branch | ✓ | ✓ |

Tasks (in `tasks/`): (t1) top ERROR-log source, (t2) highest-p95 service,
(t3) RUM presence + volume, (t4) GenAI token consumption, (t5) **trap** —
a complete log count for a multi-instance service whose logs are
entity-stamped on only a minority of records (the naive filter undercounts
by an order of magnitude), (t6) security posture where attack detections are
absent (proving absence), (t7) distinct Davis problems + currently active,
(t8) top-CPU host via metric timeseries, (t9) OOM-killed pods, (t10) **trap**
— open vulnerabilities where the current state is the LATEST state report
per vulnerability (counting raw report events overcounts ~100×), (t11) top
bizevents producer, (t12) top Davis event category, (t13) Azure/GCP absence
proof, (t14) top log retention bucket, (t15) host census + dominant OS,
(t16) PostgreSQL instance count, (t17) slowest root span (sampling hides the
max), (t18) EC2 + k8s-namespace topology census, (t19) **trap** — distinct
Davis events where row-counting the generic `events` stream double-counts
state updates (the canonical stream is `dt.davis.events`), (t20) distinct
traces through the multi-instance service, (t21) pods currently backing the
multi-instance service (topology hop across all deployments), (t22) namespace
with the most ERROR logs, (t23) log count in an explicit historical window
(24h→12h ago — probes silent default-window handling), (t24) fleet-wide
average host CPU from a metric timeseries.

## How it works

- **Agent runs are headless**: `claude -p` with `--output-format json`
  (captures the final answer, turn count, and cost), `--max-turns`, and only
  the Bash tool allowed. Web access and subagents are disallowed so the only
  knowledge sources are the model, the skills, and dtctl itself.
- **Skill visibility is isolation, not installation**: each run gets an
  isolated `CLAUDE_CONFIG_DIR` (credentials copied in). Skills variants get
  the `dt-*` skills symlinked into `<config>/skills/`; the others see none —
  regardless of what is installed globally on your machine.
- **Every dtctl call is logged**: workspaces put a wrapper `dtctl` first on
  PATH that records argv, exit code, and full output per call, and pins
  `--context $EVAL_CONTEXT`.
- **Workspaces live outside the repo** (`~/.cache/dtctl-recipes-evals/`), so
  eval agents never see this project's CLAUDE.md and run artifacts —
  which contain tenant data — can never be committed.
- **Ground truth is measured fresh per batch** by `ground_truth.sh` (direct
  queries, including the t5 trap's true count via `resolve scope` union
  `service.name`, and the naive entity-stamped count that defines the
  SILENT_WRONG verdict). Tasks use relative time windows, so scoring
  tolerances (see `score.py`) absorb GT-to-run drift.

## Running

```bash
cd test/evals/recipes
cp env.example.sh env.sh   # fill in context, trap service, skills dir
./build.sh                 # bin/dtctl-recipes (HEAD) + bin/dtctl-main (baseline)

# recipes variants need a generated book for the context:
#   bin/dtctl-recipes --context <ctx> recipes discover --pack <pack.yaml>

./run.sh                   # full matrix; or -v recipes,base -t t5,t6
./score.py ~/.cache/dtctl-recipes-evals/<batch>            # report.md + summary.json
./score.py <batch> --measure-scan                          # + scan-cost column (re-runs
                                                           #   agent queries; costs consumption)
```

Requirements: `claude` CLI (≥ 2.x) authenticated on this machine, Go
toolchain, python3, an authenticated dtctl context, and the
dynatrace-for-ai skills checked out locally.

## Cost & caveats

- Each batch spawns `variants × tasks` live agent sessions (Claude API cost,
  reported per run in `report.md`) and runs real Grail queries (consumption).
- One trial per cell is directional, not statistical — run multiple batches
  for confidence; `run.sh -b <batch>` re-runs cells into an existing batch.
- Recipes variants share the context's real recipe book, and `--recipe` runs
  update its stamps (that is the feature's designed living-cache behavior,
  so it is deliberately not isolated).
- `score.py --measure-scan` re-executes the agents' queries later than the
  agents did; relative time windows make the re-measured scan sizes
  comparable, not identical.

Results of the first runs: [docs/dev/RECIPES_EVAL.md](../../../docs/dev/RECIPES_EVAL.md).
