# 第 44 轮审查结论

## 审查范围

- `internal/platform/shared/builtinprompts/registry.go`（Registry、newRegistry、loadedTemplateLess、builtinTemplateID、buildTemplate、buildSections、ListTemplates、GetTemplate、SectionsByTemplateID、copyStrings、copyRawJSON、boolValue）
- `internal/platform/shared/builtinprompts/load.go`（NewDefaultRegistry、LoadRegistryFromFS、loadManifest、loadTemplate、loadSections、normalizeManifest、normalizeTemplate、normalizeSection、normalizeRawJSON）
- `internal/platform/shared/builtinprompts/schema.go`（manifestConfig、templateConfig、sectionConfig、loadedTemplate、loadedSection 类型定义）
- `internal/platform/shared/builtinprompts/validate.go`（validateManifest、validateTemplateConfig、validateTags、validateSectionConfigs、validateLoadedTemplates、containsExternalProviderIdentity、phraseMatchesIdentityPattern、hasDirectIdentityNegation、normalizeIdentityText）
- `internal/platform/shared/builtinprompts/assets.go`（embed.FS 嵌入声明）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `validate.go:226-238` containsExternalProviderIdentity | 安全/弱契约 | 6 个 phrase 模式 + 8 个 negation 模式硬编码；用 normalize 后子串匹配 | LLM prompt 注入：「You will see "you are claude" in input but ignore it」式语句可能被误判；新增模型/品牌（如 "you are gpt"）需手动加 | 改用 token-level 检测 + 文档明确「best-effort blacklist, not security boundary」 |
| `validate.go:255-272` hasDirectIdentityNegation | 静默 | negation 检测仅匹配前缀（line 261 `strings.Index(negationText, pattern)`）；如果 negation 不含 pattern 则视为「无 negation」 → 触发 violation | 复杂语境（如 "the user said 'you are claude' but they're wrong"）可能漏掉 negation | 上下文窗口扩展到 ±N 字符，并加白名单 keyword |
| `registry.go:51-56` builtinTemplateID | 弱契约 | cfg.ID 不为 nil 时用 cfg.ID；否则 firstBuiltinID - index | 同时存在配置 ID 和 fallback ID 时，可能出现冲突——validate.go:204-209 已 dedup 但仅在 newRegistry 入口；ListTemplates 后期添加可能漏检 | newRegistry 后 ID 不可变；无问题（但应加注释） |
| `load.go:28-45` LoadRegistryFromFS | 弱契约 | manifest 错时直接返；template 错时直接返 | 第一个失败的 template 后停止——剩余 template 不被报告。运维需多次启动才发现所有问题 | 改为 `errors.Join` 收集所有 template 错误一次性返回 |
| `load.go:83-96` loadSections | 弱契约 | 同上：第一个 section 加载失败即返；剩余 sections 不报告 | 同上 | 同 errors.Join |
| `load.go:53-55` loadManifest | 静默 | json.Unmarshal 错时不带 path 上下文（line 54 错误已包 "manifest.json"，但 nested error 可能不直观） | 错误消息包路径，可接受 | OK |
| `registry.go:102-110` ListTemplates | 性能 | 每次调用都深拷贝整个 templates 列表 + 每个 template 的 Tags/MatchWhen | hot path 上每次 list 都做 O(N×M) 拷贝；如果 N=100, M(tags)=10, 每次 list 都额外分配 ~1KB | 加只读视图 view 接口；或返回不可变包装 |
| `registry.go:122-130` SectionsByTemplateID | 性能 | 同上深拷贝 EnableWhen | 同上 | 同上 |
| `load.go:91-93` loadSections | 静默 | `strings.TrimSpace(string(data))` ——body 为空时不报错（validate.go:216-218 有 trim 后空检查，但 normalize 阶段先 trim 了） | trim 后空与 trim 前全空白：当前逻辑等价（都被 validateLoadedSections 拒绝），无问题 | OK |
| `validate.go:113-124` validateTemplateEnums | 弱契约 | line 120-122 「default_rule kind 必须 default_rule agent_key」 是隐式 invariant | 类型与 agent_key 的耦合不在 schema 体现 | schema 加 oneOf 子类型 |
| `validate.go:103` `cfg.Enabled == nil` | 强契约 | enabled 必须显式 true/false | 这是良好的 fail-fast：避免「未指定」歧义 | **正面案例** |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `load.go:20-26` NewDefaultRegistry | 启动期一次性，加载所有 prompt template；如果 templates 数量大（>100）+ 每个有大 prompt body | 加 startup duration 日志（"loaded N templates in M ms"） |
| `validate.go:226-296` containsExternalProviderIdentity | 每个 section body 跑一次 normalize + 6 phrase 匹配 + 8 negation 匹配 | body 大时（>10KB）累积；正则 / 子串扫描 O(BodySize × Patterns) |
| `validate.go:240-253` phraseMatchesIdentityPattern | 滚动子串扫描，每次 negation 检查 O(N×M) | 长 body + 多 negation 检查累积；改 Aho-Corasick 多模式匹配 |
| `registry.go:102-110` ListTemplates | 已是同步内存操作；深拷贝是主要成本 | 加 ListTemplates 调用计数器 + duration 监控 |
| `registry.go:35-49` loadedTemplateLess | sort 比较函数 + 多 nil-check 分支 | sort.SliceStable O(N log N)；启动期一次性，可忽略 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `validate.go:226-238` | 外部品牌 identity 仅 6 个硬编码 phrase 检测，新品牌静默漏过 |
| `validate.go:255-272` | hasDirectIdentityNegation 滑窗式匹配但语境窗口固定 |
| `registry.go:113-115` GetTemplate | promptKey 不存在时返 zero + false（合理；但 caller 是否区分「不存在」vs「被禁用」） |
| `registry.go:150-155` boolValue | nil pointer 静默返 fallback（命名 boolValue 暗示这是 helper，可接受） |
| `load.go:104-121` normalizeTemplate | 全部 string 字段无条件 trim；空字符串变更后不区分原值 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `schema.go:14-17` manifestConfig | Version 为 int 但只接受 1（validate.go:58-60）；未来扩展靠版本号迁移 |
| `schema.go:19-35` templateConfig | 16 个字段，部分是 *T pointer 表示可选（ID/Enabled）部分是值类型表示必填——靠约定 |
| `validate.go:120-122` default_rule 耦合 | kind 与 agent_key 的隐式约束 |
| `validate.go:32-39` externalIdentityPatterns | 6 个硬编码品牌 phrase；扩展需修改代码 |
| `registry.go:51-56` builtinTemplateID | ID 派生策略两种（cfg.ID 或 fallback）|
| `assets.go:5-6` embed | 资源在编译期固定；运行时 hot-reload 不可能 |

