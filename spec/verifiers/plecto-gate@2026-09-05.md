---
id: verifier/plecto-gate@2026-09-05
kind: verifier
title: "plecto-gate verifier: unhealthy"
date: "2026-09-05"
verdict: unhealthy
kill_rate: n/a
mutants: "7"
reference_runs: "5"
report: doctor/20260905T002350Z-e13205e7/report.json
---

# plecto-gate verifier: unhealthy

task plecto-gate at 39778ec3f02f
reference: 5 run(s), 5 failed, 0 inconclusive
mutants: 5 killed, 0 survived, 0 inconclusive, 2 equivalent
kill rate: 1.00 — not evidence: the reference itself failed, so every mutant fails with it
  reference-1: fail [out of band: dispatch_floor_us, apikey_cost_us, pooled_tail_p50_ms, ratelimit_tax_us]
  reference-2: fail [out of band: dispatch_floor_us, apikey_cost_us, pooled_tail_p50_ms, ratelimit_tax_us]
  reference-3: fail [out of band: dispatch_floor_us, apikey_cost_us, pooled_tail_p50_ms, ratelimit_tax_us]
  reference-4: fail [out of band: dispatch_floor_us, apikey_cost_us, pooled_tail_p50_ms, ratelimit_tax_us]
  reference-5: fail [out of band: dispatch_floor_us, apikey_cost_us, pooled_tail_p50_ms, ratelimit_tax_us]
  beyond the reference: mutants/0003-rr-no-advance.diff (ejection_transition_s)
verdict: unhealthy
