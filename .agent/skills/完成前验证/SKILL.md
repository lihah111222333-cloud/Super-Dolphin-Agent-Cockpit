---
name: 完成前验证
description: 准备声称工作已完成、已修复或已通过时，在提交或创建 PR 前使用；要求先运行验证命令并确认输出，再做任何成功声明；永远先证据、后断言
aliases: ["@完成前验证", "@verification-before-completion"]
---

# 完成前验证

## 概览

未经验证就声称工作完成是不诚实，不是高效。

**核心原则：** 永远先证据，后声明。

**违反这条规则的字面要求，就是违反规则精神。**

## 铁律

```
NO COMPLETION CLAIMS WITHOUT FRESH VERIFICATION EVIDENCE
```

如果你没有在这条消息中运行验证命令，就不能声称它通过。

## 门控函数

```
BEFORE claiming any status or expressing satisfaction:

1. IDENTIFY: What command proves this claim?
2. RUN: Execute the FULL command (fresh, complete)
3. READ: Full output, check exit code, count failures
4. VERIFY: Does output confirm the claim?
   - If NO: State actual status with evidence
   - If YES: State claim WITH evidence
5. ONLY THEN: Make the claim

Skip any step = lying, not verifying
```

## 常见失败

| 声明 | 需要 | 不足够 |
|-------|----------|----------------|
| 测试通过 | 测试命令输出：0 failures | 之前运行过，“应该通过” |
| Linter 干净 | Linter 输出：0 errors | 部分检查、外推 |
| 构建成功 | 构建命令：exit 0 | Linter 通过、日志看起来不错 |
| Bug 已修复 | 原始症状测试：通过 | 代码改了，假设已修复 |
| 回归测试有效 | 已验证红-绿循环 | 测试通过一次 |
| 代理已完成 | VCS diff 显示变更 | 代理报告“成功” |
| 满足需求 | 逐行检查清单 | 测试通过 |

## 红旗：停止

- 使用 “should”、“probably”、“seems to”
- 验证前表达满意（“Great!”、“Perfect!”、“Done!” 等）
- 准备在没有验证的情况下提交/推送/创建 PR
- 信任代理的成功报告
- 依赖部分验证
- 想着“就这一次”
- 疲惫并想结束工作
- **任何暗示成功但尚未运行验证的措辞**

## 防止合理化

| 借口 | 现实 |
|--------|---------|
| “现在应该能工作” | 运行验证。 |
| “我有信心” | 信心 ≠ 证据。 |
| “就这一次” | 没有例外。 |
| “Linter 通过了” | Linter ≠ 编译器。 |
| “代理说成功” | 独立验证。 |
| “我累了” | 疲惫不是借口。 |
| “部分检查足够” | 部分证明不了什么。 |
| “换了说法，所以规则不适用” | 精神高于字面。 |

## 关键模式

**测试：**
```
✅ [Run test command] [See: 34/34 pass] "All tests pass"
❌ "Should pass now" / "Looks correct"
```

**回归测试（TDD 红-绿）：**
```
✅ Write → Run (pass) → Revert fix → Run (MUST FAIL) → Restore → Run (pass)
❌ "I've written a regression test" (without red-green verification)
```

**构建：**
```
✅ [Run build] [See: exit 0] "Build passes"
❌ "Linter passed" (linter doesn't check compilation)
```

**需求：**
```
✅ Re-read plan → Create checklist → Verify each → Report gaps or completion
❌ "Tests pass, phase complete"
```

**代理委派：**
```
✅ Agent reports success → Check VCS diff → Verify changes → Report actual state
❌ Trust agent report
```

## 为什么重要

来自 24 条失败记忆：
- 你的协作者说 “I don't believe you”：信任破裂
- 未定义函数被交付：会崩溃
- 缺失需求被交付：功能不完整
- 由于虚假完成导致时间浪费 → 重定向 → 返工
- 违反：“诚实是核心价值。如果你撒谎，你会被替换。”

## 何时应用

**始终在以下情况前应用：**
- 任何形式的成功/完成声明
- 任何满意表达
- 任何关于工作状态的正面陈述
- 提交、创建 PR、完成任务
- 进入下一个任务
- 委派给代理

**规则适用于：**
- 精确短语
- 改写和同义表达
- 成功暗示
- 任何暗示完成/正确性的沟通

## 底线

**验证没有捷径。**

运行命令。读取输出。然后再声明结果。

这是不可协商的。
