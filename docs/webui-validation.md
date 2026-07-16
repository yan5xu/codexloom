# WebUI 检查与移动端验收

这份文档记录 CodexLoom WebUI 的实际检查方法。目标不是获得一张“看起来没问题”的截图，而是同时证明数据正确、交互有效、布局稳定、实时状态可达，并且最终发布的 Go 内嵌版本与开发时看到的版本一致。

## 完成标准

一次 UI 改动至少要形成四类证据：

| 证据 | 回答的问题 |
|---|---|
| Build | TypeScript、Vite 构建和 Go embed 是否成功 |
| Structured state | 当前页面实际加载了多少 Agent、选中了谁、处于哪个视图和状态 |
| Interaction | 点击、输入、切换、拖拽、滚动和实时更新是否真的发生 |
| Visual | 桌面与移动布局是否清楚，无重叠、溢出、遮挡和错误层级 |

缺少其中任何一项都不能宣称 UI 已完成。构建成功只能证明代码可编译；截图只能证明一个瞬间；自动化 state 只能证明数据，不证明视觉排版。

## 三个环境

### Vite 开发环境

```sh
cd web
npm run dev -- --host 127.0.0.1 --port 5188
```

`5188` 适合快速修改、HMR 和浏览器走查。当前 `vite.config.ts` 会把 `/api` 代理到生产 Hub `127.0.0.1:4870`，因此它不是隔离环境：打开页面是只读行为，但发送消息、修改 Profile、配置 Integration、归档 Agent 和 Restart 都会影响生产数据。需要执行写操作时，先建立独立 canary。

### Canary 环境

影响共享状态、真实通信、空状态、错误状态或迁移行为的改动，要使用独立数据目录和端口：

```sh
tmp_data=$(mktemp -d /tmp/codexloom-ui-canary.XXXXXX)
./bin/codex-loom -port 4890 -data "$tmp_data"
```

Canary 可以使用合成 Agent、Profile、Relationship、Inbox 和 Integration 数据。不要让 canary 自动开启 Remote，也不要 resume 生产 Thread。需要生产规模列表时，可以复制只读治理数据到临时目录，但不能让生产和 canary 同时写同一份 ledger。

### 生产环境

生产入口是 `http://127.0.0.1:4870`，手机通过 Tailscale 访问 `http://100.66.47.40:4870`。生产只用于最终验收：先构建新 dist 和二进制，由用户触发 Restart Loom，确认新进程加载完成后再检查。

## 标准闭环

### 1. 改前确认

```sh
git status --short
```

先读组件、数据来源和调用方。确认当前工作区已有改动，不能覆盖其他 Agent 或用户正在进行的工作。界面问题往往来自 view model、实时事件或 API 语义，不应只改表面 CSS。

### 2. 开发时检查

启动 `5188`，在目标 URL 上反复执行：

1. 打开准确的深链，例如 `#codex-hub-dev`、`#team`、`#integrations`。
2. 用 automation state 确认数据已加载。
3. 用 snapshot 获取当前可交互元素。
4. 执行真实点击、输入、滚动或拖拽。
5. 再读 state，确认行为结果。
6. 截图并实际查看图片，不只记录截图路径。
7. 在桌面与移动 viewport 上重复关键路径。

### 3. 构建发布产物

```sh
cd web && npm run build
cd ..
PATH=/usr/local/go/bin:$PATH make build
```

`npm run build` 会更新 `internal/webui/dist`；`make build` 再把这份 dist 嵌入 Go 二进制。只构建前端而没有重建 Go，生产 Restart 后仍可能加载旧的 embed 资源。

### 4. 验证 HTTP 产物

```sh
curl -fsS http://127.0.0.1:4870/api/health
curl -fsS http://127.0.0.1:4870/ | head
```

必要时从 HTML 取出 asset 文件名并请求一次，确认不是旧 HTML 引用了已删除的 hash 文件。Canary 和生产都要做这一步。

### 5. 重启后复验

用户点击 Restart Loom 后，重新执行 health、automation、关键交互和截图。开发端口上的成功不能替代生产 `4870` 验收。

## pinixc 浏览器规则

CodexLoom 日常使用 `/tmp/pinixc`。创建浏览器资源时必须指定 profile：

```sh
/tmp/pinixc browser open 'http://127.0.0.1:5188/#team' --profile default
```

