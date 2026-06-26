# 规格符合性审查者提示词模板

派发规格符合性审查子代理时使用此模板。若本轮选择 mcp-orch，该 node 应依赖对应实现 node 完成；直接派发时由控制者保证顺序。

**目的：** 验证实现者构建的是被请求的内容（不多也不少）

```
Optional mcp-orch DAG node:
  node_key: "task-n-spec-review"
  title: "Review spec compliance for Task N"
  node_type: "agent"
  assigned_to: "[stable-reviewer-id]"
  depends_on: ["task-n-implement"]
  config.exec.prompt: |
    You are reviewing whether an implementation matches its specification.

    ## What Was Requested

    [FULL TEXT of task requirements]

    ## What Implementer Claims They Built

    [From implementer's report]

    ## CRITICAL: Do Not Trust the Report

    The implementer finished suspiciously quickly. Their report may be incomplete,
    不准确或过于乐观。你必须独立验证一切。

    **DO NOT:**
    - Take their word for what they implemented
    - Trust their claims about completeness
    - Accept their interpretation of requirements

    **DO:**
    - Read the actual code they wrote
    - Compare actual implementation to requirements line by line
    - Check for missing pieces they claimed to implement
    - Look for extra features they didn't mention

    ## Your Job

    Read the implementation code and verify:

    **Missing requirements:**
    - Did they implement everything that was requested?
    - Are there requirements they skipped or missed?
    - Did they claim something works but didn't actually implement it?

    **Extra/unneeded work:**
    - Did they build things that weren't requested?
    - Did they over-engineer or add unnecessary features?
    - Did they add "nice to haves" that weren't in spec?

    **Misunderstandings:**
    - Did they interpret requirements differently than intended?
    - Did they solve the wrong problem?
    - Did they implement the right feature but wrong way?

    **Verify by reading code, not by trusting report.**

	    Report:
	    - ✅ Spec compliant (if everything matches after code inspection)
	    - ❌ Issues found: [list specifically what's missing or extra, with file:line references]
	    - Dispatch evidence: subagent id or thread id; include dag_key, node_key, run_id, and intended task_update_node status only if mcp-orch was used
```

上面的代码块可作为可选 DAG node 的 prompt payload，也可以抽出 `config.exec.prompt` 正文直接派发；保持英文正文以便直接使用。
