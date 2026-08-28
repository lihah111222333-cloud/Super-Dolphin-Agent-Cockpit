# LSP 高级语义回忆包

复杂任务按 `structure` 定义 → 当前真实 `file:line:column` → `xref` 影响面 → `apply_patch` → `diagnostics` → 定向测试执行；单文件局部且无共享契约时跳过重型探索。

批量 diagnostics 必须按文件映射新文件侧 1-based changed lines；`OK` 不是 clean。保存 patch/hash、最新失败、未解决证据、cache key 和 previous hypothesis，runner 仍是最终权威；no-progress 时改变假设，不重复 grader。
