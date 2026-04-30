package skilllibrary

import "strings"

// TrustLevel 概括 spec §10 的 origin × signature 信任语义，给 install 流程
// 与未来 ArtifactKind 删除路径用作权威 trust 决策点。
//
// 设计：origin 是来源标签，不直接等价于 trust（spec §10 强调）。例如
// marketplace origin 即使存在 signature 字段，本期未实现签名校验，必须
// 当 untrusted 处理；同样 local 用户手放置的 skill 不能因为"在文件系统里"
// 自动获得 trust。
//
// 三档：
//   - TrustLevelTrusted   builtin（harness embed）；无条件信任
//   - TrustLevelUntrusted local / dev-override / marketplace（无签名校验）
//     默认按 untrusted 处理：要求 ArtifactKind 审批 +
//     per-skill AllowedTools 收敛
//   - TrustLevelUnknown   origin 字段缺失或解析失败；保守按 untrusted 处理
type TrustLevel int

const (
	TrustLevelUnknown TrustLevel = iota
	TrustLevelUntrusted
	TrustLevelTrusted
)

// String 给日志 / 测试用。
func (l TrustLevel) String() string {
	switch l {
	case TrustLevelTrusted:
		return "trusted"
	case TrustLevelUntrusted:
		return "untrusted"
	default:
		return "unknown"
	}
}

// EvaluateTrust 报告 meta 对应的信任级别。spec §10 强约束：
//   - origin=builtin → trusted（harness 自带，链条透明）
//   - origin=marketplace + signature 非空 → 本期仍 untrusted（签名校验未实现）
//   - origin=marketplace + signature 空 → untrusted
//   - origin=local / dev-override → untrusted（用户手放，需后续审批）
//   - 其他/缺失 → unknown
//
// 当签名校验流程在 P5+ 后续 phase 真正落地后，本函数应升级为对
// origin=marketplace + 已校验 signature 返回 trusted；当前实现是占位 +
// 阻断，禁止 marketplace skill 被默认信任，避免供应链攻击面扩大。
func EvaluateTrust(meta SkillMeta) TrustLevel {
	switch meta.Origin {
	case OriginBuiltin:
		return TrustLevelTrusted
	case OriginMarketplace:
		// spec §10 现期占位：signature 字段已存在但**未校验**；marketplace
		// 必须按 untrusted 处理直到签名校验落地。即便 signature 非空也不
		// 提升 trust，避免引入"看起来已签名实际未验证"的安全错觉。
		return TrustLevelUntrusted
	case OriginLocal, OriginDevOverride:
		return TrustLevelUntrusted
	}
	if strings.TrimSpace(string(meta.Origin)) == "" {
		return TrustLevelUnknown
	}
	return TrustLevelUnknown
}

// IsTrusted 是 EvaluateTrust 的 boolean 简写：仅 builtin 现期返回 true。
// 调用方应优先用 EvaluateTrust 拿三档语义；boolean 版本仅用于"是否绕过
// 审批 / AllowedTools 收敛"这种二元决策。
func IsTrusted(meta SkillMeta) bool {
	return EvaluateTrust(meta) == TrustLevelTrusted
}
