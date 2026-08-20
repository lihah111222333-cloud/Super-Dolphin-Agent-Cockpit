package main

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// runtimeServerGraphQLConfigDirArgs 为 graphql-language-service-cli 提供启动时
// 加载 GraphQL Config 所需的文件系统项目根。通用 LSP rootUri 是 file URI，
// CLI 不能把它当作 configDir；缺失该参数时服务会返回空符号和诊断。
func runtimeServerGraphQLConfigDirArgs(command multilsp.ServerCommand, args []string, workspaceRoot string) ([]string, error) {
	base := filepath.Base(strings.TrimSpace(command.Executable))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if !strings.EqualFold(base, "graphql-lsp") {
		return args, nil
	}
	root := strings.TrimSpace(workspaceRoot)
	for index, arg := range args {
		if arg == "--configDir" {
			if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return nil, errors.New("GraphQL LSP --configDir value is empty")
			}
			return slices.Clone(args), nil
		}
		if strings.HasPrefix(arg, "--configDir=") {
			if strings.TrimSpace(strings.TrimPrefix(arg, "--configDir=")) == "" {
				return nil, errors.New("GraphQL LSP --configDir value is empty")
			}
			return slices.Clone(args), nil
		}
	}
	if root == "" {
		return nil, errors.New("GraphQL LSP requires one workspace root for --configDir")
	}
	result := slices.Clone(args)
	return append(result, "--configDir", root), nil
}
