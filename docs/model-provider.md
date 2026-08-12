# Model Provider 与模型目录

维护日期：2026-08-02

本文档描述 CodexLoom 如何管理 Agent 的 model provider 绑定，以及共享 CodexHost
加载的静态模型目录。代码入口：`internal/hub/model_provider.go`、
`internal/hub/provider_switch.go`、`internal/modelcatalog/`、
`internal/httpapi/routes_model_provider.go`、`cmd/loom/commands_provider.go`。

## 产品语义

- Agent 的 Provider 是 primary Thread 绑定，不是单次 Turn 路由。
- 共享 CodexHost 只有一份 OpenAI 主认证；custom Provider 通过 Codex TOML 的
  `model_providers.<id>` 声明。
- Agent 第一次 `thread/start` 和每次冷 `thread/resume` 都会显式传入同一个
  `modelProvider` 与 `model`。
- 切换 Provider 会 cold-resume 同一个 primary Thread，不是创建新 Agent。
- 切换失败不会自动回退到 OpenAI Provider；回滚到旧 binding 后再重新尝试。

## 内置 Provider

`openai` 是内置 Provider，使用 Codex 登录认证，不由 Loom 管理 API key：

```sh
./bin/loom provider list
```

列表会显示：

- `openai`：来源 `builtin`，模型来自受管目录的 OpenAI 条目。
- 每个 custom Provider：来源、wire API、credential 状态、可用模型、已绑定 Agent
  数量。
- 缺失来源但仍有 Agent 绑定的 Provider，会以 `missing` 状态列出，避免静默丢绑定。

## Custom Provider 管理

写入 `~/.codex/config.toml`（或当前 Codex 配置），但不会把 API key 写回配置明文；
key 通过 Keychain 或环境引用管理。CLI：

```sh
./bin/loom provider set deepseek \
  --name "DeepSeek" \
  --base-url https://api.deepseek.com \
  --wire-api responses \
  --api-key-file /absolute/path/to/deepseek.key

./bin/loom provider set deepseek --env-key DEEPSEEK_API_KEY
./bin/loom provider set deepseek --clear-credential
./bin/loom provider get deepseek
./bin/loom provider verify deepseek --model deepseek-v4-flash
./bin/loom provider disable deepseek
```

- `--api-key-file` 只能走 loopback/HTTPS，并校验文件为当前用户 `0600`/`0400`。
- `--env-key` 写入配置引用，不保存真实值。
- `--clear-credential` 只清除 credential，不删除 Provider 定义。
- `openai` 不能 disable，也不能通过 Provider upsert 修改。
- 删除 Provider 前必须没有 Agent 绑定；否则返回 `409`。

HTTP 等价入口：

```text
GET    /api/model-providers
GET    /api/model-providers/{id}
PUT    /api/model-providers/{id}
DELETE /api/model-providers/{id}
POST   /api/model-providers/{id}/verify
```

写操作和 verify 只允许 loopback 或 `CODEX_LOOM_ADMIN_TOKEN`。

## 切换 Agent Provider

CLI：

```sh
./bin/loom agent provider cici-research \
  --provider deepseek \
  --model deepseek-v4-flash
```

HTTP：

```text
POST /api/agents/{key}/provider
```

请求体：

```json
{"providerId": "deepseek", "model": "deepseek-v4-flash"}
```

切换前必须满足全局空闲门禁：

- 所有 Agent 没有 running Turn；
- 没有 pending approval；
- 没有 active Goal（需先 pause）；
- 没有正在投递的内部 Message、Inbox、Needs You answer；
- Loom 没有正在 drain/restart，且没有另一个 Provider switch 在进行。

执行时会：

1. 持久化 `PendingProviderSwitch`。
2. 重启共享 CodexHost，并使用新的 Provider/Model cold-resume 该 Agent 的 primary
   Thread。
3. 成功后提交 binding；失败则恢复旧 binding 并重启回原 Host。
4. 恢复后重新启动 Remote runtime，并 drain 之前排队的 durable work。

因此“切换一个 Agent 的 Provider”会短暂重启整个共享 Host，所有 Agent 都必须空闲。

## 模型目录

受管目录位于 `internal/modelcatalog/models.json`，当前版本：

```text
codex-0.144.1+deepseek-v4-flash-0731
```

包含：

- Codex `rust-v0.144.1` 的完整 OpenAI catalog（8 条，保持不变）。
- DeepSeek 官方 Codex 集成中的 `deepseek-v4-flash`，priority 调整为 `50`，避免
  顶替 Codex 当前 OpenAI default model。

Codex 对 `model_catalog_json` 采用**全量替换**，不是增量合并。因此目录必须包含目标
Codex 版本需要的所有模型；只写 DeepSeek 一条会替换掉整个模型列表。

启动时，Loom 会：

1. 从嵌入的 `models.json` 或 `CODEX_LOOM_MODEL_CATALOG` override 读取 catalog。
2. 校验非空、slug 非空且不重复，并计算 SHA-256。
3. 将 managed catalog 物化到 `~/.codex-loom/runtime/model-catalog/`。
4. 通过 `-c model_catalog_json=<path>` 传给共享 CodexHost。

环境变量：

```sh
CODEX_LOOM_MODEL_CATALOG=/absolute/path/to/custom-models.json
```

override 必须是完整 catalog，且在 app-server 启动前设置。`model_catalog_json` 是
startup-only：

- 运行中修改 JSON 不会热更新；
- config write + reload 不会重建 models manager；
- 已加载 Thread 也不会重新应用；
- 只有重启 owning CodexHost 后新目录才生效。

`GET /api/model-providers` 返回的 `catalog` 会给出 `applied` /
`restartRequired` 状态，用于诊断目录是否已经生效。

## 升级目录

Codex 客户端升级后，不能长期沿用旧 union：

1. 从目标 Codex 版本的 `codex-rs/models-manager/models.json` 提取完整 OpenAI
   catalog。
2. 追加已验收的 custom model，保证 slug 唯一。
3. 检查 `visibility` / `supported_in_api` / `priority`，确认不会意外改变默认模型。
4. 用目标 Codex 版本跑 `provider verify` 和 canary。
5. 更新 `internal/modelcatalog/` 的版本常量、README 和 `models.json`。

## Canary 与限制

- canary 是 read-only；Provider 写操作和外部 Provider 读取会被拒绝。
- 当前 DeepSeek Responses 白名单只接受 `deepseek-v4-flash`。
- 共享 Host 的静态 catalog 会禁用 Codex 原生动态 model refresh，直到升级目录。
- 认证或请求失败不会静默回退到 OpenAI Provider；失败会保留错误和回滚状态。

## 延伸阅读

- [codex-app-server-protocol.md](codex-app-server-protocol.md)：wire 层
  `modelProvider` / `model` / `model_catalog_json`。
- [http-api.md](http-api.md)：Provider 与 Agent switch 路由。
- [loom-cli.md](loom-cli.md)：`provider` 与 `agent provider` 命令。
- `internal/modelcatalog/README.md`：目录来源和 SHA 记录。
