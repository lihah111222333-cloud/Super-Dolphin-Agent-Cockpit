package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	productionBootstrapRootSchemaVersion uint32 = 1
	productionBootstrapVerifierVersion   uint32 = 1
	productionBootstrapRootMaxBytes             = 1 << 20
)

var (
	errProductionBootstrapRootExists   = errors.New("production bootstrap trust root already exists")
	errProductionBootstrapRootNotFound = errors.New("production bootstrap trust root does not exist")
)

// productionBootstrapRoot 是安装包签名、仓库外持久化的 generation-one 信任根。
type productionBootstrapRoot struct {
	SchemaVersion    uint32                                `json:"schema_version"`
	RepoID           string                                `json:"repo_id"`
	RemoteURL        string                                `json:"remote_url"`
	TrustedRef       string                                `json:"trusted_ref"`
	BaselineCommit   string                                `json:"baseline_commit"`
	BaselineTree     string                                `json:"baseline_tree"`
	PolicyDigest     string                                `json:"policy_digest"`
	ImageInputDigest string                                `json:"image_input_digest"`
	Runner           gatecontract.ImageIdentity            `json:"runner"`
	Controller       productionBootstrapControllerIdentity `json:"controller"`
	Signer           gatecontract.SignerIdentity           `json:"signer"`
	Ed25519PublicKey string                                `json:"ed25519_public_key"`
	VerifierVersion  uint32                                `json:"verifier_version"`
	Signature        string                                `json:"signature"`
}

// productionBootstrapControllerIdentity binds the only host executable allowed to
// orchestrate generation-one bootstrap. DesignatedRequirement is evaluated by
// macOS codesign; it is not a claim that this process can inspect Keychain ACLs.
type productionBootstrapControllerIdentity struct {
	BinaryDigest          string                      `json:"binary_digest"`
	DesignatedRequirement string                      `json:"designated_requirement"`
	Signer                gatecontract.SignerIdentity `json:"signer"`
}

// Validate 为 strict decoder 提供结构校验；外部锚点验签仍由 loader 单独完成。
func (root productionBootstrapRoot) Validate() error {
	return validateProductionBootstrapRootShape(root, true)
}

// productionBootstrapRootSigningPayload 返回排除 signature 值的 canonical JSON。
func productionBootstrapRootSigningPayload(root productionBootstrapRoot) ([]byte, error) {
	unsigned := cloneProductionBootstrapRoot(root)
	unsigned.Signature = ""
	if err := validateProductionBootstrapRootShape(unsigned, false); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return nil, fmt.Errorf("marshal production bootstrap root signing payload: %w", err)
	}
	return payload, nil
}

// verifyProductionBootstrapRoot 使用 production config 中的外部锚点验证真实 Ed25519 签名。
func verifyProductionBootstrapRoot(root productionBootstrapRoot, trusted []productionTrustedKey) error {
	if err := validateProductionBootstrapRootShape(root, true); err != nil {
		return err
	}
	publicKey, err := productionBootstrapPublicKey(root, trusted)
	if err != nil {
		return err
	}
	payload, err := productionBootstrapRootSigningPayload(root)
	if err != nil {
		return err
	}
	signature, err := decodeProductionBootstrapSignature(root.Signature)
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, payload, signature) {
		return errors.New("production bootstrap root Ed25519 signature verification failed")
	}
	return nil
}

// loadProductionBootstrapRoot 严格读取单个 0600 文件并在返回前完成验签。
func loadProductionBootstrapRoot(path string, trusted []productionTrustedKey) (productionBootstrapRoot, error) {
	canonical, err := canonicalProductionFile("bootstrap trust root", path)
	if errors.Is(err, os.ErrNotExist) {
		return productionBootstrapRoot{}, errProductionBootstrapRootNotFound
	}
	if err != nil {
		return productionBootstrapRoot{}, err
	}
	data, err := readProductionBootstrapRoot(canonical)
	if err != nil {
		return productionBootstrapRoot{}, err
	}
	var root productionBootstrapRoot
	if err := gatecontract.DecodeStrictJSON(data, &root); err != nil {
		return productionBootstrapRoot{}, fmt.Errorf("decode production bootstrap root: %w", err)
	}
	if err := verifyProductionBootstrapRoot(root, trusted); err != nil {
		return productionBootstrapRoot{}, fmt.Errorf("verify production bootstrap root: %w", err)
	}
	return cloneProductionBootstrapRoot(root), nil
}

