---
name: refactoring-guardrails
description: 在 super-agent-v3 中进行重构、函数拆分、抽取公共逻辑、重排文件或需要行为护栏时使用。
aliases: ["@refactoring-guardrails", "@重构护栏"]
---

# 重构护栏

## 基本原则

- 重构必须保持外部行为不变；任何行为变化都要先变更需求或测试。
- 先找受影响包、入口和测试，再改代码。
- 涉及 Go 文件时，每改完一个文件先跑 `./scripts/test_with_guard.sh <file.go>`。
- 禁止静默兜底：双跑、影子执行或迁移对比发现差异时必须返回错误并保留 diff 证据。

## 推荐流程

1. 记录重构前行为：测试、golden、快照或可执行验收脚本。
2. 小步移动代码，保持 public contract 不变。
3. 每个阶段运行最小验证，最后运行受影响包测试。
4. 如果 guard 或双跑对比发现差异，停止并修复差异，不要返回旧结果继续执行。

## fail-fast 对比示例

```go
func (s *Service) calcWithGuardrail(ctx context.Context, in Input) (Output, error) {
    oldOut, oldErr := s.calcLegacy(ctx, in)
    newOut, newErr := s.calcNew(ctx, in)

    if diff := compareOutput(oldOut, newOut, oldErr, newErr); diff != nil {
        return Output{}, fmt.Errorf("guardrail diff for %s: %w", in.ID, diff)
    }
    return newOut, newErr
}
```

## 验证

- Go：`./scripts/test_with_guard.sh <affected packages> -count=1`
- guard/architecture：`make guard` 或 `./scripts/test_with_guard.sh ./internal/archtest -count=1`
- docs/skills-only：`python3 scripts/validate_super_agent_skills.py` + `git diff --check`
