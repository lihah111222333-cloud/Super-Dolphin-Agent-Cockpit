package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"golang.org/x/sync/errgroup"
)

const remoteExpandedArchiveMaxBytes int64 = 20 << 30

type remoteBaselineLayerAction func(
	context.Context,
	string,
	string,
	remoteci.BaselineLayer,
) error

// runRemoteBaselineLayerStage 并发执行互不重叠的 Anchor 层阶段，并按清单顺序返回首个错误。
func runRemoteBaselineLayerStage(
	ctx context.Context,
	cacheRoot string,
	expandedRoot string,
	layers []remoteci.BaselineLayer,
	stage string,
	action remoteBaselineLayerAction,
) error {
	stageCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make([]error, len(layers))
	var group errgroup.Group
	for index := range layers {
		group.Go(func() error {
			layer := layers[index]
			errs[index] = action(stageCtx, filepath.Join(cacheRoot, layer.Archive), expandedRoot, layer)
			if errs[index] != nil {
				cancel()
			}
			return nil
		})
	}
	_ = group.Wait()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s remote baseline layers: %w", stage, err)
	}
	return remoteBaselineLayerStageError(stage, layers, errs)
}

// remoteBaselineLayerStageError 保留非取消错误优先和清单顺序的错误报告语义。
func remoteBaselineLayerStageError(stage string, layers []remoteci.BaselineLayer, errs []error) error {
	if err := remoteBaselineLayerFirstError(stage, layers, errs, true); err != nil {
		return err
	}
	return remoteBaselineLayerFirstError(stage, layers, errs, false)
}

// remoteBaselineLayerFirstError 按清单顺序查找可报告的层错误。
func remoteBaselineLayerFirstError(stage string, layers []remoteci.BaselineLayer, errs []error, skipCanceled bool) error {
	for index, err := range errs {
		if err != nil && (!skipCanceled || !errors.Is(err, context.Canceled)) {
			return fmt.Errorf("%s remote baseline layer %q: %w", stage, layers[index].Name, err)
		}
	}
	return nil
}

// materializeRemoteBaseline 先恢复唯一 Anchor，再按请求顺序应用受验 delta，最后才交给 job patch。
func materializeRemoteBaseline(
	ctx context.Context,
	cacheRoot string,
	expandedRoot string,
	sourceRoot string,
	request remoteci.ShardRequest,
	download remoteObjectDownload,
) error {
	anchor, err := extractRemoteDataCacheBase(ctx, cacheRoot, expandedRoot, request.AnchorManifest)
	if err != nil {
		return err
	}
	if anchor.StorageMode == "" {
		if err := materializeRemoteLegacyBaseline(ctx, expandedRoot, sourceRoot, request); err != nil {
			return err
		}
		return protectRemoteExpandedBaselineRoot(expandedRoot)
	}
	if err := prepareRemoteAnchorBaseline(ctx, expandedRoot, sourceRoot, request, anchor); err != nil {
		return err
	}
	if err := materializeRemoteBaselineDeltas(ctx, expandedRoot, sourceRoot, request.BaselineDeltas, anchor, download); err != nil {
		return err
	}
	if err := verifyRemoteSourceIdentity(ctx, sourceRoot, request.RunnerBaseCommit, request.RunnerBaseTree); err != nil {
		return err
	}
	return protectRemoteExpandedBaselineRoot(expandedRoot)
}

// protectRemoteExpandedBaselineRoot 在只读挂载交接前移除物化根的全部写权限。
func protectRemoteExpandedBaselineRoot(expandedRoot string) error {
	info, err := os.Lstat(expandedRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(errors.New("remote expanded baseline root is not a physical directory"), err)
	}
	if err := os.Chmod(expandedRoot, 0o555); err != nil {
		return fmt.Errorf("protect remote expanded baseline root: %w", err)
	}
	return nil
}

// materializeRemoteLegacyBaseline 保留旧单包基线不带 Anchor delta 的兼容边界。
func materializeRemoteLegacyBaseline(ctx context.Context, expandedRoot, sourceRoot string, request remoteci.ShardRequest) error {
	if len(request.BaselineDeltas) != 0 || request.AnchorManifest != request.BaselineManifest {
		return errors.New("remote legacy baseline cannot carry Anchor deltas")
	}
	return cloneRemoteDataCacheBase(ctx, filepath.Join(expandedRoot, "source"), sourceRoot)
}

