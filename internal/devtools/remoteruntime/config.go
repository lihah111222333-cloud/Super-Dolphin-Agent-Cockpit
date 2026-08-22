// Package remoteruntime 提供 provider-neutral 的四节点 runtime 配置与 SSH 传输边界。
package remoteruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	// ConfigSchemaVersion 是 runtime config 的稳定 ABI 版本。
	ConfigSchemaVersion = "remote-ci-runtime-config/v1"
	// FourNodeContractID 是四节点 runtime 所属的 accepted contract。
	FourNodeContractID = "remote-ci-four-node/v1"
	// MaxConfigBytes 限制 runtime config 的 canonical JSON 大小。
	MaxConfigBytes = 1 << 20
	// MaxNodeResultBytes 限制单次 probe、runner 或 worker 的 stdout 结果。
	MaxNodeResultBytes = 1 << 20
	// MaxNodeStderrBytes 限制被丢弃的远端 stderr，防止 pipe drain 无界增长。
	MaxNodeStderrBytes = 64 << 10

	// UbuntuPlatform 是 Ubuntu 节点 binding 的规范平台值。
	UbuntuPlatform Platform = "linux/amd64"
	// WindowsPlatform 是 Windows 节点 binding 的规范平台值。
	WindowsPlatform Platform = "windows/amd64"

	// Ubuntu01NodeID 是固定执行池中的第一个 Ubuntu 节点。
	Ubuntu01NodeID = "Ubuntu-01"
	// Ubuntu02NodeID 是固定执行池中的第二个 Ubuntu 节点。
	Ubuntu02NodeID = "Ubuntu-02"
	// Windows01NodeID 是固定执行池中的第一个 Windows 节点。
	Windows01NodeID = "Windows-01"
	// Windows02NodeID 是固定执行池中的第二个 Windows 节点。
	Windows02NodeID = "Windows-02"
)

// Platform 是 runtime binding 的目标操作系统值。
type Platform string

// RuntimeConfig 是独立 remote-ci binary 唯一接受的调用方配置。
//
// 该类型不携带 DSN secret、repository default 或派生 runtime facts；所有四个节点 binding 必须由 topology/调用方显式提供。
type RuntimeConfig struct {
	SchemaVersion        string        `json:"schema_version"`
	TopologyManifestPath string        `json:"topology_manifest_path"`
	OwnerDSNEnv          string        `json:"owner_dsn_env"`
	ReceiptDSNEnv        string        `json:"receipt_dsn_env"`
	OwnerNamespace       string        `json:"owner_namespace"`
	SSHExecutable        string        `json:"ssh_executable"`
	ProbeTimeoutMS       int64         `json:"probe_timeout_ms"`
	RunTimeoutMS         int64         `json:"run_timeout_ms"`
	CleanupTimeoutMS     int64         `json:"cleanup_timeout_ms"`
	WorkerTimeoutMS      int64         `json:"worker_timeout_ms"`
	Nodes                []NodeBinding `json:"nodes"`
}

// NodeBinding 将一个 topology node 显式绑定到 probe、runner、worker 和隔离根。
type NodeBinding struct {
	NodeID               string                 `json:"node_id"`
	Platform             Platform               `json:"platform"`
	Endpoint             string                 `json:"endpoint"`
	ProbeExecutable      string                 `json:"probe_executable"`
	ProbeConfigPath      string                 `json:"probe_config_path"`
	ProbeIdentity        string                 `json:"probe_identity"`
	RunnerExecutable     string                 `json:"runner_executable"`
	RunnerIdentity       string                 `json:"runner_identity"`
	WorkerExecutable     string                 `json:"worker_executable"`
	WorkerIdentity       string                 `json:"worker_identity"`
	RepositoryRoot       string                 `json:"repository_root"`
	ObjectStoreRoot      string                 `json:"object_store_root"`
	WorktreeRoot         string                 `json:"worktree_root"`
	CachePublicationRoot string                 `json:"cache_publication_root"`
	PrivateOverlayRoot   string                 `json:"private_overlay_root"`
	TempRoot             string                 `json:"temp_root"`
	GoExecutable         string                 `json:"go_executable"`
	ToolchainIdentity    string                 `json:"toolchain_identity"`
	Linux                *LinuxContainerBinding `json:"linux,omitempty"`
}

