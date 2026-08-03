package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

const portableGoVersion = "go1.26.5"

type portableGoAsset struct {
	URL    string
	SHA256 string
	Size   int64
}

// portableGoAssets 是唯一内置的生产下载清单，值来自 go.dev/dl。
var portableGoAssets = map[string]portableGoAsset{
	"darwin/amd64": {"https://go.dev/dl/go1.26.5.darwin-amd64.tar.gz", "6231d8d3b8f5552ec6cbf6d685bdd5482e1e703214b120e89b3bf0d7bf1ef725", 67836304},
	"darwin/arm64": {"https://go.dev/dl/go1.26.5.darwin-arm64.tar.gz", "efb87ff28af9a188d0536ef5d42e63dd52ba8263cd7344a993cc48dd11dedb6a", 64738542},
	"linux/amd64":  {"https://go.dev/dl/go1.26.5.linux-amd64.tar.gz", "5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053", 66879095},
}

type portableGoReady struct {
	ArchiveSHA256      string `json:"archive_sha256"`
	DistributionDigest string `json:"distribution_digest"`
}

// bootstrapProductionGoToolchain 在常规发现耗尽后安装精确匹配首选版本的官方 Go。
func bootstrapProductionGoToolchain(requirement productionGoRequirement) (productionGoToolchain, error) {
	if requirement.Preferred != portableGoVersion {
		return productionGoToolchain{}, fmt.Errorf("portable Go bootstrap only supports preferred %s, got %s", portableGoVersion, requirement.Preferred)
	}
	asset, err := portableGoAssetForPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return productionGoToolchain{}, err
	}
	root, err := portableGoUserRoot()
	if err != nil {
		return productionGoToolchain{}, err
	}
	install := filepath.Join(root, portableGoVersion, runtime.GOOS+"-"+runtime.GOARCH)
	if toolchain, ok := exactReusablePortableGo(install, asset, requirement); ok {
		return toolchain, nil
	}
	lockContext, cancel := gateprivate.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	lock, err := gateprivate.AcquireExclusiveFileLock(lockContext, filepath.Join(root, "."+portableGoVersion+"-"+runtime.GOOS+"-"+runtime.GOARCH+".lock"))
	if err != nil {
		return productionGoToolchain{}, fmt.Errorf("lock portable Go installation: %w", err)
	}
	defer lock.Release()
	if err := recoverPortableGoBackup(install, asset, requirement); err != nil {
		return productionGoToolchain{}, err
	}
	if toolchain, ok := exactReusablePortableGo(install, asset, requirement); ok {
		return toolchain, nil
	}
	return installPortableGo(root, install, asset, requirement)
}

// portableGoAssetForPlatform 返回受支持本机平台的唯一归档资产。
func portableGoAssetForPlatform(goos, goarch string) (portableGoAsset, error) {
	asset, ok := portableGoAssets[goos+"/"+goarch]
	if !ok {
		return portableGoAsset{}, fmt.Errorf("portable Go bootstrap is unsupported on %s/%s", goos, goarch)
	}
	return asset, nil
}

// portableGoUserRoot 创建并校验仅当前用户可写的工具链根目录。
func portableGoUserRoot() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil || !filepath.IsAbs(cache) {
		return "", fmt.Errorf("resolve user cache directory: %w", err)
	}
	root := filepath.Join(cache, "super-dolphin-gate", "toolchains")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create portable Go user directory: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", fmt.Errorf("protect portable Go user directory: %w", err)
	}
	return canonicalProductionGoDirectory("portable Go user directory", root, false)
}