// prepareRemoteAnchorBaseline 验证并恢复唯一的 Anchor 基线。
func prepareRemoteAnchorBaseline(ctx context.Context, expandedRoot, sourceRoot string, request remoteci.ShardRequest, anchor remoteci.BaselineManifest) error {
	if anchor.StorageMode != remoteci.BaselineStorageModeAnchor || anchor.Generation != request.AnchorGeneration || anchor.MainCommit != request.AnchorCommit || anchor.MainTree != request.AnchorTree {
		return errors.New("remote Anchor manifest does not match shard request")
	}
	if err := materializeRemoteCacheSeed(expandedRoot, anchor.Generation); err != nil {
		return err
	}
	if err := cloneRemoteDataCacheBase(ctx, filepath.Join(expandedRoot, "source"), sourceRoot); err != nil {
		return err
	}
	if err := verifyRemoteSourceIdentity(ctx, sourceRoot, request.AnchorCommit, request.AnchorTree); err != nil {
		return fmt.Errorf("verify remote Anchor source: %w", err)
	}
	return nil
}

// materializeRemoteBaselineDeltas 按请求顺序应用 delta，并只发布最后一层 CLI。
func materializeRemoteBaselineDeltas(ctx context.Context, expandedRoot, sourceRoot string, deltas []remoteci.BaselineDeltaLayer, anchor remoteci.BaselineManifest, download remoteObjectDownload) error {
	var latestManifest *remoteci.BaselineManifest
	var latestDelta remoteci.BaselineDeltaLayer
	for index, delta := range deltas {
		manifest, err := materializeRemoteBaselineDelta(ctx, expandedRoot, sourceRoot, anchor, delta, download)
		if err != nil {
			return fmt.Errorf("materialize remote baseline delta %d: %w", index, err)
		}
		latestManifest, latestDelta = &manifest, delta
	}
	if latestManifest == nil {
		return nil
	}
	return materializeRemoteDeltaGateBinary(ctx, expandedRoot, latestDelta, *latestManifest, download)
}

// materializeRemoteBaselineDelta 下载同代的 manifest、Git bundle 与缓存层，全部验明后才变更 source。
func materializeRemoteBaselineDelta(ctx context.Context, expandedRoot string, sourceRoot string, anchor remoteci.BaselineManifest, delta remoteci.BaselineDeltaLayer, download remoteObjectDownload) (remoteci.BaselineManifest, error) {
	if download == nil {
		return remoteci.BaselineManifest{}, errors.New("remote object downloader is required")
	}
	staging, err := os.MkdirTemp("", "super-dolphin-baseline-delta-")
	if err != nil {
		return remoteci.BaselineManifest{}, fmt.Errorf("create remote delta staging root: %w", err)
	}
	defer os.RemoveAll(staging)
	paths := newRemoteDeltaPaths(staging, delta.ObjectPrefix)
	manifest, err := loadRemoteDeltaManifest(ctx, download, delta, paths)
	if err != nil {
		return remoteci.BaselineManifest{}, err
	}
	if err := verifyRemoteDeltaManifest(manifest, delta); err != nil {
		return remoteci.BaselineManifest{}, err
	}
	if err := verifyRemoteDeltaCompatibility(anchor, manifest); err != nil {
		return remoteci.BaselineManifest{}, err
	}
	if err := downloadRemoteDeltaLayers(ctx, download, manifest, paths); err != nil {
		return remoteci.BaselineManifest{}, err
	}
	if err := materializeRemoteCacheDelta(ctx, paths.cache, expandedRoot, delta.Generation); err != nil {
		return remoteci.BaselineManifest{}, fmt.Errorf("extract remote cache delta: %w", err)
	}
	if err := verifyRemoteSourceIdentity(ctx, sourceRoot, delta.BaseCommit, delta.BaseTree); err != nil {
		return remoteci.BaselineManifest{}, fmt.Errorf("verify remote delta base: %w", err)
	}
	if err := applyRemoteSourceBundle(ctx, sourceRoot, paths.bundle, delta.MainCommit, delta.MainTree); err != nil {
		return remoteci.BaselineManifest{}, err
	}
	return manifest, nil
}

