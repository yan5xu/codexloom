#!/usr/bin/env bash
set -euo pipefail

resolve_postmortem_tool_root() {
  local candidates=()

  if [[ -n "${POSTMORTEM_TOOL_ROOT:-}" ]]; then
    candidates+=("${POSTMORTEM_TOOL_ROOT}")
  fi

  if [[ -n "${CODEX_WORKSPACE_ROOT:-}" ]]; then
    candidates+=("${CODEX_WORKSPACE_ROOT}/resources/tools/postmortem")
  fi

  candidates+=(
    "${HOME}/codex/resources/tools/postmortem"
    "${HOME}/.codex/resources/tools/postmortem"
  )

  local candidate
  for candidate in "${candidates[@]}"; do
    [[ -n "$candidate" ]] || continue
    if [[ -f "${candidate}/common.rb" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done

  echo "未找到共享 postmortem 工具目录。请设置 CODEX_WORKSPACE_ROOT 或 POSTMORTEM_TOOL_ROOT。" >&2
  return 1
}

run_postmortem_tool() {
  local tool_name="${1:?missing tool name}"
  shift || true
  local tool_root
  tool_root="$(resolve_postmortem_tool_root)"
  ruby "${tool_root}/${tool_name}.rb" "$@"
}
