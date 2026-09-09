# Lint fixtures

DocDag asks every rule a configuration declares for two corpora: one under
`ruleid/`, where the rule has to report something, and one under `ok/`, where it
must stay silent. `docdag lint --fixtures lint` reads them; `docdag lint --all`
reads them alongside the configuration and the vault.

A fixture is what turns a rule from a claim into something that has been shown
to work. It is also what keeps a young corpus honest: a rule that fires nowhere
in `spec/` is reported as `never_fired` unless a `ruleid` fixture shows it can
fire at all.

The layout is DocDag's:

```
lint/<name>/{ruleid,ok}/<kind directory>/<file>.md
```

with the kind directory written relative to the corpus root, exactly as
`docdag.yaml` writes it (`spec/edits`, `spec/patterns`, `spec/runs`, …).

## Generated directories

These are **generated** by `internal/fixture` from the typed writers in
`internal/doc`. Do not edit them by hand — regenerate with:

```
make generate            # or: go run ./internal/fixture/gen -out lint
```

`make check` regenerates them and fails on any difference, so a hand edit here
is caught in CI rather than in review.

The surfaces the documents name are read out of CMoA's vocabulary — the first
surface CMoA declares under each autonomy word — rather than written into the
generator, so a surface that changes autonomy changes the fixtures.

One directory per rule uzushio adds on top of the spec preset:

| directory | what the pair shows |
| --- | --- |
| `accepted_unvalidated` | an accepted edit with no run behind it, against one with a held-in gain and a held-out hold |
| `edit_touches_readonly` | **two** firing documents — the case the rule is named after, an edit whose `touches:` reaches a read-only component, and the degenerate one, an edit that writes no `touches:` at all (`subset_of` answers false on an absent key) — against an edit that names two of the seven surfaces. Both firing documents are written as raw text, because `internal/doc` refuses to produce either, which is the point of it |
| `edit_without_prediction` | an edit that claims nothing, against one that predicts a pattern |
| `rejected_without_run` | a rejection with no evidence, against one kept beside the run that settled it |
| `propose_only_accepted` | an accepted edit to a propose-only surface, against a proposed one |
| `accepted_without_approver` | `approval: auto` on a human-approval surface, against `approval: human` |
| `pattern_resolved_unfixed` | a resolved pattern nothing points at, against one an accepted edit predicted |
| `predicts_withdrawn` | a live edit aimed at a withdrawn pattern, against one aimed at an open one |
| `edit_without_topic` | an edit with no `about:`, against one that names a topic |

and one per projection whose truth a rule or the binding set reads:

| directory | what the pair shows |
| --- | --- |
| `validated_in` | a held-in run that held, against an edit measured only on the held-out split |
| `validated_out` | a held-out run that held, against an edit measured only on the held-in split |
| `improved` | a strict gain over the baseline, against a run that only held |
| `effective` | uzushio's kind-scoped alternative to the preset's binding projection: an accepted, measured, in-force, unreplaced edit, against a proposal |
| `has_inforce_successor` | the preset projection read over an edit lineage: a superseded edit whose replacement is accepted and in force, against an edit nobody has replaced |

## Copied from DocDag

These are **byte copies** of the fixtures DocDag ships for the `spec` preset's
own rules and structural checks, taken from
[`testdata/lint/spec/`](https://github.com/Kaikei-e/DocDag/tree/v0.4.1/testdata/lint/spec)
at **v0.4.1**:

```
deviation_pressure  excepts_strict     interop_not_must  may_without_interop
modality_conflict   no_counterexample  orphan_must       orphan_test
pending_successor   premature_superseded                 stale_premise
stale_target        status_drift
```

They are here because `docdag lint --all` reads only this repository's `lint/`
directory: without them the ten preset rules report `never_fired` against a
corpus too young to have broken any of them yet. `internal/fixture` never
writes to or removes these directories.

They carry DocDag's licence, which is the same as this repository's:
**Apache-2.0**. See DocDag's `LICENSE`.

Refresh them by re-copying from the DocDag release the repository pins:

```
cp -r <DocDag checkout>/testdata/lint/spec/<rule>/ lint/<rule>/
```