返回 `tab_id` 后，后续命令不必重复 `--profile`，因为 tab 已经绑定 profile。若同时传入 `--profile default`，它只是身份断言，profile 不一致时应失败。

```sh
/tmp/pinixc browser tabs
/tmp/pinixc browser snapshot --tabid TAB_ID -i
/tmp/pinixc browser click REF_OR_SELECTOR --tabid TAB_ID
/tmp/pinixc browser fill REF_OR_SELECTOR 'text' --tabid TAB_ID
/tmp/pinixc browser eval 'document.title' --tabid TAB_ID
/tmp/pinixc browser screenshot --tabid TAB_ID
/tmp/pinixc browser close --tabid TAB_ID
```

页面变化后 ref 可能失效，每轮交互前重新 `snapshot -i`。不要跨 tab 或跨 profile 复用 ref。检查结束后关闭测试 tab，避免以后误操作旧页面。

## 结构化断言

规范自动化入口是 `window.codexLoom`，`window.codexHub` 只是兼容别名。第一步通常是：

```sh
/tmp/pinixc browser eval 'window.codexLoom.state()' --tabid TAB_ID
```

根状态至少包含当前 view、Agent 总数、active/idle 数量、选中 Agent、打开的 Agent tabs、未读 tab、实时 Thread subscriber 数、Remote 状态、Restart 状态和 Sidebar 状态。看到 `agentsCount: 0` 时先等待数据加载，不能立刻把空壳截图当验收结果。

常用操作示例：

```sh
/tmp/pinixc browser eval '(async()=>await window.codexLoom.selectAgent("codex-hub-dev"))()' --tabid TAB_ID
/tmp/pinixc browser eval '(async()=>await window.codexLoom.openTeam())()' --tabid TAB_ID
/tmp/pinixc browser eval 'window.codexLoom.team.state()' --tabid TAB_ID
/tmp/pinixc browser eval 'window.codexLoom.graph.state()' --tabid TAB_ID
/tmp/pinixc browser eval 'window.codexLoom.integrations.state()' --tabid TAB_ID
/tmp/pinixc browser eval 'window.codexLoom.inbox.state()' --tabid TAB_ID
/tmp/pinixc browser eval 'window.codexLoom.usage.state()' --tabid TAB_ID
/tmp/pinixc browser eval 'window.codexLoom.remote.state()' --tabid TAB_ID
```

对 Team Graph 不只看“图出现了”，还要断言 node/edge 数量、当前 view mode、选中节点、筛选后的 visible count，并实际执行 select、search、moveNode、fitView。对 Integrations 要断言 Connection、Address、channel/DM 数量和 setup 状态。对 Thread 要断言选中 Agent、历史是否加载、实时订阅是否存在。

## 桌面检查矩阵

桌面至少覆盖以下宽度级别：

| Viewport | 目的 |
|---|---|
| 1440×900 | 主力桌面工作区 |
| 1024×768 | 桌面与紧凑布局边界 |
| 1920×946 或当前全屏 | 宽屏信息密度和最大行宽 |

优先检查真实工作流，而不是每个页面各截一张静态图：

- Sidebar 展开、折叠、恢复；底部工具在两种状态下都可达。
- 多 Agent tabs 打开、切换、关闭、溢出滚动和未读状态。
- Thread 历史、Markdown、代码块、表格、图片、Tool Use、Reasoning 和输入框。
- 发送后的即时反馈、按钮禁用和防重复提交。
- Config、Profile、Usage 与 Connections 面板的打开、滚动和关闭。
- Inbox、Messages、Schedules、Integrations、Remote、Team 和 Design System。
- Team 的 Organization、Collaboration、Activity、Directory，以及 Graph 拖拽和 inspector。
- Restart waiting/restarting、busy/idle、empty、loading、error 和超长内容状态。

全屏工作台优先截当前 viewport。`--full-page` 会把内部滚动容器和固定元素摊平成一张长图，容易掩盖真实的遮挡、粘性定位和底部输入框问题，只用于需要阅读整页内容的页面。

## 移动端布局检查

### 不要把 clip 当 viewport

```sh
/tmp/pinixc browser screenshot --tabid TAB_ID --clip 0,0,390,844
```

这条命令只裁剪桌面页面的左上角，不会把 CSS layout viewport 变成 390px，也不会触发 `sm`、`md` 等响应式断点，不能作为移动端证据。

### 用同源 iframe 建立真实窄 viewport

