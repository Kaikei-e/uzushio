#!/bin/sh
# The task's repository is generated from src/ so that no git repository is
# nested inside this one: src/ holds plain files, repo/ (gitignored) is the
# git repository CMoA works on. Idempotent; re-run to reset.
set -eu
cd "$(dirname "$0")"
rm -rf repo
cp -r src repo
cd repo
git init -q
git -c user.name=cmoa -c user.email=cmoa@example.com add .
git -c user.name=cmoa -c user.email=cmoa@example.com commit -q -m "task-hello: initial state"
git rev-parse HEAD
