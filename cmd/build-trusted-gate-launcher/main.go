package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/trustedlauncher"
)

// main 从精确 Git tree 构建并安装当前 OS 账户专属的受信 Gate launcher。
func main() {
	repository := flag.String("repository", "", "repository root")
	tree := flag.String("tree", "", "exact staged tree")
	flag.Parse()
	if flag.NArg() != 0 || *repository == "" || *tree == "" {
		slog.Error("构建受信 Gate launcher 需要 repository 与 tree")
		os.Exit(2)
	}
	installRoot, err := trustedlauncher.CurrentUserInstallRoot()
	if err != nil {
		slog.Error("解析受信 Gate launcher 安装根失败", "error", err)
		os.Exit(1)
	}
	result, err := trustedlauncher.Build(context.Background(), trustedlauncher.BuildOptions{
		RepositoryRoot: *repository,
		Tree:           *tree,
		InstallRoot:    installRoot,
	})
	if err != nil {
		slog.Error("构建受信 Gate launcher 失败", "error", err)
		os.Exit(1)
	}
	fmt.Println(result.BinaryPath)
}
