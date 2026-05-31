package tools

import (
	"errors"
	"fmt"
	"strings"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func isUnsupportedCapability(err error) bool {
	return errors.Is(err, lspmanager.ErrUnsupportedCapability)
}

func unsupportedCapabilityEmptyResult(capability string) emptyListEnvelope {
	return emptyListEnvelope{
		Success: true,
		Data:    []any{},
		Meta: resultMeta{
			Count:   0,
			Message: fmt.Sprintf("%s unsupported by current language server", capability),
		},
	}
}

func implementationTargetError(err error) error {
	if !isImplementationInvalidTarget(err) {
		return err
	}
	return common.NewCodedToolError(
		"invalid_target",
		err,
		false,
		"Use inspect action=implementation on an interface or method/field with implementation relationships; ordinary functions do not have implementations.",
	)
}

func typeHierarchyTargetError(err error) error {
	if !isTypeHierarchyInvalidTarget(err) {
		return err
	}
	return common.NewCodedToolError(
		"invalid_target",
		err,
		false,
		"Use xref action=type_hierarchy on a type name; use direction=supertypes for parents or direction=subtypes for children.",
	)
}

func isImplementationInvalidTarget(err error) bool {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(message, "not an implementation query target") {
		return true
	}
	return strings.Contains(message, "implementation") &&
		(strings.Contains(message, "not an interface") || strings.Contains(message, "not interface"))
}

func isTypeHierarchyInvalidTarget(err error) bool {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "not a type name") ||
		strings.Contains(message, "not a type") ||
		strings.Contains(message, "no type hierarchy item")
}