type remoteDeltaPaths struct{ manifest, bundle, cache, prefix string }

// newRemoteDeltaPaths 汇总同一个 delta 暂存目录及对象前缀。
func newRemoteDeltaPaths(staging, objectPrefix string) remoteDeltaPaths {
	return remoteDeltaPaths{manifest: filepath.Join(staging, "baseline-manifest.json"), bundle: filepath.Join(staging, "source.delta.bundle"), cache: filepath.Join(staging, "go-build-cache.delta.tar.gz"), prefix: path.Join(strings.TrimSuffix(objectPrefix, "/"), "output")}
}

// loadRemoteDeltaManifest 下载并解码当前 generation 的唯一 manifest。
func loadRemoteDeltaManifest(ctx context.Context, download remoteObjectDownload, delta remoteci.BaselineDeltaLayer, paths remoteDeltaPaths) (remoteci.BaselineManifest, error) {
	if err := downloadVerifiedFile(ctx, download, path.Join(paths.prefix, "baseline-manifest.json"), delta.ManifestDigest, remoteManifestMaxBytes, paths.manifest); err != nil {
		return remoteci.BaselineManifest{}, fmt.Errorf("download remote delta manifest: %w", err)
	}
	data, err := os.ReadFile(paths.manifest)
	if err != nil {
		return remoteci.BaselineManifest{}, fmt.Errorf("read remote delta manifest: %w", err)
	}
	return remoteci.DecodeBaselineManifest(data)
}

// downloadRemoteDeltaLayers 验证下载 source bundle 和 go-build-cache layer。
func downloadRemoteDeltaLayers(ctx context.Context, download remoteObjectDownload, manifest remoteci.BaselineManifest, paths remoteDeltaPaths) error {
	if err := downloadVerifiedFile(ctx, download, path.Join(paths.prefix, "source.delta.bundle"), manifest.Layers[0].SHA256, manifest.Layers[0].Size, paths.bundle); err != nil {
		return fmt.Errorf("download remote source delta: %w", err)
	}
	if err := downloadVerifiedFile(ctx, download, path.Join(paths.prefix, "go-build-cache.delta.tar.gz"), manifest.Layers[1].SHA256, manifest.Layers[1].Size, paths.cache); err != nil {
		return fmt.Errorf("download remote cache delta: %w", err)
	}
	return nil
}

// verifyRemoteDeltaManifest 将 OSS 对象和 source 迁移严格绑定到请求中的唯一 generation。
func verifyRemoteDeltaManifest(manifest remoteci.BaselineManifest, delta remoteci.BaselineDeltaLayer) error {
	if manifest.StorageMode != remoteci.BaselineStorageModeDelta || manifest.Generation != delta.Generation ||
		manifest.MainCommit != delta.MainCommit || manifest.MainTree != delta.MainTree || len(manifest.Layers) != 2 {
		return errors.New("remote delta manifest does not match requested generation")
	}
	sourceLayer := manifest.Layers[0]
	if sourceLayer.BaseCommit != delta.BaseCommit || sourceLayer.BaseTree != delta.BaseTree ||
		sourceLayer.TargetCommit != delta.MainCommit || sourceLayer.TargetTree != delta.MainTree {
		return errors.New("remote delta source transition does not match shard request")
	}
	return nil
}

// verifyRemoteDeltaCompatibility 拒绝 delta 改变 Anchor 固定运行时身份。
func verifyRemoteDeltaCompatibility(anchor, delta remoteci.BaselineManifest) error {
	if delta.Platform != anchor.Platform || delta.ToolchainDigest != anchor.ToolchainDigest || delta.RuntimeImage != anchor.RuntimeImage ||
		delta.RuntimeSeedManifestSHA256 != anchor.RuntimeSeedManifestSHA256 ||
		delta.CABundleSHA256 != anchor.CABundleSHA256 || delta.CABundleSize != anchor.CABundleSize {
		return errors.New("remote delta changed an immutable Anchor runtime identity")
	}
	return nil
}

