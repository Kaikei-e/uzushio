#!/usr/bin/env bash
# Re-centre this task's bands on the host that will run the verifier, and write bands.json.
#
# This exists as a script because the invocation carries one decision that is not a default and
# has to be made the same way every time: `--keep enforce_allowed_ratio`.
#
# That invariant is `allowed_rps / (refill_per_s + capacity/window_s)` — a ratio against an
# expectation that is ARITHMETIC, 1200/s by construction, so its centre is a claim about the token
# bucket's maths and not a property of this machine. The 2026-09-05 reference runs read it at
# 0.9166 to 0.9168: stable to four decimals, 8.3 % below the ideal, and inside its band. Left to
# the general rule that would be kept anyway — but only because it happens to pass, and the day it
# stops passing, re-centring it would write the shortfall down as the expectation and destroy the
# only thing the invariant asserts. `--keep` says so out loud instead of relying on luck.
#
# Everything else is the tool's own rule. See the README's "Re-centring the bands".
set -euo pipefail
cd "$(dirname "$0")"

# The reports to calibrate from. There is no default: `doctor/` is gitignored, so a default
# would name a path that exists on the machine that ran the check and nowhere else — and a
# calibration silently built from whatever report happened to be lying there is exactly the kind
# of provenance this whole file exists to make explicit.
if [ "$#" -eq 0 ]; then
  cat >&2 <<'USAGE'
usage: ./calibrate.sh <doctor report.json> [more report.json ...]

Each argument is the report of a `uzushio task doctor` run whose reference block measured THIS
host. To get one:

    uzushio task doctor --task . --parallel 1        # writes doctor/<run-id>/report.json
    ./calibrate.sh doctor/<run-id>/report.json

doctor/ is gitignored, so the reports live only on the machine that produced them; bands.json is
the committed record of what they said. N = 5 is the hard minimum and 7 is better, so pass more
than one report once you have them — the runs add up across sessions.

The committed bands.json was calibrated from run 20260905T002350Z-e13205e7 (2026-09-05, N = 5);
its header records that, and `uzushio task calibrate` refuses a set smaller than --min-runs.
USAGE
  exit 2
fi

from=()
for report in "$@"; do
  if [ ! -f "$report" ]; then
    echo "calibrate: $report is not a file" >&2
    exit 2
  fi
  from+=(--from "$report")
done

exec uzushio task calibrate \
  --task . \
  "${from[@]}" \
  --keep enforce_allowed_ratio \
  --out bands.json
