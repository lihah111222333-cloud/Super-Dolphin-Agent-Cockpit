package contract

import dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"

// CloneProviderSkillRefsMetadataOnly 复制 provider skill 引用并清空 legacy Prompt 正文载荷。
// start/session 边界仍可保留身份元数据用于诊断，但不得保留或转发完整 skill 文档；输入不会被修改。
func CloneProviderSkillRefsMetadataOnly(refs []dto.SkillRef) []dto.SkillRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]dto.SkillRef, len(refs))
	for i, ref := range refs {
		ref.Prompt = ""
		out[i] = ref
	}
	return out
}
