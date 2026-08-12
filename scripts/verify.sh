#!/usr/bin/env bash
set -euo pipefail

echo "[verify] baseline-only gate: project-specific lint/test/build 尚未配置。"
echo "[verify] TODO: 按仓库实际情况扩展 scripts/verify.sh，不要把当前脚本当成完整质量门禁。"

if [[ -x ./scripts/postmortem-lint.sh ]]; then
  bash ./scripts/postmortem-lint.sh
fi

if [[ -x ./scripts/postmortem-scan.sh ]]; then
  bash ./scripts/postmortem-scan.sh || true
fi
