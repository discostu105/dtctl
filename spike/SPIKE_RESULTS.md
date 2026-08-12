# S0 Spike Results — dtctl on Wasm/WASI

**Branch:** `spike/wasi-engine` • **Date:** 2026-08-12
**Design doc:** dtctl-contrib `dev/DTCTL_AS_A_SERVICE_DESIGN.md` (S0 gate)

Unmodified dtctl semantics, compiled with `GOOS=wasip1 GOARCH=wasm`, executed
in per-request [wazero](https://wazero.io) instances with HTTP provided by a
host-function shim. Layout:

| Piece | Where |
|---|---|
| Build-tag stubs (xdg paths, refresh flock, SIGWINCH, exec-forward) | `sdk/session/paths_*.go`, `sdk/session/refresh_lock_wasip1.go`, `pkg/output/live_wasip1.go`, `cmd/exec_forward_wasip1.go` |
| Guest HTTP transport (go:wasmimport ABI) | `sdk/wasihttp/` |
| Transport seam in both client constructors | `sdk/httpclient/transport_*.go`, `sdk/httpclient/client.go`, `sdk/session/client.go` |
| Host runner (compile-once/instantiate-many, host HTTP w/ egress allowlist, instance FS, config generation) | `spike/wasihost/` (own module — wazero stays out of CLI deps) |
| End-to-end tests incl. isolation + streaming probes | `spike/wasihost/runner/runner_test.go` |

Build: `GOOS=wasip1 GOARCH=wasm go build -o dtctl.wasm .` then
`cd spike/wasihost && go test ./runner/` (or `go build -o wasihost . && ./wasihost -h`).

## Gate scorecard

| # | Criterion | Result | Measured |
|---|---|---|---|
| 1 | Envelopes byte-identical to native `--agent --plain` | **PASS** (see finding F2) | `get buckets -o json --agent`: native vs wasm stdout **byte-identical** against the same mock API |
| 2 | Per-instance memory ≤ 256 MB p95 | **PASS** | 29.4 MB typical; 100.1 MB worst probe (13 MB API response) |
| 3 | Added latency ≤ 250 ms p95 | **PASS** | Full instance lifecycle incl. HTTP call: min 20 ms / p50 21 ms / max 47 ms over 10 runs; `version` 46 ms cold |
| 4 | Large response streams without full buffering at the boundary | **PASS** (qualified) | ABI streams 256 KB chunks; 13 MB response → 1.6 s, 100 MB instance memory. The *guest* still buffers to parse JSON — inherent CLI semantics, same as native; service-side output caps still required |
| 5 | Enumerable build-tag surface, no forked deps | **PASS** | **4 dtctl-owned stub files** + 1 two-line transport seam. adrg/xdg needed indirection; go-keyring, pkg/browser, x/term compile via their own fallbacks. Zero forks |
| 6 | Maintained runtime embeddable in target stack | **PASS (Go) / OPEN (JVM)** | wazero v1.9 (pure Go, no cgo). **Chicory untested** — required follow-up if the multi-tenant service must be JVM-hosted |

Isolation check (design requirement beyond the six): 4 concurrent instances
with distinct tenant tokens on one shared compiled module — each envelope
contained only its own tenant's data; no cross-instance observation.
Egress: host denies any hostname other than the request's environment.

## Verdict

**The gate passes for a Go-hosted service.** Per-request Wasm instantiation
delivers what the design predicted: the E1 `NewRootCmd` refactor, the
`os.Exit` sweep, stdio capture, and the env/FS seams were all **unnecessary**
— dtctl runs unmodified except for four platform stubs, with memory-level
tenant isolation. Remaining before committing the service to this substrate:
run this exact suite under Chicory (criterion 6 for a JVM host), and decide
the compile-cache strategy (12.5 s one-time module compile; wazero supports a
persistent compilation cache).

## Findings (feed back into the design doc)

- **F1 — `sdk/session.NewClient` bypasses `sdk/httpclient`.** It builds its
  own resty client, so "all traffic flows through sdk/httpclient" was false;
  the transport seam had to be applied in *two* constructors. The design's
  egress-chokepoint claim depends on this being unified — worth an engine
  work item regardless of substrate.
- **F2 — agent-envelope coverage gap.** `create bucket --agent` prints a
  human confirmation to stderr and **no envelope** on success (native
  behavior, faithfully reproduced by wasm). The service contract "response
  body = envelope" requires envelope coverage for every command; the
  B7/E8 golden-equality corpus must gate on this.
- **F3 — deadlines are host-enforced.** The single-threaded guest cannot
  interrupt a blocking host call, so in-guest ctx cancellation never fires
  mid-request; `TimeoutMillis` crosses the ABI and the host enforces it.
  Acceptable for the service (the host owns timeouts anyway); rules out
  guest-side graceful cancellation UX.
- **F4 — instance FS is a temp dir in the spike.** Request files (incl. the
  generated config with inline token) briefly touch host disk. Production
  host must implement wazero's experimental in-memory `sys.FS` (interface
  exists; effort bounded) or equivalent.
- **F5 — request bodies are buffered** across the ABI (responses stream).
  Fine for dtctl's small payloads; lift if ever needed.
- **F6 — binary is 56 MB unstripped** wasip1 (Go). `-ldflags="-s -w"` and
  wazero's compilation cache mitigate; irrelevant per-request (compile once).
- **F7 — memory scales ~7× response size** during parse+re-serialize
  (100 MB @ 13 MB payload). Very large DQL results need the already-designed
  output caps / spill-to-response semantics; do not raise instance limits
  instead.