// installProductionBootstrapRoot 原子安装已由外部安装包签署的 root，不接收任何私钥。
func installProductionBootstrapRoot(
	path string,
	root productionBootstrapRoot,
	trusted []productionTrustedKey,
) error {
	if err := verifyProductionBootstrapRoot(root, trusted); err != nil {
		return fmt.Errorf("verify production bootstrap root before install: %w", err)
	}
	canonical, err := canonicalProductionBootstrapDestination(path)
	if err != nil {
		return err
	}
	if err := rejectExistingProductionBootstrapRoot(canonical); err != nil {
		return err
	}
	data, err := marshalProductionBootstrapRoot(root)
	if err != nil {
		return err
	}
	return writeProductionBootstrapRootNoReplace(canonical, data)
}

// productionBootstrapRootDigest 返回覆盖完整已签 root 的 canonical SHA-256。
func productionBootstrapRootDigest(root productionBootstrapRoot, trusted []productionTrustedKey) (string, error) {
	if err := verifyProductionBootstrapRoot(root, trusted); err != nil {
		return "", err
	}
	data, err := json.Marshal(root)
	if err != nil {
		return "", fmt.Errorf("marshal production bootstrap root: %w", err)
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum), nil
}

// validateProductionBootstrapRootShape 把 root 校验拆成身份、runner 和签名三个固定边界。
func validateProductionBootstrapRootShape(root productionBootstrapRoot, requireSignature bool) error {
	if err := validateProductionBootstrapRootIdentity(root); err != nil {
		return err
	}
	if err := validateProductionBootstrapRunnerRoot(root); err != nil {
		return err
	}
	return validateProductionBootstrapSigningRoot(root, requireSignature)
}

// validateProductionBootstrapRootIdentity 校验仓库、remote 与 baseline Git 身份。
func validateProductionBootstrapRootIdentity(root productionBootstrapRoot) error {
	if root.SchemaVersion != productionBootstrapRootSchemaVersion {
		return fmt.Errorf("production bootstrap schema_version %d does not match required %d", root.SchemaVersion, productionBootstrapRootSchemaVersion)
	}
	if strings.TrimSpace(root.RepoID) == "" || strings.TrimSpace(root.RepoID) != root.RepoID {
		return errors.New("production bootstrap repo_id is required and canonical")
	}
	if err := validateProductionBootstrapRemoteURL(root.RemoteURL); err != nil {
		return err
	}
	if err := validateProductionBootstrapTrustedRef(root.TrustedRef); err != nil {
		return err
	}
	if err := validateProductionBootstrapOID("baseline_commit", root.BaselineCommit); err != nil {
		return err
	}
	if err := validateProductionBootstrapOID("baseline_tree", root.BaselineTree); err != nil {
		return err
	}
	if err := validateProductionBootstrapDigest("policy_digest", root.PolicyDigest); err != nil {
		return err
	}
	return validateProductionBootstrapDigest("image_input_digest", root.ImageInputDigest)
}

func validateProductionBootstrapTrustedRef(value string) error {
	if !strings.HasPrefix(value, "refs/") || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("production bootstrap trusted_ref must be a canonical full ref")
	}
	return nil
}

// validateProductionBootstrapRunnerRoot 校验固定 OCI identity 与 verifier 版本。
func validateProductionBootstrapRunnerRoot(root productionBootstrapRoot) error {
	if err := root.Runner.Validate(); err != nil {
		return fmt.Errorf("production bootstrap runner: %w", err)
	}
	if root.Runner.OCIIndexDigest == root.Runner.ConfigDigest {
		return errors.New("production bootstrap runner index and config digests must be distinct")
	}
	if root.VerifierVersion != productionBootstrapVerifierVersion {
		return fmt.Errorf("production bootstrap verifier version %d does not match required %d", root.VerifierVersion, productionBootstrapVerifierVersion)
	}
	return validateProductionBootstrapControllerIdentity(root.Controller)
}

