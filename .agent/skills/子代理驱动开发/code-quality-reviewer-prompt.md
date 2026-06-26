# 代码质量审查者提示词模板

派发代码质量审查者子代理时使用此模板。

**目的：** 验证实现是否构建良好（干净、经过测试、可维护）

**只在规格符合性审查通过后派发。** 创建为 mcp-orch DAG node，依赖对应规格审查 node。

```
mcp-orch DAG node:
  node_key: "task-n-quality-review"
  title: "Review code quality for Task N"
  node_type: "agent"
  assigned_to: "superpowers-code-reviewer"
  depends_on: ["task-n-spec-review"]
  config.exec.prompt: |
    Use template at 请求代码审查/code-reviewer.md

    WHAT_WAS_IMPLEMENTED: [from implementer's report]
    PLAN_OR_REQUIREMENTS: Task N from [plan-file]
    BASE_SHA: [commit before task]
    HEAD_SHA: [current commit]
    DESCRIPTION: [task summary]

    Return findings plus node evidence for task_update_node.
```

**除标准代码质量问题外，审查者还应检查：**
- 每个文件是否都有一个明确职责和定义良好的接口？
- 单元是否已分解到可以独立理解和测试？
- 实现是否遵循计划中的文件结构？
- 此实现是否创建了已经很大的新文件，或显著扩大了现有文件？（不要标记既有文件大小，只关注这次变更带来的增量。）

**代码审查者返回：** 优点、问题（Critical/Important/Minor）、评估
