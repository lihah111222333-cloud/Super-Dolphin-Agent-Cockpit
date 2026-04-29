# Effective Go 核心规则

> 每条规则均链接到官方文档对应章节，需要详细说明时点击锚点。

---

## 格式化 (不可协商)

> 📚 [官方: Formatting](https://go.dev/doc/effective_go#formatting)

| 规则 | 说明 |
|------|------|
| `gofmt` | 必须使用，不可协商 |
| `goimports` | 推荐使用，自动管理导入 |
| 行长 | 最大 120 字符 |
| 缩进 | Tab，非空格 |

---

## 命名规范

> 📚 [官方: Names](https://go.dev/doc/effective_go#names) | [Code Review: MixedCaps](https://github.com/golang/go/wiki/CodeReviewComments#mixed-caps)

| 类型 | 规则 | 示例 |
|------|------|------|
| 导出名 | MixedCaps (大驼峰) | `UserService` |
| 非导出名 | mixedCaps (小驼峰) | `userService` |
| 包名 | 小写单词，无下划线 | `httputil` |
| 接口名 | 单方法用 `-er` 后缀 | `Reader`, `Writer` |
| 禁止 | 下划线命名 | ~~`user_name`~~ |

---

## 错误处理

> 📚 [官方: Errors](https://go.dev/doc/effective_go#errors) | [Code Review: Error Strings](https://github.com/golang/go/wiki/CodeReviewComments#error-strings)

| 规则 | 说明 |
|------|------|
| 必须检查 | 禁止 `_ = err` 忽略错误 |
| 包装错误 | 使用 `fmt.Errorf("context: %w", err)` |
| 错误字符串 | 小写开头，无标点结尾 |
| 哨兵错误 | 使用 `errors.Is()` 检查 |

```go
// ✅ 正确
if err != nil {
    return fmt.Errorf("create user %s: %w", name, err)
}

// ❌ 错误
if err != nil {
    return err  // 无上下文
}
```

---

## 接口设计

> 📚 [官方: Interfaces](https://go.dev/doc/effective_go#interfaces_and_types) | [Code Review: Interfaces](https://github.com/golang/go/wiki/CodeReviewComments#interfaces)

| 规则 | 说明 |
|------|------|
| 保持小型 | 1-3 个方法为宜 |
| 接受接口 | 函数参数使用接口 |
| 返回具体类型 | 函数返回值使用具体类型 |
| 消费者定义 | 接口由使用方定义，非实现方 |

```go
// ✅ 小接口
type Reader interface {
    Read(p []byte) (n int, err error)
}

// ❌ 过大接口
type UserManager interface {
    Create, Update, Delete, Find, List... // 太多方法
}
```

---

## 并发

> 📚 [官方: Concurrency](https://go.dev/doc/effective_go#concurrency) | [官方: Share by communicating](https://go.dev/doc/effective_go#sharing)

| 规则 | 说明 |
|------|------|
| 通信优先 | Channel 优先于 Mutex |
| 共享原则 | 通过通信共享内存，非通过共享内存通信 |
| Goroutine 安全 | 确保可退出，使用 context 取消 |

```go
// ✅ 通过 Channel 通信
ch <- data

// ⚠️ 必要时才用 Mutex
mu.Lock()
defer mu.Unlock()
```

---

## 文档注释

> 📚 [官方: Commentary](https://go.dev/doc/effective_go#commentary) | [Code Review: Doc Comments](https://github.com/golang/go/wiki/CodeReviewComments#doc-comments)

| 规则 | 说明 |
|------|------|
| 以符号名开头 | `// User represents...` |
| 所有导出符号 | 必须有注释 |
| 包注释 | 在 `package` 语句上方 |

```go
// User represents a registered user in the system.
type User struct { ... }

// NewUser creates a user with the given email.
func NewUser(email string) (*User, error) { ... }
```

---

## 控制结构

> 📚 [官方: Control structures](https://go.dev/doc/effective_go#control-structures)

| 规则 | 说明 |
|------|------|
| Happy Path | 成功路径向下流动，错误立即返回 |
| if 初始化 | 善用 `if err := fn(); err != nil` |
| 避免 else | 错误分支先返回，减少嵌套 |

```go
// ✅ Happy Path
if err != nil {
    return err
}
// 继续正常逻辑

// ❌ 嵌套过深
if err == nil {
    if valid {
        // ...
    }
}
```

---

## 零值可用

> 📚 [官方: The zero value](https://go.dev/doc/effective_go#allocation_new)

| 规则 | 说明 |
|------|------|
| 设计零值可用 | 类型零值应能直接使用 |
| 无需构造函数 | `var buf bytes.Buffer` 可直接使用 |

```go
// ✅ 零值可用
var buf bytes.Buffer
buf.Write([]byte("hello"))  // 无需 NewBuffer()
```

---

**文档版本**: 1.0.0  
**基于**: Go 1.27+ / Effective Go 2024
