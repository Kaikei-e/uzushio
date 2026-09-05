# task-plecto-gate

A CMoA task whose verifier is a **performance gate**: PlectoProxy's T1 runbook,
`bash bench/perf/run-perf.sh gate`, run in a container over one pinned commit.

This task belongs to uzushio. Nothing here is part of PlectoProxy, nothing is
proposed to it, and its repository is never modified: `setup.sh` fetches one
commit into `repo/`, which is gitignored. The bands the gate is judged against
are PlectoProxy's own (`bench/perf/gate_tolerances.toml`), unchanged.

It exists to be measured. `uzushio task doctor` asks two questions of any
verifier — does it accept a solution it should accept, and does it reject the
ones it should reject — and a banded performance gate is the case where the
first question is the hard one. **The first thing to find out here is whether
the verifier accepts the reference at all**, which is why
`doctor.reference_runs` is 5 rather than 3.

It does not. See "The caveat", below: the check has been run, the verdict is
unhealthy, and the second question does not arise until the first one has an
answer.

## What it measures

`phase_gate` runs five steps and reduces them to one row per invariant:

| step | generator | invariants |
|---|---|---|
| interleaved WASM ladder, 3 rounds × 3 rungs | oha | `dispatch_floor_us`, `apikey_cost_us` |
| fixed-rate tails at 2000 rps | oha | `pooled_tail_p50_ms`, `apikey_tail_p50_ms`, `respctx_tail_p50_ms` (+ two `info` rows) |
| rate-limit tax and enforcement | k6 | `ratelimit_tax_us`, `enforce_allowed_ratio` |
| round-robin exactness and ejection timeline | plecto-loadgen | `rr_spread_req`, `ejection_transition_s`, `ejection_stray_failed` |
| verdict | python3 | `performance/data/gate.csv` |

`gate.csv`'s header is exactly `invariant,value,ci_half,band_lo,band_hi,verdict`,
which is the contract `verify.kind: band` reads. So `task.json` sets
`kind: band` rather than `exit-code`, and that is not cosmetic: `run-perf.sh`
returns 1 both for "an invariant left its band" and for "the proxy failed to
launch", and a gate that crashed leaves an **empty** `gate.csv` behind and still
exits 1. Reading the rows tells the two apart — no rows at all is a harness
failure (`runner_error`, inconclusive), a `fail` row is a regression.

`gate.sh` prints the CSV again after the run for exactly this reason.

## The reference is the tree itself

`reference.diff` is an **empty file**, and that is deliberate: the pinned commit
*is* the reference solution. A performance gate is not a task with a bug to fix;
it is a measurement of a tree that is already correct, and the question the
doctor asks is whether the verifier says so five times running.

`cmoa verify --diff <empty file>` verifies the revision unchanged, and
`uzushio task doctor` composes the reference and a mutant by applying an empty
patch first, which is a no-op. Neither needs a special case.

## How long it takes

- **Image build**: under a minute on top of `rust:1.97.1-bookworm`, which is
  itself roughly 570 MB to pull the first time. Nothing is compiled — oha and
  k6 are pinned release binaries, checked against their sha256.
- **The first verification**: the whole cargo build, cold. 511 packages in
  `plecto/Cargo.lock`, dominated by wasmtime and cranelift; the guest filter
  components, a cargo workspace each, built for `wasm32-unknown-unknown`; and
  80 more packages for the load generator. On four CPUs this measured just
  under **ten minutes end to end**, gate included, and left several GB in the
  target volume.
- **Every verification after that**: an incremental relink plus the gate, which
  the runbook advertises as ~6–7 minutes.

`verify.timeout_seconds` is 1500, which fits both — a cold verification with
room to spare, and a warm one twice over. `cmoa verify --timeout` overrides it.
Priming the cache once by hand (below) still makes the doctor's five reference
runs comparable to each other, which is the point of running five.

## Running it

```sh
./setup.sh                       # fetch the pinned commit into repo/, create the volumes
CMOA_CANDIDATE_DIR="$PWD/repo" docker compose -f compose.yaml build
```

