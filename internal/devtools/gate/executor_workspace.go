package gate

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// prepareExecutorWorkspace 验证挂载边界并创建一次性可写源码副本与缓存目录。
func prepareExecutorWorkspace(config executorConfig) (executorLayout, error) {
	if err := validateExecutorDirectories(config); err != nil {
		return executorLayout{}, err
	}
	layout := newExecutorLayout(config.workRoot)
	if config.goBuildCacheRoot != "" {
		layout.goCache = config.goBuildCacheRoot
		layout.ownsGoCache = false
	}
	if err := os.Mkdir(layout.runRoot, 0o700); err != nil {
		return executorLayout{}, fmt.Errorf("create executor run root: %w", err)
	}
	if err := copySourceSnapshot(config.sourcePath, layout.sourceCopy); err != nil {
		return executorLayout{}, errors.Join(err, cleanupExecutorWorkspace(layout))
	}
	directories := []string{layout.home, layout.tmp, layout.goModCache, layout.npmCache, layout.xdgCache}
	if layout.ownsGoCache {
		directories = append(directories, layout.goCache)
	}
	for _, directory := range directories {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return executorLayout{}, errors.Join(fmt.Errorf("create executor directory: %w", err), cleanupExecutorWorkspace(layout))
		}
	}
	if err := os.Mkdir(filepath.Join(layout.home, ".codex"), 0o700); err != nil {
		return executorLayout{}, errors.Join(fmt.Errorf("create executor Codex home: %w", err), cleanupExecutorWorkspace(layout))
	}
	return layout, nil
}

// seedExecutorGoBuildCache 保留单根调用方兼容，实际字节由缓存代理按需读取。
func seedExecutorGoBuildCache(seedRoot string, targetRoot string) error {
	return seedExecutorGoBuildCacheSeeds([]string{seedRoot}, targetRoot)
}

// seedExecutorGoBuildCacheSeeds 只验证有序共享 seed 与私有写层，不复制任何缓存字节。
func seedExecutorGoBuildCacheSeeds(seedRoots []string, targetRoot string) error {
	if len(seedRoots) == 0 || len(seedRoots) > goBuildCacheProxyMaxSeedRoots {
		return errors.New("Go build cache seed count is invalid")
	}
	targetPath, err := trustedDirectory(targetRoot, true, os.Geteuid())
	if err != nil {
		return fmt.Errorf("Go build cache target: %w", err)
	}
	if _, err := trustedExecutorGoBuildCacheSeeds(seedRoots, targetPath); err != nil {
		return err
	}
	entries, err := os.ReadDir(targetPath)
	if err != nil {
		return fmt.Errorf("read private Go build cache write layer: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("private Go build cache write layer is not empty")
	}
	return nil
}

// trustedExecutorGoBuildCacheSeeds 验证每个 seed 目录与私有写层及彼此互不重叠。
func trustedExecutorGoBuildCacheSeeds(seedRoots []string, targetPath string) ([]string, error) {
	trustedSeeds := make([]string, 0, len(seedRoots))
	for index, seedRoot := range seedRoots {
		seedPath, err := trustedDirectory(seedRoot, false, -1)
		if err != nil {
			return nil, fmt.Errorf("Go build cache seed %d: %w", index+1, err)
		}
		if rootsOverlap(seedPath, targetPath) {
			return nil, errors.New("Go build cache seed and private write layer must be disjoint")
		}
		if executorGoBuildCacheSeedOverlaps(seedPath, trustedSeeds) {
			return nil, errors.New("Go build cache seed roots must be unique and disjoint")
		}
		trustedSeeds = append(trustedSeeds, seedPath)
	}
	return trustedSeeds, nil
}

// executorGoBuildCacheSeedOverlaps 报告候选 seed 是否与已经接受的根重叠。
func executorGoBuildCacheSeedOverlaps(candidate string, accepted []string) bool {
	for _, existing := range accepted {
		if rootsOverlap(candidate, existing) {
			return true
		}
	}
	return false
}

// discoverExecutorGoBuildCacheSeedRoots 按新到旧发现可信 generation，缺失时使用 legacyRoot。
func discoverExecutorGoBuildCacheSeedRoots(generationsRoot string, legacyRoot string) ([]string, error) {
	info, err := os.Lstat(generationsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return []string{legacyRoot}, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Go build cache seed generations root must be a real directory")
	}
	return trustedExecutorGoBuildCacheGenerationRoots(generationsRoot)
}

// trustedExecutorGoBuildCacheGenerationRoots 读取并验证一组有序 generation 目录。
func trustedExecutorGoBuildCacheGenerationRoots(generationsRoot string) ([]string, error) {
	if _, err := trustedDirectory(generationsRoot, false, -1); err != nil {
		return nil, fmt.Errorf("Go build cache seed generations root: %w", err)
	}
	entries, err := os.ReadDir(generationsRoot)
	if err != nil {
		return nil, fmt.Errorf("read Go build cache seed generations: %w", err)
	}
	if len(entries) == 0 || len(entries) > goBuildCacheProxyMaxSeedRoots {
		return nil, errors.New("Go build cache seed generation count is invalid")
	}
	seedRoots := make([]string, 0, len(entries))
	for _, entry := range slices.Backward(entries) {
		seedRoot, err := trustedExecutorGoBuildCacheGenerationRoot(generationsRoot, entry)
		if err != nil {
			return nil, err
		}
		seedRoots = append(seedRoots, seedRoot)
	}
	return seedRoots, nil
}

// trustedExecutorGoBuildCacheGenerationRoot 验证一个 generation 条目并返回其真实目录。
func trustedExecutorGoBuildCacheGenerationRoot(generationsRoot string, entry os.DirEntry) (string, error) {
	if !validExecutorGoBuildCacheGeneration(entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("Go build cache seed generation %q must be a real directory", entry.Name())
	}
	info, err := entry.Info()
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("Go build cache seed generation %q must be a real directory", entry.Name())
	}
	root, err := trustedDirectory(filepath.Join(generationsRoot, entry.Name()), false, -1)
	if err != nil {
		return "", fmt.Errorf("Go build cache seed generation %q: %w", entry.Name(), err)
	}
	return root, nil
}

