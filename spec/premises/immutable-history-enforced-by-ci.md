---
id: premise/immutable-history-enforced-by-ci
kind: premise
title: Deletion of edits, patterns and runs is refused by CI, not by DocDag alone
status: accepted
date: 2026-09-04
---

# Deletion of edits, patterns and runs is refused by CI, not by DocDag alone

The lineage of a harness edit is only evidence while nothing can quietly
remove it. DocDag v0.4.0's `validate --immutable-since` reads the
`append_only` kinds, but it only compares documents whose committed status
is `accepted`, `superseded` or `withdrawn`; a `run` has no status and a
`rejected` edit is not in that set, so neither is protected by it.

Until DocDag can name, per kind, which statuses close a document, the
repository's CI adds two `git diff` checks against `main`: deletions under
`spec/edits` and `spec/patterns` fail the build, and deletions or
modifications under `spec/runs` and `spec/measures` fail it too. When DocDag
gains that vocabulary, this premise is retired with `retired_on:` and the
CI steps are removed in the same change.