`docker compose build` interpolates the whole file, including the mount that
`cmoa verify` normally sets, hence the variable — its value is not used by the
build.

Prime the cargo cache once, so the first real verification is not also the first
build:

```sh
cmoa verify --task . --diff reference.diff --label warm --out /tmp/plecto-warm --timeout 2h
```

Then measure the verifier:

```sh
uzushio task doctor --task . --parallel 1
```

`--parallel 1` is what a banded verifier wants, and the doctor holds it to that
by itself: with `verify.kind: band` it runs one verification at a time and says
so, unless `--parallel` was typed, in which case the number is honoured and the
warning says what it costs. Two containers measuring latency on one machine
measure each other. The shared cargo target volume wants the same thing for a
second reason — cargo locks it.

One diff by hand:

```sh
cmoa verify --task . --diff reference.diff --label once --out /tmp/once
cmoa verify --task . --diff mutants/0003-rr-no-advance.diff --label rr --out /tmp/rr
```

### CPU

`run-perf.sh` splits `nproc` in half and pins the proxy on the lower indices and
the generators on the upper, with `taskset`. `compose.yaml` therefore uses
`cpuset`, not a CPU quota: a quota would leave `nproc` reporting the whole host
while the cgroup throttled the CPUs the script had pinned onto.

`PLECTO_CPUSET` chooses the set, and defaults to `0-3`:

```sh
PLECTO_CPUSET=0-7 uzushio task doctor --task . --parallel 1
```

Two CPUs is the minimum that works at all — with one, `PROXY_CPUS` comes out as
`0--1` and every `taskset` fails. More is better, and same-kind cores on an
otherwise idle machine are better still; see the caveat.

## The mutants

Seven, all hand-written, each against the pinned tree (which is the
reference-applied tree, the reference being empty). Five should be killed, and
each names the band it targets:

| diff | site | band |
|---|---|---|
| `0001-dispatch-sleep` | `host/src/filter.rs` — 50 µs sleep in `run_on_request` | `dispatch_floor_us` |
| `0002-apikey-extra-kv` | `filter-apikey/src/lib.rs` — a second `host_kv::get` per request | `apikey_cost_us` |
| `0003-rr-no-advance` | `control/src/upstream/lb.rs` — `fetch_add` → `load` | `rr_spread_req` |
| `0004-health-slow-tick` | `server/src/health.rs` — probe floor 20 ms → 1200 ms | `ejection_transition_s` |
| `0005-ratelimit-probe` | `host/src/state.rs` — an extra `kv.get` in `try_acquire` | `ratelimit_tax_us` |

`0003` is the one that proves the verifier works at all: `rr_spread_req`'s band
is `0-0`, which is exactly host-independent, so it dies identically on bare
metal, in a container and on a loaded machine. `0005` is the opposite corner —
`ratelimit_tax_us` is measured with k6, so if k6 ever falls out of the image the
row reports `skipped`, the gate still exits 0, and this mutant survives. The
report says which invariants were skipped for exactly this reason.

Two should survive, and are declared `equivalent`:

| diff | why it cannot be caught |
|---|---|
| `0006-metrics-render` | `Metrics::render` is only called from a `/metrics` scrape, and no gate step starts an admin listener |
| `0007-cli-validate` | `crates/plecto` — the shipped CLI — is not in the dependency graph of the measured example binaries |

`0007` is more than a control. It documents a scope limit: **the T1 gate
measures the bench harnesses and the examples, never the binary the project's
own Dockerfile ships.**

Three of the ten judged bands are effectively unkillable by a subtle mutant:
`pooled_tail_p50_ms`, `apikey_tail_p50_ms` and `respctx_tail_p50_ms` have 90–230
µs of headroom against regressions that realistically cost 1–5 µs. No mutant
here targets them.

## The caveat

The bands are the project's own, and the project says plainly that they are
narrow and host-relative. From `bench/perf/gate_tolerances.toml`:

