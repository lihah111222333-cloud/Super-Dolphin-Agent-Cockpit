# 回归记录

本轮是注释增强，不应改变业务行为。最终至少需要：

- `git diff --check`
- 变更 Go 文件的 `./scripts/test_with_guard.sh <file.go>`
- `make guard`

如 worker 修改了关键 package 且 guard 不能覆盖行为风险，追加受影响 package 的：

```bash
./scripts/test_with_guard.sh <affected-package> -count=1
```
