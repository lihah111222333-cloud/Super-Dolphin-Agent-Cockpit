# Provider-native context 契约

V3 只负责把白名单内的 `provider`、已存在且 canonical 的绝对 `cwd`、（若指定）绝对 clean 的 `providerHome`、受限 token 形式的 profile 与能力探测结果传给原始 CLI/app-server；未指定 `providerHome` 时由 provider 使用其自身默认身份目录，不由 V3 复制或接管。上下文不携带 memory 或 skill 正文，也不隐式回退到 V3 管理链。

`internal/contract.ProviderNativeContext` 固定声明 `memory` 与 `skills` 两项能力。每项能力必须明确 owner 和 status；`supported` 必须是 `owner=provider` 且携带有限字符的 `evidence`；`unsupported`、`unknown`、`disabled` 必须提供稳定 reason code。调用 `RequireProviderFeature` 时，只有 `owner=provider,status=supported` 才成功，其余状态返回可由 `errors.Is` 分类的 typed error。上下文 JSON 使用 strict decoder，未知字段、重复字段、尾随值和校验失败都会阻断。

Provider adapter 可以保留 provider home 与路径安全校验，但不得在启动或恢复阶段生成、同步或校验集中式 skill mirror。原始 CLI 自己发现其目录和配置；V3 不得把缺少 native 能力静默转换为空上下文或旧 memory/skill 注入。
