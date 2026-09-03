#!/bin/sh
# UZ-C-002: a clause must name at least one topic.
set -eu
root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
cd "$root"
docdag validate --format json | python3 -c '
import json, sys
data = json.loads(sys.stdin.read())
missing = [
    f for f in data.get("findings", [])
    if f.get("rule") in ("cardinality", "missing_field")
    and "about" in (f.get("detail") or "").lower()
]
if missing:
    for f in missing:
        loc = f.get("location", {})
        print("%s: %s %s: %s" % (loc.get("path", "?"), f.get("rule"), f.get("id"), f.get("detail")), file=sys.stderr)
    sys.exit(1)
'
