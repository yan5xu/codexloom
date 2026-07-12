# CodexLoom Visual Identity

CodexLoom 的视觉识别服务于 Agent 治理工作台，而不是营销装饰。品牌表达来自“多个长期领域沿各自轨迹工作，并通过组织关系被编织到一起”。

## 核心标志

织机标志由五条彩色纬线和三条深色经线组成。交错关系必须保留，不能退化成普通表格或井号。

- 五条线代表并行、持续且边界清晰的领域。
- 交织部分代表 Agent 的关系、通信和组织协作。
- 线从织机两侧继续延伸，表达 Thread 在一次协作前后都保持连续。
- 标志可以使用全彩、纯黑或反白版本；小尺寸下只使用标志，不附加说明文字。

方向稿见 [CodexLoom VI direction](assets/codexloom-vi-direction.png)。方向稿用于确定语言和气质，不是需要逐像素复制的产品页面。

## 色彩

品牌线色顺序为 teal、vermilion、amber、green、blue。它们用于品牌标志、关系图和确实需要类别区分的数据。

日常工作台以 warm paper、system gray 和 graphite 为主。Graphite 承担命令，清晰蓝只用于选择、键盘焦点和少量链接，不作为主题底色。语义状态仍使用稳定映射：green 表示健康运行，ochre 表示等待或需要关注，brick red 表示失败。品牌五色不能为了装饰而铺满界面。

## 排版与界面

- Serif 用于产品名和页面身份。
- Sans-serif 用于高密度操作界面。
- Monospace 只用于 ID、路径、命令、时间和机器状态。
- 页面使用分栏、行和细分隔线建立层次，避免卡片套卡片。
- 选择、状态与可执行操作必须稳定可扫读，不能依赖颜色作为唯一线索。

生产 Token、组件状态和操作模式以 WebUI 的 `#design` 页面为准。React 中统一使用 `BrandMark` 和 `BrandLockup`，不要在业务组件里重画标志。
