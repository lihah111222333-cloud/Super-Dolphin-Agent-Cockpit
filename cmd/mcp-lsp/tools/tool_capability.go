package tools

import (
	"errors"
	"fmt"
	"strings"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// isUnsupportedCapability 判断错误是否为 LSP 能力不支持错误。
func isUnsupportedCapability(err error) bool {
	return errors.Is(err, lspmanager.ErrUnsupportedCapability)
}

// unsupportedCapabilityEmptyResult 返回表示不支持能力的空列表响应信封。
func unsupportedCapabilityEmptyResult(capability string, languageIDs ...string) emptyListEnvelope {
	return emptyListEnvelope{
		Success: true,
		Data:    []any{},
		Meta: resultMeta{
			Count:   0,
			Message: unsupportedCapabilityMessage(capability, languageIDs...),
		},
	}
}

// unsupportedCapabilityMessage 构建不支持能力的消息，markdown 语言有特殊降级提示。
func unsupportedCapabilityMessage(capability string, languageIDs ...string) string {
	if languageID := limitedDocumentFallbackLanguage(languageIDs...); languageID != "" {
		return fmt.Sprintf("%s is not available for %s. %s support is limited to document_symbol fallback; use structure action=document_symbol file_path=<%s file> or grep action=text_search.", capability, languageID, languageID, languageID)
	}
	return fmt.Sprintf("%s unsupported by current language server", capability)
}

// limitedDocumentFallbackLanguage 返回只支持 document_symbol 降级的语言 ID，不匹配时返回空。
func limitedDocumentFallbackLanguage(languageIDs ...string) string {
	for _, languageID := range languageIDs {
		switch strings.ToLower(strings.TrimSpace(languageID)) {
		case "markdown", "json", "yaml":
			return strings.ToLower(strings.TrimSpace(languageID))
		}
	}
	return ""
}

// implementationTargetError 把"不是有效 implementation 查询目标"的错误包装为 coded error。
func implementationTargetError(err error) error {
	if !isImplementationInvalidTarget(err) {
		return err
	}
	return common.NewCodedToolError(
		"invalid_target",
		err,
		false,
		"next: inspect action=implementation pos=<interface-or-method-field>",
	)
}

// typeHierarchyTargetError 把"不是有效类型名"错误包装为 coded error。
func typeHierarchyTargetError(err error) error {
	if !isTypeHierarchyInvalidTarget(err) {
		return err
	}
	return common.NewCodedToolError(
		"invalid_target",
		err,
		false,
		"next: xref action=type_hierarchy pos=<type-name> direction=supertypes|subtypes",
	)
}

// isImplementationInvalidTarget 通过错误消息文本判断是否为无效 implementation 目标。
func isImplementationInvalidTarget(err error) bool {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(message, "not an implementation query target") {
		return true
	}
	if strings.Contains(message, "function, not a method") {
		return true
	}
	return strings.Contains(message, "implementation") &&
		(strings.Contains(message, "not an interface") || strings.Contains(message, "not interface"))
}

// isTypeHierarchyInvalidTarget 通过错误消息文本判断是否为无效类型层次查询目标。
func isTypeHierarchyInvalidTarget(err error) bool {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "not a type name") ||
		strings.Contains(message, "not a type") ||
		strings.Contains(message, "no type hierarchy item")
}
