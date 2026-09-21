# OpenAI-GPT 渠道

从 Huanxing-api 移植 OpenAI-GPT 渠道与 Azure GPT 兼容功能，保留 new-api 的 relaykit 模块边界和渠道选择机制。

## 使用

在渠道管理中新增或编辑渠道，选择 **OpenAI-GPT**，填写上游 Base URL、密钥和模型。在高级设置的字段透传区域按需启用：

- **兼容 Azure GPT**：默认关闭。清理加密历史、移除 GPT-5/GPT-6 的 temperature、修复旧工具调用 item_* ID，并保留工具 schema、call_id 和工具结果的关联。
- **删除 GPT-5 / GPT-6 的 temperature 参数**：独立开关，默认关闭；适用于只需要移除 temperature 的上游。

兼容处理覆盖 Chat Completions、Responses、Responses Compact 和 Chat 转 Responses；也覆盖请求体透传。处理发生在最终出站请求上，参数覆盖不能重新引入被兼容规则移除的字段。

OpenAI-GPT 在本仓库使用渠道类型 **63**；类型 **62** 继续表示 Seedance。从源仓库导入渠道配置时，应将 OpenAI-GPT 类型映射为 63。两个开关沿用 `settings` JSON 中的 `remove_gpt_temperature` 和 `remove_azure_gpt_encryption` 字段。

## 行为边界

- 保留源渠道的命名空间修复、工具 schema 校验、旧 functions/function_call 兼容、Codex 请求头和流选项处理。
- 能在本地确认的请求格式问题返回明确的 400；无法安全清理的加密压缩历史或被引用的历史项也会返回错误。
- 上游缺少请求所需能力时，遵守重试预算和渠道固定约束，排除本次请求中已确认能力不匹配的渠道；此类错误不触发自动禁用。
- 普通 OpenAI 和其他渠道不启用这些 OpenAI-GPT 请求体规则。
- 此开关处理已支持的协议兼容问题，不能保证修复所有 400，例如无效工具输入、缺少上下文或上游不支持的模型。

## 验证（2026-09-21）

- 请求体集成测试覆盖渠道隔离、开关开/关、透传开/关、Chat 转 Responses、参数覆盖和 Compact 的安全拒绝。
- 前端渠道表单和交互测试：7 个文件、21 项通过；TypeScript、受影响文件 oxlint/oxfmt、i18n 同步和生产构建通过。
- `go build ./...` 与 `cd relaykit; GOWORK=off go build ./...` 通过。
- 数据库矩阵验证了兼容开关存取、缓存开/关的渠道排除和候选耗尽：SQLite 3.50.4、MySQL 8.4.11、PostgreSQL 15.19，均通过。
- 数据库命令：设置指向独立测试库的 `OPENAIGPT_TEST_MYSQL_DSN` 与 `OPENAIGPT_TEST_POSTGRES_DSN`，运行 `go test ./model -run TestOpenAIGPTChannelSelectionDatabaseMatrix -v -count=1`。测试使用 `openaigpt_test_` 表前缀；请使用专用临时测试库。
- `service` 包的既有亲和性缓存测试在 Windows 上可能出现累计计数失败。在原始提交 `eedeb7be` 的独立工作树上也已复现；本次未修改相关代码。

生产构建产物位于 `web/dist`。此移植不包含服务器部署。
