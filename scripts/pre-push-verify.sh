#!/usr/bin/env bash
set -euo pipefail

if [[ -x ./scripts/verify.sh ]]; then
  bash ./scripts/verify.sh
fi
