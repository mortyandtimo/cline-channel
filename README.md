# Cline Pass Switcher · CPA 插件

把 **Cline 订阅模型**接进 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)（CPA），
并支持**逐个模型钉住上游渠道**。

装好、填上 Cline API Key，就能在 Cherry Studio、DSH、Claude Code、Codex 等任何
走 CPA 的客户端里直接用 `cline-pass/*` 模型；想让某个模型固定走哪个上游渠道，在面板里点选即可。

---

## 它解决什么问题

Cline 的订阅模型（Cline Pass）背后不是单一上游，而是一串第三方推理服务：
`baseten`、`novita`、`fireworks`、`deepinfra`、`gmicloud`、`wafer`……由 Cline 网关自己调度。
不同渠道的成本、速度、量化格式都不一样（`fp8` / `bf16` / `fp4`），质量也有差别。

这个插件做两件事：

1. **接入**：把 Cline Pass 模型作为 CPA 的一个 provider 暴露出来，CPA 负责把 OpenAI / Anthropic /
   Gemini 等各入口协议统一翻译成 OpenAI 格式再交给插件，插件只处理一种协议。
2. **钉扎**：探测模型背后真实可用的渠道，让你自己选走哪一个，并把选择注入到请求里。

模型名保持 Cline 原生形式，天然与 CPA 里其它 provider 隔离：

```
cline-pass/deepseek-v4.1-flash    owned_by: cline
cline-pass/glm-5.3-flash          owned_by: cline
```

---

## 快速开始

### 1. 拿到插件

**方式 A：用仓库里的预编译产物**（推荐）

```
dist/cline-channel.so        # Linux amd64 / glibc，直接可用
```

**方式 B：自己构建**（见文末「构建」）

### 2. 装进 CPA

把 `.so` 放进 CPA 容器的插件目录，并确认 `config.yaml` 启用了插件。
最简配置可以参考 [`config.example.yaml`](config.example.yaml)：把它里面的
`plugins` 段落合并进 CPA 的 `config.yaml`，即可。

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    cline-channel:
      enabled: true
```

重启 CPA，日志里出现下面这行就算装好了：

```
pluginhost: plugin registered plugin_id=cline-channel version=2.2.1
```

### 3. 填写 Cline API Key

打开插件面板：

```
http://<你的 CPA 地址>/v0/resource/plugins/cline-channel/panel
```

在顶部 **「Cline 账号」** 卡片上点 **「查看 / 修改 Key」**，粘贴你的 Cline API Key（`sk_` 开头），
保存。**立即生效，不需要重启 CPA。**

面板只显示模糊化结果（例如 `sk_91c****cd49`），便于分辨是哪一把 Key，明文不会回到浏览器。

也可以不用面板，直接写 CPA 的 auth 文件（`<auth-dir>/cline.json`）：

```json
{
  "provider": "cline",
  "type": "cline",
  "label": "Cline Pass",
  "api_key": "sk_你的key",
  "disabled": false
}
```

> Key 从哪来：登录 <https://app.cline.bot> → 账号设置 → API Keys。
> 需要 **Cline Pass 订阅**才能用这些模型。

### 4. 在客户端里用

CPA 里已经能看到的模型，客户端也就能用：

```
Base URL:  http://<你的 CPA 地址>/v1
API Key:   你在 CPA 里配置的访问密钥
Model:     cline-pass/deepseek-v4.1-flash
```

---

## 钉住上游渠道

面板里每个模型一行，点 **「设置渠道」**：

1. **探测渠道** —— 用两种写法各发一个带假渠道名的请求，让 Cline 在路由阶段就拒绝，
   拒绝信息里带着该模型真实的可用渠道清单（**零 token 消耗**）。
2. 勾选渠道，可编辑顺序 —— 多个渠道按顺序尝试。
3. 选切换方式：
   - **strict** —— 只走所选渠道，按顺序尝试，全都不可用就报错
   - **preferred** —— 优先所选渠道，异常时交回 Cline 自动回退
4. 保存，立即生效。

### 关于 strict：钉几个渠道很重要

strict 的语义是「只认这些渠道」。它会把勾选的每个渠道展开成一个独立候选，
**按顺序接力**：

| 钉扎配置 | 实际行为 |
|---|---|
| strict + 1 个渠道 | 该渠道不可用/容量不足 → **直接失败**，没有任何余地 |
| strict + 3 个渠道 | 依次尝试，前一个失败自动换下一个 |
| preferred + N 个渠道 | 网关层软优先，忙时自动回退 |

Cline 的第三方渠道容量是波动的（`baseten` 会返回 503，`deepseek` 官方渠道会 429 at capacity），
**所以钉单个渠道等于把可用性押注在一个渠道上**。想稳定就多钉几个，或者用 `preferred`。

---

## 模型窗口信息（上下文 / 最大输出）

CPA 的 `/v1/models` 只返回 `id` / `object` / `owned_by` 三个字段，**不带上下文长度和最大输出**。
这会让 DSH、Cherry Studio 这类 agent 无法自动换算窗口预算，只能手填，有时直接报
`model declares no contextWindow — window-relative budgets disabled for this request`。

插件内置了 Cline Pass 全部订阅模型的窗口规格，并按 CPA 原生 `model-definitions` 的形状
通过 `/capabilities` 暴露：

```
GET /v0/resource/plugins/cline-channel/capabilities

