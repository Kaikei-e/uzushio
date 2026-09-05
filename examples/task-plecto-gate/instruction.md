PlectoProxy's T1 performance gate (`bash bench/perf/run-perf.sh gate`) measures
a set of invariants on the proxy's hot paths — the WASM dispatch floor, the
apikey filter's own per-request cost, fixed-rate tail latencies, the rate-limit
tax, round-robin exactness and the ejection timeline — and judges each of them
against a band in `bench/perf/gate_tolerances.toml`.

Every invariant must stay inside its band. Change nothing that is not needed to
keep it there: the gate reads adjacent deltas, so a cost paid uniformly on every
route is invisible to it and a cost paid on one route is not.

The bands themselves are the expectation and are not yours to widen.
