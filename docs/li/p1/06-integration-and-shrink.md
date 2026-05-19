### Task 6: 集成验证与基线收缩

**Files:**
- Modify: `internal/archtest/baseline.json` 和 `internal/archtest/baseline_test.json`

此步骤在 T1 到 T5 全部完成后执行。

- [ ] **Step 1: 全量测试验证**

运行:
```bash
make test
make build-plain
```
Expected: 测试覆盖率不降，代码构建成功。

- [ ] **Step 2: 验证基线完全解冻**

运行:
```bash
make guard
```
Expected: `baseline.json` 中的违规项已完全收缩。终端输出提示已无任何被冻结的文件。

- [ ] **Step 3: 提交收缩后的基线**

运行:
```bash
git add internal/archtest/baseline.json internal/archtest/baseline_test.json
git commit -m "chore: shrink all arch test baselines to zero violations"
```

至此，P1 质量指标清零计划全部完成！
