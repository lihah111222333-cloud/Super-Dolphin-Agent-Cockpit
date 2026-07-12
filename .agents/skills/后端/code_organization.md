# Go 代码组织与 DRY 模式 (V3 规范)

> **加载条件**: 文件拆分/合并、消除重复代码、接口设计、`fx` 模块内聚、零值可用时加载。

---

## 文件组织强制规则

| 规则 | 阈值 | 违反时行动 |
|------|------|-----------|
| 禁止 `_chains.go` 后缀 | 0 容忍 | 合并到父文件 |
| 文件名下划线层数 | ≤3 层 | 重新归类 |
| 文件边界 | 不设机械行数下限 | 仅合并无独立 owner、契约或测试价值的碎片包装 |
| 接口隔离原则 | 服务实现必须私有化 | `type service struct`，不可导出 |

---

## 接口设计 (V3 模块化核心)

- 保持小型 (1-3 方法)
- 接口优先在使用方定义；跨模块稳定端口才公开接口
- 模块公开面由真实调用方决定，不要求机械地“只暴露 Interface”

```go
// ✅ 领域公开接口
type WorkspaceService interface {
    Create(ctx context.Context, req CreateReq) (string, error)
}

// ✅ 内部私有实现
type workspaceService struct {
    store Store
}

// ✅ 构造函数返回接口，加入 fx.Provide
func newWorkspaceService(store Store) WorkspaceService {
    return &workspaceService{store: store}
}
```

---

## DRY 原则与 V3 中间件/装饰器模式

检查 DRY 前先确认语义、权限、错误分类和层级边界；只有规则相同且共享不会跨层穿透时才抽取。

**重复检测信号**:
| 信号 | 行动 (V3 方案) |
|------|------|
| 3+ 处代码结构相似 | 提取内部工厂函数 |
| 多个 RPC Handler 都有鉴权逻辑 | 提取 jrpc2 中间件 (Middleware) |
| 服务需要统一的日志/埋点 | 使用装饰器模式 (Decorator) 包装 Service |
| 构造函数参数 >5 个 | 使用 `fx.In` 或 Options 模式 |

### 装饰器模式 (Decorator)

如果每个服务方法都需要记录审计日志，不要在每个方法里重复写，使用装饰器。

```go
// 装饰器结构
type auditDecorator struct {
    next WorkspaceService
    log  *logger.Logger
}

func (d *auditDecorator) Create(ctx context.Context, req CreateReq) (string, error) {
    d.log.Info("Creating workspace...")
    res, err := d.next.Create(ctx, req)
    d.log.Info("Workspace created", logger.String("id", res))
    return res, err
}

// 在 fx 中使用 fx.Decorate 替换实现
var Module = fx.Module("workspace",
    fx.Provide(newWorkspaceService),
    fx.Decorate(func(log *logger.Logger, next WorkspaceService) WorkspaceService {
        return &auditDecorator{next: next, log: log}
    }),
)
```

### fx.In 参数收敛

当依赖项过多时，使用 `fx.In` 结构体进行聚合，避免构造函数过长。

```go
type ServiceDeps struct {
    fx.In
    Store  Store
    Bus    *event.Dispatcher
    Config *config.Config
    Logger *logger.Logger
}

func newService(deps ServiceDeps) Service {
    return &service{
        store:  deps.Store,
        bus:    deps.Bus,
        config: deps.Config,
        logger: deps.Logger,
    }
}
```

### Options 模式

对于基础组件或无需 DI 容器管理的类型，依然推荐 Options 模式。

```go
type ClientOption func(*Client)

func WithTimeout(d time.Duration) ClientOption {
    return func(s *Client) { s.timeout = d }
}

func NewClient(opts ...ClientOption) *Client {
    c := &Client{timeout: 30 * time.Second}
    for _, opt := range opts {
        opt(c)
    }
    return c
}
```

---

## 零值可用

设计基础类型使其零值可直接使用，无需构造函数:

```go
type Buffer struct {
    buf []byte
}

func (b *Buffer) Write(p []byte) (int, error) {
    b.buf = append(b.buf, p...)  // nil slice 也能 append
    return len(p), nil
}

// 无需 NewBuffer() 即可使用
var buf Buffer
buf.Write([]byte("hello"))
```
