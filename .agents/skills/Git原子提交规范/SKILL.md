---
name: Git原子提交规范
description: "仅当用户明确点名 `Git原子提交规范` 技能时使用。"
disable_model_invocation: true
aliases: ["@Git原子提交规范", "@git-commit"]
---

# super-agent-v3 Git 原子提交规范

## 基本规则

- 提交前后都跑 `git status --short`。
- 只 stage owned files；不要 `git add .`。
- 保留 unrelated dirty / untracked 文件。
- 默认分支前缀 `codex/`；只有用户明确要求才在 main 上提交。
- 修复类提交必须包含同提交回归测试、fixture、golden、snapshot 或可执行验收脚本。
- 避免 `--no-verify`；紧急旁路后必须补跑遗漏检查并报告。

## 推荐流程

```bash
git status --short
git diff -- <owned-files>
git add <owned-files>
git diff --cached --check
git status --short
git commit -m "<type>: <summary>"
```

Go 改动先按文件和包跑：

```bash
./scripts/test_with_guard.sh <file.go>
./scripts/test_with_guard.sh <affected packages> -count=1
```

## 红旗

| 红旗 | 处理 |
|---|---|
| unrelated 文件出现在 staged diff | 取消 stage 该文件，不要修改内容 |
| fix 提交没有测试锁 | 补测试或说明无法自动锁定的可执行验收 |
| hooks 失败 | 修根因，不调弱 guard |
| baseline diff | 逐项解释，未经批准不用 freeze |