// materializeRemoteCacheSeed 将已验证 Anchor 缓存发布到隔离的固定宽度 generation 目录。
func materializeRemoteCacheSeed(expandedRoot string, generation uint64) error {
	_, err := publishRemoteCacheSeed(expandedRoot, expandedRoot, generation, false)
	return err
}

// publishRemoteCacheSeed 原子发布已验证的 go-build cache seed。
func publishRemoteCacheSeed(layerRoot string, expandedRoot string, generation uint64, allowEmpty bool) (bool, error) {
	cacheSeedRoot, source, empty, err := remoteCacheSeedLayout(layerRoot)
	if err != nil {
		return false, err
	}
	if empty {
		if !allowEmpty {
			return false, errors.New("remote Anchor cache layer is empty")
		}
		return false, nil
	}
	destination := filepath.Join(expandedRoot, "cache-seeds", remoteCacheSeedGeneration(generation))
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return false, fmt.Errorf("create remote cache seed root: %w", err)
	}
	if err := os.Rename(source, destination); err != nil {
		return false, fmt.Errorf("publish remote cache seed generation: %w", err)
	}
	if err := os.Remove(cacheSeedRoot); err != nil {
		return false, err
	}
	return true, nil
}

// remoteCacheSeedLayout 验证 cache-seed/go-build 的唯一布局并报告是否为空。
func remoteCacheSeedLayout(layerRoot string) (string, string, bool, error) {
	cacheSeedRoot := filepath.Join(layerRoot, "cache-seed")
	entries, err := os.ReadDir(cacheSeedRoot)
	if err != nil || len(entries) != 1 || entries[0].Name() != "go-build" || !entries[0].IsDir() {
		return "", "", false, errors.New("remote cache layer layout is invalid")
	}
	source := filepath.Join(cacheSeedRoot, "go-build")
	entries, err = os.ReadDir(source)
	if err != nil {
		return "", "", false, errors.New("read remote cache layer")
	}
	return cacheSeedRoot, source, len(entries) == 0, nil
}

func remoteCacheSeedGeneration(generation uint64) string {
	return fmt.Sprintf("%020d", generation)
}