// LinuxContainerBinding 是 Ubuntu binding 的显式容器运行时输入。
type LinuxContainerBinding struct {
	DockerExecutable    string `json:"docker_executable"`
	DockerImage         string `json:"docker_image"`
	DockerImageIdentity string `json:"docker_image_identity"`
	CgroupRoot          string `json:"cgroup_root"`
	RunAsUser           string `json:"run_as_user"`
	DockerGroup         string `json:"docker_group"`
}

// ConfigFields 返回 top-level JSON ABI 字段的稳定排序副本。
func ConfigFields() []string {
	return []string{
		"schema_version", "topology_manifest_path", "owner_dsn_env", "receipt_dsn_env",
		"owner_namespace", "ssh_executable", "probe_timeout_ms", "run_timeout_ms",
		"cleanup_timeout_ms", "worker_timeout_ms", "nodes",
	}
}

// NodeBindingFields 返回 node binding JSON ABI 字段的稳定排序副本。
func NodeBindingFields() []string {
	return []string{
		"node_id", "platform", "endpoint", "probe_executable", "probe_config_path", "probe_identity",
		"runner_executable", "runner_identity", "worker_executable", "worker_identity",
		"repository_root", "object_store_root", "worktree_root", "cache_publication_root",
		"private_overlay_root", "temp_root", "go_executable", "toolchain_identity", "linux",
	}
}

// LinuxContainerFields 返回 Ubuntu 容器 JSON ABI 字段的稳定排序副本。
func LinuxContainerFields() []string {
	return []string{
		"docker_executable", "docker_image", "docker_image_identity", "cgroup_root",
		"run_as_user", "docker_group",
	}
}

// EncodeConfig 校验并编码 canonical runtime config。
func EncodeConfig(config RuntimeConfig) ([]byte, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode runtime config: %w", err)
	}
	if len(encoded) > MaxConfigBytes {
		return nil, errors.New("runtime config exceeds size bound")
	}
	return encoded, nil
}

// Encode 是 EncodeConfig 的 provider-neutral 别名。
func Encode(config RuntimeConfig) ([]byte, error) {
	return EncodeConfig(config)
}

