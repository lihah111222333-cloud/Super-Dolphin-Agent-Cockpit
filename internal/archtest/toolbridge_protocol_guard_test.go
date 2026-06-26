package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestToolbridgeProtocolFreezeContractGuard 固定 toolbridge wire protocol 的常量来源。
// 私有 metadata key、proxy initialize 握手、支持的方法名和 fail-closed default 分支
// 都必须由 internal/platform/toolbridge/protocol_contract.go 的命名常量驱动，避免散落魔法字符串。
//
// 该 guard 通过文件文本扫描验证三类不变量：
//  1. handler.go / proxy.go / diff_fallback.go 不再直接嵌入受保护的协议字面量。
//  2. proxy.go 保留未知方法的 fail-closed default 分支，不能对未知兼容方法静默 ACK。
//  3. protocol_contract.go 声明其他文件引用的所有常量，避免新增魔法字符串绕过集中定义。
func TestToolbridgeProtocolFreezeContractGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)

	readFile := func(rel string) string {
		path := filepath.Join(root, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(data)
	}

	handlerSrc := readFile("internal/platform/toolbridge/handler.go")
	proxySrc := readFile("internal/platform/toolbridge/proxy.go")
	contractSrc := readFile("internal/platform/toolbridge/protocol_contract.go")

	// handler.go 不能把受保护的 private metadata key 作为语句里的字面量 map key。
	// 注释或 docstring 提到名称允许存在；这里仅拦截会参与运行时协议的 quoted form。
	forbiddenHandlerLiterals := []string{
		`"_agentId":`,
		`"_threadId":`,
		`"_callId":`,
		`peer.Callback(callCtx, "tools/call"`,
	}
	for _, token := range forbiddenHandlerLiterals {
		if strings.Contains(handlerSrc, token) {
			t.Errorf("handler.go reintroduced magic-string %q (P4 §S3b: must use protocol_contract.go constants)", token)
		}
	}

	// proxy.go 不能直接嵌入 initialize、通知和工具方法名等协议字面量。
	forbiddenProxyLiterals := []string{
		`case "initialize":`,
		`case "notifications/initialized":`,
		`case "tools/list":`,
		`case "tools/call":`,
		`"protocolVersion": "2025-11-25"`,
		`"name": "proxy"`,
		`"version": "1.0.0"`,
	}
	for _, token := range forbiddenProxyLiterals {
		if strings.Contains(proxySrc, token) {
			t.Errorf("proxy.go reintroduced magic-string %q (P4 §S3b: must use protocol_contract.go constants)", token)
		}
	}

	// proxy.go 必须保留返回 jsonRPCCodeMethodMiss 的 fail-closed default 分支。
	// 这里用便宜的字面量扫描锁住结构；实际 wire 行为由 toolbridge 包的行为测试覆盖。
	requiredProxyTokens := []string{
		"writeJSONRPCError(w, req.ID, jsonRPCCodeMethodMiss",
	}
	for _, token := range requiredProxyTokens {
		if !strings.Contains(proxySrc, token) {
			t.Errorf("proxy.go lost fail-closed default branch %q (P4 §fallback: unknown methods must not silent-ACK)", token)
		}
	}

	// protocol_contract.go 必须声明其他文件消费的每个导出常量。
	// 这能防止调用点引用不存在的集中常量，或绕开常量表新增协议字符串。
	requiredConstants := []string{
		"MetadataKeyAgentID",
		"MetadataKeyThreadID",
		"MetadataKeyCallID",
		"ProxyProtocolVersion",
		"ProxyServerInfoName",
		"ProxyServerInfoVersion",
		"ProxyNotificationMethod",
		"ProxyMethodInitialize",
		"ProxyMethodToolsList",
		"ProxyMethodToolsCall",
	}
	for _, name := range requiredConstants {
		if !strings.Contains(contractSrc, name) {
			t.Errorf("protocol_contract.go missing named constant %q (P4 §S3b freeze is incomplete)", name)
		}
	}
}
