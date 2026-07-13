package capcontract

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
)

// Manifest 是能力契约清单的根结构。
// 它作为生成物的 wire 格式，记录扫描范围、摘要和包级符号快照。
type Manifest struct {
	Version     string             `json:"version"`
	GeneratedAt string             `json:"generated_at"`
	Roots       []string           `json:"roots"`
	Targets     []string           `json:"targets"`
	Provenance  []TargetProvenance `json:"target_provenance"`
	Summary     ManifestSummary    `json:"summary"`
	Packages    []PackageManifest  `json:"packages"`
}

// TargetProvenance 记录每个规范目标实际贡献的包和符号签名。
type TargetProvenance struct {
	Target   string   `json:"target"`
	Packages []string `json:"packages"`
	Symbols  []string `json:"symbols"`
}

// ManifestSummary 是清单的统计摘要，记录包数、函数数、接口数等总量。
type ManifestSummary struct {
	TotalPackages         int `json:"total_packages"`
	TotalFunctions        int `json:"total_functions"`
	TotalMethods          int `json:"total_methods"`
	TotalInterfaces       int `json:"total_interfaces"`
	TotalInterfaceMethods int `json:"total_interface_methods"`
	TotalExported         int `json:"total_exported"`
	TotalUnexported       int `json:"total_unexported"`
	TotalStructs          int `json:"total_structs"`
}

// PackageManifest 是单个 Go 包的能力清单。
// Path/Name 是比对身份，符号列表用于检测公共能力面是否发生漂移。
type PackageManifest struct {
	Path        string              `json:"path"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Functions   []FunctionManifest  `json:"functions,omitempty"`
	Methods     []MethodManifest    `json:"methods,omitempty"`
	Interfaces  []InterfaceManifest `json:"interfaces,omitempty"`
	Structs     []StructManifest    `json:"structs,omitempty"`
}

// FunctionManifest 描述包级函数的名称、导出性、参数和返回类型。
type FunctionManifest struct {
	Name     string          `json:"name"`
	Exported bool            `json:"exported"`
	Params   []ParamManifest `json:"params,omitempty"`
	Returns  []string        `json:"returns,omitempty"`
}

// MethodManifest 描述方法的接收者、名称、导出性、参数和返回类型。
type MethodManifest struct {
	Receiver string          `json:"receiver"`
	Name     string          `json:"name"`
	Exported bool            `json:"exported"`
	Params   []ParamManifest `json:"params,omitempty"`
	Returns  []string        `json:"returns,omitempty"`
}

// InterfaceManifest 描述接口的名称、导出性、方法列表和嵌入类型。
type InterfaceManifest struct {
	Name     string                 `json:"name"`
	Exported bool                   `json:"exported"`
	Methods  []InterfaceMethodEntry `json:"methods,omitempty"`
	Embeds   []string               `json:"embeds,omitempty"`
}

// InterfaceMethodEntry 描述接口内单个方法的名称、参数和返回类型。
type InterfaceMethodEntry struct {
	Name    string          `json:"name"`
	Params  []ParamManifest `json:"params,omitempty"`
	Returns []string        `json:"returns,omitempty"`
}

// StructManifest 描述结构体的名称和导出性，用于清单比对。
type StructManifest struct {
	Name     string `json:"name"`
	Exported bool   `json:"exported"`
}

// ParamManifest 描述函数/方法参数的名称和类型。
type ParamManifest struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type"`
}

// DiffResult 是两份清单比对的结果。
// Added/Removed/Changed 使用稳定排序，方便 CI 和人工审查直接比较输出。
type DiffResult struct {
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Changed []string `json:"changed,omitempty"`
}

// IsClean 判断清单 diff 是否为空，空 diff 表示能力面未发生可见漂移。
func (d DiffResult) IsClean() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// SaveManifest 校验并写入能力契约清单。
// 写入前统一走 ValidateManifest，避免把结构不完整的生成物落盘。
func SaveManifest(manifest *Manifest, path string) error {
	if err := ValidateManifest(manifest); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal capability manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write capability manifest: %w", err)
	}
	return nil
}

// LoadManifest 读取、解析并校验已提交的能力契约清单。
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read capability manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse capability manifest: %w", err)
	}
	if err := ValidateManifest(&manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// MarshalManifest 校验后以稳定缩进编码清单，返回结果总是带尾随换行。
func MarshalManifest(manifest *Manifest) ([]byte, error) {
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal capability manifest: %w", err)
	}
	return append(data, '\n'), nil
}

