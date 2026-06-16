# P18 Phase 0：基础设施

> 预计：1 天 | 依赖：无

## 目标
创建 memory 和 prompt 模块骨架，注册到 fx 生命周期。

## 模块职责全景

| 模块 | 责任 | 被谁消费 |
|------|------|---------|
| `internal/module/memory` | 磁盘 CRUD、`MEMORY.md` 索引、路径安全、截断 | Phase 1 / 5 / 6；Phase 7 只能经 `internal/contract` + `cmd/mcp-orch` 本地实现接入，禁止 `cmd/mcp-orch` 直接 import 本模块 |
| `internal/module/prompt` | PromptRegistry、PromptContext、PromptAssemblyService 的实现、缓存失效 | Phase 2 / 3 / 4 / 4.5 / 8；凡跨 provider / MCP binary 的接口上提 `internal/contract/*`，载荷上提 `internal/dto/*` |
| `internal/module/thread` | `start_session` / `turn` 注入入口，只消费 prompt 产物，不承担组装逻辑 | Phase 4 / 4.5 |
| `internal/sidecar/orch/tools` | `memory_read` 工具的 schema/handler 壳、slash command 配套边界与迁移脚本入口 | Phase 7；业务实现必须留在 `cmd/mcp-orch/*` 本地包或 `internal/contract/*`，禁止直连 `internal/module/*` |

## 与后续 Phase 的关系

- Phase 1 在 `memory` 模块内补齐磁盘存储主链
- Phase 2/3 在 `prompt` 模块内补齐规则与 section registry
- Phase 4.5 先解耦 thread/provider 语义，再由 Phase 4 接入 provider
- Phase 8 负责把以上模块串成回归与守护闭环

## fx 装配与第一落点

- 根装配层实际以 `internal/app/modules.go` 的 `app.Module = fx.Options(...)` 聚合；新增业务子模块仍沿用 `internal/module/*/module.go` 暴露 `var Module = fx.Module(...)`
- Phase 0 至少落以下骨架：`internal/module/memory/module.go`、`internal/module/memory/contract.go`、`internal/module/prompt/module.go`、`internal/module/prompt/contract.go`、`internal/module/prompt/config.go`、`internal/module/prompt/buildctx.go`、`internal/module/prompt/types.go`、`internal/module/prompt/service.go`（可配套 `registry.go` / `root.go` 等辅助骨架）
- 桌面侧 `memory.Config` + `prompt.Config` 在 Phase 0 先作为 DI 占位暴露；若 Phase 7 需要 `cmd/mcp-orch` 读取同类开关，必须从 `internal/platform/config` 或 `cmd/mcp-orch` 本地 config adapter 暴露，禁止为读 flag 反向 import `internal/module/{memory,prompt}`
- 仓库已存在 `internal/store/prompt`（prompt template store）；新增 `internal/module/prompt` 表示 system prompt assembly，文档和代码引用时必须显式区分：凡 import `internal/store/prompt` 必须写成 `promptstore ".../internal/store/prompt"`，`internal/module/prompt` 保持 `prompt` 包名；该约定是 code review 硬性检查项，并且要在 Phase 8 增加 import 规范守护测试
- `BuildCtx` 只承载 prompt 计算所需只读上下文，不直接持有 provider DTO；最少应预留 `cwd/gitRoot/language/provider/model/enabledTools/additionalWorkingDirectories/mcpSnapshot/session flags`
- `internal/module/*` 自身 outward interface 按 `contract.go` 落位；若接口会被 `internal/provider/*` 或 `cmd/mcp-*` 消费，则必须额外抽到 `internal/contract/*`，相关数据载荷抽到 `internal/dto/*`
- 模块注册顺序按依赖方向固定：`memory.Module → prompt.Module → thread/turn/provider consumers`
- `~/.multi-agent/memory/` 目录初始化放在 `fx.Invoke + Lifecycle.OnStart`，不要在 constructor 中做副作用；目录初始化 owner 统一为 `memory.Service.EnsureRoot`，如保留 `RootManager` 也只能委托 `Service.EnsureRoot`，不得再拥有独立目录初始化实现，避免双 owner 长期并存

