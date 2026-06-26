package rpc

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// approvalMethodCatalogSpec 描述审批回调方法的默认值、兼容别名和允许 push 的方法集。
type approvalMethodCatalogSpec struct {
	defaultCallback     string
	requestUserInput    string
	aliases             map[string]string
	pushEligibleMethods map[string]struct{}
}

// approvalMethodCatalog 是审批回调路由的集中配置。
var approvalMethodCatalog = approvalMethodCatalogSpec{
	defaultCallback:  DefaultApprovalCallbackMethod,
	requestUserInput: approvalCallbackMethodCommandExecution,
	aliases: map[string]string{
		legacyApprovalCallbackMethod:     DefaultApprovalCallbackMethod,
		legacyApprovalEventMethod:        approvalCallbackMethodCommandExecution,
		"codex/event/request_user_input": approvalCallbackMethodCommandExecution,
		"item/tool/request_user_input":   approvalCallbackMethodCommandExecution,
		"item/tool/requestUserInput":     approvalCallbackMethodCommandExecution,
		"request_user_input":             approvalCallbackMethodCommandExecution,
	},
	pushEligibleMethods: map[string]struct{}{
		DefaultApprovalCallbackMethod:          {},
		approvalCallbackMethodCommandExecution: {},
		approvalCallbackMethodFileChange:       {},
		approvalCallbackMethodSkillRequest:     {},
	},
}

// approvedDecision 构造自动批准决策。
func approvedDecision() contract.ApprovalDecision {
	return contract.ApprovalDecision{
		Approved: boolPtr(true),
		Reason:   "auto_approved",
	}
}

// declinedDecision 构造拒绝决策，reason 为空时使用稳定默认值。
func declinedDecision(reason string) contract.ApprovalDecision {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "decline"
	}
	return contract.ApprovalDecision{
		Approved: boolPtr(false),
		Reason:   reason,
	}
}

// errorDecision 把错误转换为审批决策原因。
func errorDecision(err error) contract.ApprovalDecision {
	return contract.ApprovalDecision{
		Reason: decisionReason(contract.ApprovalDecision{}, err),
	}
}

// normalize 把兼容方法别名标准化为当前回调方法名。
func (c approvalMethodCatalogSpec) normalize(method string) string {
	method = strings.TrimSpace(method)
	if method == "" {
		return ""
	}
	if alias, ok := c.aliases[method]; ok {
		return alias
	}
	return method
}

// callback 根据请求显式方法、来源方法和 kind 选择审批回调方法。
func (c approvalMethodCatalogSpec) callback(req ApprovalRequest) string {
	for _, candidate := range []string{req.CallbackMethod, req.SourceMethod} {
		if method := c.normalize(candidate); method != "" {
			return method
		}
	}
	if isRequestUserInputKind(req.Kind) {
		return c.requestUserInput
	}
	return c.defaultCallback
}

// isPushMethod 判断方法是否允许从 raw provider 事件转为 push。
func (c approvalMethodCatalogSpec) isPushMethod(method string) bool {
	method = c.normalize(method)
	if method == "" {
		return false
	}
	_, ok := c.pushEligibleMethods[method]
	return ok
}

// isExpectedCloseErr 判断错误是否属于连接关闭或上下文取消的预期退出。
func isExpectedCloseErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, channel.ErrClosed) ||
		channel.IsErrClosing(err)
}

// rpcError 构造不带 data 的 jrpc2 错误，并裁剪空白消息。
func rpcError(code int, msg string) error {
	return jrpc2.Errorf(jrpc2.Code(code), "%s", strings.TrimSpace(msg))
}

// rpcErrorData 构造带 data 的 jrpc2 错误；data 为空时保持普通错误形态。
func rpcErrorData(code int, msg string, data map[string]any) error {
	rpcErr := jrpc2.Errorf(jrpc2.Code(code), "%s", strings.TrimSpace(msg))
	if len(data) == 0 {
		return rpcErr
	}
	return rpcErr.WithData(data)
}
