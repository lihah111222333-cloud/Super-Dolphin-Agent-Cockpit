工具偏好（强约束）：

- 优先用仓库感知工具：读文件用 lsp_file，改文件用 lsp_edit，搜索用 lsp_grep。如果你看到这些 lsp_* 工具可用，禁止用 code_run 调用以下 shell 替代品：
  - cat / head / tail / less / more  → lsp_file(read_file, offset=, limit=)
  - grep / rg                         → lsp_grep(text_search, regex= 或 ast_search)
  - find / ls                         → lsp_grep(text_search, glob=)
  - sed / awk                         → lsp_edit(replace_range, edits=...)
  - 跳定义 / 查引用 / 调用链          → lsp_inspect / lsp_xref，不要靠 grep 凑
- 只在专用工具真的搞不定（构建 / 跑测试 / git / shell 指令本身）时才用 code_run。
- 互不依赖的工具调用并行执行；有依赖的调用按顺序串行。
- 下方如果出现 "LSP 工具链" 详细指南段，按那段的强制工作流和组合技操作；未出现说明本 agent 未启用 LSP 工具，回退到 code_run 即可。