func validExecutorGoBuildCacheGeneration(value string) bool {
	if len(value) != 20 || strings.Trim(value, "0123456789") != "" {
		return false
	}
	generation, err := strconv.ParseUint(value, 10, 64)
	return err == nil && generation != 0 && fmt.Sprintf("%020d", generation) == value
}

const (
	goBuildCacheProxyMaxRequestBytes = 16 << 10
	goBuildCacheProxyMaxBodyBytes    = int64(2 << 30)
	goBuildCacheHashBytes            = 32
	goBuildCacheProxyMaxSeedRoots    = 5
)

type goBuildCacheProxyConfig struct {
	seedRoots   []string
	privateRoot string
	metricsPath string
	metrics     *GoBuildCacheProxyMetrics
	now         func() time.Time
}

type goBuildCacheProxyRequest struct {
	ID       int64
	Command  string
	ActionID []byte `json:",omitempty"`
	OutputID []byte `json:",omitempty"`
	BodySize int64  `json:",omitempty"`
}

type goBuildCacheProxyResponse struct {
	ID            int64
	Err           string     `json:",omitempty"`
	KnownCommands []string   `json:",omitempty"`
	Miss          bool       `json:",omitempty"`
	OutputID      []byte     `json:",omitempty"`
	Size          int64      `json:",omitempty"`
	Time          *time.Time `json:",omitempty"`
	DiskPath      string     `json:",omitempty"`
}

type goBuildCacheProxyEntry struct {
	outputID []byte
	size     int64
	storedAt time.Time
	path     string
}

// ExecuteGoBuildCacheProxy 为 Go 官方 GOCACHEPROG 协议提供共享只读 seed 与私有写层。
func ExecuteGoBuildCacheProxy(args []string, input io.Reader, output io.Writer) error {
	if input == nil || output == nil {
		return errors.New("Go build cache proxy streams are required")
	}
	config, err := parseGoBuildCacheProxyConfig(args)
	if err != nil {
		return err
	}
	err = serveGoBuildCacheProxy(config, input, output)
	if config.metricsPath == "" {
		return err
	}
	return errors.Join(err, writeGoBuildCacheProxyMetrics(config.metricsPath, *config.metrics))
}

