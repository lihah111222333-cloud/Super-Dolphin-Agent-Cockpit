// Package waitprovision 提供 transport-neutral 的 durable per-target wait 候选。
//
// 该候选只编排外部 accesswait owner 的 grant/wait 端口，不拥有迁移、聚合、
// transport 或 provider 状态。生产接线必须由另一个经过审查的 adapter 显式完成。
package waitprovision

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
)

var (
	ErrInvalidRequest = errors.New("wait provision request is invalid")
	ErrInvalidResult  = errors.New("wait provision result is invalid")
	ErrReplayConflict = errors.New("wait provision replay is not exact")
	ErrDigestConflict = errors.New("wait provision operation digest conflicts")
	ErrPartial        = errors.New("wait provision partially completed")
	ErrNotFound       = errors.New("wait provision replay was not found")
	ErrNilPort        = errors.New("wait provision accesswait port is nil")
)

const maxTargets = 32

// Target 是一个固定的 per-target selector/fence。Selector 不得在 provision 后被替换。
type Target struct {
	Key           string                   `json:"key"`
	TargetFence   accesswait.FenceSnapshot `json:"target_fence"`
	Selector      accesswait.EventSelector `json:"selector"`
	AfterSequence int64                    `json:"after_sequence"`
	AfterCursor   string                   `json:"after_cursor,omitempty"`
}

// Request 是一次有界 provisioning 输入；OperationKey 是稳定幂等键，digest 是其内容摘要。
type Request struct {
	OperationKey       string                   `json:"operation_key"`
	OperationDigest    string                   `json:"operation_digest"`
	CallerPrincipalRef string                   `json:"caller_principal_ref"`
	ScopeRef           string                   `json:"scope_ref"`
	SourceFence        accesswait.FenceSnapshot `json:"source_fence"`
	Targets            []Target                 `json:"targets"`
	DeadlineAt         time.Time                `json:"deadline_at"`
}

// ProvisionRequest is the explicit name for Request in API-facing adapters.
type ProvisionRequest = Request

// Item 保存一个 target 的 grant 和 server-minted wait handle。
type Item struct {
	Key   string                 `json:"key"`
	Grant accesswait.AccessGrant `json:"grant"`
	Wait  accesswait.WaitHandle  `json:"wait"`
}

// Result 是可安全返回给调用方的完整 provisioning 结果。错误时仍保留已创建 items。
type Result struct {
	OperationKey    string `json:"operation_key"`
	OperationDigest string `json:"operation_digest"`
	Items           []Item `json:"items"`
}

// ProvisionResult is the explicit name for Result in API-facing adapters.
type ProvisionResult = Result

// AccessPort 是逐目标 grant/wait 创建端口；实现不得由 adapter 猜测或降级。
type AccessPort interface {
	CreateGrant(context.Context, accesswait.AccessGrantRequest) (accesswait.AccessGrant, error)
	CreateWait(context.Context, accesswait.WaitRequest) (accesswait.WaitHandle, error)
}

// ReplayReader 是显式外部 replay reader。按 OperationKey 返回 canonical 结果；缺失必须
// 返回 ErrNotFound。reader 不属于本候选 owner，adapter 不缓存或持久化结果。
type ReplayReader interface {
	ReadWaitProvision(context.Context, string) (Result, error)
}

// Reader is a concise compatibility name for ReplayReader.
type Reader = ReplayReader

// CleanupPort 是唯一允许清理副作用的窄端口。
type CleanupPort interface {
	CancelWait(context.Context, accesswait.CancelRequest) (accesswait.WaitResult, error)
	RevokeGrant(context.Context, accesswait.RevokeGrantRequest) (accesswait.AccessGrant, error)
}

// GrantReader is an optional durable read port used to validate partial owner
// outcomes before cleanup. An opaque grant id/revision is not sufficient proof
// of the operation digest or fence binding.
type GrantReader interface {
	GetGrant(context.Context, string) (accesswait.AccessGrant, error)
}

// Port 合并创建与显式清理端口；accesswait.Store 满足该接口。
type Port interface {
	AccessPort
	CleanupPort
}

var _ Port = (*accesswait.Store)(nil)

// Config 固定外部端口和 replay reader；不包含 Fx、transport 或 provider。
type Config struct {
	Access  AccessPort
	Cleanup CleanupPort
	Reader  ReplayReader
	// GrantReader is inferred from Cleanup when available. Partial/unknown
	// results require this durable read port before any cleanup side effect.
	GrantReader GrantReader
	Clock       func() time.Time
}

// Adapter 编排 provisioning，但不拥有任何 durable 状态。
type Adapter struct {
	access      AccessPort
	cleanup     CleanupPort
	reader      ReplayReader
	grantReader GrantReader
	clock       func() time.Time
}

// Provisioner is a semantic alias used by callers that treat the adapter as a port.
type Provisioner = Adapter

func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

// New 创建候选 adapter；缺少 create、cleanup 或 replay 端口时立即失败。
func New(config Config) (*Adapter, error) {
	if isNil(config.Access) || isNil(config.Cleanup) || isNil(config.Reader) {
		return nil, ErrNilPort
	}
	grantReader := config.GrantReader
	if isNil(grantReader) {
		if candidate, ok := config.Cleanup.(GrantReader); ok && !isNil(candidate) {
			grantReader = candidate
		}
	}
	clock := config.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Adapter{access: config.Access, cleanup: config.Cleanup, reader: config.Reader, grantReader: grantReader, clock: clock}, nil
}
