# Kimi KVV 相关问题记录（2026-11-10）

本文记录 2026-11-10 排查 Kimi K3 / K3.1 Pro 动态工具与 `tool_choice` 时发现的问题、原因、处理和待办。
对照的官方用例为 Kimi-Vendor-Verifier（KVV）的 `tests/k3_features/test_dynamic_tools.py` 与 `tests/k3_features/test_tool_choice.py`。

## 涉及的提交与分支

| 仓库 | 提交 | 说明 | 状态 |
|---|---|---|---|
| ipp-new-api | `054ce9e2e` | Messages 请求头同步；新增「无工具时兼容 required 工具选择」开关 | 已在 main |
| ipp-new-api | `b871b07e7` | 上游拒绝「只有动态工具 + required」时，把动态工具提升为顶层 tools 后重试 | 已在 main |
| ipp-new-api | `d72653b34` | KVV 请求不再执行上述兼容开关；KVV 本地校验 `tool` 消息必须带 `tool_call_id` | 分支 `fix/moonshot-kvv-tool-choice`，已推送 |
| Huanxing-api | `5a025ba3e` / `779bb10da` | 与 `054ce9e2e` / `b871b07e7` 对应 | 已在 main，api.huanxing.ai 已部署 |
| Huanxing-api | `618adf179` | 与 `d72653b34` 对应 | 分支 `fix/moonshot-kvv-tool-choice`，已推送 |

两个仓库的 `fix/moonshot-kvv-tool-choice` 分支上还有一批未提交的改动：删除兼容开关（见问题 1）。

## 问题 1：「无工具时兼容 required 工具选择」开关与 KVV 冲突

**现象**：对 api.huanxing.ai 跑 KVV，两个模型的下列用例在 stream / nostream 下全部失败（期望 400，实际 200）：

- `test_tool_choice_required_without_tools_rejected`
- `test_tool_choice_with_empty_tools_rejected`

单独复测：`tool_choice:"required"` 不带 tools，两个模型各 5 次全部 200；同样不带 tools 的 `tool_choice:"bogus"` 则全部被上游以 400 拒绝。

**原因**：该开关（`normalize_required_tool_choice_without_tools`）在没有 tools 时删除 `tool_choice:"required"`，上游因此不再返回官方要求的 400。
Huanxing-api 在 `5a025ba3e` 之前对 KVV 请求跳过这一步（`!kvvEnabled`），10 月 14 日这两个用例是通过的；`5a025ba3e` 去掉了这层判断，ipp 的 `054ce9e2e` 也没有区分 KVV。

**处理**：

1. `d72653b34` / `618adf179`：KVV 请求不再执行该开关。
2. 随后决定删除该开关（未提交）：
   - ipp：整体删除，包括后端逻辑、`relay/channel/moonshot/tool_choice.go`、relaykit 字段、前端开关和 7 个语言文件的文案。
   - Huanxing-api：OpenAI Chat、Claude Messages、Responses（含透传）都不再删除 `required`。原开关附带的「规范化非法函数名」拆成独立开关 `normalize_function_names`；后端继续读取旧字段，已开启旧开关的渠道函数名处理不变；渠道保存时前端写入新字段并删除旧字段。

**影响**：原先开着该开关的渠道，客户端发送 `required` 却不带 tools 时，会收到上游的 400，不再被静默放行。

## 问题 2：KVV 本地校验缺少 `tool_call_id`

**现象**：`test_tool_role_without_tool_call_id_rejected` 失败，kimi-k3 的 stream 和 nostream 都失败，kimi-k3.1-pro 失败了 stream。

单独复测（`{"role":"tool","content":"some tool result"}` 不带 `tool_call_id`）：

- kimi-k3：8/8 返回 200，其上游不校验这一项。
- kimi-k3.1-pro：3 次 200；4 次 400 `invalid request body: Invalid 'messages': missing field 'tool_call_id'`；1 次 400 `Request format error`。

**原因**：KVV 本地已覆盖动态工具的其他平台校验项（角色、内容、名称、重复等），唯独没校验这一项，结果取决于路由到哪个上游。

**处理**：`d72653b34` / `618adf179` 在 `adaptKVVRequest` 中增加校验，`role` 为 `tool` 且 `tool_call_id` 为空时返回 400：`messages[N].tool_call_id is required for role tool`。

