package shared

import (
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// ProviderNativeCapabilityProbeRequiredReason is the stable reason used when
// an adapter has not independently probed a provider capability yet.  The
// provider-native path must not claim support merely because a CLI/app-server
// process can be started.
const ProviderNativeCapabilityProbeRequiredReason = "capability_probe_required"

// ProviderNativeContextInput contains the non-secret identity used to build a
// provider-native context at a start/resume boundary.  CWD is canonicalized by
// BuildProviderNativeContext; callers must use the returned context rather
// than retaining the raw input path.
type ProviderNativeContextInput struct {
	Provider        string
	ProviderVersion string
	CWD             string
	ProviderHome    string
	Profile         string
	Capabilities    contract.ProviderNativeCapabilities
}

// UnknownProviderNativeCapabilities 返回尚未执行 provider 探测时的显式非乐观能力集。
// Unknown 不等于 unsupported，也不应因进程可启动就伪造 supported。
// Unknown is intentionally different from unsupported and from a successful
// RequireProviderFeature result; it prevents accidental capability claims.
func UnknownProviderNativeCapabilities() contract.ProviderNativeCapabilities {
	return contract.ProviderNativeCapabilities{
		Memory: unknownProviderNativeCapability(),
		Skills: unknownProviderNativeCapability(),
	}
}

func unknownProviderNativeCapability() contract.ProviderNativeFeatureCapability {
	return contract.ProviderNativeFeatureCapability{
		Owner:  contract.ProviderNativeOwnerProvider,
		Status: contract.ProviderNativeStatusUnknown,
		Reason: ProviderNativeCapabilityProbeRequiredReason,
	}
}

// BuildProviderNativeContext 构造并校验 start/resume/recovery 共用的最小 native context。
// capabilities 必须显式传入；尚无 provider 探测时应传 UnknownProviderNativeCapabilities，
// producer 不会静默补默认能力。
func BuildProviderNativeContext(input ProviderNativeContextInput) (contract.ProviderNativeContext, error) {
	provider, err := contract.CanonicalProviderNativeProvider(input.Provider)
	if err != nil {
		return contract.ProviderNativeContext{}, err
	}
	capabilities := input.Capabilities
	if capabilities == (contract.ProviderNativeCapabilities{}) {
		return contract.ProviderNativeContext{}, fmt.Errorf("%w: capabilities are required", contract.ErrProviderNativeContextInvalid)
	}
	ctx := contract.ProviderNativeContext{
		SchemaVersion:   contract.ProviderNativeContextSchemaVersion,
		Provider:        provider,
		ProviderVersion: input.ProviderVersion,
		CWD:             input.CWD,
		ProviderHome:    input.ProviderHome,
		Profile:         input.Profile,
		Capabilities:    capabilities,
	}
	canonical, err := contract.CanonicalizeProviderNativeContext(ctx)
	if err != nil {
		return contract.ProviderNativeContext{}, fmt.Errorf("provider native context: %w", err)
	}
	return canonical, nil
}

// ValidateProviderNativeContext 校验 context；传入 required 时还要求每项能力具备
// provider-owned supported evidence。未传 required 时只做结构闸门，unknown 仍保持显式，
// 需要能力的具体操作再选择 Require 语义。
func ValidateProviderNativeContext(ctx contract.ProviderNativeContext, required ...contract.ProviderNativeFeature) error {
	if err := ctx.Validate(); err != nil {
		return err
	}
	for _, feature := range required {
		if err := ctx.RequireProviderFeature(feature); err != nil {
			return err
		}
	}
	return nil
}

// ValidateProviderNativeContextForProvider 在通用校验外绑定调用方期望的 provider owner。
// 白名单中的另一个 provider 也不能被错误地复用到当前 adapter 的 recovery 路径。
func ValidateProviderNativeContextForProvider(ctx contract.ProviderNativeContext, provider string, required ...contract.ProviderNativeFeature) error {
	expected, err := contract.CanonicalProviderNativeProvider(provider)
	if err != nil {
		return err
	}
	if ctx.Provider != expected {
		return fmt.Errorf("%w: provider mismatch: expected %q, got %q", contract.ErrProviderNativeContextInvalid, expected, ctx.Provider)
	}
	return ValidateProviderNativeContext(ctx, required...)
}