// parseGoBuildCacheProxyConfig 严格解析并验证有序只读 seed 链与唯一私有写层。
func parseGoBuildCacheProxyConfig(args []string) (goBuildCacheProxyConfig, error) {
	config := goBuildCacheProxyConfig{now: time.Now}
	if len(args) < 4 || len(args)%2 != 0 {
		return config, errors.New("Go build cache proxy requires one or more --seed values and one --private value")
	}
	if err := parseGoBuildCacheProxyOptions(args, &config); err != nil {
		return config, err
	}
	if len(config.seedRoots) == 0 || len(config.seedRoots) > goBuildCacheProxyMaxSeedRoots ||
		config.privateRoot == "" {
		return config, errors.New("Go build cache proxy seed or private layer count is invalid")
	}
	privateRoot, err := trustedDirectory(config.privateRoot, false, os.Geteuid())
	if err != nil {
		return config, fmt.Errorf("Go build cache proxy private root: %w", err)
	}
	seedRoots, err := trustedExecutorGoBuildCacheSeeds(config.seedRoots, privateRoot)
	if err != nil {
		return config, err
	}
	if config.metricsPath != "" && !validGoBuildCacheProxyMetricsPath(privateRoot, config.metricsPath) {
		return config, errors.New("Go build cache proxy metrics path must belong to the private layer")
	}
	config.seedRoots, config.privateRoot = seedRoots, privateRoot
	metrics := newGoBuildCacheProxyMetrics(seedRoots)
	config.metrics = &metrics
	return config, nil
}

// parseGoBuildCacheProxyOptions 解析重复 seed 与唯一 private 选项，而不访问文件系统。
func parseGoBuildCacheProxyOptions(args []string, config *goBuildCacheProxyConfig) error {
	for index := 0; index < len(args); index += 2 {
		switch args[index] {
		case "--seed":
			config.seedRoots = append(config.seedRoots, args[index+1])
		case "--private":
			if config.privateRoot != "" {
				return errors.New("Go build cache proxy requires exactly one --private value")
			}
			config.privateRoot = args[index+1]
		case "--metrics":
			if config.metricsPath != "" {
				return errors.New("Go build cache proxy accepts at most one metrics path")
			}
			config.metricsPath = args[index+1]
		default:
			return fmt.Errorf("unknown Go build cache proxy option %q", args[index])
		}
	}
	return nil
}

func rootsOverlap(left string, right string) bool {
	return left == right || pathContains(left, right) || pathContains(right, left)
}

