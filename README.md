# uzushio

A standard, and a reference CLI, for the part of AI-assisted development that
the twelve-factor style of guidance leaves out: **the evaluation basis of the
AI itself**. The central rule is short. A feature without a verifier is not
started, and an automated harness-improvement loop without a verifier is not
run.

uzushio is the top of a three-layer stack:

| Layer | Project | What it guarantees |
| --- | --- | --- |
| Ground | [DocDag](https://github.com/Kaikei-e/DocDag) | Markdown + YAML frontmatter read as a typed graph; declared relations are consistent |
| Frame | [CMoA](https://github.com/Kaikei-e/CMoA) | A Mixture-of-Agents runtime: deterministic routing, selection-type aggregation, traces on disk |
| Fit-out | **uzushio** | The clauses, the conformance tests, the measurements, and the lineage of every harness edit, failed ones included |

uzushio depends on both layers below it. Neither of them depends on uzushio.

## Status

**Pre-alpha.** What exists today:

- the specification corpus under `spec/` and the conformance tests under
  `tests/conform/`;
- a Go module and its commands: `uzushio docdag-config`, which generates the
  `docdag.yaml` the corpus is validated under; `uzushio task doctor`, which
  measures a CMoA task's verifier; `uzushio task mutate`, which writes the
  mutants it is measured with; `uzushio task calibrate`, which re-centres a
  banded verifier's tolerances on the host that runs it; `uzushio harness
  render`, which materialises the harness a day's binding edits describe;
  `uzushio improve`, which mines failure patterns out of recorded traces and
  proposes the edits that answer them; `uzushio run`, which measures a
  candidate edit against that harness; and `uzushio version`;
- four kinds for the harness-improvement loop — `edit` (a proposed change to
  one harness surface), `pattern` (a recurring failure, written as an STPA
  unsafe control action), `run` (one evaluation of one edit on one split) and
  `verifier` (one health check of one task's verifier) — declared in the
  configuration, and a writer for each.

`uzushio improve` mines the `pattern` documents out of CMoA's traces and, with
`--propose`, writes the `edit` documents that answer them. `uzushio run`
measures one of those edits and writes the `run` documents that settle it.

Expect identifiers, vocabulary and layout to change; the graph records those
changes as `supersedes` lineage rather than by rewriting history.

## Task doctor

A pass rate is a number about nothing if the verifier says pass to anything,
and a harness edit accepted on a verifier that rejects a correct solution is
an edit accepted for the wrong reason. So before a task is used to measure
anything, the verifier itself is measured.

```sh
uzushio task doctor --task examples/task-hello        # measure the verifier
uzushio task doctor --task . --vault . --json         # record it, and print the report
uzushio task mutate --task . --operators arith,cond   # write more mutants
uzushio task calibrate --task . --from doctor/<id>/report.json  # re-centre its bands
```

`doctor` asks the two questions that can be asked without knowing what the
task is about.

- **False positives.** The task's `reference.diff` is verified
  `doctor.reference_runs` times (3 by default). One failure is enough: a
  verifier that is right two runs out of three is one nobody can read a result
  off.
- **Kill rate.** Each mutant under `mutants/` is verified once. A mutant the
  verifier rejects is *killed*; one it passes *survived*, and is a defect the
  verifier cannot see. The rate is `killed / (killed + survived)`, so a mutant
  that never ran — one that would not apply, a run that timed out, a runner
  that failed — is left out of both halves rather than counted as detected.

The verdict is **unhealthy** if any reference run failed, if any hand-written
mutant survived, or if the kill rate is below `doctor.kill_rate_min` (0.8 by
default, and *strictly* below, so a threshold of 1 is reachable);
**inconclusive** if nothing is wrong but something did not answer, or if there
was no killable mutant to measure a rate over; **healthy** otherwise. The exit
codes are 0, 1 and 3, with 2 for a usage or task error.

Each mutant is written against the tree with `reference.diff` already applied,
so `doctor` materialises what it verifies: a detached worktree at the task's
revision, the reference applied into its index, the mutant applied on top, and
`git diff --cached` read back out as one patch against the revision. Two
unified diffs cannot simply be concatenated — the second one's line numbers
are the first one's output.

The report goes to `<task>/doctor/<run-id>/report.json`, beside the verifier's
own output per run. It does **not** record the verifier's command line, which
names the task's compose file by absolute path; `project_name` is kept
instead, and every path in it is relative to the task or replaced with
`<task>`. With `--vault <dir>` the result is also written as a `verifier`
document, whose `report:` names the report relative to the task — and where
`--out` puts the report outside the task, the document names no report at all
rather than carrying an absolute path into somebody's repository.

`task mutate` finds the sites and writes one diff per site. It parses with
`go/parser` and then **splices bytes** at the token's offset rather than
reprinting the tree: `go/printer` normalises the whole file and moves
free-floating comments, so a one-token mutant reprinted through it is a diff of
hundreds of lines. A splice is one changed line. The operators are `arith`
(`+` for `-`, `*` for `/`), `cond` (negate an `if`), `bound` (`<` for `<=`,
`>` for `>=`, `==` for `!=`), `const` (an integer literal plus one), `stmt`
(remove a statement) and `ret` (return the zero value). Names are deterministic —
`mutants/<NNNN>-<operator>-<file>-L<line>C<col>.diff` — and a diff the task
already carries is skipped rather than written again under a new number, whether
the manifest declares it or it is only sitting in `mutants/`.

A candidate that does not compile is dropped. That is a measurement decision
rather than tidiness: a mutant the toolchain refuses fails `go test ./...`
whatever the tests do, so the verifier says fail and `doctor` would score it
`killed` — a verifier that did nothing but build the code would earn those
kills for free. The operators refuse the sites where that is provable (an array
length bumped, a short variable declaration removed, a return zeroed to a type
nobody can name), and `mutate` compiles what is left and drops the rest,
reporting the count per operator. `--keep-nonviable` turns the check off.

`doctor` needs `cmoa`, `git`, and whatever the task's verifier needs — which
for `examples/task-hello` is docker. `mutate` needs `git` and a Go toolchain,
and never runs the verifier.

### Running less than everything

A full check of a slow verifier is an hour and a half — five reference runs and
seven mutants at seven minutes each — and most questions are much smaller than
that. `--only` runs part of the check; `--reuse-reference` takes the reference
block from an earlier one.

```sh
# a mutant was added: verify that one mutant, and reuse the reference block   ~7 min
uzushio task doctor --task . --only 0003-slow-tick \
  --reuse-reference doctor/<earlier-run-id>/report.json

# drift check: does the verifier still accept correct code?                  ~35 min
uzushio task doctor --task . --only reference

# a re-calibration, or anything that changed the verifier: everything        ~90 min
uzushio task doctor --task .
```

`--only` takes `reference`, `mutants`, a run label (`reference-2`,
`mutant-2-0003-slow-tick`) or a mutant's diff (`0003-slow-tick`,
`0003-slow-tick.diff`, `mutants/0003-slow-tick.diff`), and repeats. **A selector
that matches nothing is refused**, because the failure it prevents is a typo
producing a clean report about a mutant nobody verified.

How many reference runs there are stays `doctor.reference_runs` in `task.json`.
That is the task's own judgement about how many runs it takes to see this
verifier's false-positive rate — 5 for the banded task, 3 by default — and a
flag that quietly lowered it would answer a different question from the one the
task asks.

A check narrowed to the mutants alone reports **inconclusive**, whatever its
kill rate: nothing in it said the verifier accepts a correct solution, so the
rate is not evidence of detection. That is what `--reuse-reference` is for.

#### Reusing a reference block honestly

```sh
uzushio task doctor --task . --reuse-reference doctor/<run-id>/report.json
```

The reference runs are copied into the new report whole — band rows included,
which is what a calibration is built from — each marked `reused_from: <run-id>`,
and counted as the reference runs they are. The record in the vault gets one
line saying where they came from.

It is refused unless the earlier check measured **the same task, at the same
revision, under the same verifier**. The first two are the report's `task` and
`rev`. The third is a new `environment` fingerprint that every report now
carries: a sha256 over the task's `verify` block, its compose file, everything
that compose file bind-mounts into the verify service from inside the task
directory (a file by its bytes, a directory by its sorted tree), the Dockerfile
the service builds from, and the built image's id.

The mounts are **discovered from the compose file**, not from a list of names.
What the container is given is what decides what it measures, and the compose
file is where that is written down — so a task that mounts a differently named
file is covered without this being told about it, and a scratch file nobody
mounts does not invalidate anything.

The fingerprint lists its own components, so a refusal names what differs rather
than only that something does; where docker cannot be reached the image is left
out and the component list says so, which means a fingerprint taken without
docker never silently matches one taken with it.

The image is in there because it is the part of the environment least visible in
a diff. A rebuilt base layer is a different machine to measure on and nothing in
the task directory changes when it happens — and a reference block carried
across that is a green line about a verifier that no longer exists.

## Banded verifiers

A verifier does not have to answer with an exit code. A task whose `verify.kind`
is `band` runs a verifier that prints one measured value per invariant with the
band it is held to, in a CSV block ending each run:

```
invariant,value,ci_half,band_lo,band_hi,verdict
p50_latency_ms,0.482,0.019,0.30,0.45,fail
```

`verdict` is `pass`, `fail`, `skipped` (the input never arrived) or `info`
(reported, never judged). `cmoa verify` reads those rows and reports which
invariant answered, so a report can say *killed via `queue_depth`, 120000
against a band of 0–0* instead of only *the verifier said no* — and can tell "an
invariant left its band" apart from "the harness produced nothing", which one
exit code cannot. `doctor` carries the rows into `report.json` and runs a banded
task one verification at a time, because two containers measuring latency on one
machine measure each other.

### Calibrating a band

**A band is a claim about a machine.** A gate whose bands were calibrated
somewhere else does not report that a host is different; it reports that every
solution is wrong — and the kill rate it then produces is that false positive
read back, because a verifier which rejects the reference rejects the mutants
with it. `doctor` measures exactly this, and `calibrate` is what is done about
it.

```sh
uzushio task calibrate --task . --from doctor/<run-id>/report.json --dry-run
```

**Its only input is doctor reports.** Every band row carries both the value
measured and the band it was judged against, so the reports already hold what
this host reads and what it was expected to read. Nothing is opened in the
task's repository, and this knows nothing about the format any gate keeps its
own bands in.

The rule is a two-sided normal tolerance interval about the median:

```
centre = median(v)
w      = max( k₂(N, 0.95, 0.90) · s , 0.02 · |centre| , floor[unit] )
band   = centre ∓ w
```

`s` is the sample standard deviation between whole verifications and `k₂` is the
two-sided normal tolerance factor — 4.164 at N = 5, falling to 3.021 at N = 10
(NBS Handbook 91 Table A-6; equivalently ISO 16269-6 and NIST/SEMATECH §7.2.6.3).
Three standard deviations would be the obvious multiplier and is the wrong one:
at N = 5 the sample `s` has a 34 % coefficient of variation, so `mean ± 3s` is a
**95/73** interval — it delivers the coverage its label implies about 73 % of the
time. Fewer than five runs is an error, not a warning.

The floors are what keep a band from collapsing. Only two units are recognisable
from an invariant's name, so a ratio or a count that happens to read identically
on every run would otherwise derive the band `[x, x]` — which rejects the next
clean run. The relative floor applies to every derived band; where even that has
no scale to work from (a centre of zero, no spread, no unit) the calibration
**refuses** rather than writing a band nobody can pass. `--rel-floor`,
`--floor-us`, `--floor-ms`, `--centre`, `--spread`, `--k` and `--lower` move the
rule; `--min-runs` moves the threshold.

Three refusals are the substance of it rather than details:

- a band **the reference never left** is copied through unchanged, because
  calibration repairs the bands that do not hold here and widening the ones that
  do would trade a verifier that rejects everything for one that accepts
  everything;
- a **quantised** invariant — whole units against a band of whole units — is
  never derived, because a statistical width over it states the instrument's
  resolution rather than anything about the host; derive that band from what the
  invariant means and make the regression you want caught bigger than one unit;
- an invariant named with **`--keep`** is never derived. Some bands are centred
  on arithmetic rather than on a host — a ratio against a computed expectation —
  and re-centring one on an observed shortfall writes the shortfall down as the
  expectation. The tool cannot tell which those are, so the task says:

```sh
uzushio task calibrate --task . --from doctor/<run-id>/report.json \
  --keep <invariant>
```

A `--keep` that names an invariant nobody measured is refused, since the typo
would mean the band it was meant to protect was re-centred anyway.

### bands.json, and the task's adapter

The output is `bands.json` — uzushio's shape, not any gate's:

```json
{
  "schema_version": 1,
  "task": "<id>", "rev": "<sha>",
  "source_runs": ["<doctor run id>"], "day": "2026-09-05", "n": 5,
  "rule": {
    "statistic": "normal-tolerance-interval",
    "p": 0.95, "gamma": 0.9, "k2": 4.164,
    "rel_floor": 0.02, "floors": {"us": 0.1, "ms": 0.005}, "lower": "derive"
  },
  "invariants": {
    "p50_latency_ms": {
      "lo": 0.4312, "hi": 0.5328, "centre": 0.482,
      "half_width": 0.0508, "three_ci_half": 0.057,
      "kept": false, "reason": "derived: k*stdev"
    }
  }
}
```

`half_width` is null for a kept band, which had none derived. `three_ci_half` is
three times the largest half-range the verifier itself reported — the width a
gate's own guidance usually asks for. It is **recorded and never used**: that
number is the spread *inside* one run with the machine in one state, while a
band has to cover the spread *between* whole verifications. Writing it down beside
the derived width is what makes the deviation from that guidance checkable
rather than asserted.

The key order is deterministic — the schema's for the outside, sorted for the
invariants — so a re-calibration that changed nothing is an empty diff. The file
names the task, the revision, the source runs and the rule, and **nothing about
the machine**: it is committed to somebody's repository.

**Turning `bands.json` into whatever the gate reads is the task's own adapter,
and that boundary is the point.** The shape of a gate's band file is a fact about
somebody else's project; exactly one file per task knows it, and the calibration
stays generic. A task's verifier script is the natural home: it is mounted into
the container, so a change to what the verifier does is a change reviewers can
read as a diff.

### External tasks

A task does not have to be a toy, and its repository does not have to live here:
`setup.sh` can fetch one pinned commit of another project into a gitignored
`repo/`, with nothing added to that project itself and its own verifier used as
the task's. `examples/task-plecto-gate` is the first such task, and the first
banded one — its README says what a banded verifier costs and what it does not
prove.

## Mining patterns and proposing edits

`uzushio improve` is the half of the loop that reads. It walks CMoA's recorded
runs, evaluates fifteen deterministic rules over them, and writes one
`pattern` document per recurring failure.

```sh
uzushio improve --traces <dir>... --vault .
uzushio improve --traces <dir> --vault . --dry-run    # say what it would write
uzushio improve --traces <dir> --vault . --min-support 3 --min-proposers 2
```

Every rule is a predicate over JSON fields and a small closed set of regular
expressions over the text `git apply` and the runner produced. No model is
asked anything, so a second pass over the same traces writes the same bytes —
and where a pattern already exists and is still `open`, the pass **appends** the
new run identifiers to its `evidence:` and leaves the reading somebody wrote
alone. A pattern that has been `resolved` or `withdrawn` is never rewritten;
the runs it refused are reported instead.

Two thresholds decide what becomes a document: `--min-support` (distinct runs,
default 2) and `--min-proposers` (default 1). One occurrence is noise, and a
vault of singletons is a vault nobody reads. Three of the rules carry a floor
of their own on top: a claim about the *pool* — some proposers failed where
others passed, exactly one ever passes — says nothing about a corpus run with
a single proposer, whatever the flags say. What falls below is printed rather
than dropped: "two runs short of a pattern" is the honest answer to "why did
nothing come out".

Not every failure becomes a pattern, and the line is drawn on what an edit could
change. A model server that could not be reached, or that answered 5xx, is an
infrastructure fact: no edit to any surface can fix it, and a document for it
would be one the vault nags about forever. Those are counted, their error texts
reported, and never written. A **4xx** is the other thing — the server read a
request the harness built and refused it, usually because the prompt the harness
poured its surfaces into did not fit the context window — and that *is* a
control action of the harness's own, so it becomes a pattern like any other.

Each pattern is an STPA unsafe control action: the controller is the harness,
the control action is what the harness gives the proposer — an instruction, a
file, a format rule, a time budget — and `context:` says the situation that
control action was unsafe in. A component is a rule's *attribution* and not an
observation, which the document says of itself.

With `--propose`, the second half asks the harness to improve itself, in the
same way the harness is asked to do anything else:

```sh
uzushio improve --traces <dir> --vault . \
  --propose --harness-from-vault \
  --cmoa ./cmoa --config cmoa.json --out runs/improve-1
```

The vault's binding edits are rendered into a harness directory, that directory
becomes the repository of a generated CMoA task, and the instruction is the
pattern plus the rules a harness edit has to obey. `cmoa propose` answers with
unified diffs, and each diff that applies becomes a proposed `edit`:

- a diff under `memory/` or `skills/` becomes an edit whose **body is the
  file** — the content, not a patch, because a patch against a harness state
  cannot be replayed "as of a day";
- a diff to `system-prompt.md` becomes an edit with a sidecar
  `spec/edits/<id>.diff` and its `diff_sha256:`, which is the one surface where
  at most one edit is ever in force;
- a diff touching two surfaces, or any path outside the three, is refused with
  the reason, and the other proposers' answers still land.

Nothing `improve` writes is accepted. A pattern is a claim about the harness
and an edit is a proposal about what to do; `uzushio run` is what settles
either of them.

## Rendering the harness

The vault is the source and the harness directory is derived. `harness render`
asks DocDag which edits are binding on a day, applies them to the seed in
ascending edit id order, and writes the tree with a `render.json` beside it.

```sh
uzushio harness render --vault . --out /tmp/harness
uzushio harness render --vault . --out /tmp/candidate --with-edit he-0007
uzushio harness render --vault . --out /tmp/ablation --without-edit he-0003
```

The tree is three surfaces and nothing else, because those are the three the
harness has an injection point for:

```
system-prompt.md            appended verbatim after the harness's own template
memory/<name>.md            one note per file, rendered in sorted order
skills/<name>/SKILL.md      one line each: the name and the description
```

`memory/` and `skills/` are always present even when empty — the empty
directories are part of the contract, because they are what tell a proposer
where it is allowed to act.

A `memory` or `skill` edit carries its content in the document body: the body
*is* the file, and changing it is a new edit that `supersedes` the old one. A
`system-prompt` edit carries a sidecar unified diff at `spec/edits/<id>.diff`
with its `diff_sha256:` in the frontmatter — DocDag parses only `.md`, so the
digest in the frontmatter is the one thing that makes rewriting those bytes
visible to the vault's append-only check.

A sidecar diff is enumerated before it is applied — `git apply --numstat
--summary --check` — and refused if it writes a path the edit does not declare,
a path outside the three surfaces, a symlink, a submodule, a mode other than
0644, or a binary patch. These diffs are model-generated: without the check, a
`system-prompt` edit could add skills nobody reviewed as skills, and a planted
symlink would make `tree_sha256` a fact about the machine rather than about the
vault.

`render.json` records the day, the vault revision, the ordered edits, the
digest of every file, and `tree_sha256` — SHA-256 over the concatenation of
`<path>\n<sha256>\n` for every file, sorted by path. The harness recomputes
that number from the directory it was handed, and `run` refuses a trial where
the two disagree: a paired comparison between two things nobody checked were
the same is not a comparison.

## Running an edit

`uzushio run` is the half of the loop that decides. It renders the baseline
harness and the same harness with the candidate in it, runs every task of the
suite on both at the same seeds, and compares the pairs.

```sh
uzushio run --edit he-0007 --suite examples/suite-go/suite.json --vault . \
  --cmoa ./cmoa --config cmoa.json
uzushio run --edit he-0007 ... --dry-run    # render, check and plan; run nothing
uzushio run --edit he-0007 ... --aa         # calibrate: the baseline against itself
uzushio run --replay <run-dir>/run.json     # recompute the verdicts from the journal
```

| flag | |
|---|---|
| `--edit`, `--suite`, `--vault`, `--cmoa`, `--config` | all required. `--config` names the fleet, which is the model slug in every document the run writes and part of the baseline cache's key: a run without one is not reproducible |
| `--mode screening\|confirm` | the budget; see the table below. Default `screening` |
| `--out` | the run directory. Default `<suite dir>/runs/<run-id>`, with the shared baseline cache beside it at `<suite dir>/cache` |
| `--as-of YYYY-MM-DD` | the day the baseline's binding set is read for. Default today, UTC |
| `--parallel N` | pairs in flight at once. The statistic is read after each batch, so a larger number buys wall-clock and may overshoot the stopping point by up to `N-1` pairs. Default 1 |
| `--no-cache` | draw the baseline fresh instead of reusing remembered outcomes |
| `--docdag` | the engine binary that answers which edits are binding |

It exits 2, having spent nothing, when the thing it was asked to measure is not
measurable: the edit is not `proposed`, its component has no injection point,
its `paths` and `touches` disagree, it predicts nothing, a suite split is under
the floor, the proposer ids do not make a legal model slug, or adding the edit
leaves the rendered harness unchanged.

**Read the interval, not the verdict.** At tens of tasks a ten-point pass-rate
difference is not reliably detectable, and `inconclusive` is the modal
outcome — that is the design working, not a bug. What the run publishes per
split is an anytime-valid interval on the pass-rate difference; the verdict is
a word read off it.

The unit of observation is the **matched pair**: one task, one repeat index,
one seed, run once against each harness, scored `+1` where only the edited arm
passed, `−1` where only the baseline did, `0` where they agreed. Pairing is not
a refinement — at this budget it is the difference between a usable gate and a
useless one. Baseline outcomes are cached per (task, seed, baseline digest), so
a candidate costs *n* trials rather than 2*n*.

The test is a beta-mixture e-process on the discordant pairs, one per split and
direction. An e-process satisfies Ville's inequality, so the run may look after
every pair and stop the moment a split is decided, or at its cap, with no alpha
spending and no correction for having looked. Per split *s*:

    regress       if E_harm_s >= 1/alpha or lost_tasks_s > k_s
    improve       if delta_lo_s >= -m_s and lost_tasks_s <= k_s
                                        and E_gain_s >= 2/alpha
    hold          if delta_lo_s >= -m_s and lost_tasks_s <= k_s
    inconclusive  otherwise

and the edit is promoted when both splits are `improve` or `hold` and at least
one is `improve`. `regress` is checked first, so a harmful edit is never
reported as a hold. The superiority arm is thresholded at `2/alpha` rather than
`1/alpha` because "a gain on at least one split" is a union of rejection
regions; the non-inferiority and harm arms are asked of both splits, which is
an intersection–union test and needs no correction.

`lost_tasks_s` is the count gate, and it is independent of the statistics: the
number of held-out tasks whose repeats are **all** in and **every one** of
which the baseline won, with `k = 0`. It is a catastrophe detector — a
sequential test at this budget will not notice one task going from always
passing to always failing, and a count will. Both halves of the definition are
load-bearing. Counting a task that is merely net worse, or counting one whose
remaining repeats have not run yet, turns the gate into the dominant verdict:
at twelve tasks and a discordance of 0.2 the looser reading rejects an edit
that changes nothing about four times in five.

| | screening (default) | confirm |
|---|---|---|
| repeats per task | 3 | 5 |
| cap, pairs per split | 60 | 150 |
| published margin `m` | 0.15 | 0.10 |
| certifiable at that cap, `d₀ = 0.2` | ≈ 0.30 | ≈ 0.18 |

**The published margins are not reachable at the published caps.** They were
chosen against an interval that treated the observed discordance as known;
δ = d·(2θ − 1) has *two* unknowns, and plugging the observed d in is not a
level-α statement about δ — it also puts a floor on `delta_lo` at −d̂, so an
edit that lost every discordant pair it had came back certified as no worse
than the baseline. What `run` computes instead bounds both, each at α/2, and
takes the range of d·(2θ − 1) over the product. That interval is about twice as
wide, and it is the one that is true.

The margins and the caps are the owner's, so the consequence is left where it
belongs: `hold` requires the certified bound, and a run that cannot certify its
margin ends `inconclusive`. In practice that means `hold` needs most of the
screening cap — around sixty concordant pairs at `m = 0.15` — and the honest
reading of a shorter run is that it did not settle anything.

`--aa` runs the baseline against itself and reports how often it disagrees with
itself. That number is the parameter every sample-size calculation needs, a
self-test that the harness reproduces itself at all, and the floor on any
effect that can ever be detected. Above 0.4 no verdict but `inconclusive` means
anything, and the run says so.

A run refuses before it spends anything on: an edit that is not `proposed`, a
surface the rendered harness has no injection point for, an edit whose `paths`
and `touches` disagree, an edit that predicts nothing, a suite split under the
floor, and a candidate that leaves the rendered tree unchanged.

A trial that produced no answer — a verifier that could not run, a timeout, the
fleet going away — is not a loss. It is journalled as `pending`, it is not
folded into the evidence, and it is never written to the baseline cache, where
it would be replayed as a baseline *failure* by every candidate measured
afterwards. Five unanswered pairs in a row stop the run.

It leaves behind `run.json` (every parameter, both renders, the decision rule
as a literal string, and the verdicts), `trials.jsonl` (one line per trial,
two per pair), and one `run` document per split in the vault, whose
`pass_rate` and `baseline_pass_rate` are what each arm scored over the pairs
that ran. `--replay` recomputes the pairing *and* the verdicts from the journal
and reports any disagreement with the header: the claim the record makes is
that it is sufficient to re-derive every decision offline, and the replay is
the only thing that can falsify it.

`run.json` and the run documents are written before the vault's edit is
touched, and the status transition is the last thing the run does — so a
failure leaves the edit `proposed` with the measurement on disk, never
accepted or rejected with no record behind it.

What the run does to the edit afterwards is the surface's business, not the
run's: `memory` and `skill` are accepted without asking anybody, every other
surface is left `proposed` with the instruction a reviewer needs, and a
regression is rejected either way.

## The specification is a graph

The corpus is a [DocDag](https://github.com/Kaikei-e/DocDag) v0.4.0 `spec`
graph: clauses with a BCP 14 modality, the conformance tests that enforce
them, and the topics, principles, premises and post-mortems they rest on.
`docdag.yaml` is `preset: spec`; kind directories live under `spec/`.

Ask the graph, not the directory:

```sh
docdag query --binding                 # what binds today, with modality
docdag context UZ-C-001                # a clause and its neighbourhood
docdag resolve UZ-C-001                # what replaced this clause
docdag validate                        # invariants; exits 1 on error
docdag validate --touching spec/clauses/UZ-C-001.md
docdag lint --all                      # the rules, the corpus, and the fixtures
```

`query --binding --as-of YYYY-MM-DD` answers for a day. A `MUST` with no
conformance test is an `orphan_must` error and binds only as a `SHOULD`
until a test exists. Force is derived from the graph; no document writes it.

## Writing a document

```sh
docdag new --kind topic --id topic/seed-recording "Recording the seed of a run"
docdag new --kind clause --id UZ-C-006 "A report names its grader"
docdag new --kind conform --id conform/uz-c-006 "Check that a report names its grader"
```

`--id` is required: a kind's pattern is a spelling, not a sequence. A new
clause has to state `modality:` and `about:` before `validate` is clean.
`enforces:` is declared on the `conform` document, not on the clause, and
the test body lives outside Markdown at the path the document's `test:`
names. `measure` and `run` documents are generated, never written by hand.

## The generated configuration

`docdag.yaml` at the repository root is **generated** and carries a header
saying so. It is assembled in Go under `internal/vault`, on top of DocDag's
`spec` preset, and rendered deterministically — the same code always writes
the same bytes.

It is generated rather than hand-written because most of it is an argument.
Why an accepted `edit` needs a non-regressing held-in run, a non-regressing
held-out run and a strict improvement; why `touches:` is held to the seven
harness surfaces; why a rejection is kept with the run that rejected it —
each of those is a rule with a reason, and a reason belongs beside code that
can test it. The vocabulary the rules are built from lives in
`internal/vocab`, and the harness surfaces come from CMoA itself, embedded in
`internal/surfaces` and refreshed with `go generate`.

```sh
uzushio docdag-config                  # rewrite docdag.yaml
uzushio docdag-config --out other.yaml # write it somewhere else
uzushio docdag-config --check          # exit 1 and print a diff when the file is stale
make generate                          # everything derived from code
make check                             # regenerate, then refuse a difference
```

Edit `internal/vault` and regenerate; do not edit `docdag.yaml` by hand.

The decision records for uzushio itself live under `docs/adr/` and are a
separate corpus with its own `docs/adr/docdag.yaml`
(`docdag validate --config docs/adr/docdag.yaml`).

## Install / Build

```sh
go install github.com/Kaikei-e/uzushio/cmd/uzushio@latest
go install github.com/Kaikei-e/DocDag/cmd/docdag@v0.4.0
```

Both install into `$(go env GOPATH)/bin`. From a checkout:

```sh
make            # build, test, vet, lint
make docdag     # validate, lint --all, and the decision records
make conform    # the shell conformance tests
make e2e        # the health check against real docker and a real cmoa
```

`go test ./...` needs neither docker nor a `cmoa`: the health check is driven
through a fake runner, and the one test that uses the real thing is gated on
`UZUSHIO_E2E=1`. CI runs `go vet`, `go test`, `go build`, golangci-lint and `make check`, then
installs DocDag v0.4.0 from source and runs `validate`, `lint --all` and every
conformance test under `tests/conform/`. On a pull request it also refuses a
rewritten or deleted record. Locally, `pre-commit install` runs `validate` and
`lint` on Markdown and `docdag.yaml` edits; the hook builds `docdag` from
source and needs a Go toolchain.

## Contributing

Issues and pull requests are welcome. A change to a clause is a change to
the graph, so before opening one:

1. Run `docdag validate` and `docdag lint --all` and make both clean.
2. Replace a clause with `supersedes:` and a `reason`; do not edit an
   accepted clause's meaning in place.
3. Give a new `MUST` its conformance test in the same change.
4. If you changed the rules, edit `internal/vault` and run `make check`.
5. One pull request is one roadmap step. Claim it in the title or body as
   `Step N` — the marker convention clause UZ-C-007 is checked against;
   two different steps in one pull request fail the check.

Design notes and the papers this standard leans on are recorded in the
corpus itself (`spec/principles/`, `spec/premises/`, `spec/pm/`), so the
argument for a rule is one `docdag context` away from the rule.

## License

Apache License 2.0. See [LICENSE](LICENSE).
