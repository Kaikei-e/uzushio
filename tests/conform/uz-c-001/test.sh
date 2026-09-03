#!/bin/sh
# UZ-C-001: an accepted MUST or MUST_NOT must be enforced.
set -eu
root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
cd "$root"
docdag validate --format json | python3 -c '
import json, sys
data = json.loads(sys.stdin.read())
orphans = [f for f in data.get("findings", []) if f.get("rule") == "orphan_must"]
if orphans:
    for f in orphans:
        loc = f.get("location", {})
        print("%s: orphan_must %s: %s" % (loc.get("path", "?"), f.get("id"), f.get("detail")), file=sys.stderr)
    sys.exit(1)
'
