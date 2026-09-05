# task-plecto-gate

A CMoA task whose verifier is a **performance gate**: PlectoProxy's T1 runbook,
`bash bench/perf/run-perf.sh gate`, run in a container over one pinned commit.

This task belongs to uzushio. Nothing here is part of PlectoProxy, nothing is
proposed to it, and its repository is never modified: `setup.sh` fetches one
commit into `repo/`, which is gitignored.

The bands the gate is judged against **were** PlectoProxy's own, and four of
them are now this host's — see "Re-centring the bands". The tracked file in the
pinned tree is untouched and remains the upstream contract.

It exists to be measured. `uzushio task doctor` asks two questions of any
verifier — does it accept a solution it should accept, and does it reject the
ones it should reject — and a banded performance gate is the case where the
first question is the hard one. **The first thing to find out here is whether
the verifier accepts the reference at all**, which is why
`doctor.reference_runs` is 5 rather than 3.

It did not. See "The caveat", below: the first check, on 2026-09-05, was
unhealthy with all five reference runs failing on the same four of the ten
judged bands. "Re-centring the bands" is what was done about it, and the third
check — same tree, same mutants, bands calibrated on this host — came back
**healthy**.

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

**On a new host, that first check is expected to fail** — the committed
`bands.json` is this host's, and a band is a claim about a machine. Read
`reference_failures`, then re-centre from the runs it just took and check again:

```sh
./calibrate.sh doctor/<that-run-id>/report.json
uzushio task doctor --task . --parallel 1
```

Not every question needs the whole ninety minutes; see "Running less than
everything" in the root README for `--only` and `--reuse-reference`.

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
| `0002-apikey-extra-kv` | `filter-apikey/src/lib.rs` — 8 extra `host_kv::get` per request | `apikey_cost_us` |
| `0003-rr-no-advance` | `control/src/upstream/lb.rs` — `fetch_add` → `load` | `rr_spread_req` |
| `0004-health-slow-tick` | `server/src/health.rs` — probe floor 20 ms → 3000 ms | `ejection_transition_s` |
| `0005-ratelimit-probe` | `host/src/state.rs` — 1200 extra `kv.get` in `try_acquire`, through `black_box` | `ratelimit_tax_us` |

**Every mutant is sized against the band it targets, not against how big the
change looks.** The rule is *at least 5× the calibrated half-width*, and the
2026-09-05 run is what says what that costs:

| mutant | measured shift | half-width | ratio | sized to |
|---|---|---|---|---|
| `0001` | +1.59 µs on `dispatch_floor_us` | 0.2641 | **6.0×** | kept at 50 µs |
| `0002` | +0.128 µs per extra `get` | 0.2038 | 0.6× for one | **8 gets** → 1.02 µs, 5.0× (measured +1.01/+1.14) |
| `0005` | +0.0145 µs per extra `get` | 3.0238 | 0.005× for one | **1200 gets** → 17.4 µs, 5.8× |

`0001` is the cautionary one: a 50 µs `std::thread::sleep` costs **1.59 µs/req**,
not 50, because the gate measures closed-loop throughput at `-c 50` and the
blocking sleeps overlap across tokio workers. Size a sleep against its measured
effect on the delta, never against the sleep.

`0005` is the one that took three attempts, and each failure taught something
the first two runs could not have.

1. **`let _probe = self.kv.get(&nskey);` × 32.** The result is never used, so the
   optimiser is entitled to delete the loop, and did. The calibrated 2026-09-05
   check measured it at 9.20 against a reference mean of 10.08 — *below* the
   reference, a shift of nothing — and it survived. **Injected work the compiler
   can prove dead is not injected work.**
2. **× 32, with the sum through `std::hint::black_box`.** Now genuinely executed,
   and it *still* survived at 9.14. The loop was real; it was simply far too
   small.
3. **A direct 1024-get probe** settled it: `ratelimit_tax_us` = 24.63 against a
   reference median of 9.7719. That is **14.5 ns per get** — not the 0.50 µs that
   had been inferred from a single 0.7 σ noise reading, an error of 35×. At
   14.5 ns, five half-widths (15.12 µs) needs 1042 gets, so the mutant is 1200.