// ValidateManifest 校验清单最低完整性。
// nil、缺版本、缺扫描根和重复包路径都会立即报错，防止 diff 阶段吞掉坏输入。
func ValidateManifest(manifest *Manifest) error {
	if manifest == nil {
		return fmt.Errorf("capability manifest is nil")
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return fmt.Errorf("capability manifest version is required")
	}
	if len(manifest.Roots) == 0 {
		return fmt.Errorf("capability manifest roots are required")
	}
	if err := validateTargetProvenance(manifest); err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, pkg := range manifest.Packages {
		if strings.TrimSpace(pkg.Path) == "" || strings.TrimSpace(pkg.Name) == "" {
			return fmt.Errorf("capability manifest package identity is required")
		}
		key := strings.TrimSpace(pkg.Path)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("capability manifest duplicate package path %q", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// validateTargetProvenance 校验固定平台矩阵以及逐目标来源字段的完整性和稳定顺序。
func validateTargetProvenance(manifest *Manifest) error {
	if !slices.Equal(manifest.Targets, canonicalTargets) {
		return fmt.Errorf("capability manifest targets must equal canonical matrix %v", canonicalTargets)
	}
	if len(manifest.Provenance) != len(manifest.Targets) {
		return fmt.Errorf("capability manifest target provenance is missing or stale")
	}
	for i, target := range manifest.Targets {
		if manifest.Provenance[i].Target != target {
			return fmt.Errorf("capability manifest target provenance is missing or stale for %q", target)
		}
		if !sort.StringsAreSorted(manifest.Provenance[i].Packages) || !sort.StringsAreSorted(manifest.Provenance[i].Symbols) {
			return fmt.Errorf("capability manifest target provenance is stale for %q", target)
		}
	}
	return nil
}

// DiffManifests 比较已提交清单和实时扫描清单，返回公共符号面的增删改。
func DiffManifests(committed, live *Manifest) DiffResult {
	oldSymbols := manifestSymbols(committed)
	newSymbols := manifestSymbols(live)
	var diff DiffResult
	for name, oldSig := range oldSymbols {
		newSig, ok := newSymbols[name]
		if !ok {
			diff.Removed = append(diff.Removed, name)
			continue
		}
		if oldSig != newSig {
			diff.Changed = append(diff.Changed, name)
		}
	}
	for name := range newSymbols {
		if _, ok := oldSymbols[name]; !ok {
			diff.Added = append(diff.Added, name)
		}
	}
	sort.Strings(diff.Added)
	sort.Strings(diff.Removed)
	sort.Strings(diff.Changed)
	return diff
}

// manifestSymbols 将清单展开成符号到签名的映射。
// nil manifest 返回空映射，便于调用方把缺失文件视为全量新增/删除。
func manifestSymbols(manifest *Manifest) map[string]string {
	symbols := map[string]string{}
	if manifest == nil {
		return symbols
	}
	for _, pkg := range manifest.Packages {
		prefix := pkg.Path + "."
		for _, fn := range pkg.Functions {
			symbols[prefix+fn.Name] = paramsKey(fn.Params) + "->" + strings.Join(fn.Returns, ",")
		}
		for _, method := range pkg.Methods {
			symbols[prefix+method.Receiver+"."+method.Name] = paramsKey(method.Params) + "->" + strings.Join(method.Returns, ",")
		}
		for _, iface := range pkg.Interfaces {
			ifaceName := prefix + iface.Name
			symbols[ifaceName] = "interface|embeds=" + strings.Join(iface.Embeds, ",")
			for _, embed := range iface.Embeds {
				symbols[ifaceName+".embed:"+embed] = embed
			}
			for _, method := range iface.Methods {
				symbols[ifaceName+"."+method.Name] = paramsKey(method.Params) + "->" + strings.Join(method.Returns, ",")
			}
		}
		for _, st := range pkg.Structs {
			symbols[prefix+st.Name] = fmt.Sprintf("struct|exported=%v", st.Exported)
		}
	}
	return symbols
}

// paramsKey 生成参数列表的稳定签名片段，保留参数名以捕捉 wire 面变化。
func paramsKey(params []ParamManifest) string {
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, param.Name+":"+param.Type)
	}
	return strings.Join(parts, ",")
}
