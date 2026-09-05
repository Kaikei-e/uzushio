---
id: verifier/plecto-gate@2026-09-05-1
kind: verifier
title: "plecto-gate verifier: unhealthy"
date: "2026-09-05"
verdict: unhealthy
kill_rate: "0.80"
mutants: "7"
reference_runs: "5"
report: doctor/20260905T025554Z-31045463/report.json
---

# plecto-gate verifier: unhealthy

task plecto-gate at 39778ec3f02f
reference: 5 run(s), 0 failed, 0 inconclusive
mutants: 4 killed, 1 survived, 0 inconclusive, 2 equivalent
kill rate: 0.80 (minimum 0.80)
  survived: mutants/0005-ratelimit-probe.diff (plecto/crates/host/src/state.rs:563 — 32 extra kv.get in try_acquire; band ratelimit_tax_us, which is skipped without k6. Sizing rule: at least 5x the calibrated half-width. One extra get buys 0.50 us against a half-width of 3.0238, so a single one shifted 0.17x and survived on 2026-09-05; 32 gets = 16.0 us = 5.3x. The count is dictated by this host's noise floor rather than by plausibility — ratelimit_tax_us has 7.4% run-to-run spread against 0.7% on the ladder, because it is a difference of two k6 throughputs over only 2 interleave rounds. Lowering that noise upstream is worth more than this mutant) [10 invariant(s), all in band]
verdict: unhealthy
