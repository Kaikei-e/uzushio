# suite-go

Thirty-six small Go repair tasks, in the shape `examples/task-hello`
established: a seed state that fails its test, a `reference.diff` that fixes
it, and hand-written mutants that the test has to reject.

Each task is a Go module of its own under `<task>/src/`, one or two source
files and a test. `setup.sh` copies `src/` to `repo/` and makes it a git
repository, so no git repository is nested inside this one; `repo/`,
`runs/` and `doctor/` are generated and gitignored.

```sh
cd inventory-reserve
./setup.sh
uzushio task doctor --task .                     # the verifier is measured first
cmoa propose --task . --config /path/to/cmoa.json
cmoa select  --task . --config /path/to/cmoa.json
```

`suite.json` lists every task with its split. `held-in` tasks are the ones
a harness may be tuned against; `held-out` tasks are kept back to measure
whether the tuning generalised. The tasks are listed in a difficulty
ordering and every third one is held out, so each split spans the range
rather than collecting the easy tasks at one end. Its `constraints` block
is a placeholder the orchestrator finalises.

`BASELINE.md` records what the local three-proposer fleet scored on each
task, which is what makes a later number comparable.

## What the tasks are

Every task is one small defect of a kind that turns up in real Go: an
off-by-one, a comparison the wrong way round, a nil that is not guarded, a
byte count where a rune count was meant, a lock that is not held, a channel
that is never closed. None of them is a textbook kata, because a model that
has memorised FizzBuzz tells you nothing about a harness.

An instruction says what the code has to do, in terms of behaviour, and
never how to write the fix; it is written to be enough on its own, without
the reference. Every instruction ends by saying that the test is not to be
changed, because a test edited into agreement is the cheapest wrong answer
available.

## The verifier

`compose.yaml` runs `go test ./...` in `golang:1.27`. Two tasks —
`metrics-counter` and `ingest-waitgroup` — are about concurrency, and their
verifier runs `go test -race` instead, with cgo enabled, because a data
race is not something an exit code notices on its own.

`task.json` is version 2 throughout: `reference.diff` against the seed
revision, three hand-written mutants per task written against the tree with
the reference already applied, and `doctor: {kill_rate_min: 0.8,
reference_runs: 2}`.