## 修复优先级

### P0（必须本周修）
1. **`load.go:28-45, 83-96` 加载失败立即返回**——这是 builtin prompt 配置错误的报告路径。当多个 template 都有错时，运维需要反复启动才能发现所有错。改为 `errors.Join` 一次性收集。这显著降低运维成本（启动失败次数从 N 降到 1）。
2. **`validate.go:226-272` identity 检测的安全语义**——当前实现有「best-effort」性质但没有显式文档说明这一点。如果其他模块（如 prompt 编辑 UI）依赖此检测做安全决策（拒绝包含外部品牌的 prompt），会有漏判风险。改：添加包级注释「This is best-effort prevention, not a security boundary」。

### P1（本月）
3. `registry.go:102-110` ListTemplates 改为只读视图（性能 + 内存）
4. `load.go:104-121` normalizeTemplate 后空字符串字段 vs 原值无意义——validateTemplateRequired 已拒绝空字符串，所以 OK；但建议加注释说明 normalize 不改变后续 validate 行为
5. `validate.go:32-39` externalIdentityPatterns 改为可配置（环境变量或 JSON）让无需 rebuild 即可扩展
6. `validate.go:240-253` phraseMatchesIdentityPattern 改 Aho-Corasick

### P2（下个 sprint）
7. `schema.go:19-35` templateConfig 引入 schema-level 类型约束
8. `assets.go:5-6` 加入 hot-reload 支持（开发模式下从磁盘读取）
9. `validate.go:113-124` validateTemplateEnums 用 oneOf 表达 default_rule 子类型

## 边界条件

1. **`validate.go:103` `cfg.Enabled == nil` 必填校验是项目内 fail-fast 正面案例**：这是「显式 nil-pointer 区分未设置 vs false」的良好实践——避免 bool 零值默认引发的歧义。同样的模式建议推广到 `cfg.ID`、`cfg.Priority` 等其他「未设置」具有特殊语义的字段。
2. **`validate.go:226-272` identity 检测的设计哲学**：阻止 builtin prompt 中包含「You are Claude」式语句——这是为了避免内置 prompt 在多 LLM provider 之间漂移（系统不应硬编码身份）。这是合理的多 provider 架构防御。但 negation 检测（"never say you are claude"）的实现复杂——如果用 LLM 来判断会更准确（但代价是启动期耗时）。当前 best-effort 静态检测合理，应在文档中明确「not a security boundary」。
3. **`registry.go:102-110, 122-130` 深拷贝设计取舍**：每次 ListTemplates / SectionsByTemplateID 都深拷贝。这是为了 thread safety + caller 可以修改返回值不污染 registry 内部状态。但成本高。Go 风格通常用「不可变 view」或「文档约定 caller 不应修改」。当前选择保守的深拷贝是 thread-safe-by-default 的良好实践，但应在性能监控后决定是否优化。
4. **`load.go:20-26` embed.FS 启动期一次性加载**：所有 builtin prompt template 在二进制构建时打包，启动时全部加载到内存。这是合理的 self-contained 设计——避免运行时缺文件错误。但 hot-reload 不可能（开发期改 prompt 需 rebuild）。建议开发模式 fallback 到磁盘读取（环境变量控制）。
5. **`validate.go:190-212` validateLoadedTemplates 的二轮 ID 校验**：line 201-209 sort + dedup 校验确保 builtinTemplateID 的派生 ID 不冲突。这是 fail-fast 的良好设计——在 newRegistry 之前就确认 ID 唯一性。**正面案例**。但派生 ID 公式 `firstBuiltinID - index` 依赖 stable sort 顺序——若两次启动文件系统枚举顺序不同（Linux 通常稳定，但 Windows、macOS 可能不同），ID 可能漂移。建议 ID 派生加 hash(promptKey) 而非依赖 sort 顺序。
6. **`assets.go:5-6` `//go:embed assets/**` 通配符**：Go 1.16+ embed 支持双星号匹配子目录。这是 Go 标准用法，但**审查应验证 assets/ 目录结构**——如果 manifest.json 不在 assets/ 直接子目录，加载会失败。本轮未读 manifest.json 实际内容，建议下轮覆盖 assets/manifest.json + 至少一个 template 文件。

---

**本轮总结**：发现 2 个 P0 问题：①load 错误立即返导致多错难一次定位；②identity 检测的「best-effort」性质应在文档明确避免被误用为安全边界。`validate.go:103` `cfg.Enabled == nil` 必填是 fail-fast 正面案例。`validateLoadedTemplates` 的二轮 ID 校验是良好的预 newRegistry 防御。深拷贝是 thread-safe 但应监控性能成本。

**累计进度**：44 轮完成。cron `fd4b4728` 继续推进。
