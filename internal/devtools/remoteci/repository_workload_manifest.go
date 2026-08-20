package remoteci

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"io"
	"path"
	"reflect"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// RepositoryWorkloadManifestMaxBytes 是 repository-owned manifest 的总字节上限。
	RepositoryWorkloadManifestMaxBytes = 1 << 20
	repositoryWorkloadManifestMaxBytes = RepositoryWorkloadManifestMaxBytes
	// RepositoryWorkloadManifestEntryDigestDomain 与 manifest digest 使用不同密码学域。
	RepositoryWorkloadManifestEntryDigestDomain = "portable-workload-manifest-entry/v1"
	RepositoryWorkloadManifestDigestDomain      = "portable-workload-manifest/v1"
	RepositoryWorkloadManifestABIDigestDomain   = "portable-workload-manifest-abi/v1"
	RepositoryWorkloadKindGoTest                = "go_test"
	RepositoryWorkloadPlatformLinuxAMD64        = "linux/amd64"
)

// RepositoryWorkloadKind 是 portable manifest 中受支持的 typed workload 类别。
type RepositoryWorkloadKind string

// RepositoryWorkloadPlatform 是 workload 的 canonical 目标平台。
type RepositoryWorkloadPlatform string

// RepositoryWorkloadExecutionPolicy 是 exact Go test 的显式执行策略。
type RepositoryWorkloadExecutionPolicy string

const (
	RepositoryWorkloadExecutionPolicyNormal RepositoryWorkloadExecutionPolicy = "normal"
	RepositoryWorkloadExecutionPolicyRace   RepositoryWorkloadExecutionPolicy = "race"
)

// RepositoryWorkloadManifest 是 target tree 自有的 portable-workload-manifest/v1。
// v1 只有 typed exact go_test workload，故 wire 中没有 argv、shell 或命令字符串。
type RepositoryWorkloadManifest struct {
	SchemaVersion         string                     `json:"schema_version"`
	RepositoryID          string                     `json:"repository_id"`
	Modules               []RepositoryWorkloadModule `json:"modules"`
	ExcludedNestedModules []string                   `json:"excluded_nested_modules"`
	Workloads             []RepositoryWorkload       `json:"workloads"`
	ManifestDigest        string                     `json:"manifest_digest"`
}

// RepositoryWorkloadModule binds one repository-relative module root to its Go module path.
type RepositoryWorkloadModule struct {
	Root       string `json:"root"`
	ModulePath string `json:"module_path"`
}

// RepositoryWorkload is the only executable v1 workload shape.
type RepositoryWorkload struct {
	ID              string                            `json:"id"`
	Kind            RepositoryWorkloadKind            `json:"kind"`
	ModuleRoot      string                            `json:"module_root"`
	Package         string                            `json:"package"`
	Name            string                            `json:"name"`
	Platform        RepositoryWorkloadPlatform        `json:"platform"`
	ExecutionPolicy RepositoryWorkloadExecutionPolicy `json:"execution_policy"`
	ExecutionDigest string                            `json:"execution_digest"`
}

// Validate 拒绝形状含糊、字段漂移或摘要不一致的仓库 workload manifest。
func (manifest RepositoryWorkloadManifest) Validate() error {
	if err := validateRepositoryManifestShape(manifest); err != nil {
		return err
	}
	for index, workload := range manifest.Workloads {
		digest, err := repositoryWorkloadEntryDigest(workload)
		if err != nil {
			return fmt.Errorf("workload %d: %w", index, err)
		}
		if workload.ExecutionDigest != digest {
			return fmt.Errorf("workload %q execution_digest drifted", workload.ID)
		}
	}
	material := manifest
	material.ManifestDigest = ""
	digest, err := repositoryWorkloadManifestDigest(material)
	if err != nil {
		return err
	}
	if manifest.ManifestDigest != digest {
		return errors.New("repository workload manifest_digest drifted")
	}
	return nil
}