// reusablePortableGo 复验已发布工具链的归档绑定、身份和发行版摘要。
func reusablePortableGo(install string, asset portableGoAsset) (productionGoToolchain, bool) {
	readyPath := filepath.Join(install, "ready.json")
	data, err := os.ReadFile(readyPath)
	if err != nil {
		return productionGoToolchain{}, false
	}
	var ready portableGoReady
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&ready) != nil || decoder.Decode(&struct{}{}) != io.EOF || ready.ArchiveSHA256 != asset.SHA256 || ready.DistributionDigest == "" {
		return productionGoToolchain{}, false
	}
	toolchain, err := probeProductionGoToolchain(filepath.Join(install, "go", "bin", "go"), liveProductionGoResolverDeps())
	if err != nil || toolchain.GoRoot != filepath.Join(install, "go") {
		return productionGoToolchain{}, false
	}
	digest, err := productionGoDistributionDigest(toolchain.GoRoot)
	if err != nil || digest != ready.DistributionDigest {
		return productionGoToolchain{}, false
	}
	return toolchain, true
}

// exactReusablePortableGo 同时要求缓存身份有效且版本精确匹配候选首选值。
func exactReusablePortableGo(install string, asset portableGoAsset, requirement productionGoRequirement) (productionGoToolchain, bool) {
	toolchain, ok := reusablePortableGo(install, asset)
	return toolchain, ok && validateProductionGoToolchainRequirement(toolchain, requirement) == nil
}

// installPortableGo 在私有 staging 中完成验证后原子发布工具链。
func installPortableGo(root, install string, asset portableGoAsset, requirement productionGoRequirement) (productionGoToolchain, error) {
	staging, err := os.MkdirTemp(root, ".go-staging-")
	if err != nil {
		return productionGoToolchain{}, fmt.Errorf("create portable Go staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	content, err := stagePortableGo(staging, asset, requirement)
	if err != nil {
		return productionGoToolchain{}, err
	}
	if err := publishPortableGo(content, install, asset, requirement); err != nil {
		return productionGoToolchain{}, err
	}
	return probeProductionGoToolchain(filepath.Join(install, "go", "bin", "go"), liveProductionGoResolverDeps())
}

// stagePortableGo 下载、解包并验证待发布的 Go 发行版。
func stagePortableGo(staging string, asset portableGoAsset, requirement productionGoRequirement) (string, error) {
	if err := os.Chmod(staging, 0o700); err != nil {
		return "", err
	}
	archive := filepath.Join(staging, "archive.tar.gz")
	if err := downloadPortableGo(asset, archive); err != nil {
		return "", err
	}
	content := filepath.Join(staging, "content")
	if err := extractPortableGoArchive(archive, content); err != nil {
		return "", err
	}
	if err := validateStagedPortableGo(filepath.Join(content, "go"), asset, requirement); err != nil {
		return "", err
	}
	if err := syncPortableGoTree(content); err != nil {
		return "", err
	}
	return content, nil
}

// validateStagedPortableGo 校验版本、可执行身份、需求版本和发行版摘要，并写入 ready 记录。
func validateStagedPortableGo(goRoot string, asset portableGoAsset, requirement productionGoRequirement) error {
	if err := validatePortableGoVersion(goRoot); err != nil {
		return err
	}
	toolchain, err := probeProductionGoToolchain(filepath.Join(goRoot, "bin", "go"), liveProductionGoResolverDeps())
	if err != nil || toolchain.GoRoot != goRoot {
		return fmt.Errorf("validate staged portable Go identity: %w", err)
	}
	if err := validateProductionGoToolchainRequirement(toolchain, requirement); err != nil {
		return err
	}
	digest, err := productionGoDistributionDigest(goRoot)
	if err != nil {
		return err
	}
	ready, err := json.Marshal(portableGoReady{ArchiveSHA256: asset.SHA256, DistributionDigest: digest})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(filepath.Dir(goRoot), "ready.json"), ready, 0o600)
}

// publishPortableGo 将已验证 staging 内容发布到最终私有目录，并同步父目录元数据。
func publishPortableGo(content, install string, asset portableGoAsset, requirement productionGoRequirement) error {
	backup := install + ".previous"
	if err := preparePortableGoBackup(install, backup); err != nil {
		return err
	}
	if err := os.Rename(content, install); err != nil {
		rollbackErr := os.Rename(backup, install)
		return errors.Join(fmt.Errorf("publish portable Go: %w", err), rollbackErr)
	}
	if err := syncPortableGoParent(filepath.Dir(install)); err != nil {
		return err
	}
	if _, reusable := exactReusablePortableGo(install, asset, requirement); !reusable {
		removeErr := os.RemoveAll(install)
		rollbackErr := os.Rename(backup, install)
		return errors.Join(errors.New("published portable Go failed durable identity verification"), removeErr, rollbackErr)
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove previous portable Go backup: %w", err)
	}
	return syncPortableGoParent(filepath.Dir(install))
}

// preparePortableGoBackup 准备私有版本目录并将当前安装原子移到回滚位置。
func preparePortableGoBackup(install, backup string) error {
	parent := filepath.Dir(install)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return fmt.Errorf("protect portable Go version directory: %w", err)
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove stale portable Go backup: %w", err)
	}
	exists, err := portableGoPathExists(install)
	if err != nil || !exists {
		return err
	}
	if err := os.Rename(install, backup); err != nil {
		return fmt.Errorf("backup previous portable Go installation: %w", err)
	}
	return nil
}

