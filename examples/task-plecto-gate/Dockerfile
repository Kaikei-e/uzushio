# The image the PlectoProxy T1 perf gate runs in.
#
# Everything the gate shells out to is installed here, at a pinned version, so
# that two verifications a month apart measure the same harness. Nothing is
# built with `cargo install`: a source build of a load generator is minutes of
# image build time and a version nobody wrote down.
#
# The build context is empty (see .dockerignore) — no file is copied in. The
# candidate tree and gate.sh arrive as mounts at run time.
FROM rust:1.97.1-bookworm

# The toolchain is the one plecto/rust-toolchain.toml pins, so rustup finds it
# already installed and never reaches the network mid-verification.
#
# cmake + clang build aws-lc-sys's C crypto. util-linux is taskset, which every
# generator and the proxy pin themselves with. procps is for the runbook's
# process bookkeeping. curl is the readiness probe. python3 is bookworm's 3.11,
# which is what gate_verdict.py's `tomllib` import needs.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates clang cmake curl git procps python3 util-linux \
 && rm -rf /var/lib/apt/lists/*

# The seven guest filters are componentised from wasm32-unknown-unknown by
# crates/host/build.rs. rustfmt and clippy are named by rust-toolchain.toml's
# `components`, and rustup would otherwise download them on the first cargo
# invocation inside the verification.
RUN rustup target add wasm32-unknown-unknown \
 && rustup component add rustfmt clippy

# oha: the closed-loop and fixed-rate generator. >= 1.14 is a hard requirement —
# older releases do not take `--latency-correction`, and the gate's tail step
# does. A statically linked release binary, verified against its checksum.
ARG OHA_VERSION=1.16.0
ARG OHA_SHA256=620bb9e16fb53eabc9a3fc45f88bdb41fefa3fee5c05e75892011ce320391716
RUN curl -fsSL -o /tmp/oha \
      "https://github.com/hatoo/oha/releases/download/v${OHA_VERSION}/oha-linux-amd64" \
 && echo "${OHA_SHA256}  /tmp/oha" | sha256sum -c - \
 && install -m 0755 /tmp/oha /usr/local/bin/oha \
 && rm -f /tmp/oha

# k6: the rate-limit tax and enforcement steps. Without it those two invariants
# report `skipped` and the gate still exits 0 — a strictly weaker verifier, so
# it is installed rather than left out. The published checksum file lists the
# same digest.
ARG K6_VERSION=2.2.0
ARG K6_SHA256=b5a8003c86f35f5cd5ceef1490312c48e587696c94d998cefc6d7b3b4cb1597d
RUN curl -fsSL -o /tmp/k6.tar.gz \
      "https://github.com/grafana/k6/releases/download/v${K6_VERSION}/k6-v${K6_VERSION}-linux-amd64.tar.gz" \
 && echo "${K6_SHA256}  /tmp/k6.tar.gz" | sha256sum -c - \
 && tar -xzf /tmp/k6.tar.gz -C /tmp \
 && install -m 0755 "/tmp/k6-v${K6_VERSION}-linux-amd64/k6" /usr/local/bin/k6 \
 && rm -rf /tmp/k6.tar.gz "/tmp/k6-v${K6_VERSION}-linux-amd64"

# The candidate is a git worktree whose .git is a file naming a path that does
# not exist in the container, and the directory is owned by whoever made it.
# git's ownership check would print a complaint on every call for an answer the
# runbook already treats as optional.
RUN git config --system --add safe.directory '*'

# OHA and K6 default to $HOME/.cargo/bin/oha and $(command -v k6); naming them
# means the gate does not depend on either guess. GATE_MICRO=0 skips the
# informational criterion layer, which is `|| true` anyway but costs minutes
# whenever a saved baseline is lying around in the cached target volume.
ENV OHA=/usr/local/bin/oha \
    K6=/usr/local/bin/k6 \
    GATE_MICRO=0 \
    CARGO_TERM_COLOR=never
