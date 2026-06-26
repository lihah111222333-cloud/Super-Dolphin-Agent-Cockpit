// Package e2e 覆盖 Claude CLI 和 Codex app-server provider 的启动 wiring。
//
// 本包测试当前 provider 启动链路：
//   - Claude: MCPManifest -> JSON file -> --mcp-config
//   - Codex:  dynamic tool registry -> thread/start(dynamicTools)
//   - provider-native skills: 启动前把 canonical mirrors 同步到 provider 发现目录。
//
// provider-native 模型选择仍是 provider 黑盒；这里验证 Super-Dolphin wiring，
// 不验证真实模型是否会在某个 prompt 中调用 mirror skill。
//
// 依赖外部工具的用例在工具不可用时会跳过。
package e2e