`0005`'s count is dictated by this host's noise floor and not by plausibility:
1200 kv reads per request is no kind of refactor anybody would write. **That is
the finding, not a defect in the mutant.** `ratelimit_tax_us` has 7.4 % run-to-run
spread against 0.7 % on the ladder invariants, because it is a difference of two
**k6** throughputs over only two interleave rounds — so nothing smaller is
resolvable here at all. Lowering that noise upstream is worth more than this
mutant is.

The general lesson is the one the first two attempts paid for: **size a mutant
against the measured effect on its invariant, never against how large the change
looks in the diff** — and measure that effect directly rather than reading it off
a difference that is within noise.

`0004` is a different problem: `ejection_transition_s` counts **whole one-second
buckets**, its five reference readings were all exactly 1, and its band is
`[0, 2]` — one quantum of headroom, so no sub-2-second regression is expressible
at all. At 1200 ms the mutant moved the measurement to 2 and *passed*. 3000 ms
makes the supervisor probe every 3 s, so `unhealthy_threshold = 2` takes ~6 s and
saturates `gate_verdict.py`'s 6-bucket horizon. **A quantised band is never
derived statistically — it comes from what the invariant means, and the mutant is
sized to clear it by two quanta.**

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

**That is the answer the doctor exists to produce, and it is the answer *before*
any mutant means anything.** `reference_runs: 5` on an unmodified tree turned
"the bands are narrow" into "these four bands do not hold in this container, and
everything measured against them is unreadable".

What was done about it is the next section. It is still not a selection gate —
the second check, against re-centred bands, put the kill rate at 0.80 with one
mutant surviving — but it is now an instrument whose readings mean something,
which is the difference the whole exercise was for.

## Re-centring the bands

The tracked file asks for exactly this, and names no way to do it:

> On a new host, run the gate 2-3 times, read gate.csv's value/ci_half columns,
> and re-centre lo/hi around them (half-width guidance: >= 3x the observed
> interleave half-range).

`bands.json` is that, done from the doctor's own report rather than by hand, by
`uzushio task calibrate`. The invocation is committed as `calibrate.sh`:

```sh
./calibrate.sh doctor/<run-id>/report.json                     # one check's reference block
./calibrate.sh doctor/<a>/report.json doctor/<b>/report.json   # a larger set
```

It takes no default. `doctor/` is gitignored, so a default would name a path that
exists on one machine and nowhere else — and a calibration built from whatever
report happened to be lying there is the kind of provenance `bands.json`'s header
exists to make explicit. Run with no arguments to be told how to get a report.

It is

```sh
uzushio task calibrate --task . \
  --from doctor/20260905T002350Z-e13205e7/report.json \
  --keep enforce_allowed_ratio \
  --out bands.json
```

It reads the band rows of every `reference` run — each of which carries both the
value measured and the band it was judged against — derives a band for every
invariant that left its tracked one, and copies the rest through unchanged.
**Nothing in `repo/` is read**: the calibration's whole input is the report.

`--keep enforce_allowed_ratio` is the one thing the tool cannot work out for
itself, and `calibrate.sh` exists to carry it. See "The one band that is not
about this host", below.

### The adapter

`bands.json` is **uzushio's** shape — an invariant, a `lo`, a `hi`, and how each
was arrived at — and it knows nothing about PlectoProxy. What `run-perf.sh`
reads is `bench/perf/gate_tolerances.toml`, which is **PlectoProxy's** shape: a
table per invariant, some carrying parameters the band's meaning depends on.

**Turning the first into the second is this task's job and nobody else's, and
`gate.sh` is where it happens.** That boundary is the point of the split: the
shape of a gate's band file is a fact about somebody else's project, so exactly
one file in this directory knows it and the calibration stays generic. `gate.sh`
is mounted into the container rather than baked into the image, so a change to
what the verifier does is a change reviewers read as a diff.

The rendering is deliberately narrow. The tracked file **in the worktree is the
template**, and only the `lo`/`hi` of the invariants `bands.json` actually
derived are replaced; everything else is carried through byte for byte. That is
what preserves `enforce_allowed_ratio`'s `refill_per_s`, `capacity` and
`window_s` — extra keys that are part of what its band asserts, and which a file
written from scratch would silently drop — along with every comment explaining
what each invariant is. The rendered file gains a header saying it was rendered,
from which runs, under which rule, and which bands moved.