// serveGoBuildCacheProxy 顺序处理官方 GOCACHEPROG 请求并逐条返回关联响应。
func serveGoBuildCacheProxy(config goBuildCacheProxyConfig, input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	encoder := json.NewEncoder(output)
	if err := encoder.Encode(goBuildCacheProxyResponse{
		ID: 0, KnownCommands: []string{"get", "put", "close"},
	}); err != nil {
		return err
	}
	for {
		request, err := readGoBuildCacheProxyRequest(reader)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		response, stop, err := handleGoBuildCacheProxyRequest(config, reader, request)
		if err != nil {
			response = goBuildCacheProxyResponse{ID: request.ID, Err: err.Error()}
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
		if stop {
			return nil
		}
	}
}

// readGoBuildCacheProxyRequest 跳过协议空白分隔并限制单条 JSON 请求体积。
func readGoBuildCacheProxyRequest(reader *bufio.Reader) (goBuildCacheProxyRequest, error) {
	var request goBuildCacheProxyRequest
	var line []byte
	for len(strings.TrimSpace(string(line))) == 0 {
		var err error
		line, err = reader.ReadBytes('\n')
		if err != nil {
			return request, err
		}
		if len(line) > goBuildCacheProxyMaxRequestBytes {
			return request, errors.New("Go build cache proxy request is too large")
		}
	}
	if err := json.Unmarshal(line, &request); err != nil {
		return request, fmt.Errorf("decode Go build cache proxy request: %w", err)
	}
	if request.ID <= 0 {
		return request, errors.New("Go build cache proxy request ID must be positive")
	}
	return request, nil
}

func handleGoBuildCacheProxyRequest(
	config goBuildCacheProxyConfig,
	reader *bufio.Reader,
	request goBuildCacheProxyRequest,
) (goBuildCacheProxyResponse, bool, error) {
	switch request.Command {
	case "get":
		response, err := getGoBuildCacheProxyEntry(config, request)
		return response, false, err
	case "put":
		response, err := putGoBuildCacheProxyEntry(config, reader, request)
		return response, false, err
	case "close":
		return goBuildCacheProxyResponse{ID: request.ID}, true, nil
	default:
		return goBuildCacheProxyResponse{}, false,
			fmt.Errorf("unsupported Go build cache proxy command %q", request.Command)
	}
}

// getGoBuildCacheProxyEntry 优先读取私有层；seed 命中会原子提升到私有层，以便发布完整本次工作集。
func getGoBuildCacheProxyEntry(
	config goBuildCacheProxyConfig,
	request goBuildCacheProxyRequest,
) (goBuildCacheProxyResponse, error) {
	if len(request.ActionID) != goBuildCacheHashBytes || request.BodySize != 0 || len(request.OutputID) != 0 {
		return goBuildCacheProxyResponse{}, errors.New("Go build cache get request is malformed")
	}
	entry, layer, err := findGoBuildCacheEntryWithLayer(config, request.ActionID)
	if errors.Is(err, errGoBuildCacheMiss) {
		config.metrics.recordMiss()
		return goBuildCacheProxyResponse{ID: request.ID, Miss: true}, nil
	}
	if err != nil {
		return goBuildCacheProxyResponse{}, err
	}
	if layer != 0 {
		entry, err = promoteGoBuildCacheSeedEntry(config.privateRoot, request.ActionID, entry)
		if err != nil {
			return goBuildCacheProxyResponse{}, err
		}
	}
	config.metrics.recordHit(layer)
	return goBuildCacheProxyResponse{
		ID: request.ID, OutputID: entry.outputID, Size: entry.size,
		Time: &entry.storedAt, DiskPath: entry.path,
	}, nil
}

// promoteGoBuildCacheSeedEntry 仅把已验证命中的一个 seed action 复制到私有层，避免首次执行全量复制历史缓存。
func promoteGoBuildCacheSeedEntry(
	privateRoot string,
	actionID []byte,
	seedEntry goBuildCacheProxyEntry,
) (goBuildCacheProxyEntry, error) {
	if len(actionID) != goBuildCacheHashBytes || len(seedEntry.outputID) != goBuildCacheHashBytes || seedEntry.size < 0 {
		return goBuildCacheProxyEntry{}, errors.New("Go build cache seed entry is malformed")
	}
	if err := promoteGoBuildCacheOutput(privateRoot, seedEntry); err != nil {
		return goBuildCacheProxyEntry{}, err
	}
	indexCreated, err := writeGoBuildCacheIndexIfAbsent(privateRoot, actionID, seedEntry)
	if err != nil {
		return goBuildCacheProxyEntry{}, err
	}
	promoted, err := readGoBuildCacheEntry(privateRoot, actionID)
	if err != nil {
		return goBuildCacheProxyEntry{}, fmt.Errorf("read promoted Go build cache entry: %w", err)
	}
	if !slices.Equal(promoted.outputID, seedEntry.outputID) || promoted.size != seedEntry.size {
		return goBuildCacheProxyEntry{}, errors.New("promoted Go build cache entry conflicts with seed identity")
	}
	if !indexCreated && !promoted.storedAt.Equal(seedEntry.storedAt) {
		return goBuildCacheProxyEntry{}, errors.New("promoted Go build cache entry conflicts with seed timestamp")
	}
	return promoted, nil
}

// promoteGoBuildCacheOutput 通过同目录临时文件和不覆盖链接发布输出，并复核内容哈希与大小。
func promoteGoBuildCacheOutput(privateRoot string, seedEntry goBuildCacheProxyEntry) error {
	outputPath, err := goBuildCachePath(privateRoot, seedEntry.outputID, "d")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(outputPath), ".cache-promote-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	seedOutput, err := os.Open(seedEntry.path)
	if err != nil {
		_ = temporary.Close()
		return fmt.Errorf("open Go build cache seed output: %w", err)
	}
	digest := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, digest), seedOutput)
	seedCloseErr := seedOutput.Close()
	closeErr := temporary.Close()
	if copyErr != nil || seedCloseErr != nil || closeErr != nil {
		return errors.Join(copyErr, seedCloseErr, closeErr)
	}
	if written != seedEntry.size || !slices.Equal(digest.Sum(nil), seedEntry.outputID) {
		return errors.New("Go build cache seed output does not match its declared identity")
	}
	if err := os.Link(temporaryPath, outputPath); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		existingPath, resolveErr := resolveGoBuildCacheOutput(privateRoot, seedEntry.outputID, seedEntry.size)
		if resolveErr != nil {
			return fmt.Errorf("read concurrent promoted Go build cache output: %w", resolveErr)
		}
		return validateGoBuildCacheOutputIdentity(existingPath, seedEntry.size, seedEntry.outputID)
	}
	return nil
}

func validateGoBuildCacheOutputIdentity(path string, size int64, outputID []byte) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	digest := sha256.New()
	written, err := io.Copy(digest, file)
	if err != nil {
		return err
	}
	if written != size || !slices.Equal(digest.Sum(nil), outputID) {
		return errors.New("Go build cache output does not match its declared identity")
	}
	return nil
}

