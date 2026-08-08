// Package trustedlauncher builds and verifies exact-tree host Gate launchers.
package trustedlauncher

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/godistribution"
)

const (
	// ReceiptSchemaVersion identifies the only accepted launcher receipt schema.
	ReceiptSchemaVersion = "trusted-gate-launcher/v1"
	// BinaryName is the fixed executable name inside a content-addressed install.
	BinaryName = "super-dolphin-gate"
	// ReceiptName is the fixed receipt name beside the launcher executable.
	ReceiptName                 = "receipt.json"
	launcherLinkedPayloadSchema = "trusted-gate-launcher-linked-identity/v1"
	launcherLinkedPayloadPrefix = launcherLinkedPayloadSchema + ":"
)

var (
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	treePattern   = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
)

// Receipt records the complete host build identity for one exact Git tree.
type Receipt struct {
	SchemaVersion         string   `json:"schema_version"`
	Tree                  string   `json:"tree"`
	SourceSHA256          string   `json:"source_sha256"`
	ToolchainSHA256       string   `json:"toolchain_sha256"`
	ClosureProvenance     string   `json:"closure_provenance"`
	GoVersion             string   `json:"go_version"`
	GOOS                  string   `json:"goos"`
	GOARCH                string   `json:"goarch"`
	CompilerPath          string   `json:"compiler_path"`
	CompilerSHA256        string   `json:"compiler_sha256"`
	CompilerClosureSHA256 string   `json:"compiler_closure_sha256"`
	BuildArguments        []string `json:"build_arguments"`
	BuildArgumentsSHA256  string   `json:"build_arguments_sha256"`
	BinarySHA256          string   `json:"binary_sha256"`
}

// LinkedIdentity is embedded into the launcher by the Go linker.
type LinkedIdentity struct {
	Tree                  string
	SourceSHA256          string
	ToolchainSHA256       string
	CompilerSHA256        string
	CompilerClosureSHA256 string
	BuildArgumentsSHA256  string
}

// launcherLinkedPayload 是 launcher 严格解码的第一个 linker 全局值。
// trustedlauncher 是该 wire schema、codec 和构建参数摘要的唯一 owner。
type launcherLinkedPayload struct {
	SchemaVersion         string `json:"schema_version"`
	Tree                  string `json:"tree"`
	SourceSHA256          string `json:"source_sha256"`
	ToolchainSHA256       string `json:"toolchain_sha256"`
	CompilerSHA256        string `json:"compiler_sha256"`
	CompilerClosureSHA256 string `json:"compiler_closure_sha256"`
}

func receiptFieldValidators() map[string]func(Receipt) error {
	return map[string]func(Receipt) error{
		"schema_version":          validateReceiptSchemaVersion,
		"tree":                    validateReceiptTree,
		"source_sha256":           validateReceiptSourceSHA256,
		"toolchain_sha256":        validateReceiptToolchainSHA256,
		"closure_provenance":      validateReceiptClosureProvenance,
		"go_version":              validateReceiptGoVersion,
		"goos":                    validateReceiptGOOS,
		"goarch":                  validateReceiptGOARCH,
		"compiler_path":           validateReceiptCompilerPath,
		"compiler_sha256":         validateReceiptCompilerSHA256,
		"compiler_closure_sha256": validateReceiptCompilerClosureSHA256,
		"build_arguments":         validateReceiptBuildArguments,
		"build_arguments_sha256":  validateReceiptBuildArgumentsSHA256,
		"binary_sha256":           validateReceiptBinarySHA256,
	}
}

func validateReceiptSchemaVersion(receipt Receipt) error {
	if receipt.SchemaVersion != ReceiptSchemaVersion {
		return fmt.Errorf("schema_version must be %q", ReceiptSchemaVersion)
	}
	return nil
}

func validateReceiptTree(receipt Receipt) error {
	if !treePattern.MatchString(receipt.Tree) || strings.Trim(receipt.Tree, "0") == "" {
		return errors.New("tree must be a non-zero canonical Git object ID")
	}
	return nil
}

func validateReceiptSourceSHA256(receipt Receipt) error {
	return validateDigestField("source_sha256", receipt.SourceSHA256)
}