Nothing of PlectoProxy's is written: `repo/` is the pinned checkout, `/work` is a
throwaway worktree `cmoa verify` deletes, and the tracked bands stay exactly what
the pinned commit says. `run-perf.sh` hard-codes its tolerances path and takes no
override, which is the only reason the rendering happens in the worktree at all;
a `GATE_TOLERANCES` environment variable upstream would be one line and would
make the tracked header's own advice executable for every downstream host.

`gate.sh` prints one line naming the bands it judged against, because a verdict
read against different bands is a different verdict and a reader of `stdout.txt`
who does not know which is which cannot use it for anything.

**There is no fallback to the pinned bands.** A missing, unreadable or
wrong-schema `bands.json` exits **4** — a code `run-perf.sh` never returns (0 in
band, 1 out of band, 2 unknown phase, 3 stale example), so a harness failure can
never be mistaken for a verdict about the code. Falling back would be the worst
available behaviour: this task's entire finding is that the pinned bands reject
correct code here, so a run that quietly used them would report a false positive
as a regression. The check is `-e` and not `-f`, because docker creates a
*directory* at the mount point when the bind source is missing on the host — so
"somebody deleted `bands.json`" arrives as a directory rather than as an absence.

**The revision is checked where it can be.** Bands are only about the code they
were measured on, so the adapter compares `bands.json`'s `rev` against
`git -C /work rev-parse HEAD` and exits 4 on a mismatch. In *this* task that
check does not fire: `cmoa verify` hands the container a `git worktree`, whose
`.git` is a file pointing at `repo/.git/worktrees/<name>` on the host — which is
not mounted — so git inside the container cannot resolve it. The adapter says so
rather than pretending: the rendered header and the printed line both carry the
calibration's revision and the word `unverified`, and a reader comparing it to
`task.json`'s `rev` gets the check by eye. Where `/work` *is* a self-contained
checkout the comparison runs and the word becomes `verified against the
worktree`.

### What the three doctor runs measured

The whole point of the task, in three rows:

| run | bands | reference | mutants | verdict |
|---|---|---|---|---|
| `20260905T002350Z-e13205e7` | PlectoProxy's, from another host | **5 of 5 failed** | 7 of 7 "killed" | unhealthy — and meaningless |
| `20260905T025554Z-31045463` | this host's, from `bands.json` | **5 of 5 passed** | 4 killed, **1 survived**, 2 equivalent survived | unhealthy — and *informative* |
| `20260905T044952Z-53e8bb94` | this host's, `0005` resized | **5 of 5 passed** | **5 killed, 0 survived**, 2 equivalent survived | **healthy** |

**Run 1** is a verifier that rejects correct code. Every mutant came back
`killed`, **including the two declared equivalent**, and the kill rate of 1.00 is
the false positive read back rather than detection — which is why the report
carries `kill_rate_meaningful: false` and every mutant an
`outcome_note: "reference also failed"`. Nothing it said about any mutant was
evidence about that mutant.

**Run 2** is a calibrated instrument reporting its own sensitivity: zero false
positives across five runs, both equivalent mutants correctly surviving, and one
real finding — `0005-ratelimit-probe` survived at `ratelimit_tax_us` = 9.20
against a reference mean of 10.08, *below* it, so its injected work cost nothing
at all. See "The mutants" for the two further attempts that took.

**Run 3** is the gate working: reference in band five times out of five, all five
killable mutants killed on the invariant each was aimed at, both equivalent
mutants surviving. `verdict: healthy`.

That progression is the argument for the whole exercise. Under run 1's bands,
`0005` — a mutant whose injected work the optimiser had deleted entirely — came
back `killed` along with everything else. **Only a verifier that accepts correct
code can tell you which of your mutants are real**, and getting there took
re-centring the bands first.

> **The run-3 record predates two later edits.** `gate.sh` gained its
> harness-failure refusals and its revision check, and `bands.json` gained
> `rel_floor`, after run 3 was recorded — so that report's environment
> fingerprint no longer matches this tree and `--reuse-reference` against it is
> **refused**. That is the mechanism working as designed: those five reference
> runs were measured under a different adapter, and a reference block is only
> evidence about the verifier that produced it. The bands themselves are
> unchanged — `rel_floor` is a new key recording a floor that does not bind on
> any of these ten invariants — but the fingerprint does not, and should not,
> try to judge which differences are harmless.