func writeGoBuildCacheIndexIfAbsent(
	privateRoot string,
	actionID []byte,
	entry goBuildCacheProxyEntry,
) (bool, error) {
	indexPath, err := goBuildCachePath(privateRoot, actionID, "a")
	if err != nil {
		return false, err
	}
	content := fmt.Appendf(nil, "v1 %x %x %20d %20d\n", actionID, entry.outputID, entry.size, entry.storedAt.UnixNano())
	return writeAtomicGoBuildCacheFileIfAbsent(indexPath, content)
}

func writeAtomicGoBuildCacheFileIfAbsent(path string, content []byte) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cache-index-*")
	if err != nil {
		return false, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func findGoBuildCacheEntry(config goBuildCacheProxyConfig, actionID []byte) (goBuildCacheProxyEntry, error) {
	entry, _, err := findGoBuildCacheEntryWithLayer(config, actionID)
	return entry, err
}

func findGoBuildCacheEntryWithLayer(config goBuildCacheProxyConfig, actionID []byte) (goBuildCacheProxyEntry, int, error) {
	roots := make([]string, 0, len(config.seedRoots)+1)
	roots = append(roots, config.privateRoot)
	roots = append(roots, config.seedRoots...)
	for layer, root := range roots {
		entry, err := readGoBuildCacheEntry(root, actionID)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return goBuildCacheProxyEntry{}, 0, err
		}
		return entry, layer, nil
	}
	return goBuildCacheProxyEntry{}, 0, errGoBuildCacheMiss
}

// readGoBuildCacheEntry 解析 Go 磁盘缓存 v1 索引并验证对应输出文件。
func readGoBuildCacheEntry(root string, actionID []byte) (goBuildCacheProxyEntry, error) {
	indexPath, err := goBuildCachePath(root, actionID, "a")
	if err != nil {
		return goBuildCacheProxyEntry{}, err
	}
	content, err := os.ReadFile(indexPath)
	if err != nil {
		return goBuildCacheProxyEntry{}, err
	}
	outputID, size, storedAt, err := parseGoBuildCacheIndex(indexPath, actionID, content)
	if err != nil {
		return goBuildCacheProxyEntry{}, err
	}
	outputPath, err := resolveGoBuildCacheOutput(root, outputID, size)
	if err != nil {
		return goBuildCacheProxyEntry{}, err
	}
	return goBuildCacheProxyEntry{
		outputID: outputID, size: size, storedAt: storedAt, path: outputPath,
	}, nil
}

// parseGoBuildCacheIndex 校验 v1 索引字段并返回输出身份、大小和写入时间。
func parseGoBuildCacheIndex(
	indexPath string,
	actionID []byte,
	content []byte,
) ([]byte, int64, time.Time, error) {
	fields := strings.Fields(string(content))
	if len(fields) != 5 {
		return nil, 0, time.Time{}, fmt.Errorf("Go build cache index %q is malformed", indexPath)
	}
	if fields[0] != "v1" || fields[1] != hex.EncodeToString(actionID) {
		return nil, 0, time.Time{}, fmt.Errorf("Go build cache index %q is malformed", indexPath)
	}
	outputID, err := hex.DecodeString(fields[2])
	if err != nil || len(outputID) != goBuildCacheHashBytes {
		return nil, 0, time.Time{}, fmt.Errorf("Go build cache index %q has invalid output identity", indexPath)
	}
	size, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil || size < 0 {
		return nil, 0, time.Time{}, fmt.Errorf("Go build cache index %q has invalid size", indexPath)
	}
	storedNano, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil || storedNano < 0 {
		return nil, 0, time.Time{}, fmt.Errorf("Go build cache index %q has invalid timestamp", indexPath)
	}
	return outputID, size, time.Unix(0, storedNano), nil
}

// resolveGoBuildCacheOutput 解析普通或可执行缓存输出并核对文件大小。
func resolveGoBuildCacheOutput(root string, outputID []byte, expectedSize int64) (string, error) {
	path, err := goBuildCachePath(root, outputID, "d")
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 1 {
			return "", fmt.Errorf("Go build cache executable output %q is malformed", path)
		}
		path = filepath.Join(path, entries[0].Name())
		info, err = os.Stat(path)
		if err != nil {
			return "", err
		}
	}
	if !info.Mode().IsRegular() || info.Size() != expectedSize {
		return "", fmt.Errorf("Go build cache output %q is incomplete", path)
	}
	return path, nil
}

