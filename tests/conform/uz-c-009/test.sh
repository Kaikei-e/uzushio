#!/bin/sh
# UZ-C-009: a calibration names a report that is in the repository.
set -eu
root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
cd "$root"
python3 - <<'PY'
import os, sys

sys.stdout.reconfigure(line_buffering=True)

DIR = os.path.join("spec", "calibrations")

def report_of(path):
    """The report: value of a document, read out of its frontmatter.

    The frontmatter is a small flat mapping written by a machine, so the key
    is read line by line rather than through a YAML parser: this test is a
    shell script by design and must not need a dependency to run.
    """
    inside = False
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            line = line.rstrip("\n")
            if line == "---":
                if inside:
                    return None
                inside = True
                continue
            if inside and line.startswith("report:"):
                return line.split(":", 1)[1].strip().strip('"').strip("'")
    return None

if not os.path.isdir(DIR):
    print("no %s; 0 calibration(s) checked" % DIR)
    sys.exit(0)

failed = False
checked = 0
for name in sorted(os.listdir(DIR)):
    if not name.endswith(".md"):
        continue
    checked += 1
    path = os.path.join(DIR, name)
    report = report_of(path)
    if not report:
        print("%s: names no report; the coefficients have no evidence behind them" % path,
              file=sys.stderr)
        failed = True
        continue
    if not os.path.isfile(report):
        print("%s: report %s does not exist" % (path, report), file=sys.stderr)
        failed = True
        continue
    print("%s: report %s" % (path, report))

print("%d calibration(s) checked" % checked)
sys.exit(1 if failed else 0)
PY