### What was re-centred, from what

Four bands, from the five reference runs of `doctor/20260905T002350Z-e13205e7`
(2026-09-05, N = 5):

| invariant | tracked band | this host | centre | half-width |
|---|---|---|---|---|
| `dispatch_floor_us` | 2.0 – 4.6 | **8.5042 – 9.0326** | 8.7684 | 0.2641 |
| `apikey_cost_us` | 0.3 – 1.2 | **1.8544 – 2.262** | 2.0582 | 0.2038 |
| `pooled_tail_p50_ms` | 0.04 – 0.20 | **0.0098 – 0.0416** | 0.0257 | 0.0158 |
| `ratelimit_tax_us` | 2.2 – 4.2 | **6.7481 – 12.7957** | 9.7719 | 3.0238 |

Six were **copied unchanged**, and that is a rule rather than an omission:
`apikey_tail_p50_ms`, `respctx_tail_p50_ms`, `rr_spread_req`,
`ejection_transition_s`, `ejection_stray_failed` and `enforce_allowed_ratio` were
never left by any reference run. Calibration repairs the bands that do not hold
here; widening the ones that do would trade a verifier that rejects everything
for one that accepts everything, and those six are the part of this gate that was
still judging.

#### The one band that is not about this host

`enforce_allowed_ratio` is kept **by name** as well as by that rule, which is
what `--keep enforce_allowed_ratio` in `calibrate.sh` is for.

Its centre is *arithmetic*: the ratio is `allowed_rps / (refill_per_s +
capacity/window_s)`, the denominator is 1200/s by construction, and the ideal
value is therefore 1.0 — a claim about the token bucket's maths rather than a
property of any machine. The five runs read 0.9166 to 0.9168: stable to four
decimals, 8.3 % low, and inside the band. That is a systematic shortfall, not
host noise, and the day it stops passing the general "already in band" rule would
stop protecting it — at which point re-centring would write the shortfall down as
the expectation and destroy the only thing the invariant asserts.

`--keep` says so out loud instead of relying on the shortfall staying small. The
0.9166 reading is an open question worth putting to PlectoProxy, not a band to
move.

### The rule, and why it is not the tracked file's

```
centre = median(v)
w      = max( k₂(N, p=0.95, γ=0.90) · s , floor[unit] )
band   = centre ∓ w
```

`s` is the sample standard deviation across whole verifications, and `k₂` is the
**two-sided normal tolerance factor** — 4.164 at N = 5, falling to 3.021 at
N = 10 (NBS Handbook 91 Table A-6; equivalently ISO 16269-6 and NIST/SEMATECH
§7.2.6.3). The floors are 0.1 µs and 0.005 ms.

Three sample standard deviations would be the obvious choice and is the wrong
one. At N = 5 the sample `s` has a 34 % coefficient of variation, so `mean ± 3s`
is a **95/73** interval: it delivers the 95 % coverage its label implies only
about 73 % of the time, and one calibration in twenty produces a band covering
under 75 % of clean runs. `k₂` is what pays for that, and is why the multiplier
is 4.16 rather than 3.

The tracked file's own guidance — *≥ 3× the observed interleave half-range* — is
**deliberately not followed**, and the calibration prints that quantity beside
every derived band so the deviation is checkable rather than asserted. The
interleave half-range is the spread across the three rounds *inside* one gate
run, with the machine in one state; a band has to cover the spread *between*
whole verifications. On this data the two disagree in both directions: 3× the
interleave is 0.8154 against a between-run half-width of 0.2641 on
`dispatch_floor_us` (three times too wide) and 2.2947 against 3.0238 on
`ratelimit_tax_us` (too narrow). The guidance is right for the operator it was
written for — two or three runs, and no between-run spread available at all.

**MAD is not used either**, and the reason is on this exact data:
`median ± 3·1.4826·MAD` on `apikey_cost_us` gives a lower bound of 1.9835 and
**excludes reference-5 at 1.9738** — a rule that rejects one of the five runs it
was built from. `--spread mad` exists for N ≥ 20, where it is the right tool.