func materializeRemoteCacheDelta(ctx context.Context, archivePath string, expandedRoot string, generation uint64) error {
	stagingParent, err := os.MkdirTemp(expandedRoot, ".super-dolphin-cache-delta-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagingParent)
	layerRoot := filepath.Join(stagingParent, "layer")
	if err := extractRemoteBaselineArchiveInto(ctx, archivePath, layerRoot); err != nil {
		return err
	}
	_, err = publishRemoteCacheSeed(layerRoot, expandedRoot, generation, true)
	return err
}

// materializeRemoteDeltaGateBinary 仅下载最后一层当前 CLI，并原子替换 Anchor 引导二进制。
func materializeRemoteDeltaGateBinary(ctx context.Context, expandedRoot string, delta remoteci.BaselineDeltaLayer, manifest remoteci.BaselineManifest, download remoteObjectDownload) error {
	binRoot := filepath.Join(expandedRoot, "bin")
	temporary, err := os.CreateTemp(binRoot, ".super-dolphin-gate-")
	if err != nil {
		return fmt.Errorf("create remote delta gate staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		os.Remove(temporaryPath)
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return err
	}
	defer os.Remove(temporaryPath)
	prefix := path.Join(strings.TrimSuffix(delta.ObjectPrefix, "/"), "output")
	if err := downloadVerifiedFile(ctx, download, path.Join(prefix, "bin", "super-dolphin-gate"), manifest.GateBinarySHA256, manifest.GateBinarySize, temporaryPath); err != nil {
		return fmt.Errorf("download remote delta gate binary: %w", err)
	}
	if err := os.Chmod(temporaryPath, 0o755); err != nil {
		return fmt.Errorf("make remote delta gate binary executable: %w", err)
	}
	destination := filepath.Join(binRoot, "super-dolphin-gate")
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("publish remote delta gate binary: %w", err)
	}
	return nil
}

// applyRemoteSourceBundle 只在前一层 source 身份已确认后导入一个 Git bundle。
func applyRemoteSourceBundle(ctx context.Context, sourceRoot string, bundlePath string, commit string, tree string) error {
	command := exec.CommandContext(ctx, "git", "-c", "credential.interactive=never", "fetch", "--quiet", "--no-tags", bundlePath, commit)
	command.Dir = sourceRoot
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch remote source delta bundle: %w: %s", err, strings.TrimSpace(string(output)))
	}
	checkout := exec.CommandContext(ctx, "git", "checkout", "--quiet", "--detach", "FETCH_HEAD")
	checkout.Dir = sourceRoot
	if output, err := checkout.CombinedOutput(); err != nil {
		return fmt.Errorf("checkout remote source delta: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return verifyRemoteSourceIdentity(ctx, sourceRoot, commit, tree)
}

// verifyRemoteSourceIdentity 检查每一层提交、树和工作区状态，拒绝混代或残留修改。
func verifyRemoteSourceIdentity(ctx context.Context, sourceRoot string, commit string, tree string) error {
	for _, expected := range []struct{ revision, want string }{{"HEAD^{commit}", commit}, {"HEAD^{tree}", tree}} {
		command := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--end-of-options", expected.revision)
		command.Dir = sourceRoot
		output, err := command.Output()
		if err != nil || strings.TrimSpace(string(output)) != expected.want {
			return errors.New("remote source identity mismatch")
		}
	}
	command := exec.CommandContext(ctx, "git", "status", "--porcelain=v1", "--untracked-files=all")
	command.Dir = sourceRoot
	output, err := command.Output()
	if err != nil || len(output) != 0 {
		return errors.New("remote source is not clean")
	}
	return nil
}

// extractRemoteBaselineArchiveInto 在隔离目标内逐条校验并创建受限归档条目。
func extractRemoteBaselineArchiveInto(ctx context.Context, archivePath string, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return errors.New("remote archive destination is invalid")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create remote archive parent root: %w", err)
	}
	if err := os.Mkdir(destination, 0o755); err != nil {
		return fmt.Errorf("create remote archive destination: %w", err)
	}
	return extractValidatedRemoteArchive(ctx, archivePath, destination)
}

// validateRemoteArchiveLayer 在通用安全校验外绑定该归档允许写入的顶层目录。
func validateRemoteArchiveLayer(archivePath string, layerName string) error {
	roots, ok := remoteArchiveLayerRoots(layerName)
	if !ok {
		return fmt.Errorf("remote archive layer %q is unsupported", layerName)
	}
	return validateRemoteArchiveWithRoots(archivePath, roots)
}

// validateRemoteArchiveWithRoots 完整预检归档，并可选限制所有条目和链接的顶层目录。
func validateRemoteArchiveWithRoots(archivePath string, allowedRoots map[string]struct{}) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open remote archive: %w", err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open remote archive gzip: %w", err)
	}
	defer reader.Close()
	archive := tar.NewReader(reader)
	var total int64
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read remote archive: %w", err)
		}
		if err := validateRemoteArchiveHeader(header); err != nil {
			return fmt.Errorf("remote archive header %q: %w", header.Name, err)
		}
		if len(allowedRoots) != 0 {
			if err := validateRemoteArchiveLayerHeader(header, allowedRoots); err != nil {
				return fmt.Errorf("remote archive header %q: %w", header.Name, err)
			}
		}
		if header.Size > remoteExpandedArchiveMaxBytes-total {
			return fmt.Errorf("remote archive expanded size exceeds %d bytes", remoteExpandedArchiveMaxBytes)
		}
		total += header.Size
	}
}

// remoteArchiveLayerRoots 返回规范 Anchor 层互不重叠的顶层目录集合。
func remoteArchiveLayerRoots(layerName string) (map[string]struct{}, bool) {
	switch layerName {
	case "runtime-deps":
		return map[string]struct{}{"runtime": {}}, true
	case "source":
		return map[string]struct{}{"source": {}, "frontend-embed": {}}, true
	case "go-build-cache":
		return map[string]struct{}{"cache-seed": {}}, true
	default:
		return nil, false
	}
}