// validateProductionBootstrapControllerIdentity 固定二进制、codesign 和 attestation signer。
func validateProductionBootstrapControllerIdentity(identity productionBootstrapControllerIdentity) error {
	if err := validateProductionBootstrapDigest("controller binary_digest", identity.BinaryDigest); err != nil {
		return err
	}
	if err := identity.Signer.Validate(); err != nil {
		return fmt.Errorf("production bootstrap controller signer: %w", err)
	}
	if identity.Signer.Algorithm != gatecontract.SignatureAlgorithmEd25519 {
		return errors.New("production bootstrap controller signer must use Ed25519")
	}
	requirement := identity.DesignatedRequirement
	if strings.TrimSpace(requirement) == "" || strings.TrimSpace(requirement) != requirement ||
		strings.ContainsAny(requirement, "\x00\r\n") || len(requirement) > 4096 {
		return errors.New("production bootstrap controller designated requirement is required and canonical")
	}
	return nil
}

// validateProductionBootstrapDigest 只接受规范、非零 SHA-256。
func validateProductionBootstrapDigest(name string, value string) error {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return fmt.Errorf("production bootstrap %s must be a sha256 digest", name)
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	if err != nil || strings.ToLower(value) != value || bytes.Equal(decoded, make([]byte, len(decoded))) {
		return fmt.Errorf("production bootstrap %s must be canonical and non-zero", name)
	}
	return nil
}

// validateProductionBootstrapSigningRoot 校验 signer、公钥和 signature 形态。
func validateProductionBootstrapSigningRoot(root productionBootstrapRoot, requireSignature bool) error {
	if err := root.Signer.Validate(); err != nil {
		return fmt.Errorf("production bootstrap signer: %w", err)
	}
	if root.Signer.Algorithm != gatecontract.SignatureAlgorithmEd25519 {
		return errors.New("production bootstrap signer must use Ed25519")
	}
	if root.Controller.Signer != root.Signer {
		return errors.New("production bootstrap controller attestation signer must match root signer")
	}
	if _, err := decodeProductionBootstrapPublicKey(root.Ed25519PublicKey); err != nil {
		return err
	}
	if requireSignature && strings.TrimSpace(root.Signature) == "" {
		return errors.New("production bootstrap signature is required")
	}
	return nil
}

// validateProductionBootstrapRemoteURL 只接受无凭据的 canonical HTTPS/SSH URL。
func validateProductionBootstrapRemoteURL(value string) error {
	if err := validateProductionBootstrapRemoteText(value); err != nil {
		return err
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return errors.New("production bootstrap remote_url is invalid")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "ssh" {
		return errors.New("production bootstrap remote_url scheme must be https or ssh")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.String() != value {
		return errors.New("production bootstrap remote_url must be canonical and exclude credentials or fragment")
	}
	return nil
}

// validateProductionBootstrapRemoteText 拒绝空白、控制字符和非 canonical 文本。
func validateProductionBootstrapRemoteText(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("production bootstrap remote_url is required")
	}
	if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("production bootstrap remote_url is not canonical")
	}
	return nil
}

// validateProductionBootstrapOID 校验完整 SHA-1/SHA-256 Git object ID。
func validateProductionBootstrapOID(name string, value string) error {
	if len(value) != 40 && len(value) != 64 {
		return fmt.Errorf("production bootstrap %s must be a full Git object ID", name)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || strings.ToLower(value) != value {
		return fmt.Errorf("production bootstrap %s must be canonical hexadecimal", name)
	}
	if bytes.Equal(decoded, make([]byte, len(decoded))) {
		return fmt.Errorf("production bootstrap %s must be non-zero", name)
	}
	return nil
}

// productionBootstrapPublicKey 从配置锚点选择 key，并拒绝 root 自证公钥。
func productionBootstrapPublicKey(
	root productionBootstrapRoot,
	trusted []productionTrustedKey,
) (ed25519.PublicKey, error) {
	for _, candidate := range trusted {
		if candidate.Signer != root.Signer {
			continue
		}
		if candidate.PublicKey != root.Ed25519PublicKey {
			return nil, errors.New("production bootstrap public key drifted from installation trust anchor")
		}
		return decodeProductionBootstrapPublicKey(candidate.PublicKey)
	}
	return nil, errors.New("production bootstrap signer is absent from installation trust anchors")
}

// decodeProductionBootstrapPublicKey 解码固定长度 Ed25519 public key。
func decodeProductionBootstrapPublicKey(encoded string) (ed25519.PublicKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("production bootstrap public key must be canonical base64 Ed25519")
	}
	return ed25519.PublicKey(append([]byte(nil), decoded...)), nil
}

