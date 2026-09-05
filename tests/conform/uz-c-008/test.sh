#!/bin/sh
# UZ-C-008: a held-out task does not exercise the standard's own toolchain.
set -eu
root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
cd "$root"

suite=${UZUSHIO_SUITE:-examples/suite-go/suite.json}
if [ ! -f "$suite" ]; then
  echo "$suite: no suite manifest; nothing held out to check; skipped"
  exit 0
fi

SUITE="$suite" python3 - <<'PY'
import json, os, sys

sys.stdout.reconfigure(line_buffering=True)

suite_path = os.environ["SUITE"]
suite_dir = os.path.dirname(suite_path)

FORBIDDEN = (
    "github.com/kaikei-e/uzushio",
    "github.com/kaikei-e/cmoa",
    "github.com/kaikei-e/docdag",
)

with open(suite_path, encoding="utf-8") as fh:
    suite = json.load(fh)

def entries(suite):
    """(name, split) pairs, however the manifest spells its split."""
    seen = set()
    def add(name, split):
        name = str(name)
        if name not in seen:
            seen.add(name)
            return [(name, split)]
        return []
    out = []
    tasks = suite.get("tasks")
    if isinstance(tasks, dict):
        for name, task in tasks.items():
            task = task if isinstance(task, dict) else {}
            out += add(task.get("dir") or task.get("path") or name, task.get("split"))
    elif isinstance(tasks, list):
        for task in tasks:
            if not isinstance(task, dict):
                continue
            name = task.get("dir") or task.get("path") or task.get("task") or task.get("id")
            if name:
                out += add(name, task.get("split"))
    splits = suite.get("splits")
    if isinstance(splits, dict):
        for split, names in splits.items():
            if isinstance(names, list):
                for name in names:
                    out += add(name, split)
    return out

def module_path(go_mod):
    with open(go_mod, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if line.startswith("module "):
                return line.split(None, 1)[1].strip().strip('"')
    return ""

failed = False
checked = 0

for name, split in entries(suite):
    if split != "held-out":
        continue
    checked += 1
    task_dir = os.path.normpath(os.path.join(suite_dir, name))
    if not os.path.isdir(task_dir):
        print("%s: held-out task directory %s is missing" % (name, task_dir), file=sys.stderr)
        failed = True
        continue

    # (a) the task's repository stays under the task directory.
    task_json = os.path.join(task_dir, "task.json")
    if not os.path.isfile(task_json):
        print("%s: no %s; a held-out task states the repository it runs on"
              % (name, task_json), file=sys.stderr)
        failed = True
    else:
        with open(task_json, encoding="utf-8") as fh:
            task = json.load(fh)
        repo = str(task.get("repo") or "")
        if not repo:
            print("%s: task.json declares no repo" % name, file=sys.stderr)
            failed = True
        else:
            base = os.path.abspath(task_dir)
            resolved = os.path.abspath(os.path.join(base, repo))
            if os.path.isabs(repo) or (resolved != base and not resolved.startswith(base + os.sep)):
                print("%s: repo %r resolves to %s, outside the task directory"
                      % (name, repo, resolved), file=sys.stderr)
                failed = True
            else:
                print("%s: repo %s is inside the task" % (name, repo))

    # (b) the module it presents is not one of the toolchain's own.
    go_mod = None
    for candidate in (os.path.join(task_dir, "repo", "go.mod"),
                      os.path.join(task_dir, "src", "go.mod")):
        if os.path.isfile(candidate):
            go_mod = candidate
            break
    if go_mod is None:
        print("%s: no src/go.mod or repo/go.mod; the module this task presents cannot be read"
              % name, file=sys.stderr)
        failed = True
        continue
    module = module_path(go_mod)
    hit = next((p for p in FORBIDDEN if module.lower().startswith(p)), None)
    if hit:
        print("%s: %s is module %s, the standard's own toolchain"
              % (name, go_mod, module), file=sys.stderr)
        failed = True
    elif not module:
        print("%s: %s declares no module path" % (name, go_mod), file=sys.stderr)
        failed = True
    else:
        print("%s: module %s" % (name, module))

print("%d held-out task(s) checked" % checked)
sys.exit(1 if failed else 0)
PY
