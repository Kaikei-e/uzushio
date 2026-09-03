---
id: principle/graph-over-directory
kind: principle
title: Ask the graph, not the directory
status: accepted
counterexample:
  - pm-0001
date: 2026-09-04
---

# Ask the graph, not the directory

A standard that cannot be queried is a pile of files. One command replaces
a fan-out of reads, and every answer carries identity, status and position.
Clauses point back here through `rationale:`.