## 问题 3：部分上游不认「只有动态工具 + required」

**现象**：部分上游只统计顶层 tools，对「只有动态工具（system 消息内的 tools）+ `tool_choice:"required"`」返回 400 `tool_choice 'required' requires at least one tool`。

**处理**：`b871b07e7` / `779bb10da`。仅对 KVV 请求，在收到这条 400 时，把动态工具提升为顶层 tools 后重试一次。

**注意**：

- 只在 KVV 开启时生效。
- 若请求同时带全局工具，首个请求会被上游接受，不会触发重试；如果这类上游并不渲染 `messages[].tools`，模型看不到动态工具。
- 重试依赖精确匹配错误文案（单引号、双引号两种写法）。双引号版本与本仓库 `relay/channel/openaigpt/validation.go` 的报错格式相同。

## 问题 4：`tool_choice:"required"` 不能强制工具调用

**现象**：下面这条请求在 cc.ccwork.site 和 api.huanxing.ai 上都不调用工具，返回 `finish_reason=stop` 和一句问候。

```json
{
  "model": "kimi-k3.1-pro",
  "messages": [{"role": "user", "content": "Hello!"}],
  "tools": [{"type": "function", "function": {"name": "get_weather", "description": "Get the weather of a city.",
    "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"], "additionalProperties": false}}}],
  "tool_choice": "required",
  "max_completion_tokens": 4096
}
```

实测：ccwork 上 kimi-k3.1-pro 0/3 调用；api.huanxing.ai 上 kimi-k3.1-pro 0/2、kimi-k3 0/2 调用。

**排查**：

- ccwork 网关（容器 `new-api-3309`，日志目录 `/www/server/panel/data/compose/ccworkcc/logs`）显示请求走 34 号渠道 `kimi-waninter-3-1`：Moonshot 类型，已开 KVV，上游为供应商 waninter，无参数覆写。
- 本地用 ipp 的 `TextHelper` 回放该请求：发给上游的 body 原样保留 `tool_choice:"required"` 和工具定义；开 KVV 时只额外加 `thinking`（`type: enabled`、`keep: all`、`effort: max`）和 `reasoning_effort: max`。
- 上游确实收到并校验了 `tool_choice`：

| 请求 | 上游返回 |
|---|---|
| `tool_choice:"bogus"`（带 tools） | 400 `tool_choice must be 'none', 'auto', 'required', or a supported tool choice object` |
| `tool_choice:"required"`（不带 tools） | 400 `tool_choice 'required' requires at least one tool` |
| `tool_choice` 指定函数 | 400 `tool_choice 'specified' is incompatible with thinking enabled` |
| kimi-k3 关闭思考 | 400 `The model does not support disabled thinking mode. Please enable thinking and try again, or choose another model.` |

- prompt_tokens 与官方基准一致（KVV `testdata/prompt_token_cases`，消息和工具相同，仅 `tool_choice` 不同），说明上游与官方平台一样，把 `tool_choice` 渲染成提示词：

| 用例 | `tool_choice` | 官方基准 | ccwork kimi-k3.1-pro | ccwork kimi-k3 |
|---|---|---|---|---|
| `k3_tool_single` | 不传 | 171 | 171 | 172 |
| `k3_tool_required` | `required` | 207 | 207 | 208 |
| `k3_tool_choice_none` | `none` | 209 | 209 | 211 |

**根因**：`required` 在上游只是一段约 36 个 token 的提示词，不是强制解码。两个模型在这些上游都必须开启思考，思考模式下模型可以不遵守。问题需要工具时会调用，与工具无关时（如 "Hello!"）可能不调用。这不是网关代码的问题，原样转发无法修复。

官方 KVV 的 `test_tool_choice_required_forces_call` 用的是天气问题，所以能通过；但 KVV 说明里写的是 `required` 在「提示词不需要工具时也必须调用」，严格来说上游没有达到这个要求。

**不同写法实测（ccwork，问「北京天气」）**：

