package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/authorityhttp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody/sqliteauthority"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const configSchemaVersion int64 = 1

const maxConfigBytes int64 = 64 << 10

const maxCredentialBytes int64 = 4 << 10

// The configuration file is small, but its JSON parser still needs explicit
// structural budgets.  These limits keep duplicate-key inspection fail-closed
// for adversarial nesting/member counts instead of relying on the Go stack or
// an unbounded number of maps.
const (
	maxConfigJSONDepth   = 64
	maxConfigJSONMembers = 256
)

// serviceConfig is the complete process configuration. Every authority identity
// remains explicit; values are never derived from the bearer credential or paths.
type serviceConfig struct {
	SchemaVersion  int64               `json:"schema_version"`
	InstanceID     string              `json:"instance_id"`
	Listen         string              `json:"listen"`
	CredentialFile string              `json:"credential_file"`
	Bootstrap      bool                `json:"bootstrap"`
	MaxBodyBytes   int64               `json:"max_body_bytes"`
	MaxConcurrent  int                 `json:"max_concurrent"`
	Authority      authorityConfigJSON `json:"authority"`
}

type authorityConfigJSON struct {
	// SchemaVersion is the authority/wire contract revision (v1); the private
	// SQLite database revision is fixed separately by sqliteauthority.
	SchemaVersion   int64  `json:"schema_version"`
	Path            string `json:"path"`
	ExternalService bool   `json:"external_service"`
	// These generic-caller fields are deliberately excluded from the standalone
	// service JSON schema; ExternalService owns private authority path checks.
	ProductSQLitePath          string `json:"-"`
	WorkspaceRoot              string `json:"-"`
	StoreID                    string `json:"store_id"`
	AuthorityID                string `json:"authority_id"`
	AuthorityClusterID         string `json:"authority_cluster_id"`
	AuthorityProfile           string `json:"authority_profile"`
	AuthorityGeneration        int64  `json:"authority_generation"`
	OwnerID                    string `json:"owner_id"`
	OwnerGeneration            int64  `json:"owner_generation"`
	OwnerFenceDigest           string `json:"owner_fence_digest"`
	AuthorityProfileGeneration int64  `json:"authority_profile_generation"`
	AuthorityProfileDigest     string `json:"authority_profile_digest"`
	SecurityDomainID           string `json:"security_domain_id"`
	SecurityTimeLineageDigest  string `json:"security_time_lineage_digest"`
	SecurityTimeGeneration     int64  `json:"security_time_generation"`
	SecurityTimeRevision       int64  `json:"security_time_revision"`
	RetentionPolicyDigest      string `json:"retention_policy_digest"`
	HeadID                     string `json:"head_id"`
	InitialHeadRootDigest      string `json:"initial_head_root_digest"`
	AntiRollbackGeneration     int64  `json:"anti_rollback_generation"`
	AntiRollbackAnchorDigest   string `json:"anti_rollback_anchor_digest"`
	AntiRollbackAttestationRef string `json:"anti_rollback_attestation_ref"`
	WriterEpoch                int64  `json:"writer_epoch"`
	DeploymentFence            string `json:"deployment_fence"`
}

func (c authorityConfigJSON) domain() sqliteauthority.Config {
	return sqliteauthority.Config{SchemaVersion: c.SchemaVersion, Path: c.Path, ExternalService: c.ExternalService, ProductSQLitePath: c.ProductSQLitePath, WorkspaceRoot: c.WorkspaceRoot, StoreID: c.StoreID, AuthorityID: c.AuthorityID, AuthorityClusterID: c.AuthorityClusterID, AuthorityProfile: c.AuthorityProfile, AuthorityGeneration: c.AuthorityGeneration, OwnerID: c.OwnerID, OwnerGeneration: c.OwnerGeneration, OwnerFenceDigest: c.OwnerFenceDigest, AuthorityProfileGeneration: c.AuthorityProfileGeneration, AuthorityProfileDigest: c.AuthorityProfileDigest, SecurityDomainID: c.SecurityDomainID, SecurityTimeLineageDigest: c.SecurityTimeLineageDigest, SecurityTimeGeneration: c.SecurityTimeGeneration, SecurityTimeRevision: c.SecurityTimeRevision, RetentionPolicyDigest: c.RetentionPolicyDigest, HeadID: c.HeadID, InitialHeadRootDigest: c.InitialHeadRootDigest, AntiRollbackGeneration: c.AntiRollbackGeneration, AntiRollbackAnchorDigest: c.AntiRollbackAnchorDigest, AntiRollbackAttestationRef: c.AntiRollbackAttestationRef, WriterEpoch: c.WriterEpoch, DeploymentFence: c.DeploymentFence}
}