// putGoBuildCacheProxyEntry 把 miss 结果只写入私有层并生成对应 v1 索引。
func putGoBuildCacheProxyEntry(
	config goBuildCacheProxyConfig,
	reader *bufio.Reader,
	request goBuildCacheProxyRequest,
) (goBuildCacheProxyResponse, error) {
	if len(request.ActionID) != goBuildCacheHashBytes || len(request.OutputID) != goBuildCacheHashBytes ||
		request.BodySize < 0 || request.BodySize > goBuildCacheProxyMaxBodyBytes {
		return goBuildCacheProxyResponse{}, errors.New("Go build cache put request is malformed")
	}
	outputPath, err := writeGoBuildCacheProxyBody(config.privateRoot, reader, request)
	if err != nil {
		return goBuildCacheProxyResponse{}, err
	}
	if err := writeGoBuildCacheIndex(config.privateRoot, request, config.now()); err != nil {
		return goBuildCacheProxyResponse{}, err
	}
	config.metrics.recordPut()
	return goBuildCacheProxyResponse{
		ID: request.ID, DiskPath: outputPath,
	}, nil
}

// writeGoBuildCacheProxyBody 流式落盘、校验内容身份并原子发布私有缓存输出。
func writeGoBuildCacheProxyBody(
	privateRoot string,
	reader *bufio.Reader,
	request goBuildCacheProxyRequest,
) (string, error) {
	outputPath, err := goBuildCachePath(privateRoot, request.OutputID, "d")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(filepath.Dir(outputPath), ".cache-output-*")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	digest := sha256.New()
	written, copyErr := copyGoBuildCacheProxyBody(reader, request.BodySize, io.MultiWriter(temporary, digest))
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil {
		return "", errors.Join(copyErr, closeErr)
	}
	if written != request.BodySize || !slices.Equal(digest.Sum(nil), request.OutputID) {
		return "", errors.New("Go build cache put body does not match its declared identity")
	}
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		return "", err
	}
	return outputPath, nil
}

// copyGoBuildCacheProxyBody 从官方 JSON 字符串帧流式解码指定大小的缓存内容。
func copyGoBuildCacheProxyBody(reader *bufio.Reader, size int64, destination io.Writer) (int64, error) {
	if size == 0 {
		return 0, nil
	}
	if err := readGoBuildCacheProxyBodyOpening(reader); err != nil {
		return 0, err
	}
	encodedSize := int64(base64.StdEncoding.EncodedLen(int(size)))
	encoded := &io.LimitedReader{R: reader, N: encodedSize}
	decoded := base64.NewDecoder(base64.StdEncoding, encoded)
	written, err := io.Copy(destination, decoded)
	if err != nil {
		return written, err
	}
	if err := validateGoBuildCacheProxyBodyClosing(reader, encoded.N); err != nil {
		return written, err
	}
	return written, nil
}

// readGoBuildCacheProxyBodyOpening 跳过 JSON 空白并要求正文以引号开始。
func readGoBuildCacheProxyBodyOpening(reader *bufio.Reader) error {
	for {
		opening, err := reader.ReadByte()
		if err != nil {
			return errors.New("Go build cache put body opening quote is missing")
		}
		if opening == '"' {
			return nil
		}
		switch opening {
		case ' ', '\t', '\r', '\n':
		default:
			return errors.New("Go build cache put body opening quote is missing")
		}
	}
}

// validateGoBuildCacheProxyBodyClosing 校验 base64 内容已读尽且 JSON 字符串正确结束。
func validateGoBuildCacheProxyBodyClosing(reader *bufio.Reader, remaining int64) error {
	closing, closeErr := reader.ReadByte()
	newline, newlineErr := reader.ReadByte()
	if remaining != 0 {
		return errors.New("Go build cache put body framing is invalid")
	}
	if closeErr != nil || newlineErr != nil {
		return errors.New("Go build cache put body framing is invalid")
	}
	if closing != '"' || newline != '\n' {
		return errors.New("Go build cache put body framing is invalid")
	}
	return nil
}

func writeGoBuildCacheIndex(privateRoot string, request goBuildCacheProxyRequest, storedAt time.Time) error {
	indexPath, err := goBuildCachePath(privateRoot, request.ActionID, "a")
	if err != nil {
		return err
	}
	entry := fmt.Sprintf(
		"v1 %x %x %20d %20d\n",
		request.ActionID,
		request.OutputID,
		request.BodySize,
		storedAt.UnixNano(),
	)
	return writeAtomicGoBuildCacheFile(indexPath, []byte(entry))
}

func writeAtomicGoBuildCacheFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cache-index-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func newExecutorLayout(workRoot string) executorLayout {
	runRoot := filepath.Join(workRoot, "run")
	return executorLayout{
		workRoot: workRoot, runRoot: runRoot, sourceCopy: filepath.Join(runRoot, "source"),
		home: filepath.Join(workRoot, "home"), tmp: filepath.Join(workRoot, "tmp"),
		goCache: filepath.Join(workRoot, "go-cache"), goModCache: filepath.Join(workRoot, "go-mod-cache"),
		npmCache: filepath.Join(workRoot, "npm-cache"), xdgCache: filepath.Join(workRoot, "xdg-cache"),
		ownsGoCache: true,
	}
}

// validateExecutorDirectories 要求 source、work 均为可信实目录且彼此不嵌套。
func validateExecutorDirectories(config executorConfig) error {
	sourcePath, err := trustedDirectory(config.sourcePath, false, -1)
	if err != nil {
		return fmt.Errorf("source snapshot: %w", err)
	}
	workRoot, err := trustedDirectory(config.workRoot, true, config.expectedUID)
	if err != nil {
		return fmt.Errorf("executor work root: %w", err)
	}
	if sourcePath == workRoot || pathContains(sourcePath, workRoot) || pathContains(workRoot, sourcePath) {
		return errors.New("source snapshot and work root must be disjoint")
	}
	if config.requireReadOnlySource {
		if err := validateReadOnlyMount(sourcePath); err != nil {
			return fmt.Errorf("source snapshot mount: %w", err)
		}
	}
	return validateExecutorSharedCache(config, sourcePath, workRoot)
}

// validateExecutorSharedCache 要求分片级构建缓存是与源码及 lane 工作区分离的同 UID 实目录。
func validateExecutorSharedCache(config executorConfig, sourcePath string, workRoot string) error {
	if config.goBuildCacheRoot == "" {
		return nil
	}
	cacheRoot, err := trustedDirectory(config.goBuildCacheRoot, false, config.expectedUID)
	if err != nil {
		return fmt.Errorf("shared Go build cache: %w", err)
	}
	if cacheRoot == sourcePath || cacheRoot == workRoot ||
		pathContains(cacheRoot, sourcePath) || pathContains(sourcePath, cacheRoot) ||
		pathContains(cacheRoot, workRoot) || pathContains(workRoot, cacheRoot) {
		return errors.New("shared Go build cache must be disjoint from source snapshot and executor work root")
	}
	return nil
}

// trustedDirectory 校验规范实目录、所有权权限以及按需的空目录条件。
func trustedDirectory(path string, requireEmpty bool, expectedUID int) (string, error) {
	resolved, info, err := canonicalRealDirectory(path)
	if err != nil {
		return "", err
	}
	if expectedUID >= 0 {
		if err := validateDirectoryOwner(info, expectedUID); err != nil {
			return "", err
		}
	}
	if requireEmpty {
		if err := validateEmptyDirectory(path); err != nil {
			return "", err
		}
	}
	return resolved, nil
}

// canonicalRealDirectory 拒绝非规范路径、根链接以及任意父路径链接。
func canonicalRealDirectory(path string) (string, fs.FileInfo, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", nil, errors.New("path must be canonical and absolute")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, errors.New("path must be a real directory")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", nil, errors.New("path contains a symlink")
	}
	return resolved, info, nil
}

func validateDirectoryOwner(info fs.FileInfo, expectedUID int) error {
	ownerUID, ok := fileOwnerUID(info)
	if !ok || ownerUID != expectedUID {
		return errors.New("directory owner does not match executor uid")
	}
	if info.Mode().Perm()&0o700 != 0o700 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("directory permissions must be owner-only rwx")
	}
	return nil
}

func validateEmptyDirectory(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		return errors.New("directory must be readable and empty")
	}
	return nil
}

func pathContains(parent string, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// cleanupExecutorWorkspace 汇总删除一次执行拥有的全部私有目录。
func cleanupExecutorWorkspace(layout executorLayout) error {
	if layout.workRoot == "" || layout.runRoot == "" {
		return errors.New("executor workspace layout is empty")
	}
	var cleanupErr error
	paths := []string{layout.runRoot, layout.home, layout.tmp, layout.goModCache, layout.npmCache, layout.xdgCache}
	if layout.ownsGoCache {
		paths = append(paths, layout.goCache)
	}
	for _, path := range paths {
		if err := removeExecutorWorkspacePath(path); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove executor workspace path %q: %w", path, err))
		}
	}
	return cleanupErr
}

// removeExecutorWorkspacePath 恢复私有缓存目录的所有者写权限后严格删除该路径。
func removeExecutorWorkspacePath(path string) error {
	if err := makeExecutorDirectoriesRemovable(path); err != nil {
		return err
	}
	return os.RemoveAll(path)
}