## 任务清单
- [ ] 创建 `internal/module/memory/` 模块目录
- [ ] 创建 `internal/module/prompt/` 模块目录
- [ ] 为 memory / prompt 补齐 `contract.go` outward interface 骨架
- [ ] 定义核心类型（见下方）
- [ ] 定义 `memory.Config` / `prompt.Config` / `BuildCtx` 骨架
- [ ] fx.Module 注册到应用
- [ ] 创建 `~/.multi-agent/memory/` 目录管理工具

## 核心类型

> 注：以下类型是 **V3 Go 抽象**，不是对 Claude 源码的直接映射。
> Claude 源码中没有等价的 MemoryEntry/PromptSection 结构体，
> 只有 frontmatter 格式（memoryTypes.ts）和 section 注册模型（systemPromptSections.ts: name/compute/cacheBreak）。

### MemoryType + MemoryEntry
```go
type MemoryType string
const (
    MemoryTypeUser      MemoryType = "user"
    MemoryTypeFeedback  MemoryType = "feedback"
    MemoryTypeProject   MemoryType = "project"
    MemoryTypeReference MemoryType = "reference"
)

// MemoryType 表示语义分类，不表示 scope；scope/namespace 需独立建模。
type MemoryScope string

// 持久化 frontmatter
type MemoryFrontmatter struct {
    Name        string      `yaml:"name"`
    Description string      `yaml:"description"`
    Type        *MemoryType `yaml:"type,omitempty"`
}

// MemoryEntry 是运行时表示，不是磁盘格式
// 磁盘格式是 YAML frontmatter (name/description/type) + markdown body
type MemoryEntry struct {
    Frontmatter MemoryFrontmatter `yaml:",inline"`
    Content     string            `yaml:"-"`
    FilePath    string            `yaml:"-"`
    UpdatedAt   time.Time         `yaml:"-"`
}
```

> **审查修订**（Agent 2/20）：明确区分 frontmatter 持久化字段与运行时元数据；`MemoryType` 需支持 legacy/unknown 降级，缺失/非法 type 不直接 hard fail。

### PromptSection
```go
type PromptSection struct {
    Name     string       // 唯一缓存键
    Order    int
    Region   PromptRegion // Static | Dynamic
    Volatile bool         // true = 每轮重算（DANGEROUS）
    Compute  func(ctx context.Context, b BuildCtx) (*string, error)
}

type PromptRegion int
const (
    PromptRegionStatic PromptRegion = iota
    PromptRegionDynamic
)
```

补充契约：
- `Name` 是 section 的唯一缓存身份；`nil/null` 结果也可缓存
- prompt 模块必须暴露统一失效入口（如 `InvalidateAll(reason)` / generation bump）
- 静态 section 不读取 runtime/provider-specific 状态；runtime bits 必须留在动态 section

> **审查修订**（Agent 9/20）：用 Region + Volatile 替代 Static bool，并把 context/error、缓存键、失效语义前置固化。

## 源码参考
- `restored-src/src/memdir/memoryTypes.ts:14-31` — MemoryType 定义 + parseMemoryType
- `restored-src/src/constants/systemPromptSections.ts:20-38` — section 注册模型

## 验收
- `./scripts/go_with_guard.sh build ./internal/module/memory/... ./internal/module/prompt/...` 通过
- `./scripts/go_with_guard.sh test ./internal/module/prompt` 通过
- `./scripts/go_with_guard.sh test ./internal/module/memory` 通过，且不得残留旧版常量/构造签名测试（如 `MemoryRootEnv` / `DefaultMaxLines` / 无参 `NewConfig()`）
- `fx.ValidateApp(app.Module)` 通过
- fx 应用启动 smoke test 不报错