// decodeProductionBootstrapSignature 解码固定长度 Ed25519 signature。
func decodeProductionBootstrapSignature(encoded string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != ed25519.SignatureSize {
		return nil, errors.New("production bootstrap signature must be canonical base64 Ed25519")
	}
	return decoded, nil
}

// readProductionBootstrapRoot 复用 production 文件身份检查并增加 root 专属大小/newline 合同。
func readProductionBootstrapRoot(path string) ([]byte, error) {
	data, err := readProductionCoordinatorConfig(path)
	if err != nil {
		return nil, fmt.Errorf("read production bootstrap root: %w", err)
	}
	if len(data) > productionBootstrapRootMaxBytes {
		return nil, errors.New("production bootstrap root exceeds size limit")
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return nil, errors.New("production bootstrap root must end with one newline")
	}
	return data, nil
}

// canonicalProductionBootstrapDestination 校验 installer destination 的真实私有父目录。
func canonicalProductionBootstrapDestination(path string) (string, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("production bootstrap root path must be canonical and absolute")
	}
	parent := filepath.Dir(path)
	canonicalParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", fmt.Errorf("canonicalize production bootstrap root directory: %w", err)
	}
	if canonicalParent != parent {
		return "", errors.New("production bootstrap root path must not contain symlinks")
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return "", fmt.Errorf("lstat production bootstrap root directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("production bootstrap root directory must be private and real")
	}
	return path, nil
}

// rejectExistingProductionBootstrapRoot 保证 installer 只允许 absent -> installed。
func rejectExistingProductionBootstrapRoot(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return errProductionBootstrapRootExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("lstat production bootstrap root destination: %w", err)
	}
	return nil
}

// marshalProductionBootstrapRoot 生成以单 newline 终止的 canonical root。
func marshalProductionBootstrapRoot(root productionBootstrapRoot) ([]byte, error) {
	data, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("marshal production bootstrap root for install: %w", err)
	}
	return append(data, '\n'), nil
}

// writeProductionBootstrapRootNoReplace 使用 hard-link commit 实现跨进程单赢家安装。
func writeProductionBootstrapRootNoReplace(path string, data []byte) (retErr error) {
	parent := filepath.Dir(path)
	temp, err := os.CreateTemp(parent, ".bootstrap-root-*.tmp")
	if err != nil {
		return fmt.Errorf("create production bootstrap root temp: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		if tempPath != "" {
			retErr = errors.Join(retErr, os.Remove(tempPath))
		}
	}()
	if err := writeProductionBootstrapTemp(temp, data); err != nil {
		return err
	}
	if err := os.Link(tempPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errProductionBootstrapRootExists
		}
		return fmt.Errorf("install production bootstrap root: %w", err)
	}
	if err := syncProductionBootstrapDirectory(parent); err != nil {
		return err
	}
	return nil
}

// writeProductionBootstrapTemp 写入并 fsync 单个 0600 temp file。
func writeProductionBootstrapTemp(file *os.File, data []byte) error {
	if err := file.Chmod(0o600); err != nil {
		return errors.Join(err, file.Close())
	}
	written, err := file.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}

// syncProductionBootstrapDirectory 固化 installer 目录项。
func syncProductionBootstrapDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open production bootstrap directory for sync: %w", err)
	}
	return errors.Join(directory.Sync(), directory.Close())
}

// cloneProductionBootstrapRoot 深拷贝 slice，避免调用方修改已验证 runner identity。
func cloneProductionBootstrapRoot(root productionBootstrapRoot) productionBootstrapRoot {
	root.Runner.RootFSDiffIDs = slices.Clone(root.Runner.RootFSDiffIDs)
	return root
}
