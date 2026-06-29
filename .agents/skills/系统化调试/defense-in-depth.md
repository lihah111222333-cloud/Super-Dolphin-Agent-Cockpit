# 纵深防御验证

## 概览

当你修复由无效数据导致的 bug 时，在一个地方添加验证会让人觉得足够。但这单个检查可能被不同代码路径、重构或 mock 绕过。

**核心原则：** 在数据经过的每一层验证。让 bug 在结构上不可能发生。

## 为什么需要多层

单层验证：“我们修好了 bug”
多层验证：“我们让这个 bug 不可能发生”

不同层捕获不同情况：
- 入口验证捕获大多数 bug
- 业务逻辑捕获边界情况
- 环境保护防止特定上下文中的危险
- 当其他层失败时，调试日志提供帮助

## 四层

### 第 1 层：入口点验证
**目的：** 在 API 边界拒绝明显无效输入

```typescript
function createProject(name: string, workingDirectory: string) {
  if (!workingDirectory || workingDirectory.trim() === '') {
    throw new Error('workingDirectory cannot be empty');
  }
  if (!existsSync(workingDirectory)) {
    throw new Error(`workingDirectory does not exist: ${workingDirectory}`);
  }
  if (!statSync(workingDirectory).isDirectory()) {
    throw new Error(`workingDirectory is not a directory: ${workingDirectory}`);
  }
  // ... proceed
}
```

### 第 2 层：业务逻辑验证
**目的：** 确保数据对当前操作有意义

```typescript
function initializeWorkspace(projectDir: string, sessionId: string) {
  if (!projectDir) {
    throw new Error('projectDir required for workspace initialization');
  }
  // ... proceed
}
```

### 第 3 层：环境保护
**目的：** 防止特定上下文中的危险操作

```typescript
async function gitInit(directory: string) {
  // In tests, refuse git init outside temp directories
  if (process.env.NODE_ENV === 'test') {
    const normalized = normalize(resolve(directory));
    const tmpDir = normalize(resolve(tmpdir()));

    if (!normalized.startsWith(tmpDir)) {
      throw new Error(
        `Refusing git init outside temp dir during tests: ${directory}`
      );
    }
  }
  // ... proceed
}
```

### 第 4 层：调试仪表
**目的：** 捕获上下文，便于事后取证

```typescript
async function gitInit(directory: string) {
  const stack = new Error().stack;
  logger.debug('About to git init', {
    directory,
    cwd: process.cwd(),
    stack,
  });
  // ... proceed
}
```

## 应用该模式

发现 bug 后：

1. **追踪数据流**：坏值从哪里来？在哪里使用？
2. **绘制所有检查点**：列出数据经过的每个点
3. **在每层添加验证**：入口、业务、环境、调试
4. **测试每层**：尝试绕过第 1 层，验证第 2 层能捕获

## 会话示例

Bug：空 `projectDir` 导致在源代码中执行 `git init`

**数据流：**
1. 测试设置 → 空字符串
2. `Project.create(name, '')`
3. `WorkspaceManager.createWorkspace('')`
4. `git init` 在 `process.cwd()` 中运行

**添加的四层：**
- 第 1 层：`Project.create()` 验证不为空/存在/可写
- 第 2 层：`WorkspaceManager` 验证 projectDir 不为空
- 第 3 层：`WorktreeManager` 在测试中拒绝在 tmpdir 外执行 git init
- 第 4 层：git init 前记录堆栈跟踪

**结果：** 1847 个测试全部通过，bug 无法复现

## 关键洞察

四层都必要。测试期间，每一层都捕获了其他层漏掉的 bug：
- 不同代码路径绕过了入口验证
- Mock 绕过了业务逻辑检查
- 不同平台的边界情况需要环境保护
- 调试日志识别出结构性误用

**不要停在单个验证点。** 在每一层都添加检查。
