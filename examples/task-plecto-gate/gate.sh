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

# THE ADAPTER. This is the one place that knows both formats.
#
# `uzushio task calibrate` writes bands.json, which is uzushio's shape: an invariant name, a lo
# and a hi, and how each was arrived at. It knows nothing about PlectoProxy. What run-perf.sh
# reads is bench/perf/gate_tolerances.toml, which is PlectoProxy's shape: a table per invariant,
# some of them carrying parameters that the band's own meaning depends on. Turning the first into
# the second is this task's job and nobody else's, and it lives here — in a file mounted into the
# container — so that a change to what the verifier does is a change reviewers read as a diff.
#
# The rendering is deliberately narrow: the tracked file in the worktree is the template, and only
# the lo/hi of the invariants bands.json actually derived are replaced. Everything else is carried
# through byte for byte — the parameterised tables whose extra keys are part of what their band
# asserts, and every band the reference never left, which calibration keeps rather than widens.
# A band written from scratch would silently drop those keys.
#
# Nothing of PlectoProxy's is written: repo/ is the pinned checkout, /work is a throwaway worktree
# that `cmoa verify` deletes, and the tracked bands stay exactly what the pinned commit says.
# run-perf.sh hard-codes "$HERE/gate_tolerances.toml" and takes no override, so rewriting it in
# the worktree is the only way to judge against this host without changing the project. (The clean
# fix is one line upstream — `"${GATE_TOLERANCES:-$HERE/gate_tolerances.toml}"` — worth proposing,
# and not worth waiting for.)
#
# The line is printed because a verdict read against different bands is a different verdict. A
# reader of stdout.txt who does not know which bands judged the run cannot use it for anything.
#
# There is NO fallback to the pinned bands. This task's whole finding is that they belong to
# another machine and reject correct code here, so a run that quietly used them would report a
# false positive as a regression — the exact confusion the calibration exists to remove. A missing
# or unreadable bands.json is a harness failure and exits 4, which run-perf.sh never returns, so
# it cannot be mistaken for a verdict.
#
# `-e` rather than `-f`: docker creates a DIRECTORY at the mount point when the bind source does
# not exist on the host, so "somebody deleted bands.json" arrives here as a directory and not as
# an absence.
if [ ! -e /bands.json ]; then
  echo "gate: /bands.json is not mounted. compose mounts it from the task directory; there is no" >&2
  echo "      fallback to the pinned bench/perf/gate_tolerances.toml, which belongs to another" >&2
  echo "      host and rejects correct code here. Run ./calibrate.sh and retry." >&2
  exit 4
fi
if [ -d /bands.json ]; then
  echo "gate: /bands.json is a directory, which is what docker creates when the bind source is" >&2
  echo "      missing on the host. The task directory has no bands.json: run ./calibrate.sh." >&2
  exit 4
fi

python3 - /bands.json bench/perf/gate_tolerances.toml <<'RENDER'
import json, re, subprocess, sys, tomllib

bands_path, toml_path = sys.argv[1], sys.argv[2]


def fail(*lines):
    for line in lines:
        print("gate: " + line, file=sys.stderr)
    # 4 is not a code run-perf.sh returns (0 in band, 1 out of band, 2 unknown phase, 3 stale
    # example), so a harness failure here can never be read as a verdict about the code.
    sys.exit(4)


try:
    with open(bands_path) as f:
        bands = json.load(f)
except (OSError, ValueError) as err:
    fail(f"/bands.json could not be read as JSON: {err}",
         "It is written by `uzushio task calibrate`; do not edit it by hand.")

if bands.get("schema_version") != 1:
    fail(f"/bands.json is schema_version {bands.get('schema_version')}, and this adapter reads 1.")
for key in ("rev", "invariants", "source_runs"):
    if key not in bands:
        fail(f"/bands.json carries no {key}.")

# The bands were derived from runs of ONE revision, and a band is only about the code it was
# measured on. The worktree is a detached checkout whose .git is a file pointing at the host
# repository, which is not mounted — so this check works only where /work is a self-contained
# checkout, and is skipped (loudly) where it is not. See the README: the recorded rev in the
# rendered header is what a reader has otherwise.
verified = "unverified"
try:
    seen = subprocess.run(["git", "-C", "/work", "rev-parse", "HEAD"],
                          capture_output=True, text=True, timeout=10)
    worktree_rev = seen.stdout.strip() if seen.returncode == 0 else ""
except (OSError, subprocess.SubprocessError):
    worktree_rev = ""
if worktree_rev:
    if worktree_rev != bands["rev"]:
        fail(f"/bands.json was calibrated on {bands['rev'][:12]} and /work is {worktree_rev[:12]}.",
             "Bands measured on one revision are not bands for another: re-calibrate.")
    verified = "verified against the worktree"

try:
    with open(toml_path, "rb") as f:
        tracked = tomllib.load(f)
    text = open(toml_path).read()
except (OSError, ValueError) as err:
    fail(f"the tracked {toml_path} could not be read: {err}")

# Only the invariants this host actually re-centred are rewritten. A band bands.json marks
# `kept` is the tracked one already, so touching it could only introduce a difference.
derived = {n: b for n, b in bands["invariants"].items() if not b["kept"]}
missing = sorted(set(derived) - set(tracked))
if missing:
    fail("bands.json derives " + ", ".join(missing) + ", which the tracked file does not declare",
         "— the pinned revision and the calibration disagree about what is measured.")


def edge(body: str, key: str, value: float) -> str:
    # Replace the key inside one table body, keeping its position and the comments around it.
    pattern = re.compile(rf"^(\s*{key}\s*=\s*)(\S+)(.*)$", re.MULTILINE)
    replaced, n = pattern.subn(rf"\g<1>{value!r}\g<3>", body, count=1)
    if n != 1:
        fail(f"could not find {key} to replace in the {key} table")
    return replaced


# Split on table headers so a replacement lands in the table it belongs to.
parts = re.split(r"(?m)^(\[[^\]]+\]\s*)$", text)
out = [parts[0]]
for header, body in zip(parts[1::2], parts[2::2]):
    name = header.strip()[1:-1].strip()
    if name in derived:
        body = edge(edge(body, "lo", derived[name]["lo"]), "hi", derived[name]["hi"])
    out.append(header)
    out.append(body)
rendered = "".join(out)

header = [
    "# RENDERED FROM bands.json BY gate.sh — do not edit, and do not read this as PlectoProxy's.",
    "# The tracked bands are whatever the pinned commit says; this is the same file with the",
    "# lo/hi of the invariants this host re-centred replaced, and every other table untouched.",
    "#",
    f"#   task        = {bands['task']}",
    f"#   rev         = {bands['rev']}  ({verified})",
    "#   source runs = " + ", ".join(bands["source_runs"]) + f"  ({bands['day']}, N={bands['n']})",
    "#   rule        = {statistic}, p={p}, gamma={gamma}, k2={k2}".format(**bands["rule"]),
    "#   re-centred  = " + (", ".join(sorted(derived)) or "nothing"),
    "#   kept        = " + ", ".join(sorted(n for n, b in bands["invariants"].items() if b["kept"])),
    "#",
]
with open(toml_path, "w") as f:
    f.write("\n".join(header) + "\n" + rendered)
print("gate: rendered bench/perf/gate_tolerances.toml from bands.json — re-centred " +
      (", ".join(sorted(derived)) or "nothing") + "; source runs " + ", ".join(bands["source_runs"]) +
      f"; calibrated on rev {bands['rev'][:12]} ({verified})")
RENDER

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
