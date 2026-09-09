# P1 chat evaluation

P1 keeps exploration and adoption separate. D is a development set, R is a
known-failure diagnostic set, and H is a final set that remains unopened until
one D/R finalist is fixed. Evaluation tasks, candidate answers, labels,
packets, and any generator that embeds them are non-public data. This document
specifies their required form and operation; it does not publish task text,
answers, labels, or a claim that any local data are representative.

At present, only the evaluation foundation and private diagnostic preparation
exist. No H assessment has run, no real-model quality result has been recorded,
and no selector change has been adopted.

`metadata.json` uses `EvaluationDataset` schema version 1. Every item records
the set, source/source ID, license, task cluster, raw conversation SHA-256,
reason, required language/category/length strata, and candidate IDs. Source IDs
and conversation digests must be unique across D/R/H. Treat one task or
conversation as one cluster; average its repeated turns before inference.

## Labels without identity leakage

Prepare H membership, its strata, sample size, alpha, non-inferiority margin,
quality floor, serious-regression limit, execution conditions, and the label
protocol before finalist selection. Two independent people label every H item.
They receive only `packet.md` and `answers.json`; never give an annotator
`mapping.json`, candidate IDs, model/condition IDs, selection results, existing
labels, or the other annotator's answers. The coordinator retains the mapping
because it joins anonymous A/B/C labels to candidate IDs.

```sh
uzushio judge label-packet --dataset data/p1/metadata.json \
  --suite data/p1/suite.json --set H --annotator reviewer-1 --seed 101 \
  --out data/p1/packets/reviewer-1
uzushio judge label-packet --dataset data/p1/metadata.json \
  --suite data/p1/suite.json --set H --annotator reviewer-2 --seed 202 \
  --out data/p1/packets/reviewer-2

uzushio judge label-import --packet data/p1/packets/reviewer-1 \
  --answers data/p1/returned/reviewer-1.json --out data/p1/labels/reviewer-1.json
uzushio judge label-import --packet data/p1/packets/reviewer-2 \
  --answers data/p1/returned/reviewer-2.json --out data/p1/labels/reviewer-2.json
```

`label-import` verifies the mapping, packet, and input hashes and emits one
human annotation per item. Each imported annotation binds SHA-256 values for
the task declaration, exact conversation, rubric, optional reference, and every
candidate body. The declaration's paths are used for both annotation and judging.
Merge only imports
that bind identical content; assessment rechecks these values against the H
assets after the H claim and before measurement.

```sh
# Writes a reviewable merged file. It exits nonzero after writing if a human
# disagreement remains; give an adjudications JSON only after a third person
# supplies a rationale and decision for each disagreement.
uzushio judge label-merge --dataset data/p1/metadata.json --set H \
  --input data/p1/labels/reviewer-1.json --input data/p1/labels/reviewer-2.json \
  --out data/p1/labels/h-merged.json
uzushio judge label-merge --dataset data/p1/metadata.json --set H \
  --input data/p1/labels/reviewer-1.json --input data/p1/labels/reviewer-2.json \
  --adjudications data/p1/labels/h-adjudications.json \
  --out data/p1/labels/h-resolved.json
```

Keep packet mappings, original answers, imports, adjudications, and the merged
label artifact as pinned private inputs.

## Finalist versus assessment

`judge trial` is the exploratory A/B path in [ADR 0010](adr/0010-judge-trials.md).
Its D/R point-estimate outcome `finalist` only identifies the one candidate
eligible for H; it is neither non-inferiority evidence nor adoption. R is
reported separately and cannot compensate for a D regression.

`judge assess` runs exactly that finalist on frozen H. Its stage-C trial plan
has one H manifest, full positive H take equal to `max_items`, fresh execution
(`"reuse":{"kind":"none"}`), counterbalanced alternating blocks, and pinned
base/candidate configs. Conditions with `switch` are unsupported: H confirms
one pinned base/candidate fleet, rather than a routing or fallback policy. The
enclosing assessment card pins the D/R finalist
report, resume and journal; dataset, labels and H digest; and the rule below.