// validateRemoteArchiveLayerHeader 拒绝条目或链接跨越声明层的目录边界。
func validateRemoteArchiveLayerHeader(header *tar.Header, allowedRoots map[string]struct{}) error {
	clean := path.Clean(header.Name)
	if !remoteArchivePathUsesAllowedRoot(clean, allowedRoots) {
		return errors.New("entry crosses its declared layer root")
	}
	switch header.Typeflag {
	case tar.TypeSymlink:
		link, ok := remoteArchiveSymlinkTarget(clean, header.Linkname)
		if !ok || !remoteArchivePathUsesAllowedRoot(path.Join(path.Dir(clean), link), allowedRoots) {
			return errors.New("symbolic link crosses its declared layer root")
		}
	case tar.TypeLink:
		if !remoteArchivePathUsesAllowedRoot(path.Clean(header.Linkname), allowedRoots) {
			return errors.New("hard link crosses its declared layer root")
		}
	}
	return nil
}

// remoteArchivePathUsesAllowedRoot 判断规范归档路径是否属于声明层。
func remoteArchivePathUsesAllowedRoot(name string, allowedRoots map[string]struct{}) bool {
	root, _, _ := strings.Cut(path.Clean(name), "/")
	_, ok := allowedRoots[root]
	return ok
}

func validateRemoteArchiveHeader(header *tar.Header) error {
	if !validRemoteArchivePathAndSize(header) {
		return errors.New("path or size is invalid")
	}
	switch header.Typeflag {
	case tar.TypeReg, 0, tar.TypeDir:
		return nil
	case tar.TypeSymlink:
		return validateRemoteArchiveSymlink(header)
	case tar.TypeLink:
		return validateRemoteArchiveHardLink(header)
	default:
		return fmt.Errorf("type %d is not allowed", header.Typeflag)
	}
}

// validRemoteArchivePathAndSize 拒绝空、绝对、逃逸或负长度的 archive 条目。
func validRemoteArchivePathAndSize(header *tar.Header) bool {
	clean := path.Clean(header.Name)
	return header.Name != "" && !strings.HasPrefix(header.Name, "/") && clean != "." && !strings.HasPrefix(clean, "../") && clean != ".." && header.Size >= 0
}

// validateRemoteArchiveSymlink 校验零长度的受限符号链接。
func validateRemoteArchiveSymlink(header *tar.Header) error {
	if _, ok := remoteArchiveSymlinkTarget(path.Clean(header.Name), header.Linkname); header.Size != 0 || !ok {
		return errors.New("symbolic link target is invalid")
	}
	return nil
}

// validateRemoteArchiveHardLink 校验零长度的相对硬链接。
func validateRemoteArchiveHardLink(header *tar.Header) error {
	if header.Size != 0 || !validRemoteArchiveLink(".", header.Linkname) {
		return errors.New("hard link target is invalid")
	}
	return nil
}

func validRemoteArchiveLink(base string, link string) bool {
	if link == "" || path.IsAbs(link) {
		return false
	}
	resolved := path.Clean(path.Join(base, link))
	return resolved != ".." && !strings.HasPrefix(resolved, "../")
}

// remoteArchiveSymlinkTarget 将 archive 链接转换为受限的相对目标。
func remoteArchiveSymlinkTarget(name string, link string) (string, bool) {
	if link == "" {
		return "", false
	}
	if !path.IsAbs(link) {
		return link, validRemoteArchiveLink(path.Dir(name), link)
	}
	rootFS, ok := remoteArchiveRootFSPrefix(name)
	if !ok {
		return "", false
	}
	target := path.Join(rootFS, strings.TrimPrefix(path.Clean(link), "/"))
	if target == rootFS {
		return "", false
	}
	relative, err := filepath.Rel(filepath.FromSlash(path.Dir(name)), filepath.FromSlash(target))
	relative = filepath.ToSlash(relative)
	if err != nil || !validRemoteArchiveLink(path.Dir(name), relative) {
		return "", false
	}
	return relative, true
}

func remoteArchiveRootFSPrefix(name string) (string, bool) {
	components := strings.Split(path.Clean(name), "/")
	for index, component := range components[:len(components)-1] {
		if component == "rootfs" {
			return strings.Join(components[:index+1], "/"), true
		}
	}
	return "", false
}

