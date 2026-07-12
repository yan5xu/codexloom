# chub 兼容说明

CodexLoom 的规范 CLI 是 `loom`，完整使用与 Agent 通信文档见
[loom-cli.md](loom-cli.md)。

`bin/chub` 是迁移期兼容入口，与 `bin/loom` 使用同一实现；旧脚本可以继续运行，但新脚本应使用
`loom agent ...`、`loom thread ...` 和 `CODEX_LOOM_URL`。
