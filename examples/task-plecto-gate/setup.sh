#!/bin/sh
# The task's repository is PlectoProxy at one pinned commit, fetched into repo/.
#
# Nothing is added to PlectoProxy by this task: the task lives here, and the
# commit it measures is named below and does not move. repo/ is gitignored, so
# no git repository is nested inside this one.
#
# Idempotent: re-run to put repo/ back on the pinned commit and to make sure the
# named volumes the verifier caches its build in exist.
set -eu
cd "$(dirname "$0")"

REMOTE=https://github.com/Kaikei-e/PlectoProxy
# PlectoProxy main as of 2026-09-05. task.json's `rev` is the same SHA: change
# both together, or the doctor measures a tree the manifest does not describe.
COMMIT=39778ec3f02f1db8883a11a4864f9b43f2dc3fac

if [ ! -d repo/.git ]; then
  rm -rf repo
  mkdir -p repo
  git -C repo init -q
  git -C repo remote add origin "$REMOTE"
fi
# One commit and no history. The gate never looks past the tree it is handed,
# and `git fetch --depth 1 <sha>` is what keeps a re-run cheap.
git -C repo fetch -q --depth 1 origin "$COMMIT"
git -C repo checkout -q --detach "$COMMIT"

# The verifier keeps cargo's two target directories and its registry in named
# volumes, so the wasmtime/cranelift build is paid once instead of per
# verification. They are declared `external` in compose.yaml on purpose: CMoA
# ends every verification with `docker compose down -v`, which would delete a
# volume the compose project owned, and with it the whole cache.
for volume in \
  uzushio-plecto-gate-target \
  uzushio-plecto-gate-loadgen-target \
  uzushio-plecto-gate-cargo-registry
do
  docker volume create "$volume" >/dev/null
done

git -C repo rev-parse HEAD