// extractRemoteBaselineLayer 在隔离目录中校验并解压单层，完整成功后才发布允许的顶层目录。
func extractRemoteBaselineLayer(ctx context.Context, archivePath string, destination string, layerName string) (returnErr error) {
	allowedRoots, ok := remoteArchiveLayerRoots(layerName)
	if !ok {
		return fmt.Errorf("remote archive layer %q is unsupported", layerName)
	}
	staging, err := os.MkdirTemp(destination, ".extract-"+layerName+"-")
	if err != nil {
		return fmt.Errorf("create remote archive layer staging root: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, os.RemoveAll(staging))
	}()
	if err := extractRemoteArchiveWithRoots(ctx, archivePath, staging, allowedRoots); err != nil {
		return err
	}
	roots := make([]string, 0, len(allowedRoots))
	for root := range allowedRoots {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	for _, root := range roots {
		info, err := os.Lstat(filepath.Join(staging, root))
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("remote archive layer root %q is missing or invalid", root)
		}
		if _, err := os.Lstat(filepath.Join(destination, root)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remote archive layer destination root %q already exists", root)
		}
	}
	for _, root := range roots {
		if err := os.Rename(filepath.Join(staging, root), filepath.Join(destination, root)); err != nil {
			return fmt.Errorf("publish remote archive layer root %q: %w", root, err)
		}
	}
	return nil
}

// extractValidatedRemoteArchive 在一次流式读取中校验并解压 archive。
func extractValidatedRemoteArchive(ctx context.Context, archivePath string, destination string) error {
	return extractRemoteArchiveWithRoots(ctx, archivePath, destination, nil)
}

func extractRemoteArchiveWithRoots(
	ctx context.Context,
	archivePath string,
	destination string,
	allowedRoots map[string]struct{},
) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open remote archive: %w", err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open remote archive gzip: %w", err)
	}
	defer reader.Close()
	archive := tar.NewReader(reader)
	directoryModes := make([]remoteArchiveDirectoryMode, 0)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return applyRemoteArchiveDirectoryModes(directoryModes)
		}
		if err != nil {
			return fmt.Errorf("read remote archive: %w", err)
		}
		if err := validateRemoteArchiveHeader(header); err != nil {
			return fmt.Errorf("remote archive header %q: %w", header.Name, err)
		}
		if len(allowedRoots) != 0 {
			if err := validateRemoteArchiveLayerHeader(header, allowedRoots); err != nil {
				return fmt.Errorf("remote archive header %q: %w", header.Name, err)
			}
		}
		if header.Size > remoteExpandedArchiveMaxBytes-total {
			return fmt.Errorf("remote archive expanded size exceeds %d bytes", remoteExpandedArchiveMaxBytes)
		}
		total += header.Size
		mode, err := extractRemoteArchiveEntry(archive, destination, header)
		if err != nil {
			return err
		}
		if mode != nil {
			directoryModes = append(directoryModes, *mode)
		}
	}
}

// extractRemoteArchiveEntry 创建一个已经通过预检的 archive 条目。
func extractRemoteArchiveEntry(archive *tar.Reader, destination string, header *tar.Header) (*remoteArchiveDirectoryMode, error) {
	target, err := remoteArchiveEntryTarget(destination, header.Name)
	if err != nil {
		return nil, err
	}
	if err := createRemoteArchiveParents(destination, target); err != nil {
		return nil, err
	}
	switch header.Typeflag {
	case tar.TypeDir:
		return extractRemoteArchiveDirectory(target, header.Mode)
	case tar.TypeSymlink:
		return nil, extractRemoteArchiveSymlink(target, header)
	case tar.TypeLink:
		return nil, extractRemoteArchiveHardLink(destination, target, header.Linkname)
	default:
		return nil, extractRemoteArchiveFile(archive, target, header.Size, header.Mode)
	}
}

// remoteArchiveEntryTarget 生成并约束 archive 条目在目标根中的路径。
func remoteArchiveEntryTarget(destination, name string) (string, error) {
	target := filepath.Join(destination, filepath.FromSlash(path.Clean(name)))
	relative, err := filepath.Rel(destination, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("remote archive path escapes destination")
	}
	return target, nil
}

// extractRemoteArchiveDirectory 创建目录并保留稍后恢复的模式。
func extractRemoteArchiveDirectory(target string, mode int64) (*remoteArchiveDirectoryMode, error) {
	if err := os.Mkdir(target, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create remote archive directory: %w", err)
	}
	return &remoteArchiveDirectoryMode{path: target, mode: os.FileMode(mode) & 0o777}, nil
}