// recoverPortableGoBackup 在当前安装缺失或损坏时恢复发布中断留下的上一份工具链。
func recoverPortableGoBackup(install string, asset portableGoAsset, requirement productionGoRequirement) error {
	backup := install + ".previous"
	installExists, err := portableGoPathExists(install)
	if err != nil {
		return err
	}
	if installExists {
		if _, reusable := exactReusablePortableGo(install, asset, requirement); reusable {
			return nil
		}
	}
	backupExists, err := portableGoPathExists(backup)
	if err != nil || !backupExists {
		return err
	}
	return restorePortableGoBackup(install, backup, installExists)
}

// portableGoPathExists 在拒绝其他文件系统错误的同时判断路径是否存在。
func portableGoPathExists(filePath string) (bool, error) {
	if _, err := os.Lstat(filePath); err == nil {
		return true, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else {
		return false, fmt.Errorf("inspect portable Go path %q: %w", filePath, err)
	}
}

// restorePortableGoBackup 隔离损坏目录并恢复上一份发布。
func restorePortableGoBackup(install, backup string, installExists bool) error {
	broken := install + ".broken"
	if err := os.RemoveAll(broken); err != nil {
		return fmt.Errorf("remove stale portable Go broken directory: %w", err)
	}
	if installExists {
		if err := os.Rename(install, broken); err != nil {
			return fmt.Errorf("quarantine broken portable Go installation: %w", err)
		}
	}
	if err := os.Rename(backup, install); err != nil {
		return fmt.Errorf("recover portable Go backup: %w", err)
	}
	if err := syncPortableGoParent(filepath.Dir(install)); err != nil {
		return err
	}
	return os.RemoveAll(broken)
}

// syncPortableGoTree 在发布前持久化 staging 中的全部普通文件和目录元数据。
func syncPortableGoTree(root string) error {
	directories := make([]string, 0)
	err := filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, filePath)
			return nil
		}
		if entry.Type().IsRegular() {
			return syncPortableGoPath(filePath)
		}
		return fmt.Errorf("portable Go staging contains unsupported entry %q", filePath)
	})
	if err != nil {
		return fmt.Errorf("sync portable Go staging files: %w", err)
	}
	sort.Slice(directories, func(left, right int) bool { return len(directories[left]) > len(directories[right]) })
	for _, directory := range directories {
		if err := syncPortableGoPath(directory); err != nil {
			return fmt.Errorf("sync portable Go staging directory: %w", err)
		}
	}
	return nil
}

// syncPortableGoPath 持久化一个普通文件或目录并关闭描述符。
func syncPortableGoPath(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

// syncPortableGoParent 将目录重命名持久化到父目录。
func syncPortableGoParent(directory string) error {
	parent, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open portable Go parent directory: %w", err)
	}
	if err := errors.Join(parent.Sync(), parent.Close()); err != nil {
		return fmt.Errorf("sync portable Go parent directory: %w", err)
	}
	return nil
}