{"channel":"cline","models":[
  {"id":"cline-pass/deepseek-v4.1-flash","context_length":1048576,"max_completion_tokens":384000},
  {"id":"cline-pass/deepseek-v4-pro",   "context_length":1048576,"max_completion_tokens":393216},
  ...
]}
```

数值来自 [OpenRouter 公共目录](https://openrouter.ai/api/v1/models) 的 `context_length`
与 `top_provider.max_completion_tokens`。Cline 的模型 ID 用的就是 OpenRouter 的
`provider/model-name` 命名约定（官方 Models 文档明确说明），实测响应里的 `canonicalSlug`
与目录条目一一对应，可以逐条核对。OpenRouter 尚未收录的新模型（例如 `qwen3.8-max`）
不提供数值 —— 宁可让客户端拿不到，也不塞一个猜测值误导上下文预算。

**要让客户端真正看到这些字段，需要一个中间层把它们注入 `/v1/models`**，因为 CPA 本身
不透传（对内置 provider 也不透传，`/v1/models` 一律只有三个字段）。参考做法见 `cpa-stack`
里的 `compat-proxy`：它先试 CPA 原生的 `model-definitions`，遇到插件 provider 返回的
`{"error":"unknown channel"}` 时，改从上面的端点取。

### 关于思考强度

目前**选不了**。Cline API 的请求参数只有 `model` / `messages` / `stream` / `tools` /
`temperature`（官方 Chat Completions 文档的参数表），以下写法实测**一律被 500 拒绝**：

```
reasoning_effort=low|high
reasoning.{effort, max_tokens}
thinking.budget_tokens
providerOptions.openrouter.reasoning / providerOptions.gateway.reasoning
```

对照：钉扎用的 `providerOptions.gateway.{only,order,sort}` 是生效的，说明 Cline 对
`providerOptions` 走白名单校验，而 `reasoning` 不在名单内。模型目录（445 个）也只返回
`id/object/created/owned_by`，没有任何能力字段，`clinePass` 的 15 个模型也没有
`-thinking` / `-high` 这类变体。

所以在 Cline 这条链路上，能影响的只有：**选哪个模型**（`flash` 系列与 `pro`/`max`
系列的推理量差别明显）、以及 **`max_tokens`**（它约束 reasoning + 正文的**总预算**，
调大是给思考留空间，不是提高思考强度）。

> CPA 侧的契约其实是齐的（`ThinkingSupport` / `ThinkingConfig{Mode,Budget,Level}` /
> `ThinkingApplier`），插件也预留了接入位，但上游不认这些参数，所以没有实现。
> 真要控制推理强度，只能绕开 Cline Pass，直连支持该能力的 provider。

## 重要：别让一次抖动拉黑整个 provider

这一条和插件无关，但**强烈建议照做**，否则会踩一个很隐蔽的坑。

Cline 的第三方渠道偶发返回 `500 empty response content`（瞬时故障，重试就好）。
问题在于 CPA 会把这种失败记到 **凭据账上**，累积几次就把 cline 账号标记为
`unavailable`，随后 **所有** `cline-pass/*` 模型一起返回 `auth_unavailable` ——
看起来像"整个插件挂了"，其实只是被上游抖了一下。

在 CPA 的 `config.yaml` 加上这两行（顶层，与 `plugins` 同级）：

```yaml
disable-cooling: true
transient-error-cooldown-seconds: -1
```

含义：

- `disable-cooling` —— 全局禁用凭据冷却调度，消除失败后的黑窗
- `transient-error-cooldown-seconds: -1` —— 瞬态错误（408/500/502/503/504）不冷却

本机每个 provider 只有一个凭据时，禁用冷却不损失任何东西。如果你给 Cline 配了多账号池、
想保留坏凭据隔离，可以改成只对这一个凭据生效：把 `"disable-cooling": true`
写进 `auths/cline.json`，并去掉全局那行。

插件侧另外做了一层防护：无钉扎时最多尝试 3 次上游请求，只对 429/5xx 换渠道重试，
把瞬时抖动尽量吸收在插件内部。

---

## 面板

```
http://<CPA 地址>/v0/resource/plugins/cline-channel/panel
```

- **Cline 账号** —— 是否已接入、当前 Key（模糊化）、查看/修改 Key
- **模型列表** —— 15 个订阅模型，可搜索，逐行显示当前渠道设置与**最近实际命中**
- **设置渠道** —— 探测、勾选、排序、strict/preferred、排除渠道、逐个校验
- **最近请求** —— 实际命中的渠道、耗时、尝试次数
- **刷新官方列表** —— 从 Cline 官方接口同步订阅模型清单

「最近实际命中」是判断钉扎有没有生效的**唯一可靠依据**：它取自上游响应里的
`routing.finalProvider`。与所选渠道不一致时面板会标黄提示。

---

## 配置项

`config.yaml` → `plugins.configs.cline-channel`：

| 字段 | 默认 | 说明 |
|---|---|---|
| `enabled` | `true` | 是否启用 |
| `priority` | `10` | 插件优先级 |
| `base_url` | `https://api.cline.bot/api/v1` | Cline 网关地址 |
| `api_key` | 空 | Cline API Key；留空则读 `<auth-dir>/cline.json` |
| `accounts` | 空 | 账号池（`name` / `key` / `enabled`），用于轮询与故障切换 |
| `account_mode` | `single` | `single` 用当前账号；`roundrobin` 按请求轮换 |
| `pin_mode` | `preferred` | `strict` 只走钉住的渠道；`preferred` 允许回退 |
| `pin_style` | `auto` | 钉扎注入写法：`auto` / `vercel` / `openrouter` / `both` / `none` |
| `pin_rules` | 空 | 静态钉扎规则（面板选择优先） |
| `pin_state_file` | 空 | 面板选择落盘路径，默认 `<auth-dir>/cline-channel-pins.json` |
| `models` | 空 | 显式模型白名单；留空从 Cline 官方接口同步 |
| `stream_mode` | `collect` | 流式转发实现；见下 |
| `timeout_seconds` | `300` | 单次上游请求超时 |
| `debug` | `false` | 路由与钉扎详情日志 |

### 为什么 `stream_mode` 默认是 `collect`

CPA 宿主提供的 `host.stream.emit` 是**同步**回调，而 CPA 要等插件返回
`execute_stream` 之后才把响应头交给客户端 —— 两者互等，形成死锁：

```
插件等 host.stream.emit 返回 → 它等客户端连接可写 → 客户端在等响应头 → CPA 在等插件返回
```

实测表现是**流式请求永久挂起**（上游其实 2 秒内就正常返回了 SSE），
Cherry Studio、DSH 这类默认走流式的客户端会直接卡死。

所以默认走 `collect`：把上游 SSE 收完再一次性交回，客户端拿到的仍是标准流式响应
（标准事件序列 + `data: [DONE]`），只是首字延迟等于完整生成时间。
宿主修好这个回调后，把 `stream_mode` 改成 `emit` 即可切回真流式。

---

## 构建

需要 Docker 与 `golang:1.24-bookworm` 镜像（与 CPA 容器同为 Debian 12 / glibc）：

```powershell
./build.ps1
```

脚本依次执行 `go vet ./...`、`go test -race ./...`，再构建 Linux `c-shared` 产物到
`dist/cline-channel.so`，并校验 ELF 头。

> 不要在 Windows 上直接把 `.dll` 改名成 `.so`，架构与 libc 都不对，CPA 加载会失败。

---

## 兼容性

- 目标 CPA：**v7.2.146**（`schema_version 4`）
- ABI 版本 1；`sdk/` 下为 vendored 的 CPA 插件 SDK，无第三方运行时依赖（仅 `yaml.v3`）
- 可与其它插件（如 `workbuddy`、`qoderwork`）共存，模型名互不冲突
- 已通过 `go vet`、`go test -race` 与真实请求验证

---

## 已知限制

- **`count_tokens` 是估算值**（按 4 字节/token）。Cline 网关没有公开的 token 计数端点。
- **模型清单是「快照 + 动态拉取」**：内置快照来自实测，启动时会尝试从官方接口刷新，成功后覆盖。
- **渠道名是两套命名**：直连管道用 OpenRouter 的 provider slug，规划器管道用 Vercel 的 slug。
  钉错名字时 strict 会报错、preferred 会静默回退，所以面板会告诉你该用哪种写法。
- **探测依赖上游拒绝信息的格式**，若 Cline 改了措辞会退到备用路径，全部失败时面板会如实说明，
  可以手工填写渠道名。

---

## 许可

MIT，见 [LICENSE](LICENSE)。
