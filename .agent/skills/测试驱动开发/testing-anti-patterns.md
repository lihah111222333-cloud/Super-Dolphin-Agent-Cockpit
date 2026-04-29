# 测试反模式

**何时加载此参考：** 编写或修改测试、添加 mock，或想向生产代码添加仅供测试使用的方法时。

## 概览

测试必须验证真实行为，而不是 mock 行为。Mock 是隔离手段，不是被测试对象。

**核心原则：** 测试代码做什么，不测试 mock 做什么。

**严格遵循 TDD 可以防止这些反模式。**

## 铁律

```
1. 绝对不能 test mock behavior
2. 绝对不能 add test-only methods to production classes
3. 绝对不能 mock without understanding dependencies
```

## 反模式 1：测试 Mock 行为

**违规：**
```typescript
// ❌ BAD: Testing that the mock exists
test('renders sidebar', () => {
  render(<Page />);
  expect(screen.getByTestId('sidebar-mock')).toBeInTheDocument();
});
```

**为什么错：**
- 你在验证 mock 能工作，而不是组件能工作
- 有 mock 时测试通过，没有 mock 时失败
- 对真实行为没有任何说明

**你的协作者的纠正：** “我们是在测试 mock 的行为吗？”

**修复：**
```typescript
// ✅ GOOD: Test real component or don't mock it
test('renders sidebar', () => {
  render(<Page />);  // Don't mock sidebar
  expect(screen.getByRole('navigation')).toBeInTheDocument();
});

// OR if sidebar must be mocked for isolation:
// Don't assert on the mock - test Page's behavior with sidebar present
```

### 门控函数

```
BEFORE asserting on any mock element:
  Ask: "Am I testing real component behavior or just mock existence?"

  IF testing mock existence:
    STOP - Delete the assertion or unmock the component

  Test real behavior instead
```

## 反模式 2：生产代码中的测试专用方法

**违规：**
```typescript
// ❌ BAD: destroy() only used in tests
class Session {
  async destroy() {  // Looks like production API!
    await this._workspaceManager?.destroyWorkspace(this.id);
    // ... cleanup
  }
}

// In tests
afterEach(() => session.destroy());
```

**为什么错：**
- 生产类被测试专用代码污染
- 如果在生产中被误调用会有危险
- 违反 YAGNI 和关注点分离
- 混淆对象生命周期和实体生命周期

**修复：**
```typescript
// ✅ GOOD: Test utilities handle test cleanup
// Session has no destroy() - it's stateless in production

// In test-utils/
export async function cleanupSession(session: Session) {
  const workspace = session.getWorkspaceInfo();
  if (workspace) {
    await workspaceManager.destroyWorkspace(workspace.id);
  }
}

// In tests
afterEach(() => cleanupSession(session));
```

### 门控函数

```
BEFORE adding any method to production class:
  Ask: "Is this only used by tests?"

  IF yes:
    STOP - Don't add it
    Put it in test utilities instead

  Ask: "Does this class own this resource's lifecycle?"

  IF no:
    STOP - Wrong class for this method
```

## 反模式 3：不理解依赖就 Mock

**违规：**
```typescript
// ❌ BAD: Mock breaks test logic
test('detects duplicate server', () => {
  // Mock prevents config write that test depends on!
  vi.mock('ToolCatalog', () => ({
    discoverAndCacheTools: vi.fn().mockResolvedValue(undefined)
  }));

  await addServer(config);
  await addServer(config);  // Should throw - but won't!
});
```

**为什么错：**
- 被 mock 的方法有测试依赖的副作用（写配置）
- 为了“保险”而过度 mock，会破坏真实行为
- 测试会因为错误理由通过，或神秘失败

**修复：**
```typescript
// ✅ GOOD: Mock at correct level
test('detects duplicate server', () => {
  // Mock the slow part, preserve behavior test needs
  vi.mock('MCPServerManager'); // Just mock slow server startup

  await addServer(config);  // Config written
  await addServer(config);  // Duplicate detected ✓
});
```

### 门控函数