func authorityJSONFromDomain(c sqliteauthority.Config) authorityConfigJSON {
	return authorityConfigJSON{SchemaVersion: c.SchemaVersion, Path: c.Path, ExternalService: c.ExternalService, ProductSQLitePath: c.ProductSQLitePath, WorkspaceRoot: c.WorkspaceRoot, StoreID: c.StoreID, AuthorityID: c.AuthorityID, AuthorityClusterID: c.AuthorityClusterID, AuthorityProfile: c.AuthorityProfile, AuthorityGeneration: c.AuthorityGeneration, OwnerID: c.OwnerID, OwnerGeneration: c.OwnerGeneration, OwnerFenceDigest: c.OwnerFenceDigest, AuthorityProfileGeneration: c.AuthorityProfileGeneration, AuthorityProfileDigest: c.AuthorityProfileDigest, SecurityDomainID: c.SecurityDomainID, SecurityTimeLineageDigest: c.SecurityTimeLineageDigest, SecurityTimeGeneration: c.SecurityTimeGeneration, SecurityTimeRevision: c.SecurityTimeRevision, RetentionPolicyDigest: c.RetentionPolicyDigest, HeadID: c.HeadID, InitialHeadRootDigest: c.InitialHeadRootDigest, AntiRollbackGeneration: c.AntiRollbackGeneration, AntiRollbackAnchorDigest: c.AntiRollbackAnchorDigest, AntiRollbackAttestationRef: c.AntiRollbackAttestationRef, WriterEpoch: c.WriterEpoch, DeploymentFence: c.DeploymentFence}
}

// loadConfig 读取绝对配置路径，并拒绝未知字段、重复字段或尾随数据。
func loadConfig(path string) (serviceConfig, error) {
	return loadConfigWithOpener(path, os.Open)
}

// loadConfigWithOpener 将打开器留作确定性 TOCTOU 回归测试；生产路径固定使用 os.Open。
func loadConfigWithOpener(path string, opener func(string) (*os.File, error)) (serviceConfig, error) {
	var cfg serviceConfig
	if err := validateConfigPath(path); err != nil {
		return cfg, err
	}
	if opener == nil {
		return cfg, errors.New("recovery custody config opener is required")
	}
	parentPath := filepath.Dir(path)
	parentBefore, fileBefore, err := inspectConfigBefore(path, parentPath)
	if err != nil {
		return cfg, err
	}
	file, err := opener(path)
	if err != nil || file == nil {
		return cfg, errors.New("recovery custody config cannot be opened")
	}
	return readOpenedConfig(path, parentPath, parentBefore, fileBefore, file)
}

func validateConfigPath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("recovery custody config path must be an absolute canonical path")
	}
	return nil
}

func inspectConfigBefore(path, parentPath string) (os.FileInfo, os.FileInfo, error) {
	parentInfo, err := lstatPrivateConfigParent(parentPath)
	if err != nil {
		return nil, nil, err
	}
	fileInfo, err := os.Lstat(path)
	if err != nil {
		return nil, nil, errors.New("recovery custody config is unavailable")
	}
	if err := validateConfigFile(path, fileInfo); err != nil {
		return nil, nil, err
	}
	return parentInfo, fileInfo, nil
}

// readOpenedConfig 在已打开句柄上读取配置，并完成路径与父目录的前后身份复核。
func readOpenedConfig(path, parentPath string, parentBefore, fileBefore os.FileInfo, file *os.File) (serviceConfig, error) {
	var cfg serviceConfig
	defer func() { _ = file.Close() }()
	handleInfo, err := file.Stat()
	if err != nil || !sameStableFile(fileBefore, handleInfo) {
		return cfg, errors.New("recovery custody config identity changed")
	}
	if err := verifyConfigParent(parentPath, parentBefore); err != nil {
		return cfg, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return cfg, errors.New("recovery custody config cannot be read")
	}
	if int64(len(data)) > maxConfigBytes {
		return cfg, errors.New("recovery custody config exceeds 64 KiB")
	}
	fileAfter, err := os.Lstat(path)
	if err != nil || !sameStableFile(fileBefore, fileAfter) {
		return cfg, errors.New("recovery custody config identity changed")
	}
	if err := verifyConfigParent(parentPath, parentBefore); err != nil {
		return cfg, err
	}
	return decodeConfig(data)
}

