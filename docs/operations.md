# CodexLoom Operations Checklist

维护日期：2026-08-02

本文档是 operator-facing 的发布、重启、备份和恢复清单。实现细节与架构解释见
[handbook.md](handbook.md)，审计状态见 [technical-debt-audit.md](technical-debt-audit.md)。

## 发布清单

1. 确认目标分支/工作树包含预期代码，并检查是否有未提交的、会影响行为的工作。
2. 运行 `make build`。该目标先构建 WebUI，再编译二进制，并校验
   `bin/codex-loom` 包含当前 Vite entrypoint。
3. 确认 `GET /api/version` 的 `build.webAsset` 等于 `internal/webui/dist/index.html`
   当前引用的 asset；如果 `/api/...` 返回 HTML，说明二进制内嵌了旧 WebUI。
4. 对影响启动、状态迁移或 WebUI 的改动，先跑 canary：

   ```sh
   ./bin/loom dev canary start --from "$CODEX_LOOM_DATA" --port auto
   ./bin/loom dev canary status
   ./bin/loom dev canary stop
   ```

5. 发布前创建显式备份：

   ```sh
   ./bin/loom backup --reason before-release
   ./bin/loom backups
   ```

6. 通过 WebUI 或 `POST /api/admin/restart` 执行优雅重启。Restart API 会先创建
   `pre-restart` 备份，等待 active Turn 和 Connector claim，然后由 reloader 替换进程。
7. 重启后验证：

   ```sh
   ./bin/loom doctor
   ./bin/loom version --running
   ./bin/loom agent list
   ./bin/loom integration status <connection-id>
   ```

   重点确认 `restart required` 不再出现、Agent 数量和 Thread history 正确、managed
   Gateway heartbeat 恢复为 `connected`。

## 回滚清单

1. 保留上一次可用的 `bin/` 和 `internal/webui/dist` 产物；不要只覆盖 `bin/codex-hub`。
2. 如果新进程已启动但行为异常，先通过页面 Restart 或 reloader 切回旧 executable；
   直接删除/替换正在运行的二进制不保证 launchd 使用新 inode。
3. 如果状态文件被新版本写入且需要回退，使用发布前备份恢复到临时目录，先检查
   `manifest.json`，再执行受覆盖范围约束的业务 ledger/rollout 恢复。

## 备份检查

规范快照位于 `~/.codex-loom/backups/`：

```sh
./bin/loom backups
./bin/loom backups prune
```

默认保留策略：至少 2 份、最多 5 份、总计不超过 2 GiB、最长 30 天。恢复底线优先于
容量和年龄限制。快照是 tar.gz，包含 `codex-loom/` 业务 ledger、`codex-sessions/`
rollout、`pinix-edge/names.json` 和 `codex/config.toml`；可重建的 SSE event cache
不进入快照。Owner-only `credentials/**`（managed secret 与 gateway rollback anchor）也明确排除，
所以普通快照不是完整可运行恢复；secret restore 需要独立备份合同与授权。

每次恢复演练前先确认：

- 快照文件权限和 hash 可读取；
- `manifest.json` 中 Agent/Thread 数量与预期一致；
- `manifest.json` 明确列出 `credentials/**` excluded，且 operator 不把该快照标记为 runnable restore；
- `codex-loom/` 和 `codex-sessions/` 均有内容；
- 目标数据目录有足够剩余空间，避免恢复过程再次触发磁盘写失败。

## 恢复演练

当前没有自动化 restore 命令，因此演练必须在不影响生产数据目录的临时位置执行：

```sh
mkdir -p /tmp/codexloom-restore-drill
tar -tzf ~/.codex-loom/backups/codex-loom-*.tar.gz | head -80
tar -xzf ~/.codex-loom/backups/codex-loom-*.tar.gz -C /tmp/codexloom-restore-drill
```

检查解包后的 `manifest.json` 与目录结构。通过 canary 用恢复出来的数据目录启动：

```sh
./bin/loom dev canary start --from /tmp/codexloom-restore-drill/codex-loom --port auto
./bin/loom dev canary status
```

在 canary 中验证：

```sh
./bin/loom agent list
./bin/loom schedule list
./bin/loom topic list
./bin/loom trigger list
```

确认后停止 canary。只有演练和 Operator 明确授权才执行生产恢复：停 Loom，将
`codex-loom/` 恢复到数据目录，将 `codex-sessions/` 恢复到 `CODEX_SESSIONS_DIR` 或
`~/.codex/sessions`，再启动并运行 `loom doctor` 与业务 smoke。

## 已知限制

- 当前没有内置 `restore` CLI 或自动恢复演练命令；本节是人工操作清单。
- 备份未内置校验和；恢复前应自行用 `shasum`/外部存储校验快照。
- 恢复后的外部投递、ProviderOperation 和 Inbox 状态以快照中的 ledger 为准；如果
  provider 已返回成功但快照较旧，不能安全重放。
- 生产恢复属于高风险动作，必须由 Operator 确认授权后执行。
