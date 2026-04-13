# P18 Phase 0：基础设施

> 预计：1 天 | 依赖：无

## 目标
创建 memory 和 prompt 模块骨架，注册到 fx 生命周期。

## 任务清单
- [ ] 创建 `internal/module/memory/` 模块目录
- [ ] 创建 `internal/module/prompt/` 模块目录
- [ ] 定义核心类型（见下方）
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

// MemoryEntry 是运行时表示，不是磁盘格式
// 磁盘格式是 YAML frontmatter (name/description/type) + markdown body
type MemoryEntry struct {
    // --- frontmatter 字段（持久化） ---
    Name        string     `yaml:"name"`
    Description string     `yaml:"description"`
    Type        MemoryType `yaml:"type"`
    // --- 运行时字段（不持久化） ---
    Content     string     `yaml:"-"`
    FilePath    string     `yaml:"-"`
    UpdatedAt   time.Time  `yaml:"-"`
}
```

> **审查修订**（Agent 2）：明确区分 frontmatter 持久化字段与运行时元数据。
> MemoryType 需支持 legacy/unknown 降级：`parseMemoryType()` 对无法识别的 type 应返回空而不是报错。

### PromptSection
```go
type PromptSection struct {
    Name      string
    Order     int
    Region    PromptRegion // Static | Dynamic
    Volatile  bool         // true = 每轮重算（DANGEROUS）
    Compute   func(ctx *BuildCtx) *string
}

type PromptRegion int
const (
    PromptRegionStatic  PromptRegion = iota
    PromptRegionDynamic
)
```

> **审查修订**（Agent 9）：用 Region + Volatile 替代 Static bool，避免混淆两层缓存。

## 源码参考
- `restored-src/src/memdir/memoryTypes.ts:14-31` — MemoryType 定义 + parseMemoryType
- `restored-src/src/constants/systemPromptSections.ts:20-38` — section 注册模型

## 验收
- `go build ./internal/module/memory/... ./internal/module/prompt/...` 通过
- fx 应用启动不报错
