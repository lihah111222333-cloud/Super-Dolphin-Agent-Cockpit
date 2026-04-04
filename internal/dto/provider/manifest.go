package provider

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
)

type ToolFamily string

const (
	FamilyLSP  ToolFamily = "lsp"
	FamilyOrch ToolFamily = "orch"
	FamilyIDA  ToolFamily = "ida"
)

type MCPBinary struct {
	Name        string            `json:"name"`
	Command     []string          `json:"command"`
	Env         map[string]string `json:"env,omitempty"`
	AutoApprove []string          `json:"autoApprove,omitempty"`
}

type MCPManifest struct {
	Binaries []MCPBinary `json:"binaries,omitempty"`
}

type ManifestContext struct {
	AgentID     string
	CWD         string
	ThreadCaps  CapabilitySet
	BinaryDir   string
	Env         map[string]string
	AutoApprove []string
}

// BuildManifest returns declarative MCP binary metadata for external executors
// such as Claude CLI. The core process builds this manifest but never launches
// MCP binaries itself.
func BuildManifest(ctx ManifestContext) MCPManifest {
	families := []ToolFamily{FamilyLSP, FamilyOrch}
	if ctx.ThreadCaps.Has("ida") {
		families = append(families, FamilyIDA)
	}

	env := normalizeManifestEnv(ctx.Env)
	autoApprove := append([]string(nil), ctx.AutoApprove...)

	bins := make([]MCPBinary, 0, len(families))
	for _, fam := range families {
		binaryName := "mcp-" + string(fam)
		// Use the short family name (e.g. "lsp", "orch") as the MCP server
		// name so the codex CLI produces concise tool names like
		// mcp__lsp__lsp_grep instead of the redundant mcp__mcp-lsp__lsp_grep.
		serverName := string(fam)
		bins = append(bins, MCPBinary{
			Name:        serverName,
			Command:     []string{filepath.Join(ctx.BinaryDir, binaryName)},
			Env:         cloneManifestEnv(env),
			AutoApprove: append([]string(nil), autoApprove...),
		})
	}
	return MCPManifest{Binaries: bins}
}

func cloneManifestEnv(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

// mcpRequiredEnvKeys lists the canonical env vars that MCP binaries need to
// connect back to the control plane.
var mcpRequiredEnvKeys = []string{
	"GO_AGENT_CTL_RPC_ADDR",
	"GO_AGENT_CTL_INSTANCE_ID",
	"GO_AGENT_CTL_BOOT_ID",
	"GO_AGENT_CTL_BINARY_NAME",
	"GO_AGENT_CTL_CLIENT_KIND",
	"GO_AGENT_CTL_AGENT_ID",
	"GO_AGENT_CTL_THREAD_ID",
	"GO_AGENT_CTL_SESSION_TOKEN",
	"GO_AGENT_CTL_BOOTSTRAP_JSON",
}

var mcpLegacyEnvAliases = map[string][]string{
	"GO_AGENT_CTL_RPC_ADDR":       {"RPC_ADDR"},
	"GO_AGENT_CTL_INSTANCE_ID":    {"GO_AGENT_MCP_INSTANCE_ID"},
	"GO_AGENT_CTL_BOOT_ID":        {"GO_AGENT_MCP_BOOT_ID"},
	"GO_AGENT_CTL_BINARY_NAME":    {"GO_AGENT_MCP_BINARY_NAME"},
	"GO_AGENT_CTL_CLIENT_KIND":    {"GO_AGENT_MCP_CLIENT_KIND"},
	"GO_AGENT_CTL_AGENT_ID":       {"GO_AGENT_MCP_AGENT_ID"},
	"GO_AGENT_CTL_THREAD_ID":      {"GO_AGENT_MCP_THREAD_ID"},
	"GO_AGENT_CTL_SESSION_TOKEN":  {"GO_AGENT_MCP_SESSION_TOKEN"},
	"GO_AGENT_CTL_BOOTSTRAP_JSON": {"GO_AGENT_MCP_BOOT_CONTEXT"},
}

// normalizeManifestEnv ensures MCP binaries get all required GO_AGENT_CTL_*
// env vars. When the caller does not supply explicit values (req.Config["env"]
// is empty), it auto-collects them from the current process environment.
// This fixes both Claude (where CLI inherits parent env but JSON env may be
// empty) and Codex (where app-server reads config.toml env sub-table).
func normalizeManifestEnv(in map[string]string) map[string]string {
	out := cloneManifestEnv(in)
	for key, aliases := range mcpLegacyEnvAliases {
		promoteManifestEnv(out, key, aliases...)
	}
	for _, key := range mcpRequiredEnvKeys {
		if value := strings.TrimSpace(out[key]); value != "" {
			continue
		}
		if val := strings.TrimSpace(os.Getenv(key)); val != "" {
			out[key] = val
			continue
		}
		for _, alias := range mcpLegacyEnvAliases[key] {
			if val := strings.TrimSpace(os.Getenv(alias)); val != "" {
				out[key] = val
				break
			}
		}
	}
	return out
}

func promoteManifestEnv(env map[string]string, canonical string, aliases ...string) {
	if value := strings.TrimSpace(env[canonical]); value != "" {
		env[canonical] = value
		for _, alias := range aliases {
			delete(env, alias)
		}
		return
	}
	for _, alias := range aliases {
		if value := strings.TrimSpace(env[alias]); value != "" {
			env[canonical] = value
			break
		}
	}
	for _, alias := range aliases {
		delete(env, alias)
	}
}