// DecodeConfig 严格解码 canonical runtime config，拒绝 unknown、duplicate、null、trailing 和非 canonical bytes。
func DecodeConfig(data []byte) (RuntimeConfig, error) {
	if len(data) == 0 || len(data) > MaxConfigBytes {
		return RuntimeConfig{}, errors.New("runtime config size is invalid")
	}
	if err := walkStrictJSON(data); err != nil {
		return RuntimeConfig{}, fmt.Errorf("runtime config JSON is not strict: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config RuntimeConfig
	if err := decoder.Decode(&config); err != nil {
		return RuntimeConfig{}, fmt.Errorf("decode runtime config: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return RuntimeConfig{}, err
	}
	if err := config.Validate(); err != nil {
		return RuntimeConfig{}, err
	}
	canonical, err := EncodeConfig(config)
	if err != nil {
		return RuntimeConfig{}, err
	}
	if !bytes.Equal(canonical, data) {
		return RuntimeConfig{}, errors.New("runtime config is not canonical")
	}
	return config, nil
}

// Decode 是 DecodeConfig 的 provider-neutral 别名。
func Decode(data []byte) (RuntimeConfig, error) {
	return DecodeConfig(data)
}

type configHandle interface {
	io.Reader
	Stat() (os.FileInfo, error)
	Close() error
}

type configFileOps struct {
	lstat func(string) (os.FileInfo, error)
	open  func(string) (configHandle, error)
}

func realConfigFileOps() configFileOps {
	return configFileOps{lstat: os.Lstat, open: func(path string) (configHandle, error) { return os.Open(path) }}
}

// LoadConfig 从调用方指定的文件读取并严格解码 runtime config。
func LoadConfig(path string) (RuntimeConfig, error) {
	return loadConfigWithOps(path, realConfigFileOps())
}

func loadConfigWithOps(path string, ops configFileOps) (RuntimeConfig, error) {
	if err := validateLoadConfigInput(path, ops); err != nil {
		return RuntimeConfig{}, err
	}
	initial, err := lstatInitialConfig(path, ops.lstat)
	if err != nil {
		return RuntimeConfig{}, err
	}
	if err := rejectConfigSymlinkComponents(path, ops.lstat); err != nil {
		return RuntimeConfig{}, err
	}
	handle, err := ops.open(path)
	if err != nil {
		return RuntimeConfig{}, fmt.Errorf("open runtime config: %w", err)
	}
	if handle == nil || isNilInterface(handle) {
		return RuntimeConfig{}, errors.New("open runtime config returned an invalid handle")
	}
	data, readErr := readStableConfigHandle(handle, initial)
	closeErr := handle.Close()
	if readErr != nil || closeErr != nil {
		var wrappedClose error
		if closeErr != nil {
			wrappedClose = fmt.Errorf("close runtime config: %w", closeErr)
		}
		return RuntimeConfig{}, errors.Join(readErr, wrappedClose)
	}
	return DecodeConfig(data)
}

func validateLoadConfigInput(path string, ops configFileOps) error {
	if _, err := canonicalAbsolutePath(path, false); err != nil {
		return errors.New("runtime config path is invalid")
	}
	if ops.lstat == nil || ops.open == nil {
		return errors.New("runtime config file operations are invalid")
	}
	return nil
}

func lstatInitialConfig(path string, lstat func(string) (os.FileInfo, error)) (os.FileInfo, error) {
	initial, err := lstat(path)
	if err != nil {
		return nil, fmt.Errorf("stat runtime config: %w", err)
	}
	if err := validateConfigFileInfo(initial); err != nil {
		return nil, err
	}
	return initial, nil
}

func readStableConfigHandle(handle configHandle, initial os.FileInfo) ([]byte, error) {
	opened, err := handle.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat opened runtime config: %w", err)
	}
	if err := compareConfigFileInfo(initial, opened); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(handle, MaxConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read runtime config: %w", err)
	}
	final, err := handle.Stat()
	if err != nil {
		return nil, fmt.Errorf("restat runtime config: %w", err)
	}
	if err := compareConfigFileInfo(initial, final); err != nil {
		return nil, err
	}
	if len(data) > MaxConfigBytes || int64(len(data)) != final.Size() {
		return nil, errors.New("runtime config changed while reading")
	}
	return data, nil
}

func validateConfigFileInfo(info os.FileInfo) error {
	if info == nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("runtime config must be a regular non-symlink file")
	}
	if info.Size() < 0 || info.Size() > MaxConfigBytes {
		return errors.New("runtime config size is invalid")
	}
	return nil
}

func compareConfigFileInfo(expected, actual os.FileInfo) error {
	if err := validateConfigFileInfo(actual); err != nil {
		return err
	}
	if !os.SameFile(expected, actual) || expected.Size() != actual.Size() {
		return errors.New("runtime config identity or size changed")
	}
	return nil
}

func rejectConfigSymlinkComponents(path string, lstat func(string) (os.FileInfo, error)) error {
	clean := filepath.Clean(path)
	current := string(filepath.Separator)
	for component := range strings.SplitSeq(strings.TrimPrefix(clean, current), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lstat runtime config component: %w", err)
		}
		if info == nil {
			return errors.New("lstat runtime config component returned no identity")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("runtime config path contains a symlink component")
		}
	}
	return nil
}

// Validate 校验 runtime config 的 ABI、调用方字段、四节点拓扑和隔离根。
func (config RuntimeConfig) Validate() error {
	if config.SchemaVersion != ConfigSchemaVersion {
		return fmt.Errorf("runtime config schema_version must equal %q", ConfigSchemaVersion)
	}
	if _, err := canonicalAbsolutePath(config.TopologyManifestPath, false); err != nil {
		return errors.New("runtime config topology_manifest_path is invalid")
	}
	if err := validateEnvironmentName(config.OwnerDSNEnv); err != nil {
		return fmt.Errorf("runtime config owner_dsn_env: %w", err)
	}
	if err := validateEnvironmentName(config.ReceiptDSNEnv); err != nil {
		return fmt.Errorf("runtime config receipt_dsn_env: %w", err)
	}
	if config.OwnerDSNEnv == config.ReceiptDSNEnv {
		return errors.New("runtime config owner and receipt DSN environment names must differ")
	}
	if err := validateStableIdentity(config.OwnerNamespace); err != nil {
		return errors.New("runtime config owner_namespace is invalid")
	}
	if _, err := canonicalAbsolutePath(config.SSHExecutable, false); err != nil {
		return errors.New("runtime config ssh_executable is invalid")
	}
	if err := validateTimeouts(config); err != nil {
		return err
	}
	if err := validateNodeSet(config.Nodes); err != nil {
		return err
	}
	return validateRootTopology(config.Nodes)
}

