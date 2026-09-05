#!/usr/bin/env bash
# The verifier's body. compose.yaml mounts this file read-only at /gate.sh and
# runs `bash /gate.sh`; the candidate worktree is at /work.
#
# It exists as a file in the task directory rather than inside the image so that
# a change to what the verifier does is a change reviewers can read as a diff,
# without rebuilding anything.
set -euo pipefail
cd /work

# The container is root and /work is a worktree the host user owns. The build
# writes into it — performance/data/, and a target/ inside each guest filter
# workspace under plecto/examples/filters, plecto/crates/host/fixtures and
# bench/filters, which crates/host/build.rs builds with an explicit --target-dir
# rather than under OUT_DIR — and a root-owned directory inside a user-owned
# tree is a directory that user cannot then delete. `cmoa verify` removes the
# worktree when it is done, so without this the cleanup fails with a permission
# error and leaves the tree behind, once per verification.
#
# The two cached target directories are pruned: they are volume mounts, they
# hold gigabytes, and nothing on the host ever looks inside them.
restore_ownership() {
  owner="$(stat -c '%u:%g' /work)"
  find /work \
    -path /work/plecto/target -prune -o \
    -path /work/bench/loadgen/target -prune -o \
    -exec chown -h "$owner" {} + 2>/dev/null || true
}
trap restore_ownership EXIT

# `just gate` does not build — the runbook's bench-build recipe is a separate
# step — and run-perf.sh's freshness guard hard-codes
# plecto/target/release/examples and ignores CARGO_TARGET_DIR. So the build has
# to happen first, and it has to land at exactly that path: compose mounts a
# named volume there. Without this the preflight exits 3 on "missing release
# example(s)" and nothing is ever measured.
#
# Only the two examples the gate phase launches are built. The runbook's own
# bench-build also makes tls-http and swap-bench, which no gate step starts.
(
  cd plecto
  cargo build --release -p plecto-server --features bench-harnesses \
    --example bench-server --example load-balancing
)
# plecto-loadgen drives the round-robin and ejection steps. run-perf.sh builds
# it lazily on first use; building it here keeps the measurement window free of
# a cargo build.
cargo build --release --manifest-path bench/loadgen/Cargo.toml

# No `-e` around the gate itself: run-perf.sh's exit code is the answer (0 in
# band, 1 an invariant left its band or a step failed, 2 an unknown phase, 3 a
# missing or stale example), and it has to survive to become the container's.
rc=0
bash bench/perf/run-perf.sh gate || rc=$?

# The band verifier reads the LAST `invariant,value,ci_half,band_lo,band_hi,
# verdict` block on stdout. phase_gate already cats gate.csv when it reaches its
# verdict; printing it again is what makes the other case legible. A run that
# died before the verdict, or one whose reducer crashed over a malformed oha
# JSON, leaves no CSV and no rows — which the verifier reports as a harness
# failure rather than as a regression, the distinction run-perf.sh's exit code 1
# cannot make on its own.
if [ -s performance/data/gate.csv ]; then
  cat performance/data/gate.csv
fi
exit "$rc"
