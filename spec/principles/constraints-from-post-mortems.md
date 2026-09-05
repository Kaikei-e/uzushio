---
id: principle/constraints-from-post-mortems
kind: principle
title: A constraint is a lesson a post-mortem paid for
status: accepted
counterexample:
  - pm-0002
date: 2026-09-05
---

# A constraint is a lesson a post-mortem paid for

A rule invented at design time is a preference; a rule extracted from a
project that failed is a price already paid. So a numeric limit here is
not a taste — it is the smallest thing that would have made an earlier
failure visible while it was still cheap. Clauses derived this way name
their post-mortem under `counterexample:`, and when the post-mortem is
found to have been misread, the clause goes with it rather than surviving
as a habit.