func validateReceiptToolchainSHA256(receipt Receipt) error {
	return validateDigestField("toolchain_sha256", receipt.ToolchainSHA256)
}

func validateReceiptClosureProvenance(receipt Receipt) error {
	return validateDigestField("closure_provenance", receipt.ClosureProvenance)
}

func validateReceiptGoVersion(receipt Receipt) error {
	if receipt.GoVersion != godistribution.Version {
		return fmt.Errorf("go_version must be %q", godistribution.Version)
	}
	return nil
}

func validateReceiptGOOS(receipt Receipt) error {
	_, err := godistribution.Lookup(receipt.GOOS, receipt.GOARCH)
	return err
}

func validateReceiptGOARCH(receipt Receipt) error {
	_, err := godistribution.Lookup(receipt.GOOS, receipt.GOARCH)
	return err
}

func validateReceiptCompilerPath(receipt Receipt) error {
	if !filepath.IsAbs(receipt.CompilerPath) || filepath.Clean(receipt.CompilerPath) != receipt.CompilerPath {
		return errors.New("compiler_path must be a clean absolute path")
	}
	return nil
}

func validateReceiptCompilerSHA256(receipt Receipt) error {
	return validateDigestField("compiler_sha256", receipt.CompilerSHA256)
}

func validateReceiptCompilerClosureSHA256(receipt Receipt) error {
	return validateDigestField("compiler_closure_sha256", receipt.CompilerClosureSHA256)
}

func validateReceiptBuildArguments(receipt Receipt) error {
	expected, err := expectedBuildArguments(linkedIdentityFromReceipt(receipt))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(receipt.BuildArguments, expected) {
		return errors.New("build_arguments do not match the canonical launcher build")
	}
	return nil
}

func validateReceiptBuildArgumentsSHA256(receipt Receipt) error {
	if err := validateDigestField("build_arguments_sha256", receipt.BuildArgumentsSHA256); err != nil {
		return err
	}
	observed, err := buildArgumentsIdentityDigest(linkedIdentityFromReceipt(receipt))
	if err != nil {
		return err
	}
	if observed != receipt.BuildArgumentsSHA256 {
		return errors.New("build_arguments_sha256 does not match build_arguments")
	}
	return nil
}

func validateReceiptBinarySHA256(receipt Receipt) error {
	return validateDigestField("binary_sha256", receipt.BinarySHA256)
}

// Validate 动态枚举回执字段并拒绝缺失、陈旧或未知的校验覆盖。
func (receipt Receipt) Validate() error {
	return receipt.validateWithValidators(receiptFieldValidators())
}

func (receipt Receipt) validateWithValidators(validators map[string]func(Receipt) error) error {
	fields, err := receiptJSONFields()
	if err != nil {
		return err
	}
	for _, field := range fields {
		validator, ok := validators[field]
		if !ok {
			return fmt.Errorf("launcher receipt field %q has no validator", field)
		}
		if err := validator(receipt); err != nil {
			return fmt.Errorf("validate launcher receipt %s: %w", field, err)
		}
	}
	if len(validators) != len(fields) {
		return errors.New("launcher receipt validator registry contains stale fields")
	}
	return nil
}

// DecodeReceipt 严格解码单个回执并拒绝未知字段和尾随值。
func DecodeReceipt(data []byte) (Receipt, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var receipt Receipt
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, fmt.Errorf("decode launcher receipt: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Receipt{}, errors.New("decode launcher receipt: trailing JSON value")
	}
	if err := receipt.Validate(); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func encodeReceipt(receipt Receipt) ([]byte, error) {
	if err := receipt.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode launcher receipt: %w", err)
	}
	return append(data, '\n'), nil
}

func receiptJSONFields() ([]string, error) {
	receiptType := reflect.TypeFor[Receipt]()
	fields := make([]string, 0, receiptType.NumField())
	seen := make(map[string]struct{}, receiptType.NumField())
	for field := range receiptType.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			return nil, fmt.Errorf("launcher receipt field %s has no JSON identity", field.Name)
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("launcher receipt JSON field %q is duplicated", name)
		}
		seen[name] = struct{}{}
		fields = append(fields, name)
	}
	sort.Strings(fields)
	return fields, nil
}