// CanonicalEncode 重算全部摘要并生成 canonical JSON，绝不信任调用方摘要。
func (manifest RepositoryWorkloadManifest) CanonicalEncode() ([]byte, error) {
	if err := validateRepositoryManifestShape(manifest); err != nil {
		return nil, err
	}
	manifest.Modules = slices.Clone(manifest.Modules)
	manifest.ExcludedNestedModules = slices.Clone(manifest.ExcludedNestedModules)
	manifest.Workloads = slices.Clone(manifest.Workloads)
	for index := range manifest.Workloads {
		digest, err := repositoryWorkloadEntryDigest(manifest.Workloads[index])
		if err != nil {
			return nil, fmt.Errorf("workload %d: %w", index, err)
		}
		manifest.Workloads[index].ExecutionDigest = digest
	}
	manifest.ManifestDigest = ""
	digest, err := repositoryWorkloadManifestDigest(manifest)
	if err != nil {
		return nil, err
	}
	manifest.ManifestDigest = digest
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode repository workload manifest: %w", err)
	}
	if len(encoded) > repositoryWorkloadManifestMaxBytes {
		return nil, errors.New("repository workload manifest exceeds 1 MiB")
	}
	return encoded, nil
}

// DecodeRepositoryWorkloadManifest 严格解码 canonical manifest 字节。
func DecodeRepositoryWorkloadManifest(data []byte) (RepositoryWorkloadManifest, error) {
	if len(data) == 0 || len(data) > repositoryWorkloadManifestMaxBytes {
		return RepositoryWorkloadManifest{}, errors.New("repository workload manifest size is invalid")
	}
	if err := walkRepositoryManifestJSON(data); err != nil {
		return RepositoryWorkloadManifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest RepositoryWorkloadManifest
	if err := decoder.Decode(&manifest); err != nil {
		return RepositoryWorkloadManifest{}, fmt.Errorf("decode repository workload manifest: %w", err)
	}
	if err := ensureRepositoryManifestEOF(decoder); err != nil {
		return RepositoryWorkloadManifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return RepositoryWorkloadManifest{}, fmt.Errorf("validate repository workload manifest: %w", err)
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return RepositoryWorkloadManifest{}, fmt.Errorf("marshal repository workload manifest: %w", err)
	}
	if !bytes.Equal(canonical, data) {
		return RepositoryWorkloadManifest{}, errors.New("repository workload manifest is not canonical")
	}
	return manifest, nil
}

// ValidateRepositoryWorkloadManifest 为 codec 调用方提供 Validate 的函数形式。
func ValidateRepositoryWorkloadManifest(manifest RepositoryWorkloadManifest) error {
	return manifest.Validate()
}

// CanonicalEncode 为 codec 调用方提供 manifest 方法的函数形式。
func CanonicalEncode(manifest RepositoryWorkloadManifest) ([]byte, error) {
	return manifest.CanonicalEncode()
}

// EncodeRepositoryWorkloadManifest 返回 canonical 字节及冻结后的 manifest 摘要。
func EncodeRepositoryWorkloadManifest(manifest RepositoryWorkloadManifest) ([]byte, string, error) {
	encoded, err := manifest.CanonicalEncode()
	if err != nil {
		return nil, "", err
	}
	decoded, err := DecodeRepositoryWorkloadManifest(encoded)
	if err != nil {
		return nil, "", err
	}
	return encoded, decoded.ManifestDigest, nil
}

// validateRepositoryManifestShape 校验顶层身份、非空数组与嵌套集合。
func validateRepositoryManifestShape(manifest RepositoryWorkloadManifest) error {
	if manifest.SchemaVersion != PortableWorkloadManifestSchemaVersion {
		return fmt.Errorf("schema_version must equal %q", PortableWorkloadManifestSchemaVersion)
	}
	if err := validateRepositoryIdentifier(manifest.RepositoryID, "repository_id"); err != nil {
		return err
	}
	if manifest.Modules == nil || manifest.ExcludedNestedModules == nil || manifest.Workloads == nil {
		return errors.New("manifest arrays must be present and non-null")
	}
	if len(manifest.Modules) == 0 || len(manifest.Workloads) == 0 {
		return errors.New("manifest requires at least one module and workload")
	}
	if err := validateRepositoryModules(manifest.Modules); err != nil {
		return err
	}
	if err := validateExcludedNestedModules(manifest.ExcludedNestedModules); err != nil {
		return err
	}
	return validateRepositoryWorkloads(manifest.Modules, manifest.ExcludedNestedModules, manifest.Workloads)
}

// validateRepositoryModules 校验模块根、module path、排序和唯一性。
func validateRepositoryModules(modules []RepositoryWorkloadModule) error {
	seenRoots := make(map[string]struct{}, len(modules))
	seenPaths := make(map[string]struct{}, len(modules))
	for index, module := range modules {
		if err := validateModuleRoot(module.Root); err != nil {
			return fmt.Errorf("module %d root: %w", index, err)
		}
		if err := validateModulePath(module.ModulePath); err != nil {
			return fmt.Errorf("module %d module_path: %w", index, err)
		}
		if _, ok := seenRoots[module.Root]; ok {
			return fmt.Errorf("module root %q is duplicated", module.Root)
		}
		if _, ok := seenPaths[module.ModulePath]; ok {
			return fmt.Errorf("module path %q is duplicated", module.ModulePath)
		}
		seenRoots[module.Root], seenPaths[module.ModulePath] = struct{}{}, struct{}{}
		if index > 0 && modules[index-1].Root >= module.Root {
			return errors.New("modules must be sorted by unique root")
		}
	}
	return nil
}

// validateExcludedNestedModules 校验排除模块路径的排序和唯一性。
func validateExcludedNestedModules(modules []string) error {
	seen := make(map[string]struct{}, len(modules))
	for index, module := range modules {
		if err := validateRelativePath(module, false); err != nil {
			return fmt.Errorf("excluded_nested_modules[%d]: %w", index, err)
		}
		if _, ok := seen[module]; ok {
			return fmt.Errorf("excluded nested module %q is duplicated", module)
		}
		seen[module] = struct{}{}
		if index > 0 && modules[index-1] >= module {
			return errors.New("excluded_nested_modules must be sorted and unique")
		}
	}
	return nil
}

// validateRepositoryWorkloads 校验 workload 与模块集合的绑定及稳定排序。
func validateRepositoryWorkloads(modules []RepositoryWorkloadModule, excluded []string, workloads []RepositoryWorkload) error {
	moduleRoots := make(map[string]struct{}, len(modules))
	for _, module := range modules {
		moduleRoots[module.Root] = struct{}{}
	}
	excludedSet := make(map[string]struct{}, len(excluded))
	for _, module := range excluded {
		excludedSet[module] = struct{}{}
	}
	seen := make(map[string]struct{}, len(workloads))
	for index, workload := range workloads {
		if err := validateRepositoryWorkload(moduleRoots, excludedSet, workload); err != nil {
			return fmt.Errorf("workload %d: %w", index, err)
		}
		if _, ok := seen[workload.ID]; ok {
			return fmt.Errorf("workload %q is duplicated", workload.ID)
		}
		seen[workload.ID] = struct{}{}
		if index > 0 && workloads[index-1].ID >= workload.ID {
			return errors.New("workloads must be sorted by unique id")
		}
	}
	return nil
}

// validateRepositoryWorkload 校验单个 typed exact Go test workload。
func validateRepositoryWorkload(moduleRoots map[string]struct{}, excluded map[string]struct{}, workload RepositoryWorkload) error {
	if err := validateRepositoryIdentifier(workload.ID, "workload id"); err != nil {
		return err
	}
	if workload.Kind != RepositoryWorkloadKindGoTest {
		return errors.New("workload kind must be go_test")
	}
	if _, ok := moduleRoots[workload.ModuleRoot]; !ok {
		return fmt.Errorf("module_root %q is not declared", workload.ModuleRoot)
	}
	if err := validatePackagePath(workload.Package); err != nil {
		return err
	}
	if packageEntersExcludedModule(workload.ModuleRoot, workload.Package, excluded) {
		return errors.New("workload package enters an excluded nested module")
	}
	if err := validateGoTestName(workload.Name); err != nil {
		return err
	}
	if workload.Platform != RepositoryWorkloadPlatformLinuxAMD64 {
		return errors.New("workload platform must be linux/amd64")
	}
	if !validExecutionPolicy(workload.ExecutionPolicy) {
		return errors.New("workload execution_policy is unsupported")
	}
	return nil
}

func validateModuleRoot(root string) error {
	if root != "." {
		if err := validateRelativePath(root, false); err != nil {
			return err
		}
	}
	return nil
}

// validateModulePath 校验 Go module path 不含路径逃逸或平台分隔符。
func validateModulePath(modulePath string) error {
	if modulePath == "" || strings.TrimSpace(modulePath) != modulePath || path.IsAbs(modulePath) ||
		path.Clean(modulePath) != modulePath || strings.Contains(modulePath, "\\") || hasControl(modulePath) {
		return errors.New("module_path is not a clean module path")
	}
	for component := range strings.SplitSeq(modulePath, "/") {
		if component == ".." || component == "" {
			return errors.New("module_path contains an invalid component")
		}
	}
	return nil
}

// validateRelativePath 校验仓库相对 slash path 与每个 path component。
func validateRelativePath(value string, allowDot bool) error {
	if !validRelativePathShape(value, allowDot) {
		return errors.New("path is not a clean repository-relative slash path")
	}
	for component := range strings.SplitSeq(value, "/") {
		if component == ".." || component == "" {
			return errors.New("path contains an invalid component")
		}
	}
	return nil
}

// validRelativePathShape 判断相对路径的顶层词法形状。
func validRelativePathShape(value string, allowDot bool) bool {
	return value != "" && (allowDot || value != ".") && !path.IsAbs(value) &&
		path.Clean(value) == value && !strings.Contains(value, "\\") && !hasControl(value)
}

// validatePackagePath 校验 module-relative exact package，不允许递归模式。
func validatePackagePath(value string) error {
	if !validPackagePathShape(value) {
		return errors.New("package must be a ./ repository-relative package without ...")
	}
	relative := strings.TrimPrefix(value, "./")
	if relative != "" && path.Clean(relative) != relative {
		return errors.New("package is not clean")
	}
	for component := range strings.SplitSeq(relative, "/") {
		if component == ".." || component == "" {
			return errors.New("package contains an invalid component")
		}
	}
	return nil
}

// validPackagePathShape 判断 package 的前缀与字符集形状。
func validPackagePathShape(value string) bool {
	return strings.HasPrefix(value, "./") && !strings.Contains(value, "...") && value != "./" &&
		!strings.Contains(value, "\\") && !hasControl(value)
}

// packageEntersExcludedModule 判断 module-relative package 是否落入已排除 nested module。
func packageEntersExcludedModule(moduleRoot, packagePath string, excluded map[string]struct{}) bool {
	relativePackage := strings.TrimPrefix(packagePath, "./")
	repositoryPath := relativePackage
	if moduleRoot != "." {
		repositoryPath = path.Join(moduleRoot, relativePackage)
	}
	for excludedRoot := range excluded {
		if repositoryPath == excludedRoot || strings.HasPrefix(repositoryPath, excludedRoot+"/") {
			return true
		}
	}
	return false
}

// validateGoTestName 校验 top-level Test、Fuzz 或 Example 名称。
func validateGoTestName(name string) error {
	if !token.IsIdentifier(name) {
		return errors.New("name is not a Go identifier")
	}
	for _, prefix := range []string{"Test", "Fuzz"} {
		if suffix, ok := strings.CutPrefix(name, prefix); ok {
			if suffix == "" {
				return nil
			}
			first, _ := utf8.DecodeRuneInString(suffix)
			if unicode.IsLower(first) {
				return errors.New("name has a lowercase Go test suffix")
			}
			return nil
		}
	}
	if name == "Example" || strings.HasPrefix(name, "Example") {
		return nil
	}
	return errors.New("name must be a top-level Test, Fuzz, or Example")
}

func validateRepositoryIdentifier(value, label string) error {
	if value == "" || strings.TrimSpace(value) != value || hasControl(value) || strings.Contains(value, "\\") {
		return fmt.Errorf("%s is invalid", label)
	}
	return nil
}

func validExecutionPolicy(policy RepositoryWorkloadExecutionPolicy) bool {
	switch policy {
	case RepositoryWorkloadExecutionPolicyNormal, RepositoryWorkloadExecutionPolicyRace:
		return true
	default:
		return false
	}
}

type repositoryWorkloadManifestABIField struct {
	goName          string
	jsonName        string
	goType          string
	wireKind        string
	elementRelation string
}

// RepositoryWorkloadManifestABIDigest 返回由 manifest wire 类型动态派生的 ABI 摘要。
// ABI 摘要与 manifest 内容摘要使用独立密码学域，且所有材料均以长度框定编码。
func RepositoryWorkloadManifestABIDigest() (string, error) {
	material, err := repositoryWorkloadManifestABIMaterial()
	if err != nil {
		return "", err
	}
	return repositoryFramedDigest(RepositoryWorkloadManifestABIDigestDomain, material), nil
}

func repositoryWorkloadManifestABIMaterial() ([]byte, error) {
	types := []reflect.Type{
		reflect.TypeFor[RepositoryWorkloadManifest](),
		reflect.TypeFor[RepositoryWorkloadModule](),
		reflect.TypeFor[RepositoryWorkload](),
	}
	var material bytes.Buffer
	writeRepositoryFrame(&material, []byte(PortableWorkloadManifestSchemaVersion))
	for _, typ := range types {
		fields, err := repositoryWorkloadManifestABIFields(typ)
		if err != nil {
			return nil, err
		}
		writeRepositoryFrame(&material, []byte(typ.Name()))
		writeRepositoryFrame(&material, []byte(fmt.Sprintf("%d", len(fields))))
		for _, field := range fields {
			writeRepositoryFrame(&material, []byte(field.goName))
			writeRepositoryFrame(&material, []byte(field.jsonName))
			writeRepositoryFrame(&material, []byte(field.goType))
			writeRepositoryFrame(&material, []byte(field.wireKind))
			writeRepositoryFrame(&material, []byte(field.elementRelation))
		}
	}
	return material.Bytes(), nil
}

// repositoryWorkloadManifestABIFields 动态枚举导出字段并校验 wire 元数据。
func repositoryWorkloadManifestABIFields(typ reflect.Type) ([]repositoryWorkloadManifestABIField, error) {
	if typ.Kind() != reflect.Struct || typ.NumField() == 0 {
		return nil, fmt.Errorf("ABI root %s must be a non-empty struct", typ)
	}
	fields := make([]repositoryWorkloadManifestABIField, 0, typ.NumField())
	seenJSONNames := make(map[string]struct{}, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		abiField, err := repositoryWorkloadManifestABIFieldFor(typ, field, seenJSONNames)
		if err != nil {
			return nil, err
		}
		fields = append(fields, abiField)
	}
	return fields, nil
}

// repositoryWorkloadManifestABIFieldFor 校验单个导出字段并生成其 ABI 元数据。
func repositoryWorkloadManifestABIFieldFor(typ reflect.Type, field reflect.StructField, seenJSONNames map[string]struct{}) (repositoryWorkloadManifestABIField, error) {
	if field.PkgPath != "" {
		return repositoryWorkloadManifestABIField{}, fmt.Errorf("ABI root %s has unexported field %s", typ.Name(), field.Name)
	}
	if field.Anonymous {
		return repositoryWorkloadManifestABIField{}, fmt.Errorf("ABI root %s has unsupported anonymous field %s", typ.Name(), field.Name)
	}
	tag, ok := field.Tag.Lookup("json")
	if !ok {
		return repositoryWorkloadManifestABIField{}, fmt.Errorf("ABI root %s field %s is missing a JSON tag", typ.Name(), field.Name)
	}
	jsonName, _, hasOptions := strings.Cut(tag, ",")
	if jsonName == "" || jsonName == "-" || hasOptions {
		return repositoryWorkloadManifestABIField{}, fmt.Errorf("ABI root %s field %s has an unsupported JSON tag", typ.Name(), field.Name)
	}
	if _, duplicate := seenJSONNames[jsonName]; duplicate {
		return repositoryWorkloadManifestABIField{}, fmt.Errorf("ABI root %s has duplicate JSON field %q", typ.Name(), jsonName)
	}
	seenJSONNames[jsonName] = struct{}{}
	wireKind, elementRelation, err := repositoryWorkloadManifestABIWireShape(field.Type)
	if err != nil {
		return repositoryWorkloadManifestABIField{}, fmt.Errorf("ABI root %s field %s: %w", typ.Name(), field.Name, err)
	}
	return repositoryWorkloadManifestABIField{
		goName:          field.Name,
		jsonName:        jsonName,
		goType:          field.Type.String(),
		wireKind:        wireKind,
		elementRelation: elementRelation,
	}, nil
}

// repositoryWorkloadManifestABIWireShape 返回 Go kind 及 slice element 关系。
func repositoryWorkloadManifestABIWireShape(typ reflect.Type) (string, string, error) {
	switch typ.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.String:
		return typ.Kind().String(), "scalar", nil
	case reflect.Slice:
		return repositoryWorkloadManifestABISliceWireShape(typ)
	default:
		return "", "", fmt.Errorf("unsupported Go wire type %s", typ)
	}
}

