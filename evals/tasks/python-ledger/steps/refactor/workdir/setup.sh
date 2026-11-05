#!/bin/bash
# Snapshot the workspace so the verifier can measure this step's change.
set -euo pipefail
rm -- "$0"
rm -rf /eval/before && mkdir -p /eval && cp -a /app /eval/before