The calibration refuses rather than guesses in three cases: fewer than five
reference runs, a quantised invariant that left its band (see `0004`, above), and
a table carrying parameters beside its band. It also self-checks: a derived band
that would exclude one of its own calibration values is an error, not a file.

### What this does not settle

The prediction here was that the reference block would pass, that the kill rate
would become meaningful for the first time, and that it would be *low*. The
second check measured all three: 5 of 5 reference runs passed, both equivalent
mutants survived correctly, and the rate came out at **0.80 against a
`kill_rate_min` of 0.80** — one mutant short of the threshold, and that mutant
was one the compiler had deleted rather than one the gate could not see.

So the verdict is still `unhealthy`, and it is now a statement about a mutant
rather than about the instrument. That is the difference the calibration bought:
"the verifier accepts correct code and detects 4 of 5 injected regressions, and
here is why the fifth did not land" is a different kind of sentence from "the
verifier rejects correct code, so nothing it says means anything".

What remains genuinely unsettled is sensitivity. Three of the ten judged bands
cannot be moved by a subtle mutant at all, and `ratelimit_tax_us` needs a 16 µs
regression before this host can see one. Reducing that noise — more interleave
rounds, a lighter generator, more CPUs — is worth more than any further mutant.

N = 5 is the hard minimum and not a comfortable one. Seven is better; ten is
better still and costs about seventy minutes. `--from` repeats, so a calibration
set grows across sessions:

```sh
./calibrate.sh doctor/<a>/report.json doctor/<b>/report.json
```

### The CPU set is the calibration's operating point

`PLECTO_CPUSET` is the highest-value reproducibility knob this task has, and it
is **part of what the bands mean**. The committed `bands.json` was measured at
the default `0-3`; running the gate at `0-7` against it is comparing two
different machines.

**Any change to the CPU set requires a fresh reference block and a fresh
calibration** — `./calibrate.sh doctor/<new-run>/report.json`, not an edit to a
band. The same is true of a change to the image (`Dockerfile`, the pinned oha or
k6), of the pinned PlectoProxy commit, and of a kernel upgrade. Re-validate by
re-running the doctor and reading `reference_failures`.

The doctor now enforces this by itself. Every report carries an `environment`
fingerprint over the compose file, everything it mounts — `gate.sh` and
`bands.json` included — the `Dockerfile`, the built image's id, **and the output
of `docker compose config`**; `--reuse-reference` is **refused** when it differs.
So re-calibrating, or editing the adapter, invalidates the reference block
measured before it, which is right: those runs are not evidence about these
bands.

The resolved-compose component is what covers `PLECTO_CPUSET`. The raw compose
file has the same bytes at every setting of `${PLECTO_CPUSET:-0-3}`, so hashing
it alone would let a reference block measured on four processors be reused for a
check running on eight; `docker compose config` interpolates the variable, so the
digest moves with it. Where docker cannot be reached the component is recorded as
omitted, and a fingerprint taken without docker never matches one taken with it.
Re-running the doctor is still the only thing that tells you whether the bands
still hold — the fingerprint refuses a stale reuse, it does not re-measure.

Calibrating at a wider set is worth doing: four CPUs is close to the harness's
stated minimum, and `ratelimit_tax_us`'s 7.4 % spread is partly two k6 processes
sharing two of them. But it is a re-measurement, not an adjustment.

## Files

```
README.md        this
setup.sh         fetch the pinned commit into repo/; create the three cache volumes
task.json        version 2, kind band, 7 mutants, reference_runs 5
reference.diff   empty: the pinned tree is the reference
calibrate.sh     the documented `uzushio task calibrate` invocation; needs a report named
bands.json       this host's bands; generated by calibrate.sh, committed
mutants/*.diff   seven hand-written diffs against the pinned tree
compose.yaml     one service, cpuset, three external volumes, the bands.json mount
Dockerfile       rust 1.97.1 + wasm target + pinned oha and k6 (checksummed)
gate.sh          THE ADAPTER: render the tracked toml from bands.json (exit 4 if it cannot),
                 build the examples, run the gate, print the CSV
```

`bands.json` is uzushio's format and `gate.sh` is the only file that knows
PlectoProxy's. Nothing else here — and nothing in `uzushio` itself — has to.

`repo/` and `doctor/` are generated and gitignored.
