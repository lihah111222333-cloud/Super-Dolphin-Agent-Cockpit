package capcontract

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Manifest 是能力契约清单的根结构，记录版本、生成时间、扫描根目录和各包清单。
type Manifest struct {
	Version     string            `json:"version"`
	GeneratedAt string            `json:"generated_at"`
	Roots       []string          `json:"roots"`
	Summary     ManifestSummary   `json:"summary"`
	Packages    []PackageManifest `json:"packages"`
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

// PackageManifest 是单个 Go 包的能力清单，包含路径、名称和所有导出符号。
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

// DiffResult 是两份清单比对的结果，包含新增、删除和变更的符号名列表。
type DiffResult struct {
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Changed []string `json:"changed,omitempty"`
}

// IsClean 判断clean是否可用。
func (d DiffResult) IsClean() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// SaveManifest 保存manifest。
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

// LoadManifest 加载manifest。
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

// MarshalManifest 编码manifest。
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

// ValidateManifest 校验manifest。
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

// DiffManifests 处理diffmanifests。
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

// manifestSymbols 处理manifest符号。
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

func paramsKey(params []ParamManifest) string {
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, param.Name+":"+param.Type)
	}
	return strings.Join(parts, ",")
}
