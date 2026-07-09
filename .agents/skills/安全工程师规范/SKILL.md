---
name: 安全工程师规范
description: 在 super-agent-v3 中进行安全设计、代码审计、漏洞修复或合规检查时使用。
tags: [security, appsec, owasp, audit, encryption, auth, vulnerability, 安全, 渗透测试, 代码审计, 加密, 认证]
---

# 安全工程师规范 (Security Engineer Standards)

本指南定义了 super-agent-v3 项目的安全工程标准。安全不是附加功能，而是系统的免疫系统。

## super-agent-v3 默认安全审查面

优先审查当前仓库实际边界，而不是套用通用 Web 清单：

* **Wails / JSON-RPC 边界**：host-facing RPC 必须校验 `cwd`、provider、workspace、thread/session 所属关系；错误返回遵循 jrpc2 映射，不泄露内部路径、token 或堆栈。
* **MCP sidecar 与 toolbridge**：`cmd/mcp-*`、`internal/mcpserver/common`、tool approval、stdio envelope 和 provider tool 调用必须 fail-fast；不得静默降级、吞错或绕过 approval。
* **技能与 provider home**：canonical skill 只来自 `<cwd>/.agents/skills` 与 active personal roots；`.claude/skills` / `.agents/skills` provider mirror 是生成物；必须防 symlink、路径穿越、unmanaged mirror 和同名冲突污染。
* **文件系统与命令执行**：所有路径基于显式 `cwd` / project root 解析；拒绝越权写入、符号链接逃逸、shell 拼接和无审计命令执行。
* **SQLite/sqlc**：默认持久化是 SQLite + sqlc；禁止字符串拼接 SQL、隐式迁移绕过、空配置兜底和跨 store 直接共享业务查询。
* **日志与密钥**：日志字段使用预留常量，禁止输出 provider token、approval payload、prompt/memory 私密内容、用户文件正文和本机绝对密钥路径。

## 何时使用

在以下场景参考此规范：
*   **架构设计**：设计认证(AuthN)、授权(AuthZ)、加密方案时。
*   **代码开发**：处理敏感数据、输入验证、SQL/命令注入防护时。
*   **代码审计**：进行 PR Review，检查是否存在安全漏洞时。
*   **漏洞修复**：修复由扫描器或白帽子报告的漏洞时。
*   **部署上线**：配置防火墙、容器隔离、密钥管理时。

---

## 第一部分：核心安全原则 (Core Principles)

### 1. 零信任架构 (Zero Trust)
*   **Never Trust, Always Verify**：不信任任何内网流量。服务间调用必须鉴权（如 mTLS 或 Token）。
*   **最小权限原则 (Least Privilege)**：只授予应用/用户完成任务所需的最小权限。DB 用户不应有 DROP TABLE 权限，普通 API Token 不应有 Admin 权限。

### 2. 纵深防御 (Defense in Depth)
*   不要依赖单一防御层。例如：前端校验 + 后端 WAF + API 输入清洗 + 数据库预编译语句 + 最小数据库权限。

### 3. 安全左移 (Shift Left)
*   在编码阶段（甚至设计阶段）就发现安全问题，而不是在上线后的渗透测试中。

---

## 第二部分：Web 应用安全 (AppSec)

### 1. OWASP Top 10 防护
*   **A01: Broken Access Control (越权)**
    *   **强制检查**：所有 API 必须检查 `current_user.id == resource.owner_id`。
    *   **禁止默认允许**：使用白名单机制控制资源访问。
*   **A02: Cryptographic Failures (加密失败)**
    *   **传输加密**：全站强制 HTTPS (TLS 1.2+)。
    *   **存储加密**：密码必须哈希加盐 (Argon2id 或 bcrypt)，严禁明文存储。敏感字段 (PII, API Key) 需应用层加密 (AES-GCM)。
*   **A03: Injection (注入)**
    *   **SQL 注入**：100% 使用 `sqlc` 自动生成的类型安全查询，或 `database/sql` 的参数化查询。严禁字符串拼接 SQL。
    *   **命令注入**：避免使用 `exec`, `system` 等执行 Shell 命令。如必须，需严格校验参数 whitelist。

### 2. 输入与输出处理
*   **Sanitize Input**：对所有用户输入进行类型、长度、格式校验。
*   **Escape Output**：防止 XSS。在 HTML 上下文输出时进行 HTML Entity 编码；在 JSON 上下文正确序列化。

### 3. 认证与会话管理
*   **Token 安全**：JWT 必须签名 (HS256/RS256)，避免 `None` 算法漏洞。Set-Cookie 必须包含 `HttpOnly; Secure; SameSite=Strict`。
*   **防暴力破解**：登录接口必须实施限流 (Rate Limiting) 和验证码机制。

---

## 第三部分：代码审计清单 (Code Audit Checklist)

在 Review 代码时，必须核对以下项目：

