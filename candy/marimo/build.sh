#!/usr/bin/env bash
# Post-pixi install for the marimo pixi env. Runs in the
# arch-builder pixi-builder stage AFTER `pixi install`, so
# every wheel + binary in ~/.pixi/envs/default/ is available
# (torch 2.11.x+cu130, cuda runtime, gcc, etc.) and gets COPIED
# into the final image alongside whatever this script adds.
#
# We install three PyTorch Geometric native extensions that ship
# as Linux-cp313 wheels but at a PyG-hosted HTML directory listing
# (https://data.pyg.org/whl/torch-2.11.0+cu130.html). pixi's
# per-package `index =` syntax requires a PEP 503 simple index;
# the PyG URL is a plain HTML <a href=...> listing, so it has to
# go through `pip --find-links` here.
#
# Skipped on purpose (no Linux cp313 wheel exists upstream as of
# 2026-05; revisit when wheels land):
#   - pyg-lib: only a Windows cp313 wheel published
#   - torch-spline-conv: no cp313 wheel anywhere
# PyG is fully functional without them — they're optional
# accelerators for specific layer types. The remaining three
# (torch_scatter / torch_sparse / torch_cluster) cover the
# performance-critical scatter/gather/clustering ops most GNN
# workloads use.
#
# torch is pinned to ==2.11.* in pixi.toml so the build-tag on
# the PyG wheels (`pt211cu130`) stays ABI-compatible. When
# bumping torch, update the URL below to the matching
# torch-<NEW>+cu130 index.

set -euo pipefail

PYG_WHEEL_INDEX="https://data.pyg.org/whl/torch-2.11.0+cu130.html"

"${HOME}/.pixi/envs/default/bin/pip" install \
    --no-deps \
    --find-links "${PYG_WHEEL_INDEX}" \
    torch-scatter \
    torch-sparse \
    torch-cluster

# Sanity-check the imports now (catches a broken install at build
# time instead of at first notebook open). All three are CUDA
# extensions; importing requires libtorch to be on LD_LIBRARY_PATH,
# which the pixi env's site-packages handles internally.
"${HOME}/.pixi/envs/default/bin/python" -c '
import torch_scatter, torch_sparse, torch_cluster
print(f"torch_scatter={torch_scatter.__version__}")
print(f"torch_sparse={torch_sparse.__version__}")
print(f"torch_cluster={torch_cluster.__version__}")
'
