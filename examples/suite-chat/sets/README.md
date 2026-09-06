# The item sets a `judge trial` runs over

`D.json`, `R.json` and `H.json` are item manifests in the schema `uzushio judge
trial` reads (see the README's *Trying a change before paying for a
calibration*, and ADR 0010 D8). They are over `../suite.json` — 200 items
derived from `lmsys/mt_bench_human_judgments` — and an experiment card names
them by path:

```jsonc
"suite": "../examples/suite-chat/suite.json",
"manifests": ["../examples/suite-chat/sets/D.json",
              "../examples/suite-chat/sets/R.json"]
```

This directory is the one convention: a set over a suite lives beside that
suite. Nothing here is a model output, and nothing here names a path outside
the repository.

## D — the development representative set (40 items)

Stratified by **category** (the corpus's own eight), **length_bin** and
**language**, and sampled at seed **20260906**.

- **Category quotas** are the corpus shares, allocated over 40 by largest
  remainder. `weights` in the file carries those shares, so
  `judge trial` can re-weight D's mean onto the corpus rather than onto
  whatever the sample happened to contain. The shares are
  `stem 0.180, math 0.150, humanities 0.130, roleplay 0.125, writing 0.115,
  extraction 0.110, coding 0.095, reasoning 0.095`, giving quotas
  `stem 7, math 6, humanities 5, roleplay 5, writing 5, extraction 4,
  coding 4, reasoning 4`.
- **Length bins** are terciles of the corpus by total candidate characters,
  cut at **2,454** and **5,224** characters: `short` below the first cut,
  `medium` between, `long` above. Inside each category the bin quota is again
  proportional with largest remainder, which lands D at 14 short, 13 medium,
  13 long.
- **Language** is `en` throughout; the corpus is English. A Japanese set would
  be over `examples/suite-chat-ja`, and there is not one yet.
- Within a (category, bin) pool the items are sorted by id and shuffled with
  `random.Random(20260906)`. Every item's `reason` field records its own
  category share, its two quotas and that sampling step, so one item can be
  traced without re-running anything.
- **R's items are excluded from D's pool**, so the two sets do not overlap and
  a card may list both. `judge trial` refuses a card whose manifests name one
  item twice.

### The execution order

The sample decides **which** 40 items. It does not decide the order they run
in, and the order is what a stage actually uses: a stage runs a **prefix**, so
a set ordered by id is a set whose first eight items are whatever the ids
happened to be — for this sample, five writing items and three roleplay ones,
with `stem` and `humanities` unseen at 24. A prefix like that is not a
representative set, and no weighting recovers a category nobody ran.

So the file order is the execution order, it is fixed here before any result,
and it is this recipe:

1. **Categories in corpus-share order**: `stem, math, humanities, roleplay,
   writing, extraction, reasoning, coding` (0.180, 0.150, 0.130, 0.125, 0.115,
   0.110, 0.095, 0.095; `reasoning` before `coding` at the tie).
2. **Round-robin**: one item per category per round, skipping a category that
   has run out. Seven rounds empty the set.
3. **Length bins inside a category**: `short → medium → long`, rotating, and
   the rotation **continues across rounds**. Category *k* (0-based in the order
   above) starts its rotation at bin *k* mod 3 — `stem` at short, `math` at
   medium, `humanities` at long, and so on — so the first round already holds
   all three lengths instead of eight short items. A bin that is empty is
   skipped and the rotation moves on; ties inside a bin go by id.

Every item also carries `order`, its 1-based position, which is the same
statement twice on purpose: `judge trial` refuses a manifest whose numbering
disagrees with its file order, so a reordering has to be deliberate rather than
a diff nobody read.

**What the prefixes are made of** (D alone, before R is interleaved):

| first | categories | short / medium / long |
|---:|---|---|
| 4 | stem, math, humanities, roleplay — 1 each | 1 / 2 / 1 |
| 6 | + writing, extraction — 1 each | 1 / 3 / 2 |
| 8 | all eight — 1 each | 2 / 4 / 2 |
| 12 | stem, math, humanities, roleplay 2; the other four 1 | 3 / 6 / 3 |
| 24 | all eight — 3 each | 8 / 9 / 7 |
| 40 | stem 7, math 6, humanities 5, roleplay 5, writing 5, extraction 4, reasoning 4, coding 4 | 14 / 13 / 13 |

Categories are balanced from the first four; the lengths are balanced from the
first four as well, which is what the phase offset in step 3 buys. Nothing
about the composition is chosen after a result, and a set that has to change
gets a new file.

### Where R goes

A card that names D and R runs them **interleaved**, not one after the other.
The reason is the clock: a run that put its two known-failure items last would,
on the day the budget stopped it, drop exactly the items it was carrying them
for — and drop both. R item *i* of *r* follows D item round(*i·d*/(*r*+1)), so
the stage A take of four and two runs

```
D1  R1  D2  D3  R2  D4
```

— `mtb-142-t1-j` (stem, medium), `mtb-136-t1-h` (R, extraction, short),
`mtb-120-t2-a` (math, medium), `mtb-152-t1-g` (humanities, long),
`mtb-105-t1-e` (R, reasoning, short), `mtb-095-t1-j` (roleplay, short): six
categories, and short 3 / medium 2 / long 1. A cut at four or five items still
leaves one R item and four categories. The order is written into `trial.json`
**before the first call**, with the composition counted, so a prefix cannot be
chosen after the fact.

**29 of the 40 carry a human label naming a position** (`c1`/`c2`/`c3`); the
other 11 are `tie` or `all_bad` in the corpus's own aggregation. Under the
trial's quality handling — `human-position-only, failure-as-zero` — only those
29 enter ΔQ, and the rest are counted as `no_reference_items`. The committed
A prefix D4 has **two** evaluable D labels (the full six-item D+R order has
three labels total, with R remaining diagnostic only).

That is below the floor. `judge trial` prints **no representative quality
difference** under `min_evaluable_items` (default 8) and reports 未評価 instead.
No ordering of the six A items can create eight labels. A stage A run measures
**behaviour, retry, and time**; changed selections remain named, and the
two-item heuristic still reads D/R individual regressions. It is a development
ordering rule, not a quality result; quality comparisons move to B, subject to
the same evidence gates.

## R — the known-failure set (6 items)

Chosen by hand, not sampled, from what the 2026-09-05 and 2026-09-06
calibrations recorded: items that reached `invalid_output` at both seeds, the
two longest runs in the corpus, the item with the most swap disagreements, and
an item settled by the hash tie-break at both seeds over all three candidates.
Each item's `reason` names the evidence and where it was read.

**R carries no `weights`.** It stands for no population — it is a set of items
picked because they already go wrong — so weighting its mean onto the corpus
would be a category error. `judge trial` reports R on its own diagnostic line
and never averages it into D: an R rescue cannot offset a D regression. An
R-only card consequently has no representative quality result. The file was
edited to drop the population shares the generator had copied onto it so that
nobody is tempted. That is the one edit made to the generated manifests.

## H — held out for the adoption check (0 items)

**H is empty, and that is the finding rather than an omission.** All 200
labelled items were used in the 2026-09-05 and 2026-09-06 calibrations, so
there is nothing left that development has not already seen. Splitting the
corpus now would not recover a holdout: a set that has already been read is not
independent because a file says it is.

So the generalisation confirmation is **未済 / not done**. Existing-corpus
replay continues as a regression check and is worth having; it is not evidence
that a change generalises, and a report must not call it that. H gets items
when there are labelled items nobody has developed against — the Japanese
labelling page is the likely source.

`uzushio judge trial` **refuses `H.json`** with `holds no items`, and that
refusal is correct: a card cannot claim a stage C confirmation over an empty
set. The file is committed as the record of why, and as the place items go when
there are any. It is not fixed by editing it, because the only edit that would
make it load is inventing items.

## What fits in a time box

From a pilot on this corpus, with the trial's own measured per-item wall time:

| | |
|---|---|
| per-item mean | **48.0 s** |
| slow-item allowance used for planning | **67.0 s** |
| load and aggregate allowance | 60 s |

ADR 0010 D1 plans by time before it plans by count —
`planned = min(stage cap, floor((budget − load/aggregate) / per-item cost))` —
and with the allowance above:

| stage | box | candidate only | both conditions measured |
|---|---|---|---|
| A | 600 s | **8 items** | **4 items** |
| B | 1800 s cumulative | **~25 items** | **~12 items** |

A **condition switch** comes out of the same budget. Where a condition is not a
configuration key — a reasoning budget in a compose file — entering it is a
rewrite, a restart and a ready wait, and §4.1 puts that inside `T_eval`: an
experiment whose inference fits ten minutes only because the two restarts
around it were not counted has not fitted ten minutes. A card declares
`planning.switch_seconds_estimate` and the runner subtracts *switches × that*
from the budget before deciding what the take affords. The model load alone
measured **19.9 s**; the stop and the readiness polling on top of it are not
measured yet, so an estimate built from 19.9 alone is optimistic and says so.
First-time preparation — a download, a compile, a second binary — is **not** in
`T_eval` and is not in that number.

The committed stage A cards take D4 + R2, which is **six** items with both
conditions measured: about 606 s against a 600 s box. The answer is not a
bigger box. They set `planning.accept_cut`, the runner runs the fixed order,
the budget stops new work, and the report says `interrupted_items`,
`out_of_budget` / 時間・資源切れ and `inconclusive`. **Expect 4–5 of the 6 to
finish** at today's speeds — and because R is interleaved rather than appended,
a cut there still leaves a known-failure item in the comparison.

**40 items do not fit thirty minutes.** At the slow-item allowance they are
about 45 minutes with the base reused and about 90 with both conditions
measured. The 40-item cap in ADR 0010 D8 is a ceiling on the set, not a
promise about the box; a stage B card should name the number the arithmetic
above allows and let the budget stop the rest, which the report then shows as
`interrupted_items` rather than hiding.

Reusing the base is what buys the difference between the two columns, and it
is only available when the reuse key matches — same harness build, same
selection rule, same judge settings, same seeds. A run that changes any of
those pays the right-hand column.

## Reproducing the sample and the order

The generator was a throwaway Python script over a derived `strata.json`; this
repository is a Go module with one generator, `internal/fixture/gen`, wired
into `make generate`, and a script that neither `make generate` nor `make
check` runs is an artefact nobody verifies. It is not committed. What
reproduces the sample is written down instead: seed 20260906, corpus category
shares as the quotas, largest-remainder allocation over 40 and again over the
length bins inside each category, bins cut at 2,454 and 5,224 characters, pools
sorted by id and shuffled with `random.Random(20260906)`, R excluded from D's
pool. Every item also carries its own derivation in `reason`.

The **order** is reproducible without any of that, from the file alone: the
recipe above is deterministic over the 40 items and their strata, and the
`order` field is checked against the file's own order every time a manifest
loads.

These sets are fixed. Adding or dropping items after seeing a candidate's
results is how a set stops measuring anything, so a set that has to change gets
a new file and the card that used the old one keeps naming it.
