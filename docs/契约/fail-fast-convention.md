# Fail-Fast 契约与禁止兜底代码规范 (Fail-Fast & No-Fallback Code Policy)

## 1. 核心理念

在 `super-agent-v3` 系统中，我们严格遵守 **Fail-Fast (快速失败 / 阻断式报错)** 契约。
系统的可靠性来源于确定性，而隐式的兜底、容错和静默降级是引入非确定性行为和“隐形 Bug”的最大温床。

### 绝对准则：禁止兜底代码
> 遇到异常、配置为空或数据缺失时，必须立即报错并阻断（Fail-Fast）。
> 严禁使用包括但不限于静默降级、默认配置、吞错捕获等隐式兜底逻辑。

所有服务、模块与底层平台在面对非预期输入、缺失配置、或者下游异常时，必须让问题在发生的最早阶段暴露出来（Fail-Fast），而不是尝试“自作聪明”地帮系统恢复到一个含糊的“默认状态”并继续运行。

---

## 2. 规范细则

为将这一核心理念落到实处，系统在编码中遵循以下四项核心禁止规范与四项强制要求：

### 2.1 四大禁用行为 (The Four Bans)

| 禁用行为 | 定义 | 危害 | 替代方案 |
| :--- | :--- | :--- | :--- |
| **隐式默认配置** | 在配置项未填、为空或文件加载失败时，隐式地采用一个写死的“兜底值”或“开发测试用默认值”继续运行。 | 导致生产环境因配置未生效而走入测试状态，配置错误难以被发现。 | **Fail-Fast 阻断**：启动时检测到关键配置为空，立即报 panic 并退出进程。 |
| **静默降级** | 当依赖的第三方 API、底层服务或下游 MCP 工具调用失败时，返回一个空对象、空数组或假数据，试图让上层业务“继续跑”。 | 掩盖依赖链断裂的真相，导致核心业务流程在不知情的情况下输出错误结果。 | **向上抛出 Error**：完整保留原始调用链上下文信息，向上阻断，由最高层决定如何给用户交互式的错误反馈。 |
| **吞错捕获** | 使用空的 `recover`、忽略 `err` 返回值、或者捕获异常后仅记录一条 info/debug 日志，随后继续执行。 | 堆栈上下文在出错点断裂，后续逻辑继续使用不完整的内存状态，引发更严重的二次空指针异常。 | **显式传播或 Panic**：在初始化/生命周期阶段直接 `panic`；在运行时流程中将错误附加上下文后 `return err`。 |
| **隐式时间与状态补全** | 当系统时间缺失、字段为空或内存状态不一致时，自动用 `time.Now()` 或初始状态隐式补充后继续。 | 导致数据流中携带错误的上下文，干扰时间敏感性逻辑，使问题无法回溯。 | **拦截校验 (Validate)**：必须前置执行参数和状态校验，缺则直接拦截报错。 |

### 2.2 四大强制要求 (The Four Mandatories)

1.  **启动时强校验 (Validate at Boot)**:
    所有 `fx.Provide` 构造出的 Factory 或 Component，在其构造函数 (constructor) 中必须首先对入参（Config、依赖）进行前置强校验。一旦发现缺失，必须直接返回 `error` 阻断 `fx` 的依赖装配。
2.  **错误传播链条完整性 (Complete Error Propagation)**:
    在抛出和返回错误时，绝不允许丢弃底层的 Root Cause。必须使用 `fmt.Errorf("context message: %w", err)` 或包装库对原始错误进行包裹，确保堆栈与上下文中携带故障点的完整排查证据。
3.  **Fail-Fast 接口签名 (Signatures that Fail)**:
    所有可能失败的函数、内部组件方法、以及跨模块方法调用，其返回值中必须包含 `error`。严禁为了“写代码省事”而使用单返回值并静默吞掉报错。
4.  **同提交锁定测试 (Same-Commit Regression Test)**:
    任何针对异常阻断、边界校验或 Bug 修复的改动，必须在同一个 commit 中包含对应的单元测试或集成测试，通过提供无效配置/触发异常来锁定 Fail-Fast 的行为，防止后续开发中被再次重构为“隐式兜底”。

---

## 3. 典型代码示例

### 3.1 场景一：构造函数与配置校验

#### ❌ 反模式：隐式兜底 (Bad Code)
```go
type LLMConfig struct {
	ModelName string // 未填时自动隐式降级为 "gpt-4o"
	Timeout   int    // 未填时自动兜底为 30
}

func NewLLMClient(cfg *LLMConfig) *LLMClient {
	model := cfg.ModelName
	if model == "" {
		// 隐式兜底！生产环境可能会因为配置加载失败而走入默认模型，导致账单暴涨或能力不匹配
		model = "gpt-4o"
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30
	}
	return &LLMClient{
		model:   model,
		timeout: timeout,
	}
}
```