当前 `pinixc browser` 没有独立的 resize/device 命令，`window.resizeTo()` 对受控 Edge tab 也不可靠。自动化检查使用同源 iframe 作为移动 viewport：iframe 的 browsing context 确实是 `390×844`，其中的媒体查询、flex/grid 和容器宽度都会按 390px 计算。

先正常打开 CodexLoom，再执行：

```sh
/tmp/pinixc browser eval '(async()=>{document.getElementById("loom-mobile-viewport")?.remove();const f=document.createElement("iframe");f.id="loom-mobile-viewport";f.src=location.origin+"/#integrations";Object.assign(f.style,{position:"fixed",left:"0",top:"0",width:"390px",height:"844px",border:"0",zIndex:"2147483647",background:"white"});document.body.appendChild(f);await new Promise((resolve,reject)=>{f.onload=resolve;setTimeout(()=>reject(new Error("iframe timeout")),5000)});await new Promise(r=>setTimeout(r,800));return {width:f.contentDocument.documentElement.clientWidth,height:f.contentDocument.documentElement.clientHeight,mobile:f.contentWindow.matchMedia("(max-width: 767px)").matches,state:f.contentWindow.codexLoom?.state?.()};})()' --tabid TAB_ID --timeout 10000
```

结构化结果必须确认 `width: 390`、`height: 844` 和 `mobile: true`。随后只截 iframe：

```sh
/tmp/pinixc browser screenshot --tabid TAB_ID --selector '#loom-mobile-viewport'
```

也可以通过父页面操作 iframe 内的 automation：

```sh
/tmp/pinixc browser eval '(async()=>{const w=document.getElementById("loom-mobile-viewport").contentWindow;await w.codexLoom.selectAgent("codex-hub-dev");return w.codexLoom.state()})()' --tabid TAB_ID
```

完成后移除 harness，或直接关闭测试 tab：

```sh
/tmp/pinixc browser eval 'document.getElementById("loom-mobile-viewport")?.remove()' --tabid TAB_ID
```

常用移动尺寸：

| Viewport | 目的 |
|---|---|
| 390×844 | 主力 iPhone 纵向布局 |
| 360×800 | 更窄的 Android 纵向布局 |
| 844×390 | 横屏和低高度压力测试 |

修改 iframe 的 `width`、`height` 后要重新读取尺寸和 state。截图返回后使用 `sips` 检查像素尺寸，并实际打开图片阅读：

```sh
sips -g pixelWidth -g pixelHeight /absolute/path/to/screenshot.png
```

### 移动端专项清单

- 页面根节点没有水平滚动；代码块、表格和 tab strip 可以在自己的局部容器内滚动。
- Sidebar 默认是 drawer，不占据主内容宽度；菜单按钮、backdrop、关闭按钮都可点击且不互相遮挡。
- Agent tabs 不挤压标题和操作按钮；长 Agent 名称能截断，关闭和信息入口仍可达。
- 顶栏不过高，不在窄屏继续展示模型、effort、路径、Thread ID 等次要配置。
- Thread 输入框始终可见，发送/停止按钮不被遮挡，底部使用 `safe-area-inset-bottom`。
- Jump to latest 不遮挡输入框；历史滚动、自动跟随和向上读旧记录互不打架。
- Markdown 表格、代码、长 URL、长英文单词、图片和 Tool Use 卡片不撑破 viewport。
- Config、Admin、Integration setup 和详情编辑器不超出屏幕；内部内容可滚动，保存/取消操作可达。
- Team 在窄屏默认使用 Directory；Graph 可以 pan/drag，但不能把整个页面拖走，inspector 不应永久占据首屏。
- Integrations 的 Connection、Address、群和 DM 编辑按单列排布；Advanced 内容默认收起，长 ID 不抢占主要信息。
- 触摸目标有足够间距；hover 才能发现的信息必须有点击或 focus 替代入口。
- 纵向与横向切换后布局重新计算，不保留遮挡页面的旧 drawer 或 modal 状态。

### iframe 不能替代真机

iframe 方法可靠验证 CSS 响应式布局，但不模拟移动 Safari/Chrome 的 User-Agent、DPR、触摸事件、软键盘、地址栏收缩、`100vh` 差异和刘海安全区。最终交付至少在一台真实手机上通过 Tailscale 打开：

```text
http://100.66.47.40:4870
```

