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

**29 of the 40 carry a human label naming a position** (`c1`/`c2`/`c3`); the
other 11 are `tie` or `all_bad` in the corpus's own aggregation. Under the
trial's quality handling — `human-position-only, failure-as-zero` — only those
29 enter ΔQ, and the rest are counted as `no_reference_items`. A stage A card
drawing 4 items from D should expect roughly three of them to be evaluable.

## R — the known-failure set (6 items)

Chosen by hand, not sampled, from what the 2026-09-05 and 2026-09-06
calibrations recorded: items that reached `invalid_output` at both seeds, the
two longest runs in the corpus, the item with the most swap disagreements, and
an item settled by the hash tie-break at both seeds over all three candidates.
Each item's `reason` names the evidence and where it was read.

**R carries no `weights`.** It stands for no population — it is a set of items
picked because they already go wrong — so weighting its mean onto the corpus
would be a category error. `judge trial` reports R on its own line and never
averages it into D, and the file was edited to drop the population shares the
generator had copied onto it so that nobody is tempted. That is the one edit
made to the generated manifests.

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

## Reproducing the sample

The generator was a throwaway Python script over a derived `strata.json`; this
repository is a Go module with one generator, `internal/fixture/gen`, wired
into `make generate`, and a script that neither `make generate` nor `make
check` runs is an artefact nobody verifies. It is not committed. What
reproduces the sample is written down instead: seed 20260906, corpus category
shares as the quotas, largest-remainder allocation over 40 and again over the
length bins inside each category, bins cut at 2,454 and 5,224 characters, pools
sorted by id and shuffled with `random.Random(20260906)`, R excluded from D's
pool. Every item also carries its own derivation in `reason`.

These sets are fixed. Adding or dropping items after seeing a candidate's
results is how a set stops measuring anything, so a set that has to change gets
a new file and the card that used the old one keeps naming it.
