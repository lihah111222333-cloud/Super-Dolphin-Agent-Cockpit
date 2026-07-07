package difftracker

import "context"

// WorkDirResolver 负责把 agentID 解析为可执行 git diff 的工作目录。
// difftracker 只依赖该窄接口，避免直接耦合 thread 或 binding 存储实现。
type WorkDirResolver interface {
	ResolveAgentCWD(ctx context.Context, agentID string) (string, error)
}