// extractRemoteArchiveSymlink 创建预检后的相对符号链接。
func extractRemoteArchiveSymlink(target string, header *tar.Header) error {
	linkTarget, ok := remoteArchiveSymlinkTarget(path.Clean(header.Name), header.Linkname)
	if !ok {
		return errors.New("remote archive symbolic link target is invalid")
	}
	if err := os.Symlink(filepath.FromSlash(linkTarget), target); err != nil {
		return fmt.Errorf("create remote archive symbolic link: %w", err)
	}
	return nil
}

// extractRemoteArchiveHardLink 创建指向已解压普通文件的硬链接。
func extractRemoteArchiveHardLink(destination, target, linkName string) error {
	linkTarget := filepath.Join(destination, filepath.FromSlash(path.Clean(linkName)))
	info, err := os.Lstat(linkTarget)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("remote archive hard link target is not an extracted regular file")
	}
	if err := os.Link(linkTarget, target); err != nil {
		return fmt.Errorf("create remote archive hard link: %w", err)
	}
	return nil
}

// extractRemoteArchiveFile 解压单个普通文件并恢复其权限。
func extractRemoteArchiveFile(archive *tar.Reader, target string, size, mode int64) error {
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create remote archive file: %w", err)
	}
	copyErr := copyAndCloseRemoteArchiveFile(output, archive, size)
	if copyErr != nil {
		return fmt.Errorf("extract remote archive file: %w", copyErr)
	}
	if err := os.Chmod(target, os.FileMode(mode)&0o777); err != nil {
		return fmt.Errorf("set remote archive file mode: %w", err)
	}
	return nil
}

// copyAndCloseRemoteArchiveFile 合并写入和关闭错误。
func copyAndCloseRemoteArchiveFile(output *os.File, archive *tar.Reader, size int64) error {
	_, copyErr := io.CopyN(output, archive, size)
	return errors.Join(copyErr, output.Close())
}

type remoteArchiveDirectoryMode struct {
	path string
	mode fs.FileMode
}

// applyRemoteArchiveDirectoryModes 自深至浅恢复 archive 目录的声明权限。
func applyRemoteArchiveDirectoryModes(directories []remoteArchiveDirectoryMode) error {
	sort.Slice(directories, func(left, right int) bool {
		leftDepth := strings.Count(filepath.Clean(directories[left].path), string(filepath.Separator))
		rightDepth := strings.Count(filepath.Clean(directories[right].path), string(filepath.Separator))
		if leftDepth != rightDepth {
			return leftDepth > rightDepth
		}
		return directories[left].path > directories[right].path
	})
	for _, directory := range directories {
		info, err := os.Lstat(directory.path)
		if err != nil || !info.IsDir() {
			return errors.New("remote archive directory changed before mode restore")
		}
		if err := os.Chmod(directory.path, directory.mode); err != nil {
			return fmt.Errorf("restore remote archive directory mode: %w", err)
		}
	}
	return nil
}

// createRemoteArchiveParents 建立目标文件所需且没有符号链接的父目录。
func createRemoteArchiveParents(destination string, target string) error {
	components, err := remoteArchiveParentComponents(destination, target)
	if err != nil {
		return err
	}
	current := destination
	for _, component := range components {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		if err := ensureRemoteArchiveParent(current); err != nil {
			return err
		}
	}
	return nil
}

// remoteArchiveParentComponents 计算目标父路径相对 destination 的组件。
func remoteArchiveParentComponents(destination, target string) ([]string, error) {
	relative, err := filepath.Rel(destination, filepath.Dir(target))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("remote archive parent escapes destination")
	}
	return strings.Split(relative, string(filepath.Separator)), nil
}

// ensureRemoteArchiveParent 创建缺失父目录或确认现有路径是真实目录。
func ensureRemoteArchiveParent(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o755); err != nil {
			return fmt.Errorf("create remote archive parent: %w", err)
		}
		return nil
	}
	if err != nil || !info.IsDir() {
		return errors.New("remote archive parent is not a real directory")
	}
	return nil
}
