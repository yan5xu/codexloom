#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)/postmortem-common.sh"
run_postmortem_tool lint "$@"