####  契约模式：Fail-Fast 阻断 (Good Code)
```go
type LLMConfig struct {
	ModelName string
	Timeout   int
}

func NewLLMClient(cfg *LLMConfig) (*LLMClient, error) {
	// Fail-Fast: 立即校验配置
	if cfg == nil {
		return nil, errors.New("llm config is nil")
	}
	if cfg.ModelName == "" {
		// 阻断装配！缺失模型名称配置属于致命异常，必须报错迫使运维/开发者修复
		return nil, errors.New("llm config invalid: ModelName must be specified")
	}
	if cfg.Timeout <= 0 {
		return nil, fmt.Errorf("llm config invalid: Timeout must be positive, got %d", cfg.Timeout)
	}

	return &LLMClient{
		model:   cfg.ModelName,
		timeout: cfg.Timeout,
	}, nil
}
```

---

### 3.2 场景二：外部组件/第三方调用

#### ❌ 反模式：静默降级与吞错 (Bad Code)
```go
func (s *MemoryService) GetUserContext(ctx context.Context, userID string) *UserContext {
	contextData, err := s.redisStore.Get(ctx, userID)
	if err != nil {
		// 吞错捕获！仅打印一条日志，随后返回空对象
		s.logger.Warn("failed to fetch user context from redis, returning empty default context", "err", err)
		return &UserContext{UserID: userID} // 隐式兜底：使上层完全感知不到存储故障
	}
	return contextData
}
```

####  契约模式：异常向上抛出并阻断 (Good Code)
```go
func (s *MemoryService) GetUserContext(ctx context.Context, userID string) (*UserContext, error) {
	contextData, err := s.redisStore.Get(ctx, userID)
	if err != nil {
		// 保留原始 Root Cause，添加业务上下文后向上层链路显式阻断
		return nil, fmt.Errorf("failed to fetch user context for user %s: %w", userID, err)
	}
	
	if contextData == nil {
		// 区分“存储故障”与“业务上不存在此用户”，如果该处空指针属于非预期，同样需要快速失败
		return nil, fmt.Errorf("user context not found for user %s", userID)
	}
	
	return contextData, nil
}
```

---

### 3.3 场景三：下游多组件组装与 Fail-Fast 运行

#### ❌ 反模式：降级运行 (Bad Code)
```go
func (e *Engine) RunTools(ctx context.Context, toolNames []string) {
	for _, name := range toolNames {
		tool, err := e.registry.Get(name)
		if err != nil {
			// 隐式兜底：找不到这个 tool 就直接忽略跳过，可能导致核心推理链残缺而输出无效回答
			continue 
		}
		_ = tool.Execute(ctx) // 吞掉执行错误
	}
}
```

####  契约模式：快速失败，强力阻断 (Good Code)
```go
func (e *Engine) RunTools(ctx context.Context, toolNames []string) error {
	for _, name := range toolNames {
		tool, err := e.registry.Get(name)
		if err != nil {
			// 下游组件缺失，立刻返回错误，阻断整个引擎执行流
			return fmt.Errorf("aborted tool execution chain: tool %s not found in registry: %w", name, err)
		}
		if err := tool.Execute(ctx); err != nil {
			// 下游执行失败，立刻抛出，绝不静默跳过
			return fmt.Errorf("failed to execute tool %s: %w", name, err)
		}
	}
	return nil
}
```

---

## 4. 架构守卫与自动化保障

我们通过以下手段确立和维护 Fail-Fast 的神圣性：
1.  **架构静态扫描**: 在 `internal/archtest` 中加入代码尺寸与错误返回审计守卫，强力约束导出方法的错误签名。
2.  **单元测试拦截**: 在单元测试中，所有由于“配置为空”、“数据缺失”抛出的 `error` 均需在边界测试中覆盖，以此确保没有静默逻辑能穿透到 CI。
3.  **零隐式降级政策**: 任何评审人员在 Code Review 中若发现 `if err != nil { logger.Warn(...); return nil }` 或 `cfg.Model = GetEnv("MODEL", "default")` 这类非全局共享基础库的业务配置隐式降级代码，**必须一票否决**。

---

## 5. 常见问题解答 (FAQ)

**Q: 彻底禁用兜底逻辑，会不会导致系统非常脆弱，动不动就因为一点配置错误而起不来？**
> **A:** 这正是我们的目的。我们情愿让一个配置错误的系统在容器启动阶段 (Validate at Boot) 立即挂掉崩溃（Crash Loop BackOff），让运维和开发人员立刻发现并修正；也不愿意让一个带着“默认测试模型”、“默认超时配置”或“数据库静默降级”的系统带着暗病在生产环境里苟延残喘，制造出难以回溯的静默脏数据或高昂的意料外账单。

**Q: 那如果是类似用户输入时的拼写容错呢？这算兜底吗？**
> **A:** 这不属于系统设计的“隐式兜底”。用户的交互容错属于 **显式业务需求**，需要在领域层有清晰定义的模糊搜索逻辑、解析逻辑及对应的异常类型返回值。
> 我们禁止的是**开发层面在遇到非预期系统状态（如依赖宕机、系统配置为空、内部数据结构缺失）时，程序员自行决定的静默降级和吞错逻辑**。两者有着本质的区别。
