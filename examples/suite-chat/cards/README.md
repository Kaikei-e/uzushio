# The experiment cards a `judge trial` runs

A card is written **before** the run and echoed into `trial.json`, which is the
whole of its job: a threshold moved after the numbers are in is only visible as
one when the card and the result are in the same file. The schema is in the
README's *Trying a change before paying for a calibration* and the policy is
ADR 0010.

Two cards are committed here. Both are stage A, both take **4 items of D and 2
of R**, both give the box **600 seconds**, and both **measure the base** rather
than reading it back.

| card | change | conditions |
|---|---|---|
| `control.json` | nothing | the same configuration twice, under two identifiers |
| `cf-2-reason-200.json` | the judge's reason budget, 400 → 200 characters | two harness builds |

## No absolute paths

Nothing in a committed card names a machine. Every path is relative to the
card's own directory — `../suite.json`, `../sets/D.json` — and the two
placeholders are:

- **`cmoa-chat.json`**, the harness configuration. It is a local file: a
  configuration lives wherever its owner keeps it, and this repository does not
  hold one. Put yours beside the card under that name, or point the card at
  your own relative path before running it. The card pins nothing until you add
  `config_sha256`, and once you do, `judge trial` checks it before spending
  anything.
- **`cmoa-reason-200`**, the second harness build. A value with no `/` is a
  name looked up on `PATH`, which is how a card names a second binary without
  naming a directory. Build it, put it on `PATH` under that name, and the
  runner will use it for the candidate condition only.

## Why `control.json` exists

It measures nothing and that is the point. Two identical conditions over the
same six items give the run-to-run difference of the judge itself — the ΔQ and
the time ratio a change has to beat to mean anything. A candidate that moves
one item on six is inside whatever this card reports, and reading it as an
effect is the mistake the control is committed to prevent. Run it once at the
current settings, and again whenever the judge settings change.

## Why `cf-2-reason-200.json` names a binary

The reason budget is a constant in CMoA's source, not a key in its
configuration, so the two conditions are two builds rather than two files. The
card says so with `cmoa` on the candidate condition, and the build reaches the
reuse key as `cmoa_version`, read from the run the harness actually made.

That has a consequence the runner takes without being asked: **when the two
conditions name two builds, the base is always measured.** The base's expected
key is resolved from the candidate's first run, and a run made by the other
build cannot answer for it.

## Why neither card reuses a saved run

The saved calibration runs of 2026-09-05 were made at judge `max_tokens` 512
and `parallel` 3. Today's configuration is 256 and 6. Those are different judge
settings, so the reuse key differs, so **the 2026-09-05 runs are not the base
condition for anything measured today** — a comparison against them would mix
the change under test with a generation budget and a parallelism nobody wrote
into the card. `judge trial` refuses them by key rather than by anybody
remembering, and names the field that rejected them in `reuse.key_mismatch`.

So a judge-setting experiment measures the base **once**, at today's settings.
After that the trial's own journal is the base for the next card:

```jsonc
"reuse": {"kind": "trial", "source": "../cards/trial-control-2026-09-06"}
```

which reuses only where the whole key still matches — same build, same
selection rule, same judge settings, same seeds.

## What the clock is expected to do

At the measured per-item cost these cards do not fit their own box: six items
with both conditions measured is an estimated **636 seconds** against 600: six
items × two conditions × 48 seconds plus 60 seconds of overhead.
`planning.accept_cut` records that the card accepts this risk: the runner runs
the fixed order, the budget stops new work, and the report says
`interrupted_items`, `stop_reason: out_of_budget` (時間・資源切れ) and
`suggested: inconclusive`. It is not a guarantee that the six items finish in
ten minutes, or an automatic improvement in evaluation agility. Expect **4–5
of the 6** to finish at today's speeds.

These six items cannot measure representative quality at the default
`min_evaluable_items: 8`, even if their order changes. A reports behaviour,
retry, time, and individual D/R regressions; its two-item early cut keeps the
D/R counts. Representative quality is D-only in A/B (H-only in C), so R is a
separate diagnosis and cannot rescue a D regression; quality comparisons move
to B, subject to the same evidence gates.

The order is fixed before the first call, and the interleave is why a cut is
survivable: `D1 R1 D2 D3 R2 D4`, so a clock that stops the run drops at most
one known-failure item rather than both.

## A condition that costs something to enter

Where a condition is not a configuration key — a reasoning budget that lives in
a compose file, say — entering it is a rewrite and a restart, and that cost is
**inside** `T_eval`. A condition may carry a switch hook:

```jsonc
"candidate": {
  "id": "cf-1-budget-96",
  "config": "cmoa-chat.json",
  "switch": {
    "command": "./switch-judge.sh 96",
    "ready_url": "http://127.0.0.1:8090/v1/models",
    "timeout_seconds": 300
  }
}
```

The runner runs it when it enters the condition — including the first time,
because the fleet starts in whatever state the last experiment left — waits for
the URL, and reports the seconds as their own `switch` phase beside load, wait,
measure and aggregate. The planning estimate subtracts them from the budget
before it decides what the take can afford; the measured model load alone was
**19.9 s**, and the stop and the polling on top of it are not yet measured.

What is **not** in `T_eval` is first-time preparation — downloading a model,
compiling a runtime, building the second binary. That is a preparation cost
with its own line, and hiding it inside a per-switch number is how "it takes
ten minutes to try" stops being true.
