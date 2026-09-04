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
- a Go module and three commands: `uzushio docdag-config`, which generates the
  `docdag.yaml` the corpus is validated under; `uzushio task doctor`, which
  measures a CMoA task's verifier; and `uzushio task mutate`, which writes the
  mutants it is measured with;
- four kinds for the harness-improvement loop — `edit` (a proposed change to
  one harness surface), `pattern` (a recurring failure, written as an STPA
  unsafe control action), `run` (one evaluation of one edit on one split) and
  `verifier` (one health check of one task's verifier) — declared in the
  configuration. Only `verifier` has a writer that fills its directory so far.

What does not exist yet: the `run` and `improve` commands that drive the loop
and write `run` documents. Expect identifiers, vocabulary and layout to change;
the graph records those changes as `supersedes` lineage rather than by
rewriting history.

## Task doctor

A pass rate is a number about nothing if the verifier says pass to anything,
and a harness edit accepted on a verifier that rejects a correct solution is
an edit accepted for the wrong reason. So before a task is used to measure
anything, the verifier itself is measured.

```sh
uzushio task doctor --task examples/task-hello        # measure the verifier
uzushio task doctor --task . --vault . --json         # record it, and print the report
uzushio task mutate --task . --operators arith,cond   # write more mutants
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

Design notes and the papers this standard leans on are recorded in the
corpus itself (`spec/principles/`, `spec/premises/`, `spec/pm/`), so the
argument for a rule is one `docdag context` away from the rule.

## License

Apache License 2.0. See [LICENSE](LICENSE).