func repositoryWorkloadManifestABISliceWireShape(typ reflect.Type) (string, string, error) {
	element := typ.Elem()
	if element.Kind() != reflect.String && (element.Kind() != reflect.Struct || !repositoryWorkloadManifestABIKnownStruct(element)) {
		return "", "", fmt.Errorf("unsupported slice element type %s", element)
	}
	return typ.Kind().String(), "element:" + element.Kind().String() + ":" + element.String(), nil
}

func repositoryWorkloadManifestABIKnownStruct(typ reflect.Type) bool {
	switch typ {
	case reflect.TypeFor[RepositoryWorkloadManifest](), reflect.TypeFor[RepositoryWorkloadModule](), reflect.TypeFor[RepositoryWorkload]():
		return true
	default:
		return false
	}
}

func repositoryWorkloadEntryDigest(workload RepositoryWorkload) (string, error) {
	material := workload
	material.ExecutionDigest = ""
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("marshal workload digest material: %w", err)
	}
	return repositoryFramedDigest(RepositoryWorkloadManifestEntryDigestDomain, encoded), nil
}

func repositoryWorkloadManifestDigest(manifest RepositoryWorkloadManifest) (string, error) {
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("marshal manifest digest material: %w", err)
	}
	return repositoryFramedDigest(RepositoryWorkloadManifestDigestDomain, encoded), nil
}

