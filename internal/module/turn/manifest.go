package turn

import (
	"os"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/discovery"
)

// manifestBuilder 封装 provider manifest 构建函数，并补充 turn 运行时的二进制目录。
type manifestBuilder struct {
	binaryDir string
	buildFn   contract.ManifestBuildFunc
}

// newManifestBuilder 创建 manifest 构建器；buildFn 由上层注入以便测试替换。
func newManifestBuilder(binaryDir string, buildFn contract.ManifestBuildFunc) *manifestBuilder {
	return &manifestBuilder{
		binaryDir: strings.TrimSpace(binaryDir),
		buildFn:   buildFn,
	}
}

// Build 将 PrepareInput 中的工作区、MCP 快照和 peer 地址汇总成 provider 可消费的 manifest。
func (b *manifestBuilder) Build(input PrepareInput, threadID string) dto.MCPManifest {
	peerAddrs, peerTokens := discoverPeers()
	return b.buildFn(dto.ManifestContext{
		AgentID:                      input.AgentID,
		ThreadID:                     threadID,
		CWD:                          input.CWD,
		AdditionalWorkingDirectories: append([]string(nil), input.AdditionalWorkingDirectories...),
		ThreadCaps:                   input.ThreadCaps,
		BinaryDir:                    b.binaryDirFor(input.BinaryDir),
		ExtraBinaries:                mcpServerConfigBinaries(input.MCPSnapshot.ServerConfigs),
		PeerHTTPAddrs:                peerAddrs,
		PeerHTTPTokens:               peerTokens,
		TransportMode:                dto.ManifestTransportStdioOnly,
	})
}

// binaryDirFor 优先使用 turn 输入里的二进制目录，缺省时回退到服务启动时配置。
func (b *manifestBuilder) binaryDirFor(binaryDir string) string {
	if binaryDir = strings.TrimSpace(binaryDir); binaryDir != "" {
		return binaryDir
	}
	return strings.TrimSpace(b.binaryDir)
}

// mcpServerConfigBinaries 把线程运行时 MCP server 配置转成稳定排序的 manifest binary 列表。
func mcpServerConfigBinaries(configs map[string]contract.MCPServerConfig) []dto.MCPBinary {
	if len(configs) == 0 {
		return nil
	}
	names := turnMCPServerConfigNames(configs)
	sort.Strings(names)
	binaries := make([]dto.MCPBinary, 0, len(names))
	for _, name := range names {
		config := configs[name]
		if binary, ok := mcpServerConfigBinary(name, config); ok {
			binaries = append(binaries, binary)
		}
	}
	return binaries
}

// mcpServerConfigBinary 按 transport 构造 HTTP 或 stdio binary，无效 stdio 命令会被跳过。
func mcpServerConfigBinary(name string, config contract.MCPServerConfig) (dto.MCPBinary, bool) {
	switch strings.ToLower(strings.TrimSpace(config.Transport)) {
	case "http":
		return dto.MCPBinary{
			Name:    name,
			Type:    "http",
			URL:     strings.TrimSpace(config.URL),
			Headers: cloneMCPServerHeaders(config.Headers),
		}, true
	case "stdio":
		command := strings.TrimSpace(config.Command)
		if command == "" {
			return dto.MCPBinary{}, false
		}
		return dto.MCPBinary{
			Name:    name,
			Command: append([]string{command}, cloneMCPServerArgs(config.Args)...),
			Env:     cloneMCPServerHeaders(config.Env),
		}, true
	default:
		return dto.MCPBinary{}, false
	}
}

// cloneMCPServerArgs 复制 stdio 参数并去掉空白项，避免 manifest 带入不可见空参数。
func cloneMCPServerArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg = strings.TrimSpace(arg); arg != "" {
			out = append(out, arg)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// cloneMCPServerHeaders 复制 HTTP headers 或 stdio env，并清理空白 key/value。
func cloneMCPServerHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sessionTokenFromEnv 按新旧环境变量顺序读取控制平面 session token。
// 与 bootstrap.SessionTokenFromEnv 语义相同，但避免 module/turn 对 mcpserver 的越层依赖。
func sessionTokenFromEnv() string {
	for _, key := range []string{"GO_AGENT_CTL_SESSION_TOKEN", "GO_AGENT_MCP_SESSION_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

// discoverPeers 探测已启动的 LSP/Orch peer HTTP 端点，并附带同一 session token。
func discoverPeers() (map[dto.ToolFamily]string, map[dto.ToolFamily]string) {
	token := sessionTokenFromEnv()
	addrs := make(map[dto.ToolFamily]string)
	tokens := make(map[dto.ToolFamily]string)
	for _, fam := range []dto.ToolFamily{dto.FamilyLSP, dto.FamilyOrch} {
		addr, err := discovery.DiscoverPeerHTTPAddrWithToken("mcp-"+string(fam), token)
		if err != nil || addr == "" {
			continue
		}
		addrs[fam] = addr
		if token != "" {
			tokens[fam] = token
		}
	}
	if len(addrs) == 0 {
		return nil, nil
	}
	if len(tokens) == 0 {
		return addrs, nil
	}
	return addrs, tokens
}