| 写法 | kimi-k3.1-pro | kimi-k3 |
|---|---|---|
| user 在前，带 tools 的 system 在后，`required` | 3/3 调用 | 3/3 调用 |
| 同上，流式 | 2/2 调用 | 2/2 调用 |
| 带 tools 的 system 在前，user 在后，`required` | 2/2 调用 | 2/2 调用 |
| 顶层 tools + `required` | 2/2 调用 | 2/2 调用 |
| user 在前，带 tools 的 system 在后，`auto` | 2/2 调用 | 2/2 调用 |
| 问题换成 "Hello!"，动态工具 + `required` | 0/2 | 2/2（城市参数是编的） |

结论：写法（顶层 tools 或动态工具、位置）不影响是否调用，决定因素是问题本身是否需要工具。

**额外指令实测（ccwork，"Hello!" + 顶层工具 + `required`，各 3 次）**：

指令文本：`You must call at least one of the provided tools in this response. Do not answer the user directly without a tool call.`

| 方式 | kimi-k3.1-pro | kimi-k3 |
|---|---|---|
| 原样 | 0/3 | 2/3 |
| 指令作为开头的 system 消息 | 3/3 | 1/3 |
| 指令作为结尾的 system 消息 | 3/3 | 2/3 |

强制调用时模型只能编造参数（例如城市填 "New York"）。

**可选补救方案（未决定）**：

1. 没调工具就重试：第一次请求原样发送（与官方 prompt 一致，不影响 KVV），返回中没有工具调用时加指令重试一次。流式请求需缓存到正文开始，带 `required` 的流式请求首字会变慢；只有重试时多花 token。
2. 渠道开关「始终追加指令」：实现最简单，流式不受影响；每次多约 25 个 prompt token，开启时 KVV 的 prompt_tokens 用例对不上，对 kimi-k3 基本无效。
3. 不改代码：保持与官方一致，把复现请求和以上证据反馈给上游供应商 / Moonshot。

## 问题 5：kimi-k3.1-pro 背后多个上游行为不一致

同一请求连续发送，kimi-k3.1-pro 会落到不同上游：返回的 id 格式不同；对缺 `tool_call_id`、关闭思考等请求，有时 200、有时 400。KVV 结果会随路由变化，正式认证时应固定到同一个已开 KVV 的渠道。

## 其他发现（与本次改动无关）

- ipp-new-api：`go test ./...` 中 `service` 包的 `TestObserveChannelAffinityUsageCacheByRelayFormat_*` 失败，撤掉本次改动后同样失败，属于原有问题。
- Huanxing-api：`go build ./...` 在 `tests/openai/gpt_400_20260907_20260908/local_validation/.../executor.go` 处失败，原因是缺少模块 `github.com/codex2api/auth`。排除 `tests/` 目录后构建和测试均通过。

## 验证方法

官方 KVV（在 Kimi-Vendor-Verifier 仓库根目录执行；key 通过环境变量传入，不要写进文件）：

```bash
export KIMI_API_KEY=<key> KIMI_BASE_URL=https://<gateway>/v1
.venv/Scripts/python.exe -m pytest tests/k3_features/test_dynamic_tools.py tests/k3_features/test_tool_choice.py \
  --smoke-model kimi-k3.1-pro --think-mode kimi -n 4 -p no:cacheprovider \
  --junitxml=reports/<run>/junit.xml -ra -v --tb=short
```

2026-11-10 对 api.huanxing.ai（已部署 `779bb10da`）的结果：

| 模型 | 通过 | 失败 | 跳过 |
|---|---|---|---|
| kimi-k3 | 68 | 6 | 2 |
| kimi-k3.1-pro | 69 | 5 | 2 |

跳过的 2 个是官方标记 skip 的 `test_tool_choice_named_function_rejected`；失败项即问题 1、问题 2。报告在 `D:\kimi\Kimi-Vendor-Verifier\reports\huanxing-dynamic-tools-20261110-kimi-k3\` 和 `...-kimi-k3.1-pro\`。

## 待办

- [ ] 提交并推送两个仓库中删除兼容开关的改动，合并到 main 后部署。
- [ ] 部署后对 api.huanxing.ai 和 cc.ccwork.site 重跑上述两个 KVV 测试文件。
- [ ] 确认 cc.ccwork.site 当前部署的提交。
- [ ] 决定问题 4 的补救方案，或向上游反馈。
- [ ] 向上游反馈：kimi-k3 所在上游不校验 `tool_call_id`；kimi-k3.1-pro 多上游行为不一致。