真机重点检查 drawer 手势、输入框聚焦后的软键盘遮挡、底部 safe area、长历史滚动、Graph 触摸、横竖屏切换和返回导航。只有 iframe 验证时，应在交付说明中明确“移动布局已模拟，真机交互未验收”，不能写成“手机版完成”。

## 视觉检查方法

截图前先确认 state 已加载、目标 panel 已打开、loading 已结束。截图后从以下顺序阅读：

1. **轮廓**：页面分区、主次层级和信息密度是否合理。
2. **边界**：是否出现全局横向滚动、裁切、重叠、穿透或意外空白。
3. **文本**：长名称、正文、ID、按钮文字和错误信息是否可读。
4. **操作**：主操作是否明显，危险操作是否克制，图标是否有 tooltip 或可访问名称。
5. **状态**：selected、hover、focus、disabled、loading、success、error 是否能区分。
6. **连续性**：切换 Agent、收到实时消息、打开 drawer 或 modal 时是否发生布局跳动。

不要只看首屏最漂亮的状态。真实问题常出现在长 subject、多行 Profile、很多 tabs、空列表、错误提示、busy Agent、重复群名、图片附件和窄屏弹层。

## 交互与实时性

对一次交互同时验证三个结果：

1. UI 立即给出反馈，例如发送后立刻禁用按钮或清空已提交草稿。
2. automation state 或 API 数据发生预期变化。
3. SSE、Thread history 或外部平台最终出现对应结果。

发送消息属于真实写操作。使用生产 Agent 测试前必须确认目标和文本；不要因为按钮反馈延迟连续点击。验证实时同步时，用两个不同表面观察同一 Thread，例如 WebUI 加 Codex Mobile，发送一次唯一 canary 文本，确认另一端无需刷新即可出现。

Browser 网络检查可用于确认请求是否重复或失败：

```sh
/tmp/pinixc browser requests --tabid TAB_ID --filter /api/
/tmp/pinixc browser response REQUEST_ID --tabid TAB_ID
```

布局溢出可以用 DOM 断言辅助，但不能代替视觉判断：

```sh
/tmp/pinixc browser eval '({viewport:document.documentElement.clientWidth,page:document.documentElement.scrollWidth,overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth+1})' --tabid TAB_ID
```

## 状态覆盖

高风险页面至少覆盖以下状态：

| 状态 | 检查内容 |
|---|---|
| Loading | 骨架或提示不造成布局跳动，加载结束后控件可操作 |
| Empty | 说明准确，不显示失效操作，不把空数据误报为错误 |
| Error | 错误可读，重试入口明确，长错误文本不撑破布局 |
| Busy | running 状态、停止按钮、Restart waiting 和队列提示一致 |
| Long content | Agent 名、路径、subject、Markdown、URL 和 ID 不破版 |
| Many items | Agent list、tabs、Messages、群列表和 Graph 在大量数据下仍可导航 |
| Realtime | 状态和消息无需刷新更新，切换 tab 不重复订阅或漏消息 |

生产数据不适合制造错误和空状态。使用 canary fixture 或临时 API 数据覆盖这些场景，完成后停止 canary。

## 最小验收记录

一次前端交付至少记录：

```text
Source:        changed components
Build:         npm run build / make build
Target:        dev, canary or production URL
Desktop:       viewport, URL, automation assertion, screenshot path
Mobile:        viewport, automation assertion, screenshot path
Interactions:  actions performed and observed results
Realtime:      SSE or second-surface result when relevant
Residual risk: untested real device, provider, error state or destructive path
```

这份记录的价值是让下一位维护者知道“验证了什么”，而不是只知道“某次截图存在”。

## 常见误区

- `npm run build` 通过就宣布完成。
- 只截图，不读 `window.codexLoom.state()`。
- 只读 state，不实际点击和查看截图。
- 用 `--clip 390×844` 冒充移动 viewport。
- 页面还显示 0 Agent 或 loading 时就截图。
- 只截 full page，忽略真实 viewport 内的固定元素和内部滚动。
- 在 `5188` 上执行写操作，却忘记它代理生产 `4870`。
- 只验证 Vite，不构建 dist 和 Go embed binary。
- Restart 后不再检查生产 URL，导致验收的是旧 tab 或旧资源。
- 只做桌面模拟，却宣称真机软键盘、touch 和 safe area 已通过。
- 得到截图路径但没有打开图片进行视觉复核。
