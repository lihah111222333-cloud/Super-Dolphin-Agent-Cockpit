package nodeexec

import "context"

// HybridExecutor 是 node_type=hybrid 的占位执行器。
// 当前生产路由尚未启用 hybrid 节点；保留该实现只为维持 NodeExecutor 接口和测试 fixture，
// 真正路由到 hybrid 时应在 router 层 fail-fast，而不是把这里的 done 当成业务成功。
type HybridExecutor struct{}

// Execute 返回 done 只服务于接口占位测试。
// 生产 dispatcher 不应调用此路径；如果未来接通 hybrid，需要替换为真实 automation+verifier 流程。
func (HybridExecutor) Execute(_ context.Context, _ Node, _ RunContext) (NodeOutcome, error) {
	return NodeOutcome{Status: NodeStatusDone}, nil
}

// Hooks 返回 nil，表示占位执行器不注册任何生命周期副作用。
func (HybridExecutor) Hooks() map[HookPoint]HookHandler { return nil }
