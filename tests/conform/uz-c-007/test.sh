#!/bin/sh
# UZ-C-007: a pull request claims exactly one roadmap step.
set -eu
root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
cd "$root"

if [ "${GITHUB_EVENT_NAME:-}" != "pull_request" ]; then
  echo "not a pull request; skipped"
  exit 0
fi
if [ -z "${GITHUB_EVENT_PATH:-}" ] || [ ! -r "${GITHUB_EVENT_PATH:-}" ]; then
  echo "pull request event payload not readable; skipped"
  exit 0
fi

python3 - <<'PY'
import json, os, re, sys

sys.stdout.reconfigure(line_buffering=True)

STEPS = range(1, 8)

path = os.environ["GITHUB_EVENT_PATH"]
with open(path, encoding="utf-8") as fh:
    event = json.load(fh)

pr = event.get("pull_request") or {}
text = "\n".join(str(pr.get(k) or "") for k in ("title", "body"))

# The marker convention of UZ-C-007. Spaces and tabs only: a marker does not
# span a line break, so a line ending "as a first step" above one opening "3
# tasks remain" is not a claim on step 3.
marker = re.compile(r"\bstep[ \t]+(\d+)\b", re.IGNORECASE)
claimed = sorted({int(m.group(1)) for m in marker.finditer(text)})

failed = False

outside = [n for n in claimed if n not in STEPS]
if outside:
    print("pull request names a step the roadmap does not have: %s"
          % ", ".join("Step %d" % n for n in outside), file=sys.stderr)
    failed = True

inside = [n for n in claimed if n in STEPS]
if len(inside) > 1:
    print("pull request claims %d roadmap steps: %s"
          % (len(inside), ", ".join("Step %d" % n for n in inside)), file=sys.stderr)
    print("a pull request corresponds to exactly one roadmap step", file=sys.stderr)
    failed = True
elif inside:
    print("pull request claims Step %d" % inside[0])
elif not outside:
    print("pull request carries no step marker; unlabelled")

sys.exit(1 if failed else 0)
PY