// PolicyDigest 返回同一份 runtime policy 的稳定摘要；target facts 不属于该摘要。
func PolicyDigest(config RuntimeConfig) (string, error) {
	encoded, err := EncodeConfig(config)
	if err != nil {
		return "", fmt.Errorf("encode runtime policy: %w", err)
	}
	digest := sha256.Sum256(append([]byte("remote-ci-runtime-policy/v1\x00"), encoded...))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validateTimeouts(config RuntimeConfig) error {
	for name, value := range map[string]int64{
		"probe_timeout_ms": config.ProbeTimeoutMS, "run_timeout_ms": config.RunTimeoutMS,
		"cleanup_timeout_ms": config.CleanupTimeoutMS, "worker_timeout_ms": config.WorkerTimeoutMS,
	} {
		if value <= 0 || value > math.MaxInt64/int64(time.Millisecond) {
			return fmt.Errorf("runtime config %s is invalid", name)
		}
	}
	return nil
}

func validateNodeSet(nodes []NodeBinding) error {
	if len(nodes) != 4 {
		return errors.New("runtime config must contain exactly four node bindings")
	}
	wanted := []struct {
		id       string
		platform Platform
	}{
		{Ubuntu01NodeID, UbuntuPlatform}, {Ubuntu02NodeID, UbuntuPlatform},
		{Windows01NodeID, WindowsPlatform}, {Windows02NodeID, WindowsPlatform},
	}
	for index, node := range nodes {
		if index > 0 && nodes[index-1].NodeID >= node.NodeID {
			return errors.New("runtime config node bindings must be sorted by node_id")
		}
		if node.NodeID != wanted[index].id || node.Platform != wanted[index].platform {
			return errors.New("runtime config node binding identity or platform is invalid")
		}
		if err := node.Validate(); err != nil {
			return fmt.Errorf("runtime config node %q: %w", node.NodeID, err)
		}
	}
	return nil
}

// Validate 校验单个 node binding 的路径、identity、endpoint 和平台字段。
func (node NodeBinding) Validate() error {
	if err := validateStableIdentity(node.NodeID); err != nil {
		return errors.New("node_id is invalid")
	}
	if err := validateEndpoint(node.Endpoint); err != nil {
		return errors.New("node endpoint is invalid")
	}
	if err := validateNodePaths(node); err != nil {
		return err
	}
	if err := validateNodeIdentities(node); err != nil {
		return err
	}
	return validateNodePlatform(node)
}

func validateNodePaths(node NodeBinding) error {
	for name, value := range map[string]string{
		"probe_executable": node.ProbeExecutable, "probe_config_path": node.ProbeConfigPath,
		"runner_executable": node.RunnerExecutable, "worker_executable": node.WorkerExecutable,
		"go_executable": node.GoExecutable, "repository_root": node.RepositoryRoot,
		"object_store_root": node.ObjectStoreRoot, "worktree_root": node.WorktreeRoot,
		"cache_publication_root": node.CachePublicationRoot, "private_overlay_root": node.PrivateOverlayRoot,
		"temp_root": node.TempRoot,
	} {
		if _, err := canonicalNodePath(value, node.Platform); err != nil {
			return fmt.Errorf("%s is invalid", name)
		}
	}
	return nil
}

func validateNodeIdentities(node NodeBinding) error {
	for name, value := range map[string]string{
		"probe_identity": node.ProbeIdentity, "runner_identity": node.RunnerIdentity,
		"worker_identity": node.WorkerIdentity, "toolchain_identity": node.ToolchainIdentity,
	} {
		if err := validateStableIdentity(value); err != nil {
			return fmt.Errorf("%s is invalid", name)
		}
	}
	return nil
}

func validateNodePlatform(node NodeBinding) error {
	switch node.Platform {
	case UbuntuPlatform:
		if node.Linux == nil {
			return errors.New("ubuntu node requires linux container fields")
		}
		return node.Linux.Validate()
	case WindowsPlatform:
		if node.Linux != nil {
			return errors.New("windows node must not carry linux container fields")
		}
		return nil
	default:
		return errors.New("node platform is unsupported")
	}
}

// Validate 校验 Ubuntu 的 Linux container 字段；Windows 不允许携带该结构。
func (binding LinuxContainerBinding) Validate() error {
	for name, value := range map[string]string{
		"docker_executable": binding.DockerExecutable, "cgroup_root": binding.CgroupRoot,
	} {
		if _, err := canonicalAbsolutePath(value, false); err != nil {
			return fmt.Errorf("%s is invalid", name)
		}
	}
	for name, value := range map[string]string{
		"docker_image": binding.DockerImage, "docker_image_identity": binding.DockerImageIdentity,
		"run_as_user": binding.RunAsUser, "docker_group": binding.DockerGroup,
	} {
		if err := validateStableIdentity(value); err != nil {
			return fmt.Errorf("%s is invalid", name)
		}
	}
	return nil
}

type topologyRoot struct {
	nodeID string
	name   string
	path   string
}

func validateRootTopology(nodes []NodeBinding) error {
	all, err := collectTopologyRoots(nodes)
	if err != nil {
		return err
	}
	if err := validateTopologyRootPairs(all); err != nil {
		return err
	}
	return validateUbuntuRootSharing(nodes)
}

func collectTopologyRoots(nodes []NodeBinding) ([]topologyRoot, error) {
	all := make([]topologyRoot, 0, len(nodes)*5)
	for _, node := range nodes {
		for name, value := range map[string]string{
			"object_store_root": node.ObjectStoreRoot, "worktree_root": node.WorktreeRoot,
			"cache_publication_root": node.CachePublicationRoot, "private_overlay_root": node.PrivateOverlayRoot,
			"temp_root": node.TempRoot,
		} {
			canonical, err := canonicalAbsolutePath(value, true)
			if err != nil {
				return nil, fmt.Errorf("node %q root %s is invalid", node.NodeID, name)
			}
			all = append(all, topologyRoot{nodeID: node.NodeID, name: name, path: canonical})
		}
	}
	return all, nil
}

func validateTopologyRootPairs(all []topologyRoot) error {
	for index := range all {
		for other := index + 1; other < len(all); other++ {
			left, right := all[index], all[other]
			if left.nodeID == right.nodeID && pathsOverlap(left.path, right.path) {
				return fmt.Errorf("node %q roots %s and %s overlap", left.nodeID, left.name, right.name)
			}
			if left.nodeID != right.nodeID && !allowedUbuntuSharedRoot(left, right) && pathsOverlap(left.path, right.path) {
				return fmt.Errorf("node roots %s/%s and %s/%s overlap", left.nodeID, left.name, right.nodeID, right.name)
			}
		}
	}
	return nil
}

func validateUbuntuRootSharing(nodes []NodeBinding) error {
	for _, name := range []string{"object_store_root", "cache_publication_root"} {
		if nodes[0].root(name) != nodes[1].root(name) {
			return fmt.Errorf("Ubuntu nodes must share %s", name)
		}
	}
	for _, name := range []string{"worktree_root", "private_overlay_root", "temp_root"} {
		if pathsOverlap(nodes[0].root(name), nodes[1].root(name)) {
			return fmt.Errorf("Ubuntu nodes must isolate %s", name)
		}
	}
	return nil
}

func allowedUbuntuSharedRoot(left, right topologyRoot) bool {
	if left.name != right.name || (left.name != "object_store_root" && left.name != "cache_publication_root") {
		return false
	}
	return (left.nodeID == Ubuntu01NodeID && right.nodeID == Ubuntu02NodeID) ||
		(left.nodeID == Ubuntu02NodeID && right.nodeID == Ubuntu01NodeID)
}

func (node NodeBinding) root(name string) string {
	switch name {
	case "object_store_root":
		return canonicalPathForComparison(node.ObjectStoreRoot)
	case "worktree_root":
		return canonicalPathForComparison(node.WorktreeRoot)
	case "cache_publication_root":
		return canonicalPathForComparison(node.CachePublicationRoot)
	case "private_overlay_root":
		return canonicalPathForComparison(node.PrivateOverlayRoot)
	case "temp_root":
		return canonicalPathForComparison(node.TempRoot)
	default:
		return ""
	}
}

func validateEnvironmentName(value string) error {
	if err := validateEnvironmentShape(value); err != nil {
		return err
	}
	for _, character := range value {
		if !validEnvironmentCharacter(character) {
			return errors.New("environment name is invalid")
		}
	}
	return nil
}

func validateEnvironmentShape(value string) error {
	if value == "" || len(value) > 128 || value[0] >= '0' && value[0] <= '9' {
		return errors.New("environment name is invalid")
	}
	return nil
}

func validEnvironmentCharacter(character rune) bool {
	return character == '_' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
}

func validateStableIdentity(value string) error {
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return errors.New("stable identity is invalid")
	}
	return nil
}

