# Go 命名、格式化与导入规范

> **加载条件**: 命名变量/函数/包/接口/常量、格式化代码、组织导入时加载。

---

## 格式化

MUST 使用 `gofmt`，不可协商。推荐 `goimports` (自动管理导入)。

```bash
gofmt -w path/to/file.go
goimports -w path/to/file.go
```

最大行长: **120 字符**。在逻辑点断行:

```go
func ProcessOrderWithValidation(
    ctx context.Context,
    order *Order,
    validator OrderValidator,
) (*Result, error) {
    return nil, fmt.Errorf(
        "process order %s for customer %s: %w",
        order.ID,
        order.CustomerID,
        err,
    )
}
```

---

## Happy Path 编码

成功路径垂直向下，错误立即返回:

```go
// ✅ 正确
func ProcessUser(id string) (*User, error) {
    user, err := db.GetUser(id)
    if err != nil {
        return nil, fmt.Errorf("get user %s: %w", id, err)
    }
    if err := user.Validate(); err != nil {
        return nil, fmt.Errorf("validate user %s: %w", id, err)
    }
    return user, nil
}

// ❌ 错误：主逻辑嵌套在条件中
func ProcessUser(id string) (*User, error) {
    user, err := db.GetUser(id)
    if err == nil {
        if err := user.Validate(); err == nil {
            return user, nil
        } else {
            return nil, err
        }
    }
    return nil, err
}
```

---

## 命名规范

| 类型 | 规则 | 示例 |
|------|------|------|
| 导出名称 | MixedCaps (大驼峰) | `UserService`, `GetUserByID` |
| 非导出名称 | mixedCaps (小驼峰) | `userService`, `getUserByID` |
| 包名 | 小写单词，无下划线，单数 | `user`, `httputil` |
| 禁止 | 下划线命名 | ~~`user_service`~~ |
| 避免简称 | 除非广泛接受的缩写 | `userID` ✅, `usrId` ❌ |
| 接口 | 单方法用 `-er` 后缀 | `Reader`, `Writer` |
| 常量 | 导出用 MixedCaps，`const` 块分组 | `MaxRetries` |

**Package 声明规则 (关键)**:
- NEVER 重复 `package` 声明 — 每个 Go 文件只能有一行
- 编辑现有文件时 MUST 保留原有的 package 声明
- 新建文件时先检查同目录其他文件使用的包名

**禁止遮蔽内置标识符** — NEVER 将 `new`, `len`, `make`, `copy` 等用作变量名:

```go
// ❌ 遮蔽内置标识符
func process(new string, len int, make bool) error {
    copy := "data"
    return nil
}

// ✅ 使用描述性名称
func process(name string, length int, shouldCreate bool) error {
    dataCopy := "data"
    return nil
}
```

---

## 导入分组

按以下顺序，用空行分隔:

```go
import (
    // 1. 标准库
    "context"
    "fmt"
    "net/http"

    // 2. 第三方库
    "github.com/creachadair/jrpc2"
    "go.uber.org/fx"

    // 3. 本项目包
    "your-project/internal/user"
    "your-project/pkg/utils"
)
```

---

## 文档注释

所有导出符号 MUST 有注释，以符号名开头:

```go
// Package user 用户包提供用户管理功能
package user

// User 用户代表系统中的注册用户
type User struct {
    ID    string
    Email string
}

// NewUser 使用给定的电子邮件创建新用户
func NewUser(email string) (*User, error) { ... }
```