func verifyConfigParent(path string, expected os.FileInfo) error {
	actual, err := lstatPrivateConfigParent(path)
	if err != nil || !sameStableFile(expected, actual) {
		return errors.New("recovery custody config parent changed")
	}
	return nil
}

func lstatPrivateConfigParent(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("recovery custody config parent must be a private directory")
	}
	if err := securefs.CheckPrivateOwnerOnly(path, info); err != nil {
		return nil, errors.New("recovery custody config parent must be a private directory")
	}
	return info, nil
}

// validateConfigFile 要求配置是有界、私有且权限严格为 0600 的普通文件。
func validateConfigFile(path string, info os.FileInfo) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("recovery custody config must be a regular non-symlink file")
	}
	if info.Size() < 0 || info.Size() > maxConfigBytes {
		return errors.New("recovery custody config exceeds 64 KiB")
	}
	if info.Mode().Perm() != 0o600 {
		return errors.New("recovery custody config must have mode 0600")
	}
	if err := securefs.CheckPrivateOwnerOnly(path, info); err != nil {
		return errors.New("recovery custody config must be private")
	}
	return nil
}

// sameStableFile 比较文件身份以及读取期间必须保持不变的大小和权限。
func sameStableFile(expected, actual os.FileInfo) bool {
	return expected != nil && actual != nil && os.SameFile(expected, actual) &&
		expected.Size() == actual.Size() && expected.Mode().Type() == actual.Mode().Type() &&
		expected.Mode().Perm() == actual.Mode().Perm()
}

