//go:build e2e
// +build e2e

package nested

// 嵌套 memory 包不再把 AutoMem/TeamMem MEMORY.md 纳入 ClaudeMd 候选集。
// 父级 MemoryEntrypointProvider 负责 prompt 注入，相关覆盖位于 memory 包入口测试中。
