// Package observation 将 turn、tool、UI 事件归一化为终态、token、计数、时间戳、调用归因和去重事实。
// DTO 与接口定义由 internal/dto/observation 承载，本包通过别名复用这些类型，避免消费者依赖 turn 子树。
package observation

import (
	dtoobs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/observation"
)

// 下面的别名让 turn 内部实现复用 dto/observation 的公共契约，避免出现两套事实类型。

// TerminalKind 表示 turn 终止状态分类的 wire 类型别名。
type TerminalKind = dtoobs.TerminalKind

const (
	TerminalUnknown     = dtoobs.TerminalUnknown
	TerminalCompleted   = dtoobs.TerminalCompleted
	TerminalStalled     = dtoobs.TerminalStalled
	TerminalFailed      = dtoobs.TerminalFailed
	TerminalInterrupted = dtoobs.TerminalInterrupted
	TerminalAborted     = dtoobs.TerminalAborted
)

// Terminal 是 observation 记录的终态事实别名。
type Terminal = dtoobs.Terminal

// TokenSnapshot 是 provider token 快照事实别名。
type TokenSnapshot = dtoobs.TokenSnapshot

// DedupeKey 是事件去重键的事实别名。
type DedupeKey = dtoobs.DedupeKey

// Counts 是工具调用、失败和审批计数事实别名。
type Counts = dtoobs.Counts

// Timestamps 是 turn 开始和完成时间事实别名。
type Timestamps = dtoobs.Timestamps

// ObservationReader 是只读 observation 契约别名。
type ObservationReader = dtoobs.ObservationReader

// ObservationWriter 是写入 observation 事实的契约别名。
type ObservationWriter = dtoobs.ObservationWriter

// Contract 聚合 observation 读写能力，供 turn wiring 和消费者共享。
type Contract = dtoobs.Contract

// terminalPrecedence 返回 RecordTerminal 使用的优先级顺序，数值越大越优先。
// Interrupted/Aborted 为粘性种类，即便同优先级也不会被覆盖。
func terminalPrecedence(k TerminalKind) int {
	switch k {
	case TerminalInterrupted, TerminalAborted:
		return 5
	case TerminalFailed:
		return 4
	case TerminalStalled:
		return 3
	case TerminalCompleted:
		return 2
	default:
		return 0
	}
}