func repositoryFramedDigest(domain string, payload []byte) string {
	hasher := sha256.New()
	writeRepositoryFrame(hasher, []byte(domain))
	writeRepositoryFrame(hasher, payload)
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}

func writeRepositoryFrame(writer io.Writer, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write(value)
}

func hasControl(value string) bool {
	for _, runeValue := range value {
		if unicode.IsControl(runeValue) {
			return true
		}
	}
	return false
}

func walkRepositoryManifestJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkRepositoryJSONValue(decoder); err != nil {
		return fmt.Errorf("repository workload manifest JSON is not strict: %w", err)
	}
	return ensureRepositoryManifestEOF(decoder)
}

// walkRepositoryJSONValue 递归遍历 JSON 并拒绝未知 delimiter。
func walkRepositoryJSONValue(decoder *json.Decoder) error {
	tokenValue, err := decoder.Token()
	if err != nil {
		return err
	}
	if tokenValue == nil {
		return errors.New("JSON null is not allowed")
	}
	delimiter, ok := tokenValue.(json.Delim)
	if !ok {
		return nil
	}
	if delimiter == '{' {
		return walkRepositoryJSONObject(decoder)
	}
	if delimiter != '[' {
		return errors.New("unexpected JSON delimiter")
	}
	for decoder.More() {
		if err := walkRepositoryJSONValue(decoder); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

// walkRepositoryJSONObject 遍历对象并拒绝重复字段名。
func walkRepositoryJSONObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("JSON object key is not a string")
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("duplicate JSON object key %q", name)
		}
		seen[name] = struct{}{}
		if err := walkRepositoryJSONValue(decoder); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

func ensureRepositoryManifestEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("repository workload manifest has trailing JSON")
	}
	return nil
}