func validateEndpoint(value string) error {
	host, ok := strings.CutPrefix(value, "ssh://")
	if !ok || strings.TrimSpace(value) != value {
		return errors.New("endpoint is invalid")
	}
	return validateEndpointHost(host)
}

func validateEndpointHost(host string) error {
	if host == "" || len(host) > 256 {
		return errors.New("endpoint is invalid")
	}
	for _, character := range host {
		if !validEndpointCharacter(character) {
			return errors.New("endpoint is invalid")
		}
	}
	return nil
}

func validEndpointCharacter(character rune) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._-@:[]", character)
}

func validatePathText(value string) error {
	if value == "" || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return errors.New("path is invalid")
	}
	if strings.Contains(value, "~") {
		return errors.New("path alias is invalid")
	}
	return nil
}

func normalizeAbsolutePath(value string, allowWindows bool) (string, bool, error) {
	normalized := strings.ReplaceAll(value, "\\", "/")
	isWindows := len(normalized) >= 3 && normalized[1] == ':' && normalized[2] == '/'
	if isWindows {
		if !allowWindows || !isASCIIAlpha(normalized[0]) {
			return "", false, errors.New("windows path is invalid")
		}
		return strings.ToLower(normalized[:1]) + normalized[1:], true, nil
	}
	if !strings.HasPrefix(normalized, "/") || strings.HasPrefix(normalized, "//") {
		return "", false, errors.New("absolute path is invalid")
	}
	return normalized, false, nil
}

