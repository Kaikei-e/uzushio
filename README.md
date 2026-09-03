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

**Pre-alpha.** What exists today is the specification corpus under `spec/`
and the conformance tests under `tests/conform/`. The reference CLI (task
manifests, the verifier runner, generated `measure` and `run` documents) is
not written yet. Expect identifiers, vocabulary and layout to change; the
graph records those changes as `supersedes` lineage rather than by rewriting
history.

## The specification is a graph

The corpus is a [DocDag](https://github.com/Kaikei-e/DocDag) v0.3.0 `spec`
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
names. `measure` documents are generated, never written by hand.

## Install

```sh
go install github.com/Kaikei-e/DocDag/cmd/docdag@v0.3.0
```

This installs `docdag` into `$(go env GOPATH)/bin`. CI downloads the
v0.3.0 release binary via `Kaikei-e/DocDag@v0.3.0`, runs `validate`, and
runs every conformance test under `tests/conform/`. Locally,
`pre-commit install` runs `validate` and `lint` on Markdown and
`docdag.yaml` edits; the hook builds `docdag` from source and needs a Go
toolchain.

## Contributing

Issues and pull requests are welcome. A change to a clause is a change to
the graph, so before opening one:

1. Run `docdag validate` and `docdag lint --all` and make both clean.
2. Replace a clause with `supersedes:` and a `reason`; do not edit an
   accepted clause's meaning in place.
3. Give a new `MUST` its conformance test in the same change.

Design notes and the papers this standard leans on are recorded in the
corpus itself (`spec/principles/`, `spec/premises/`, `spec/pm/`), so the
argument for a rule is one `docdag context` away from the rule.

## License

Apache License 2.0. See [LICENSE](LICENSE).
