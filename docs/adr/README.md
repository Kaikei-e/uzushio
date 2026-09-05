# Architecture decision records

These records explain why the uzushio *implementation* is shaped as it is. They are not clauses of
the standard: the normative corpus lives under [`../../spec/`](../../spec/) and answers to the `spec`
preset, while this directory answers to DocDag's `adr` preset through its own
[`docdag.yaml`](docdag.yaml). Records from 0001 onward are written in Japanese, the language they
were argued in.

Check them with:

```sh
docdag validate --config docs/adr/docdag.yaml
```

Run that from the repository root: the corpus's `dir` is resolved against the working directory.
Only `validate` applies to this corpus; `lint --all` reads the fixtures under `lint/`, which belong
to the specification vault.

A decision that replaces one of these declares `supersedes:` in its frontmatter and moves the old
record's status to `superseded`; nobody edits an accepted record to change what it decided.

| record | decides |
| --- | --- |
| [0001](0001-adopt-docdag-for-decision-records.md) | decision records live here, under the `adr` preset, apart from the normative vault |
| [0002](0002-vault-configuration-in-go.md) | the normative vault's `docdag.yaml` is generated from Go values in `internal/vault`; the harness-surface vocabulary is embedded from `cmoa surfaces --format json` |
| [0003](0003-harness-edit-vocabulary.md) | the `edit` / `pattern` / `run` kinds, the `predicts` / `validates` edges, the projections that define a binding edit, the nine rules, and the four-valued verdict behind the acceptance rule |
| [0004](0004-fixtures-and-generated-documents.md) | per-rule fixtures and machine-written documents come from the same Go types; fixtures are committed under `lint/`; append-only history is checked twice |
| [0005](0005-task-doctor.md) | superseded by 0006 — `task doctor` measures a verifier by false positives against the reference solution and the kill rate against injected mutants, records the verdict as a `verifier` document, and `task mutate` generates Go mutants deterministically as committed diffs |
| [0006](0006-band-verifiers-and-the-first-external-task.md) | carries 0005 forward and adds: banded verifiers (a gate CSV judged by `cmoa verify`) are measured too; the first external task wraps an OSS performance gate under `examples/`, not in that project; the doctor runs it one verification at a time, locally, and not on GitHub Actions |
| [0007](0007-calibrate-bands.md) | bands of a banded verifier are re-centred on the task's side as a normal tolerance interval from the reference runs; the generic `uzushio task calibrate` emits only `bands.json`, and the task's own adapter renders whatever the gate reads; mutants are sized against the derived half-width; the environment fingerprint decides when a re-calibration is due |
| [0008](0008-run-and-improve.md) | `run` and `improve`: the harness is rendered from the vault's binding edits (memory and skill bodies as files, system-prompt as ordered sidecar diffs) and read by `cmoa propose --harness`; edits are judged on paired trials with an anytime-valid e-process and accepted according to each surface's autonomy; patterns are mined deterministically and edits proposed by `cmoa propose`; the previous project's constraints become clauses UZ-C-006–008 |
| [0009](0009-judge-calibration.md) | a judge is a grader whose health is agreement, not a kill rate: reliability (position-swap and re-run κ) and validity (human κ) are three coefficients kept apart, every number carries its tie handling and its n, the `calibration` document expires 30 days after its window so an unmeasured judge drops out of `binding`, and the calibration corpus is derived from public pairwise human judgments as acyclic triples stratified by margin (`uzushio judge import-mtbench|calibrate|status`, UZ-C-009) |
