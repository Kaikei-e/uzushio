#!/bin/sh
# UZ-C-006: test lines must not exceed 3.0x product lines.
#
# The repository this runs in is always measured. Set UZUSHIO_GOVERNED_REPOS
# to a colon-separated list of paths to measure others under the same ceiling.
set -eu
root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
cd "$root"
python3 - <<'PY'
import json, os, sys

sys.stdout.reconfigure(line_buffering=True)

DEFAULT_CEILING = 3.0
SUITE = os.environ.get("UZUSHIO_SUITE", os.path.join("examples", "suite-go", "suite.json"))
SKIP_DIRS = {".git", "vendor", "testdata", "node_modules"}

def ceiling():
    if not os.path.exists(SUITE):
        return DEFAULT_CEILING, "default"
    try:
        with open(SUITE, encoding="utf-8") as fh:
            data = json.load(fh)
        value = (data.get("constraints") or {}).get("max_test_to_product_lines")
        if value is None:
            return DEFAULT_CEILING, "%s (no ceiling declared)" % SUITE
        return float(value), SUITE
    except (OSError, ValueError, TypeError, AttributeError) as err:
        print("%s: unreadable (%s); using the default ceiling" % (SUITE, err))
        return DEFAULT_CEILING, "default"

def walk(root):
    """Yield (path, is_test) for the root module's own Go files.

    Every line of every file counts, blanks and comments included. Files
    under a nested module, testdata/ or a vendored directory are material
    the repository holds rather than code it maintains.
    """
    root = os.path.abspath(root)
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(d for d in dirnames if d not in SKIP_DIRS)
        if os.path.abspath(dirpath) != root and "go.mod" in filenames:
            dirnames[:] = []          # a nested module: not this repository's code
            continue
        for name in sorted(filenames):
            if name.endswith(".go"):
                yield os.path.join(dirpath, name), name.endswith("_test.go")

def lines(path):
    with open(path, "rb") as fh:
        return sum(1 for _ in fh)

def measure(label, root, limit):
    test = product = 0
    for path, is_test in walk(root):
        n = lines(path)
        if is_test:
            test += n
        else:
            product += n
    if product == 0:
        print("%s: no product Go lines; nothing to cap" % label)
        return True
    ratio = test / product
    ok = ratio <= limit
    print("%s: test=%d product=%d ratio=%.3f ceiling=%.3f %s"
          % (label, test, product, ratio, limit, "ok" if ok else "OVER"))
    if not ok:
        print("%s: %d test lines against %d product lines is %.3fx, over the %.3f ceiling"
              % (label, test, product, ratio, limit), file=sys.stderr)
    return ok

def roots():
    """This repository, then whatever the environment names, deduplicated."""
    seen = set()
    out = []
    named = os.environ.get("UZUSHIO_GOVERNED_REPOS", "")
    for path in ["."] + [p for p in named.split(":") if p.strip()]:
        key = os.path.abspath(path)
        if key in seen:
            continue
        seen.add(key)
        out.append(path)
    return out

limit, source = ceiling()
print("ceiling %.3f from %s" % (limit, source))

failed = False
for path in roots():
    label = os.path.basename(os.path.abspath(path))
    if not os.path.isdir(path):
        print("%s: UZUSHIO_GOVERNED_REPOS names %s, which is not a directory"
              % (label, path), file=sys.stderr)
        failed = True
        continue
    if not measure(label, path, limit):
        failed = True

sys.exit(1 if failed else 0)
PY