// downloadPortableGo 流式下载并同时校验归档的精确大小和 SHA-256。
func downloadPortableGo(asset portableGoAsset, destination string) error {
	client := &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if !portableGoRedirectAllowed(asset, request, via) {
				return errors.New("portable Go archive redirect is not allowlisted")
			}
			return nil
		},
	}
	response, err := client.Get(asset.URL)
	if err != nil {
		return fmt.Errorf("download portable Go archive: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength != asset.Size {
		return fmt.Errorf("portable Go archive response is not the expected size")
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), response.Body)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written != asset.Size || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		return errors.New("portable Go archive size or SHA-256 verification failed")
	}
	return nil
}

// portableGoRedirectAllowed 仅允许 go.dev 到固定 Google 下载地址的一次 HTTPS 重定向。
func portableGoRedirectAllowed(asset portableGoAsset, request *http.Request, via []*http.Request) bool {
	filename := path.Base(asset.URL)
	return len(via) == 1 && request.URL.Scheme == "https" && request.URL.Host == "dl.google.com" &&
		request.URL.Path == "/go/"+filename && request.URL.RawQuery == "" && request.URL.Fragment == ""
}

// extractPortableGoArchive 将可信归档解压到 staging，逐项执行路径和类型校验。
func extractPortableGoArchive(archive, destination string) error {
	input, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer input.Close()
	gzipReader, err := gzip.NewReader(input)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	for {
		header, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
		if err := extractPortableGoEntry(reader, header, destination); err != nil {
			return err
		}
	}
	return nil
}

// extractPortableGoEntry 校验单个 tar 条目并仅写入目录或常规文件。
func extractPortableGoEntry(reader *tar.Reader, header *tar.Header, destination string) error {
	target, err := portableGoArchiveTarget(header, destination)
	if err != nil {
		return err
	}
	switch header.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o700)
	case tar.TypeReg, tar.TypeRegA:
		return writePortableGoArchiveFile(reader, target)
	default:
		return fmt.Errorf("portable Go archive contains forbidden entry type for %q", header.Name)
	}
}

// portableGoArchiveTarget 返回经过顶层和逃逸检查后的 staging 目标路径。
func portableGoArchiveTarget(header *tar.Header, destination string) (string, error) {
	name := path.Clean(header.Name)
	if (name != "go" && !strings.HasPrefix(name, "go/")) || strings.HasPrefix(name, "../") || path.IsAbs(name) || strings.Contains(name, "\x00") {
		return "", fmt.Errorf("portable Go archive entry is unsafe: %q", header.Name)
	}
	if name == "go" && header.Typeflag != tar.TypeDir {
		return "", fmt.Errorf("portable Go archive top-level entry is invalid: %q", header.Name)
	}
	target := filepath.Join(destination, filepath.FromSlash(name))
	if relative, err := filepath.Rel(destination, target); err != nil || strings.HasPrefix(relative, "..") {
		return "", errors.New("portable Go archive entry escapes staging")
	}
	return target, nil
}

// writePortableGoArchiveFile 以独占方式写入普通归档文件，拒绝覆盖已有路径。
func writePortableGoArchiveFile(reader *tar.Reader, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, reader)
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return errors.New("write portable Go archive entry")
	}
	return nil
}

// validatePortableGoVersion 校验 staging 发行版版本文件和 Go 二进制可执行位。
func validatePortableGoVersion(goRoot string) error {
	version, err := os.ReadFile(filepath.Join(goRoot, "VERSION"))
	if err != nil || strings.SplitN(string(version), "\n", 2)[0] != portableGoVersion {
		return errors.New("staged portable Go VERSION is invalid")
	}
	info, err := os.Stat(filepath.Join(goRoot, "bin", "go"))
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return errors.New("staged portable Go executable is invalid")
	}
	return nil
}