func validatePathShape(normalized string) error {
	if strings.Contains(normalized, "//") || strings.HasSuffix(normalized, "/") {
		return errors.New("path separators are not canonical")
	}
	return nil
}

func validatePathComponents(normalized string, isWindows bool) error {
	start := 1
	if isWindows {
		start = 3
	}
	for component := range strings.SplitSeq(normalized[start:], "/") {
		if component == "" || component == "." || component == ".." {
			return errors.New("path component is invalid")
		}
	}
	return nil
}

func isASCIIAlpha(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func canonicalAbsolutePath(value string, allowWindows bool) (string, error) {
	if err := validatePathText(value); err != nil {
		return "", err
	}
	normalized, isWindows, err := normalizeAbsolutePath(value, allowWindows)
	if err != nil {
		return "", err
	}
	if err := validatePathShape(normalized); err != nil {
		return "", err
	}
	if err := validatePathComponents(normalized, isWindows); err != nil {
		return "", err
	}
	return normalized, nil
}

func canonicalNodePath(value string, platform Platform) (string, error) {
	if platform == UbuntuPlatform && strings.Contains(value, "\\") {
		return "", errors.New("Ubuntu path uses a Windows separator")
	}
	if platform == WindowsPlatform && !(len(value) >= 3 && value[1] == ':' && (value[2] == '/' || value[2] == '\\')) {
		return "", errors.New("Windows path is not drive absolute")
	}
	return canonicalAbsolutePath(value, platform == WindowsPlatform)
}

func canonicalPathForComparison(value string) string {
	canonical, _ := canonicalAbsolutePath(value, true)
	return canonical
}

func pathsOverlap(left, right string) bool {
	if left == right {
		return true
	}
	return strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func walkStrictJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkStrictJSONValue(decoder); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func walkStrictJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("JSON null is not allowed")
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return walkStrictJSONObject(decoder)
	case '[':
		for decoder.More() {
			if err := walkStrictJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("JSON array is unterminated")
		}
		return nil
	default:
		return errors.New("JSON delimiter is invalid")
	}
}

func walkStrictJSONObject(decoder *json.Decoder) error {
	seen := map[string]struct{}{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("JSON object key is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("duplicate JSON object key %q", name)
		}
		seen[name] = struct{}{}
		if err := walkStrictJSONValue(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return errors.New("JSON object is unterminated")
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("runtime config has trailing JSON")
	}
	return nil
}