```
BEFORE mocking any method:
  STOP - Don't mock yet

  1. Ask: "What side effects does the real method have?"
  2. Ask: "Does this test depend on any of those side effects?"
  3. Ask: "Do I fully understand what this test needs?"

  IF depends on side effects:
    Mock at lower level (the actual slow/external operation)
    OR use test doubles that preserve necessary behavior
    NOT the high-level method the test depends on

  IF unsure what test depends on:
    Run test with real implementation FIRST
    Observe what actually needs to happen
    THEN add minimal mocking at the right level

  Red flags:
    - "I'll mock this to be safe"
    - "This might be slow, better mock it"
    - Mocking without understanding the dependency chain
```

## 反模式 4：不完整 Mock

**违规：**
```typescript
// ❌ BAD: Partial mock - only fields you think you need
const mockResponse = {
  status: 'success',
  data: { userId: '123', name: 'Alice' }
  // Missing: metadata that downstream code uses
};

// Later: breaks when code accesses response.metadata.requestId
```

**为什么错：**
- **部分 mock 隐藏结构性假设**：你只 mock 了自己知道的字段
- **下游代码可能依赖你没包含的字段**：静默失败
- **测试通过但集成失败**：mock 不完整，真实 API 完整
- **虚假信心**：测试没有证明真实行为

**铁律：** 按现实中存在的完整数据结构进行 mock，而不是只 mock 当前测试直接使用的字段。

**修复：**
```typescript
// ✅ GOOD: Mirror real API completeness
const mockResponse = {
  status: 'success',
  data: { userId: '123', name: 'Alice' },
  metadata: { requestId: 'req-789', timestamp: 1234567890 }
  // All fields real API returns
};
```

### 门控函数

```
BEFORE creating mock responses:
  Check: "What fields does the real API response contain?"

  Actions:
    1. Examine actual API response from docs/examples
    2. Include ALL fields system might consume downstream
    3. Verify mock matches real response schema completely

  Critical:
    If you're creating a mock, you must understand the ENTIRE structure
    Partial mocks fail silently when code depends on omitted fields

  If uncertain: Include all documented fields
```

## 反模式 5：把集成测试当事后补充

**违规：**
```
✅ 实现已完成
❌ No tests written
"Ready for testing"
```

**为什么错：**
- 测试是实现的一部分，不是可选后续事项
- TDD 本会捕获这个问题
- 没有测试就不能声称完成

**修复：**
```
TDD cycle:
1. Write failing test
2. Implement to pass
3. Refactor
4. THEN claim complete
```

## 当 Mock 变得过于复杂

**警示信号：**
- Mock 设置比测试逻辑更长
- 为了让测试通过而 mock 一切
- Mock 缺少真实组件拥有的方法
- Mock 一改测试就坏

**你的协作者的问题：** “我们需要在这里用 mock 吗？”

**考虑：** 使用真实组件的集成测试往往比复杂 mock 更简单。

## TDD 会防止这些反模式

**为什么 TDD 有帮助：**
1. **先写测试** → 迫使你思考自己到底在测试什么
2. **看它失败** → 确认测试的是行为，而不是 mock
3. **最小实现** → 不会混入测试专用方法
4. **真实依赖** → 在 mock 前先看到测试实际需要什么

**如果你在测试 mock 行为，就违反了 TDD**：你在没有先看真实代码下测试失败的情况下添加了 mock。

## 快速参考

| 反模式 | 修复 |
|--------------|-----|
| 对 mock 元素断言 | 测试真实组件，或取消 mock |
| 生产代码中的测试专用方法 | 移到测试工具 |
| 不理解就 mock | 先理解依赖，最小化 mock |
| 不完整 mock | 完整镜像真实 API |
| 事后补测试 | TDD：测试先行 |
| 过度复杂 mock | 考虑集成测试 |

## 红旗

- 断言检查 `*-mock` test ID
- 方法只在测试文件中调用
- Mock 设置超过测试的 50%
- 移除 mock 时测试失败
- 无法解释为什么需要 mock
- “只是为了保险”而 mock

## 底线

**Mock 是隔离工具，不是要测试的东西。**

如果 TDD 暴露出你在测试 mock 行为，就说明走错了。

修复：测试真实行为，或质疑为什么要 mock。
