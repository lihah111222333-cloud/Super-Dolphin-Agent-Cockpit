# 结果闸门

## Gate 0

- [x] 生产 Go 文件清单生成：1119 files。
- [x] 生成代码已排除。
- [x] 初始单文件守卫：1119 files exit 0。
- [x] 30 个 worker 已派发。

## Gate 1

等待 worker 返回后逐项填写。

## Gate 2

等待整合后执行：

```bash
git diff --check
./scripts/test_with_guard.sh <changed-go-files>
```

## Gate 3

等待最终执行：

```bash
make guard
```