```jsonc
{
  "schema_version": 1, "id": "selector-x-h",
  "finalist": {
    "report": {"path":"../trial/trial.json","sha256":"<64 hex>"},
    "resume": {"path":"../trial/resume.json","sha256":"<64 hex>"},
    "results":{"path":"../trial/results.jsonl","sha256":"<64 hex>"}
  },
  "dataset":{"path":"../data/p1/metadata.json","sha256":"<64 hex>"},
  "holdout_sha256":"<HoldoutDigest>",
  "labels":{"path":"../data/p1/labels/h-resolved.json","sha256":"<64 hex>"},
  "rules":{"alpha":0.05,"noninferiority_margin":0.10,"quality_floor":0.80,
           "min_clusters":238,"max_observed_serious_regressions":0},
  "trial":{"schema_version":1,"id":"selector-x-h","hypothesis":"frozen before H",
    "change":"the D/R finalist only","stage":"C","suite":"../data/p1/suite.json",
    "manifests":["../data/p1/sets/H.json"],"take":{"H":238},"max_items":238,
    "base":{"id":"base","config":"base.json","config_sha256":"<64 hex>"},
    "candidate":{"id":"selector-x","config":"candidate.json","config_sha256":"<64 hex>"},
    "reuse":{"kind":"none"},"seed":1,"judge_seed":7,"rules":{"kind":"quality"},
    "alternating_blocks":true,"block_size":4}
}
```

Values are examples, never defaults. `--dry-run` validates card syntax without
reading, hashing, claiming, or consuming H. The real run needs one shared,
durable opening registry:

```sh
uzushio judge assess --card data/p1/cards/selector-x-h.json --cmoa /path/to/cmoa \
  --out data/p1/assessments/selector-x-h --registry data/p1/holdout-registry --vault .
```

Before opening H, the command checks pins. It atomically writes three O_EXCL
claims per item: `conversation`, `source`, and `source_cluster`. Their keys are
canonical hashes of the relevant stable metadata, and every record carries H
digest, assessment/runtime fingerprints, candidate ID, opening time, source,
source ID, conversation SHA, cluster, and key kind. The independent source and
conversation claims prevent either field being substituted under a new local
task ID; the source-cluster key protects their combination. A fleet failure or
partial claim leaves H claimed. Only `--resume` of the identical
assessment/card/runtime may continue. Runtime includes the canonical `--out`
directory, so resume must use the same output directory and its nested
`trial/` journal; changing it is a new runtime and cannot resume the claim. A
different candidate or runtime needs a new H/audit set. Preserve this registry
as the opening ledger.

## Fixed cluster recommendation

For each task cluster, assessment calculates base accuracy, candidate accuracy,
and paired delta after averaging internal rows. It applies Maurer--Pontil's
fixed-sample empirical-Bernstein interval to paired delta on `[-1,1]` and
candidate quality on `[0,1]`. The card alpha is split: each interval receives
`alpha/2`, and each two-sided interval internally uses a union bound. This
keeps a finite nonzero interval when all cluster differences tie, unlike a
naive bootstrap.

`adopt` requires complete measurement, `min_clusters`, no more than the frozen
serious-regression limit, paired lower bound at least `-margin`, and candidate
quality lower bound at least the floor. Exceeding a serious-regression limit or
missing a frozen boundary even at its upper bound gives `reject`; otherwise the
result is `inconclusive`. Machine failures, invalid judge output, timeouts and
unmeasured items remain distinct and prevent a pass; they are not silently
dropped as complete cases. Route/settlement metrics are diagnostics, not
post-treatment adoption strata.

The 238 example is the best-case, zero-variance cluster count for a 0.10
paired margin at total alpha 0.05 after the two-interval split and two-sided
bound. It follows from `14*ln(8/0.05)/(3*(n-1)) <= 0.10`; the corresponding
0.05 margin needs 475 clusters. Observed variance can require more clusters,
and the quality-floor interval (for example, a floor of 0.80) can independently
be the limiting condition. No interval makes a small or selected H
representative by itself.

## Sources and ADR status

[Maurer & Pontil (2009)](https://arxiv.org/abs/0907.3740) supplies the finite
empirical-Bernstein bound; its independent-observation and fixed-hypothesis
conditions are required. [Hoeffding (1963)](https://doi.org/10.1080/01621459.1963.10500830)
is the conservative bounded alternative. [Dwork et al. (2015)](https://arxiv.org/abs/1506.02629)
and [Blum & Hardt (2015)](https://arxiv.org/abs/1502.04585) explain the need
to prevent adaptive H reuse. [MT-Bench](https://arxiv.org/abs/2306.05685)
motivates measuring selection separately from candidate generation.

No new ADR is needed: ADR 0010 already reserves independent, fixed stage-C
confirmation with `adopt/drop/inconclusive`. `assess` implements that scope
with pinned inputs, anonymous labels and a holdout registry; it does not modify
the accepted trial policy, calibration semantics, or deployment authority.
A change to those decisions requires a superseding ADR.
