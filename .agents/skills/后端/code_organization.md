# Go 代码组织与 DRY 模式 (V3 规范)

> **加载条件**: 文件拆分/合并、消除重复代码、接口设计、`fx` 模块内聚、零值可用时加载。

---

## 文件与 owner 边界

| 规则 | 阈值 | 违反时行动 |
|------|------|-----------|
| 文件边界 | 不设机械行数下限 | 仅合并无独立 owner、契约或测试价值的碎片包装；上限与复杂度以当前`make guard`为准。 |
| owner | 一个事实只允许一个可写 owner | adapter、mirror、生成物和展示模型不得反向成为事实源。 |
| 接口隔离 | 默认保持实现私有 | 只有真实调用方需要具体类型时才扩大公开面。 |
| 运行时状态 | 构造函数/Fx 注入 | 禁止用包级 map、service locator 或可变注册表逃避依赖关系。 |

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

type WorkspaceWriter interface {
    Insert(ctx context.Context, workspace Workspace) (Workspace, error)
}

// ✅ 内部私有实现
type workspaceService struct {
    writer WorkspaceWriter
}

// ✅ 构造函数返回接口，加入 fx.Provide
func newWorkspaceService(writer WorkspaceWriter) WorkspaceService {
    return &workspaceService{writer: writer}
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
    if err != nil {
        d.log.Error("workspace creation failed", logger.Any(logger.FieldError, err))
        return "", err
    }
    d.log.Info("Workspace created", logger.String("id", res))
    return res, nil
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
    Writer WorkspaceWriter
    Bus    *event.Dispatcher
    Config *config.Config
    Logger *logger.Logger
}

func newService(deps ServiceDeps) Service {
    return &service{
        writer: deps.Writer,
        bus:    deps.Bus,
        config: deps.Config,
        logger: deps.Logger,
    }
}
```

### Options 模式

Options 只能调整已经显式提供并验证的配置，不能用静默默认值补齐必填配置。

```go
type ClientConfig struct {
    Timeout time.Duration
}

type ClientOption func(*Client) error

func WithTimeout(d time.Duration) ClientOption {
    return func(c *Client) error {
        if d <= 0 {
            return errors.New("client timeout must be positive")
        }
        c.timeout = d
        return nil
    }
}

func NewClient(cfg ClientConfig, opts ...ClientOption) (*Client, error) {
    if cfg.Timeout <= 0 {
        return nil, errors.New("client timeout is required")
    }
    c := &Client{timeout: cfg.Timeout}
    for _, opt := range opts {
        if opt == nil {
            return nil, errors.New("client option is nil")
        }
        if err := opt(c); err != nil {
            return nil, err
        }
    }
    return c, nil
}
```

---

## 零值可用

只有零值语义明确且安全的基础类型才设计为零值可用。配置、身份、owner、持久化依赖和生命周期组件不得用零值代表“采用默认值”或“稍后补齐”。

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