func linkedIdentityFromReceipt(receipt Receipt) LinkedIdentity {
	return LinkedIdentity{
		Tree:                  receipt.Tree,
		SourceSHA256:          receipt.SourceSHA256,
		ToolchainSHA256:       receipt.ToolchainSHA256,
		CompilerSHA256:        receipt.CompilerSHA256,
		CompilerClosureSHA256: receipt.CompilerClosureSHA256,
		BuildArgumentsSHA256:  receipt.BuildArgumentsSHA256,
	}
}

func validateDigestField(name, value string) error {
	if !digestPattern.MatchString(value) {
		return fmt.Errorf("%s must be a canonical SHA-256 digest", name)
	}
	return nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func buildArgumentsDigest(arguments []string) (string, error) {
	data, err := json.Marshal(arguments)
	if err != nil {
		return "", fmt.Errorf("encode launcher build arguments: %w", err)
	}
	return digestBytes(data), nil
}

func buildArgumentsIdentityDigest(identity LinkedIdentity) (string, error) {
	_, digest, err := BuildLinkedIdentityValues(identity)
	return digest, err
}

func encodeLauncherLinkedPayload(identity LinkedIdentity) (string, error) {
	payload := launcherLinkedPayload{
		SchemaVersion:         launcherLinkedPayloadSchema,
		Tree:                  identity.Tree,
		SourceSHA256:          identity.SourceSHA256,
		ToolchainSHA256:       identity.ToolchainSHA256,
		CompilerSHA256:        identity.CompilerSHA256,
		CompilerClosureSHA256: identity.CompilerClosureSHA256,
	}
	if err := validateLauncherLinkedPayload(payload); err != nil {
		return "", err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode launcher linked payload: %w", err)
	}
	return launcherLinkedPayloadPrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

// IsLinkedIdentityPayload 判断 linker 值是否声明 canonical launcher payload。
func IsLinkedIdentityPayload(value string) bool {
	return strings.HasPrefix(value, launcherLinkedPayloadPrefix)
}

// BuildLinkedIdentityValues 生成 launcher 唯一允许的两个 linker 值。
func BuildLinkedIdentityValues(identity LinkedIdentity) (string, string, error) {
	linkedPayload, err := encodeLauncherLinkedPayload(identity)
	if err != nil {
		return "", "", err
	}
	digest, err := buildArgumentsDigest(launcherBuildArguments(linkedPayload, ""))
	if err != nil {
		return "", "", err
	}
	return linkedPayload, digest, nil
}

// DecodeLinkedIdentity 严格解码并交叉验证 launcher 的两个 linker 值。
func DecodeLinkedIdentity(linkedPayload, buildArgumentsSHA256 string) (LinkedIdentity, error) {
	payload, err := decodeLauncherLinkedPayload(linkedPayload)
	if err != nil {
		return LinkedIdentity{}, err
	}
	if err := validateLauncherBuildArgumentsDigest(linkedPayload, buildArgumentsSHA256); err != nil {
		return LinkedIdentity{}, err
	}
	return LinkedIdentity{
		Tree:                  payload.Tree,
		SourceSHA256:          payload.SourceSHA256,
		ToolchainSHA256:       payload.ToolchainSHA256,
		CompilerSHA256:        payload.CompilerSHA256,
		CompilerClosureSHA256: payload.CompilerClosureSHA256,
		BuildArgumentsSHA256:  buildArgumentsSHA256,
	}, nil
}

// decodeLauncherLinkedPayload 严格解码唯一版本的 base64url JSON payload。
func decodeLauncherLinkedPayload(linked string) (launcherLinkedPayload, error) {
	encoded, ok := strings.CutPrefix(linked, launcherLinkedPayloadPrefix)
	if !ok || encoded == "" {
		return launcherLinkedPayload{}, errors.New("launcher linked payload prefix is missing")
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return launcherLinkedPayload{}, fmt.Errorf("decode launcher linked payload base64url: %w", err)
	}
	payload, err := decodeLauncherLinkedPayloadJSON(data)
	if err != nil {
		return launcherLinkedPayload{}, err
	}
	if err := validateLauncherLinkedPayload(payload); err != nil {
		return launcherLinkedPayload{}, err
	}
	return payload, nil
}

func decodeLauncherLinkedPayloadJSON(data []byte) (launcherLinkedPayload, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var payload launcherLinkedPayload
	if err := decoder.Decode(&payload); err != nil {
		return launcherLinkedPayload{}, fmt.Errorf("decode launcher linked payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return launcherLinkedPayload{}, errors.New("decode launcher linked payload: trailing JSON value")
	}
	return payload, nil
}

func validateLauncherLinkedPayload(payload launcherLinkedPayload) error {
	return validateLauncherLinkedPayloadWith(payload, launcherLinkedPayloadValidators())
}

// validateLauncherLinkedPayloadWith 校验动态生产字段与给定 validator registry 完整互锁。
func validateLauncherLinkedPayloadWith(payload launcherLinkedPayload, validators map[string]func(launcherLinkedPayload) error) error {
	fields, err := launcherLinkedPayloadJSONFields()
	if err != nil {
		return err
	}
	for _, field := range fields {
		validator, ok := validators[field]
		if !ok {
			return fmt.Errorf("launcher linked payload field %q has no validator", field)
		}
		if err := validator(payload); err != nil {
			return fmt.Errorf("validate launcher linked payload %s: %w", field, err)
		}
	}
	if len(validators) != len(fields) {
		return errors.New("launcher linked payload validator registry contains stale fields")
	}
	return nil
}

func launcherLinkedPayloadValidators() map[string]func(launcherLinkedPayload) error {
	return map[string]func(launcherLinkedPayload) error{
		"schema_version": func(payload launcherLinkedPayload) error {
			if payload.SchemaVersion != launcherLinkedPayloadSchema {
				return fmt.Errorf("schema_version must be %q", launcherLinkedPayloadSchema)
			}
			return nil
		},
		"tree": func(payload launcherLinkedPayload) error {
			if !treePattern.MatchString(payload.Tree) || strings.Trim(payload.Tree, "0") == "" {
				return errors.New("tree must be a non-zero canonical Git object ID")
			}
			return nil
		},
		"source_sha256": func(payload launcherLinkedPayload) error {
			return validateDigestField("source_sha256", payload.SourceSHA256)
		},
		"toolchain_sha256": func(payload launcherLinkedPayload) error {
			return validateDigestField("toolchain_sha256", payload.ToolchainSHA256)
		},
		"compiler_sha256": func(payload launcherLinkedPayload) error {
			return validateDigestField("compiler_sha256", payload.CompilerSHA256)
		},
		"compiler_closure_sha256": func(payload launcherLinkedPayload) error {
			return validateDigestField("compiler_closure_sha256", payload.CompilerClosureSHA256)
		},
	}
}

func launcherLinkedPayloadJSONFields() ([]string, error) {
	payloadType := reflect.TypeFor[launcherLinkedPayload]()
	fields := make([]string, 0, payloadType.NumField())
	seen := make(map[string]struct{}, payloadType.NumField())
	for field := range payloadType.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			return nil, fmt.Errorf("launcher linked payload field %s has no JSON identity", field.Name)
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("launcher linked payload JSON field %q is duplicated", name)
		}
		seen[name] = struct{}{}
		fields = append(fields, name)
	}
	sort.Strings(fields)
	return fields, nil
}

func validateLauncherBuildArgumentsDigest(linkedPayload, observed string) error {
	if err := validateDigestField("build_arguments_sha256", observed); err != nil {
		return err
	}
	expected, err := buildArgumentsDigest(launcherBuildArguments(linkedPayload, ""))
	if err != nil {
		return err
	}
	if observed != expected {
		return errors.New("build_arguments_sha256 does not match canonical launcher build arguments")
	}
	return nil
}

func launcherBuildArguments(linkedPayload, buildArgumentsSHA256 string) []string {
	linked := "-X main.gateSourceDigest=" + linkedPayload + " -X main.gateToolchainDigest=" + buildArgumentsSHA256
	return []string{"build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags", linked, "./cmd/super-dolphin-gate"}
}
