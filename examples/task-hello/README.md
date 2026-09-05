# task-hello

A copy of CMoA's own example, carried here because it is what `uzushio task
doctor` and `uzushio task mutate` are demonstrated on. CMoA owns the schema;
this directory tracks it.

The smallest task CMoA can run end to end: a Go package whose `Add`
subtracts, and a test that says so.

```sh
./setup.sh                                  # copies src/ to repo/ and makes it a git repository
cmoa propose --task . --config /path/to/cmoa.json
cmoa select  --task .                       # verifies each candidate in docker
ls runs/*/                                  # the trace
```

`src/` is committed as ordinary files; `repo/` is generated and ignored, so
no git repository is nested inside this one.

## Verifying one diff

`cmoa verify` applies a single diff to a worktree at `rev`, runs the same
container `select` runs, and prints one JSON object. It writes nothing under
`runs/`.

```sh
cmoa verify --task . --diff reference.diff        # pass, exit 0
cmoa verify --task . --diff /dev/null             # the seed state: fail, exit 1
```

## The version 2 fields

`task.json` is version 2, which adds what a verifier doctor measures:

- `reference.diff` is the task's own solution against `rev`. A verifier that
  fails it has a false positive.
- `mutants/*.diff` are deliberate defects a healthy verifier catches. **Each
  mutant is written against the tree with `reference.diff` already applied**,
  so a doctor applies the reference first and the mutant second; a mutant
  diff on its own does not apply to `rev`. All four break `Add` in a way
  `TestAdd` notices.
- `doctor.kill_rate_min` and `doctor.reference_runs` are the thresholds the
  task is judged against.
- `verify.kind` is `exit-code`: the service passes when it exits 0. (The other
  kind is `band`, whose answer is read off the rows the verifier prints rather
  than off its exit code — see `examples/task-plecto-gate`.)
  `verify.timeout_seconds` bounds the container, and
  overrides `verify.timeout_seconds` in `cmoa.json` for both `select` and
  `verify`; `cmoa verify --timeout` overrides both.

To verify a mutant by hand, compose the two diffs in a worktree first:

```sh
rev=$(git -C repo rev-parse HEAD)
git -C repo worktree add --detach --quiet /tmp/m "$rev"
git -C /tmp/m apply "$PWD/reference.diff"
git -C /tmp/m apply "$PWD/mutants/0001-add-times.diff"
git -C /tmp/m diff > /tmp/m.diff
git -C repo worktree remove --force /tmp/m
cmoa verify --task . --diff /tmp/m.diff           # fail, exit 1: the mutant is killed
```

## Measuring the verifier

```sh
./setup.sh                                  # repo/ from src/, if it is not there
uzushio task doctor --task .                # exit 0 healthy, 1 unhealthy, 3 inconclusive
uzushio task doctor --task . --json         # and print the report on stdout
uzushio task mutate --task . --dry-run      # what mutate would add
```

`doctor` verifies `reference.diff` three times and each mutant once, and says
whether the verifier accepted the solution every time and rejected every
defect. It composes the reference and the mutant into one patch itself — a
mutant is written against the reference-applied tree, so neither diff verifies
on its own — and writes `doctor/<run-id>/report.json`.

The first two mutants are hand-written; `0003` and `0004` were produced by
`uzushio task mutate` and carry `origin: generated` and the operator that made
them. `repo/` and `doctor/` are generated and gitignored.
