package shared

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"

// SafeToolArgumentsPreview 将 provider 侧工具参数转成统一脱敏预览。
// 真实规则在 platform/observability，provider/shared 保留这个入口给 provider 翻译层复用。
func SafeToolArgumentsPreview(raw any) string {
	return observability.SafeToolArgumentsPreview(raw)
}

// SafeToolArgumentsPreviewString 处理 provider 已生成的字符串形态工具参数预览。
// 该包装避免 provider 调用方直接依赖平台实现细节，同时保持跨 provider 行为一致。
func SafeToolArgumentsPreviewString(raw string) string {
	return observability.SafeToolArgumentsPreviewString(raw)
}