// makeExecutorDirectoriesRemovable 只修改目录权限，不跟随缓存中的符号链接。
func makeExecutorDirectoriesRemovable(root string) error {
	if _, err := os.Lstat(root); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.Chmod(path, info.Mode().Perm()|0o700)
	})
}

// validateGoRuntimeSeedLocks 校验提交锁与已接受运行时清单一致。
func validateGoRuntimeSeedLocks(layout executorLayout, manifest RuntimeSeedManifest) error {
	if err := validateBoundRuntimeFile(filepath.Join(layout.sourceCopy, "go.sum"), manifest.GoSumSHA256); err != nil {
		return fmt.Errorf("validate Go dependency lock: %w", err)
	}
	proxyLock := filepath.Join(layout.sourceCopy, "build", "gate", "runtime-proxy", "go.sum")
	if err := validateBoundRuntimeFile(proxyLock, manifest.ModuleProxyLockSHA256); err != nil {
		return fmt.Errorf("validate Go module proxy lock: %w", err)
	}
	return nil
}

// validateGoRuntimeSeedTrees 校验分片共享的完整 Go proxy 与模块缓存。
func validateGoRuntimeSeedTrees(runtimeSeedRoot string, manifest RuntimeSeedManifest) error {
	if err := validateRuntimeSeedTree(
		filepath.Join(runtimeSeedRoot, "go-proxy"), manifest.ModuleProxyTreeSHA256,
	); err != nil {
		return fmt.Errorf("validate Go module proxy seed: %w", err)
	}
	if err := validateRuntimeSeedTree(
		filepath.Join(runtimeSeedRoot, "go-mod-cache"), manifest.GoModCacheTreeSHA256,
	); err != nil {
		return fmt.Errorf("validate shared Go module cache seed: %w", err)
	}
	return nil
}

// executeGoModuleOverlayCommand 为 Seed 与 worker 建立同一套只读共享、私有写入的模块缓存视图。
func executeGoModuleOverlayCommand(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: super-dolphin-gate worker go-module-overlay <shared-root> <private-root>")
	}
	return bindSharedGoModuleCache(args[0], args[1])
}

// bindSharedGoModuleCache 共享模块文件字节，同时为 Go 下载状态保留私有可写目录。
func bindSharedGoModuleCache(sharedRoot string, privateRoot string) error {
	sharedPath, err := validateSharedGoModuleCache(sharedRoot)
	if err != nil {
		return err
	}
	privatePath, err := trustedDirectory(privateRoot, true, os.Geteuid())
	if err != nil {
		return fmt.Errorf("private cache mountpoint: %w", err)
	}
	if sharedPath == privatePath || pathContains(sharedPath, privatePath) || pathContains(privatePath, sharedPath) {
		return errors.New("shared and private Go module cache paths must be disjoint")
	}
	privateEntries, err := os.ReadDir(privatePath)
	if err != nil {
		return fmt.Errorf("read private cache mountpoint: %w", err)
	}
	if len(privateEntries) != 0 {
		return errors.New("private Go module cache mountpoint is not empty")
	}
	return installGoModuleCacheOverlay(sharedPath, privatePath)
}

// installGoModuleCacheOverlay 链接不可变模块目录，并展开可写 download 元数据目录。
func installGoModuleCacheOverlay(sharedRoot string, privateRoot string) error {
	entries, err := os.ReadDir(sharedRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		sharedEntry := filepath.Join(sharedRoot, entry.Name())
		privateEntry := filepath.Join(privateRoot, entry.Name())
		if entry.Name() == "cache" {
			if err := installGoModuleMetadataOverlay(sharedEntry, privateEntry); err != nil {
				return err
			}
			continue
		}
		if err := os.Symlink(sharedEntry, privateEntry); err != nil {
			return err
		}
	}
	return nil
}

// installGoModuleMetadataOverlay 只展开 cache/download 的目录拓扑，其余缓存仍共享。
func installGoModuleMetadataOverlay(sharedRoot string, privateRoot string) error {
	if err := os.Mkdir(privateRoot, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(sharedRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		sharedEntry := filepath.Join(sharedRoot, entry.Name())
		privateEntry := filepath.Join(privateRoot, entry.Name())
		if entry.Name() == "download" {
			if err := mirrorGoModuleMetadataTree(sharedEntry, privateEntry); err != nil {
				return err
			}
			continue
		}
		if err := os.Symlink(sharedEntry, privateEntry); err != nil {
			return err
		}
	}
	return nil
}
