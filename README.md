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
- a Go module and one command, `uzushio docdag-config`, which generates the
  `docdag.yaml` the corpus is validated under;
- three kinds for the harness-improvement loop — `edit` (a proposed change to
  one harness surface), `pattern` (a recurring failure, written as an STPA
  unsafe control action) and `run` (one evaluation of one edit on one split) —
  declared in the configuration, with their directories still empty.

What does not exist yet: the reference CLI for the loop itself — task
manifests, the verifier runner, and the `doctor` / `run` / `improve` commands
that would write `measure` and `run` documents. Expect identifiers, vocabulary
and layout to change; the graph records those changes as `supersedes` lineage
rather than by rewriting history.

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
```

CI runs `go vet`, `go test`, `go build`, golangci-lint and `make check`, then
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
