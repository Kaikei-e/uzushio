---
id: premise/docdag-v0-4-0
kind: premise
title: The vault is read by DocDag v0.4.0
status: accepted
date: 2026-09-04
supersedes:
  - {ref: premise/docdag-v0-3-0, reason: premise-collapse}
---

# The vault is read by DocDag v0.4.0

The clauses rest on this being true: `docdag.yaml` names `preset: spec`, CI
installs `Kaikei-e/DocDag@v0.4.0`, and the binary that gates a change is the
one that understands kinds, modality and `--as-of`.

v0.4.0 is what the harness kinds need beyond that: `append_only` on a kind,
`validate --immutable-since <rev>` to read it, and `lint --fixtures` over a
per-rule corpus, so a rule uzushio adds is a rule something has shown can
fire. The version this premise names is the one the workflow pins, the
pre-commit hooks pin, and `go.mod` requires.

The premise it replaces named v0.3.0 and said the same three things. It did
not collapse because it was wrong; it collapsed because the version moved.