// decodeConfig 对 JSON 字节执行重复字段、未知字段、尾随数据和领域配置校验。
func decodeConfig(data []byte) (serviceConfig, error) {
	var cfg serviceConfig
	if int64(len(data)) > maxConfigBytes {
		return cfg, errors.New("recovery custody config exceeds 64 KiB")
	}
	if duplicate, err := duplicateTopLevelField(data); err != nil {
		return cfg, errors.New("recovery custody config is invalid JSON")
	} else if duplicate {
		return cfg, errors.New("recovery custody config contains duplicate fields")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, errors.New("recovery custody config is invalid JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return cfg, errors.New("recovery custody config contains trailing data")
		}
		return cfg, errors.New("recovery custody config contains invalid trailing data")
	}
	if cfg.SchemaVersion != configSchemaVersion {
		return cfg, errors.New("recovery custody config schema_version must be 1")
	}
	if err := validateServiceConfig(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// duplicateTopLevelField 扫描配置对象并拒绝任意嵌套对象中的重复字段。
//
// encoding/json 默认接受重复对象键并以最后一个值覆盖前值。配置中的
// authority 是嵌套对象，因此只扫描根对象会让重复 authority 字段静默
// 改变语义。这里先做有界令牌遍历，再交给 DisallowUnknownFields 解码；
// 错误不会包含令牌或配置内容，避免把秘密带入日志。
func duplicateTopLevelField(data []byte) (bool, error) {
	if int64(len(data)) > maxConfigBytes {
		return false, errors.New("config JSON exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return false, err
	}
	if first != json.Delim('{') {
		return false, nil
	}
	members := 0
	duplicate, err := scanConfigJSONObject(decoder, 0, &members)
	if err != nil {
		return false, err
	}
	return duplicate, nil
}

// scanConfigJSONValue 在已读取值令牌后递归检查对象和数组，并执行结构上限。
// scanConfigJSONValue consumes one JSON value after its opening delimiter (if
// any), recursively checking every object and array while enforcing bounds.
func scanConfigJSONValue(decoder *json.Decoder, depth int, members *int) (bool, error) {
	token, err := decoder.Token()
	if err != nil {
		return false, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return false, nil
	}
	if depth >= maxConfigJSONDepth {
		return false, errors.New("config JSON nesting exceeds limit")
	}
	switch delimiter {
	case '{':
		return scanConfigJSONObject(decoder, depth, members)
	case '[':
		return scanConfigJSONArray(decoder, depth, members)
	default:
		return false, errors.New("config JSON contains an unexpected delimiter")
	}
}

// scanConfigJSONObject 递归扫描对象成员并拒绝同一对象内的重复键。
func scanConfigJSONObject(decoder *json.Decoder, depth int, members *int) (bool, error) {
	seen := make(map[string]struct{})
	for decoder.More() {
		duplicate, err := scanConfigJSONObjectMember(decoder, depth, members, seen)
		if err != nil || duplicate {
			return duplicate, err
		}
	}
	return false, expectConfigJSONDelimiter(decoder, json.Delim('}'), "object")
}

// scanConfigJSONObjectMember 读取一个对象成员，检查键唯一性并递归扫描值。
func scanConfigJSONObjectMember(decoder *json.Decoder, depth int, members *int, seen map[string]struct{}) (bool, error) {
	key, err := decoder.Token()
	if err != nil {
		return false, err
	}
	name, ok := key.(string)
	if !ok {
		return false, errors.New("config JSON object key is not a string")
	}
	if _, exists := seen[name]; exists {
		return true, nil
	}
	seen[name] = struct{}{}
	if err := countConfigJSONMember(members); err != nil {
		return false, err
	}
	return scanConfigJSONValue(decoder, depth+1, members)
}

// scanConfigJSONArray 递归扫描数组成员，并在读取结束符时确认结构完整。
func scanConfigJSONArray(decoder *json.Decoder, depth int, members *int) (bool, error) {
	for decoder.More() {
		if err := countConfigJSONMember(members); err != nil {
			return false, err
		}
		duplicate, err := scanConfigJSONValue(decoder, depth+1, members)
		if err != nil || duplicate {
			return duplicate, err
		}
	}
	return false, expectConfigJSONDelimiter(decoder, json.Delim(']'), "array")
}

func expectConfigJSONDelimiter(decoder *json.Decoder, want json.Delim, kind string) error {
	end, err := decoder.Token()
	if err != nil {
		return err
	}
	if end != want {
		return fmt.Errorf("config JSON %s is not terminated", kind)
	}
	return nil
}

func countConfigJSONMember(members *int) error {
	*members++
	if *members > maxConfigJSONMembers {
		return errors.New("config JSON member limit exceeded")
	}
	return nil
}

// validateServiceConfig enforces loopback-only transport, private credential
// storage, bounded resources and the authority package's path isolation rules.
// validateServiceConfig 校验身份、loopback 监听、私有凭据、资源上限和 authority 路径隔离。
func validateServiceConfig(cfg serviceConfig) error {
	if err := validateServiceIdentity(cfg); err != nil {
		return err
	}
	if err := validateServiceLimits(cfg); err != nil {
		return err
	}
	return validateExternalAuthority(cfg)
}

// validateServiceIdentity 校验实例标识、loopback 监听和凭据路径。
func validateServiceIdentity(cfg serviceConfig) error {
	if err := validateText("instance_id", cfg.InstanceID, 128); err != nil {
		return err
	}
	if err := validateLoopbackAddress(cfg.Listen); err != nil {
		return err
	}
	if !filepath.IsAbs(cfg.CredentialFile) || filepath.Clean(cfg.CredentialFile) != cfg.CredentialFile {
		return errors.New("recovery custody credential_file must be an absolute canonical path")
	}
	return nil
}

// validateServiceLimits 校验 HTTP body 和并发上限，避免资源隐式无限增长。
func validateServiceLimits(cfg serviceConfig) error {
	if cfg.MaxBodyBytes <= 0 || cfg.MaxBodyBytes > 64<<20 {
		return errors.New("recovery custody max_body_bytes must be between 1 and 67108864")
	}
	if cfg.MaxConcurrent <= 0 || cfg.MaxConcurrent > 1024 {
		return errors.New("recovery custody max_concurrent must be between 1 and 1024")
	}
	return nil
}

// validateExternalAuthority 校验服务必须使用独立 private-only authority 模式。
func validateExternalAuthority(cfg serviceConfig) error {
	if !cfg.Authority.ExternalService {
		return errors.New("recovery custody authority external_service must be true")
	}
	if cfg.Authority.ProductSQLitePath != "" || cfg.Authority.WorkspaceRoot != "" {
		return errors.New("recovery custody authority product/workspace paths must be empty in external_service mode")
	}
	if err := cfg.Authority.domain().Validate(); err != nil {
		return fmt.Errorf("recovery custody authority config: %w", err)
	}
	return nil
}

func validateText(name, value string, limit int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > limit || strings.ContainsRune(value, 0) {
		return fmt.Errorf("recovery custody %s must be bounded canonical text", name)
	}
	return nil
}

// validateLoopbackAddress 只接受显式 IPv4 loopback TCP 地址。
func validateLoopbackAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return errors.New("recovery custody listen must be an explicit 127.0.0.1 TCP address")
	}
	if strings.HasPrefix(port, "+") || port == "" {
		return errors.New("recovery custody listen port is invalid")
	}
	parsed, err := strconv.Atoi(port)
	if err != nil || parsed < 0 || parsed > 65535 {
		return errors.New("recovery custody listen port is invalid")
	}
	return nil
}

// readCredential 校验 owner-only 凭据文件并返回 opaque BearerCredential，错误绝不包含凭据值。
func readCredential(path string) (authorityhttp.BearerCredential, error) {
	var zero authorityhttp.BearerCredential
	parentPath := filepath.Dir(path)
	parentBefore, fileBefore, err := inspectCredentialBefore(path, parentPath)
	if err != nil {
		return zero, err
	}
	file, err := os.Open(path)
	if err != nil || file == nil {
		return zero, errors.New("recovery custody credential_file cannot be read")
	}
	return readOpenedCredential(path, parentPath, parentBefore, fileBefore, file)
}

func inspectCredentialBefore(path, parentPath string) (os.FileInfo, os.FileInfo, error) {
	parentInfo, err := lstatPrivateCredentialParent(parentPath)
	if err != nil {
		return nil, nil, err
	}
	fileInfo, err := os.Lstat(path)
	if err != nil {
		return nil, nil, errors.New("recovery custody credential_file is unavailable")
	}
	if err := validateCredentialFile(path, fileInfo); err != nil {
		return nil, nil, err
	}
	return parentInfo, fileInfo, nil
}

// readOpenedCredential 在已打开句柄上有界读取凭据，并复核文件与父目录身份。
func readOpenedCredential(path, parentPath string, parentBefore, fileBefore os.FileInfo, file *os.File) (authorityhttp.BearerCredential, error) {
	var zero authorityhttp.BearerCredential
	defer func() { _ = file.Close() }()
	handleInfo, err := file.Stat()
	if err != nil || !sameStableFile(fileBefore, handleInfo) {
		return zero, errors.New("recovery custody credential_file identity changed")
	}
	if err := verifyCredentialParent(parentPath, parentBefore); err != nil {
		return zero, err
	}
	data, err := readCredentialBody(file)
	if err != nil {
		return zero, err
	}
	if err := verifyCredentialFile(path, fileBefore); err != nil {
		return zero, err
	}
	if err := verifyCredentialParent(parentPath, parentBefore); err != nil {
		return zero, err
	}
	credential, err := authorityhttp.NewBearerCredential(string(data))
	if err != nil {
		return zero, errors.New("recovery custody credential_file contains an invalid credential")
	}
	return credential, nil
}

func readCredentialBody(file io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(file, maxCredentialBytes+1))
	if err != nil || int64(len(data)) > maxCredentialBytes {
		return nil, errors.New("recovery custody credential_file cannot be read")
	}
	return data, nil
}

