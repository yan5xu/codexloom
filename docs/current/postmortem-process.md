# Postmortem 流程

> 更新日期：2026-08-12
> 状态：当前事实

## 目标

- 把已确认的事故和高风险回归沉淀到项目仓库。
- 让 Claude / Codex 共享同一份项目级事故知识。
- 在发布前对照历史事故做回归预警。

## 正式目录

- 当前流程：`docs/current/postmortem-process.md`
- 正式档案：`docs/postmortems/`
- 模板：`docs/postmortems/_template.md`

## 最小规则

- `draft`
  - 至少 1 类 evidence
  - 不能只来自 memory
- `verified`
  - 至少 2 类不同 evidence
  - 且至少 1 类来自原始事实源或代码事实源（如 bugqueue / issue / commit / tracked repo_code）

## 脚本入口

- `scripts/postmortem-lint.sh`
- `scripts/postmortem-scan.sh`
- `scripts/postmortem-new.sh`
- `scripts/postmortem-promote.sh`

## 默认门禁

- `scripts/verify.sh` 默认执行 postmortem lint
- `scripts/postmortem-scan.sh` 默认先以 warning-only 运行

## memory 规则

- `~/.ai-memory/` 只保留“去哪看正式 PM”的指针
- 事故正文必须写在当前仓库，不得只留在 memory