> **🛡️ 安全审计 Checkpoints**
>
> **逻辑漏洞**
> - [ ] 修改密码/邮箱是否验证了旧密码？
> - [ ] 这是一个支付接口吗？金额是否为负数？是否处理了并发扣款（竞态条件）？
> - [ ] 订单查询是否限制了只能查自己的？(IDOR)
>
> **数据泄露**
> - [ ] 日志中是否打印了用户密码、Token 或 PII？
> - [ ] 错误信息是否暴露了数据库结构或堆栈信息？
> - [ ] Git 提交是否包含了 `.env` 或密钥文件？
>
> **依赖安全**
> - [ ] 此 PR 引入的新依赖是否有已知 CVE？
> - [ ] `go.sum` / `package-lock.json` 是否被篡改？

---

## 第四部分：基础设施安全 (InfraSec)

### 1. Secrets Management
*   **严禁硬编码**：代码中绝对不能出现 AK/SK、数据库密码。
*   **使用环境变量**：所有密钥通过 ENV 注入。在生产环境使用 AWS Secrets Manager 或 HashiCorp Vault。

### 2. 容器与镜像安全
*   **非 Root 运行**：Dockerfile 中必须创建非特权用户并切换 (`USER appuser`)。
*   **最小基础镜像**：使用 Distroless 或 Alpine 减少攻击面。
*   **定期扫描**：CI 流水线集成 Trivy 或 Snyk 扫描镜像漏洞。

---

## 第五部分：应急响应 (Incident Response)

当发生安全事件时：
1.  **止损 (Containment)**：隔离受感染主机，封禁攻击者 IP，暂停相关服务。
2.  **根除 (Eradication)**：修补漏洞，轮换所有相关密钥 (Rotate Secrets)。
3.  **恢复 (Recovery)**：从干净的备份恢复数据，重新部署服务。
4.  **复盘 (Post-Mortem)**：分析根本原因 (Root Cause)，产出改进报告。

---

## 结语

安全不是买来的产品，而是一个持续的过程。作为安全工程师，你的职责是让攻击者的成本高于其收益。

---

## 第六部分：安全代码示例

### SQL 注入防护

```go
// ❌ 危险：直接拼接 SQL
query := fmt.Sprintf("SELECT * FROM users WHERE id = '%s'", userID)

// ✅ 安全：使用参数化查询 (database/sql)
query := "SELECT * FROM users WHERE id = ?"
db.Query(query, userID)

// ✅ 使用 V3 标准 sqlc
// sqlc 生成的强类型方法自动防御注入
user, err := querier.GetUserByID(ctx, userID)
```

### XSS 防护

```go
import "html"

// ❌ 危险：直接输出用户输入
template.HTML(userInput)

// ✅ 安全：转义 HTML
html.EscapeString(userInput)

// ✅ 使用内容安全策略头
w.Header().Set("Content-Security-Policy",
    "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'")
```

### JWT 安全配置

```go
// ✅ 安全的 JWT 配置
token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
    "sub":  userID,
    "exp":  time.Now().Add(15 * time.Minute).Unix(),  // 短过期时间
    "iat":  time.Now().Unix(),
    "iss":  "your-app",
    "aud":  "your-app-users",
})

// ✅ 验证时检查所有声明
token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
    if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
        return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
    }
    return []byte(secretKey), nil
})
```

### 密码安全

```go
import "golang.org/x/crypto/bcrypt"

// ✅ 密码哈希（注册时）
hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)

// ✅ 密码验证（登录时）
err := bcrypt.CompareHashAndPassword(hashedPassword, []byte(inputPassword))
if err != nil {
    // 密码错误 - 使用统一错误消息防止枚举
    return errors.New("用户名或密码错误")
}
```

### 速率限制

```go
import "golang.org/x/time/rate"

// ✅ 创建限速器：每秒 10 个请求，最大突发 30
limiter := rate.NewLimiter(10, 30)

func rateLimitMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !limiter.Allow() {
            http.Error(w, "请求过于频繁", http.StatusTooManyRequests)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

---

## 审查清单

### 输入验证
- [ ] 所有用户输入已验证和清理
- [ ] SQL 查询使用参数化
- [ ] 文件上传限制类型和大小
- [ ] URL 重定向验证白名单

### 认证授权
- [ ] 密码使用 bcrypt 哈希
- [ ] JWT 过期时间合理（< 15 分钟）
- [ ] 敏感操作需要重新认证
- [ ] 权限检查在服务端执行

### 数据保护
- [ ] 敏感数据加密存储
- [ ] 日志不记录密码/令牌
- [ ] HTTPS 强制启用
- [ ] 响应头设置正确（CSP, X-Frame-Options）

### 基础设施
- [ ] 密钥通过环境变量注入
- [ ] 容器以非 root 用户运行
- [ ] 依赖定期扫描漏洞
- [ ] 日志记录安全事件



---
