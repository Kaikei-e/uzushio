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