> Bands are host-relative (loopback, no governor pinning): these initial values
> are calibrated from the 2026-07-11 report snapshots on the reference host. On
> a new host, run the gate 2-3 times, read gate.csv's value/ci_half columns, and
> re-centre lo/hi around them (half-width guidance: >= 3x the observed
> interleave half-range).

And from `performance/README.md`, on a run of unchanged code:

> **Verdicts this pass**: T1 `gate` **PASS**, then **FAIL**, then **PASS** — the
> single excursion was `ratelimit_tax_us` at 4.88 µs against a 2.2–4.2 band,
> with the phase's other three same-day measurements at 3.11 / 3.42 / 3.79 µs.
> This host was *not* idle (a browser, cadvisor and a clickhouse-server were
> resident; 15-min load average ~10), which is the honest explanation for a
> between-session excursion the interleave cannot cancel; the band was left
> untouched.

One failure in four on correct code is a false-positive rate that no selection
gate can absorb, and a container adds its own tax on top — seccomp filtering on
every syscall, cgroup scheduling, namespaced network sysctls — against a
`dispatch_floor_us` with 0.54 µs of headroom and an `apikey_cost_us` with 0.14.
The gate reads *adjacent deltas*, so a uniform tax largely cancels; a
multiplicative slowdown does not.

**And it does not cancel here.** The doctor has been run, and the answer is not
a false-positive *rate*: the verdict is **unhealthy**, and all five reference
runs failed, on the same four of the ten judged invariants, with a spread far
narrower than their distance from the band.

| invariant | 5 reference runs | band |
|---|---|---|
| `dispatch_floor_us` | 8.69 – 8.85 | 2.0 – 4.6 |
| `apikey_cost_us` | 1.97 – 2.11 | 0.3 – 1.2 |
| `ratelimit_tax_us` | 8.4 – 10.3 | 2.2 – 4.2 |
| `pooled_tail_p50_ms` | 0.024 – 0.032 | 0.04 – 0.20 |

Three high and one **low** — this container is faster than the reference host on
the pooled tail and slower on everything measured per request. That is not
noise: it is a band centred on somebody else's machine, and the run-to-run
spread being this tight is what says so. Every host-independent invariant passed
every time.

All seven mutants then came back `killed` — **including the two the task
declares equivalent**, which is the tell. A verifier that rejects the reference
rejects everything, so the kill rate of 1.00 is the false positive read back,
not detection. The doctor says so rather than reporting it: the report carries
`kill_rate_meaningful: false`, the record writes `kill_rate: n/a`, and each
mutant carries `outcome_note: "reference also failed"` beside its `killed`. The
one thing the check still measures is the differential — a mutant that breaks a
band the reference held — which the report records per mutant as
`bands_beyond_reference`.

**So this task is not a selection gate, and it is not shipped as one.** It is a
doctor exercise, and it has already paid for itself: `reference_runs: 5` on an
unmodified tree turned "the bands are narrow" into "these four bands do not hold
in this container, and everything measured against them is unreadable". That is
the answer the doctor exists to produce, and it is the answer *before* any
mutant means anything.

What it does not settle is what to do about it. Re-centring the four bands for a
container would make the task usable and would also fork the project's own
expectation, which is a decision rather than a fix. Until somebody makes it, the
six invariants that passed all five times — `rr_spread_req`,
`ejection_transition_s`, `ejection_stray_failed`, `enforce_allowed_ratio` and
the two remaining `*_tail_p50_ms` — are the part of this gate that judges.

## Files

```
README.md        this
setup.sh         fetch the pinned commit into repo/; create the three cache volumes
task.json        version 2, kind band, 7 mutants, reference_runs 5
reference.diff   empty: the pinned tree is the reference
mutants/*.diff   seven hand-written diffs against the pinned tree
compose.yaml     one service, cpuset, three external volumes
Dockerfile       rust 1.97.1 + wasm target + pinned oha and k6 (checksummed)
gate.sh          build the examples, run the gate, print the CSV
```

`repo/` and `doctor/` are generated and gitignored.