// lstatPrivateCredentialParent 要求凭据父目录是非符号链接且严格为 0700/private。
func lstatPrivateCredentialParent(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return nil, errors.New("recovery custody credential_file parent must have mode 0700")
	}
	if err := securefs.CheckPrivateOwnerOnly(path, info); err != nil {
		return nil, errors.New("recovery custody credential_file parent must have mode 0700")
	}
	return info, nil
}

// validateCredentialFile 要求凭据是有界、私有且权限严格为 0600 的普通文件。
func validateCredentialFile(path string, info os.FileInfo) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("recovery custody credential_file must be a regular non-symlink file")
	}
	if info.Mode().Perm() != 0o600 {
		return errors.New("recovery custody credential_file must have mode 0600")
	}
	if info.Size() < 0 || info.Size() > maxCredentialBytes {
		return errors.New("recovery custody credential_file cannot be read")
	}
	if err := securefs.CheckPrivateOwnerOnly(path, info); err != nil {
		return errors.New("recovery custody credential_file must be private")
	}
	return nil
}

func verifyCredentialParent(path string, expected os.FileInfo) error {
	actual, err := lstatPrivateCredentialParent(path)
	if err != nil || !sameStableFile(expected, actual) {
		return errors.New("recovery custody credential_file parent changed")
	}
	return nil
}

func verifyCredentialFile(path string, expected os.FileInfo) error {
	actual, err := os.Lstat(path)
	if err != nil || !sameStableFile(expected, actual) {
		return errors.New("recovery custody credential_file identity changed")
	}
	return nil
}
