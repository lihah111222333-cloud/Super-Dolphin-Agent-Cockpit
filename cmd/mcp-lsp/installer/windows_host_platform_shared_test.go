package installer

// 本测试故意不加 windows build tag：只验证共享架构别名、PE 常量和 DTO 契约；
// Win32 原生探测由 windows && e2e 测试单独证明。

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestNormalizeImageFileMachine(t *testing.T) {
	tests := []struct {
		name    string
		machine uint16
		want    string
	}{
		{name: "arm64", machine: WindowsImageFileMachineARM64, want: WindowsHostArchARM64},
		{name: "amd64", machine: WindowsImageFileMachineAMD64, want: WindowsHostArchX64},
		{name: "i386", machine: WindowsImageFileMachineI386, want: WindowsHostArchX86},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeWindowsImageFileMachine(tt.machine)
			if err != nil {
				t.Fatalf("NormalizeWindowsImageFileMachine(0x%04x) error = %v", tt.machine, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeWindowsImageFileMachine(0x%04x) = %q, want %q", tt.machine, got, tt.want)
			}
		})
	}
}

func TestNormalizeImageFileMachineRejectsUnknown(t *testing.T) {
	for _, machine := range []uint16{WindowsImageFileMachineUnknown, 0xffff} {
		if _, err := NormalizeWindowsImageFileMachine(machine); err == nil {
			t.Fatalf("NormalizeWindowsImageFileMachine(0x%04x) error = nil, want unsupported error", machine)
		}
	}
}

func TestNormalizeArchitectureAlias(t *testing.T) {
	tests := []struct {
		alias string
		want  string
	}{
		{alias: "ARM64", want: WindowsHostArchARM64},
		{alias: "aarch64", want: WindowsHostArchARM64},
		{alias: "arm64-v8a", want: WindowsHostArchARM64},
		{alias: "AMD64", want: WindowsHostArchX64},
		{alias: "x64", want: WindowsHostArchX64},
		{alias: "x86_64", want: WindowsHostArchX64},
		{alias: "x86-64", want: WindowsHostArchX64},
		{alias: "386", want: WindowsHostArchX86},
		{alias: "i386", want: WindowsHostArchX86},
		{alias: "i686", want: WindowsHostArchX86},
		{alias: " x86 ", want: WindowsHostArchX86},
	}

	for _, tt := range tests {
		t.Run(tt.alias, func(t *testing.T) {
			got, err := NormalizeWindowsArchitectureAlias(tt.alias)
			if err != nil {
				t.Fatalf("NormalizeWindowsArchitectureAlias(%q) error = %v", tt.alias, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeWindowsArchitectureAlias(%q) = %q, want %q", tt.alias, got, tt.want)
			}
		})
	}
}

func TestNormalizeArchitectureAliasRejectsUnknown(t *testing.T) {
	for _, alias := range []string{"", "mips64", "unknown"} {
		if _, err := NormalizeWindowsArchitectureAlias(alias); err == nil {
			t.Fatalf("NormalizeWindowsArchitectureAlias(%q) error = nil, want unsupported error", alias)
		}
	}
}

// TestWindowsHostPlatformFieldCoverageGuard 动态枚举宿主事实字段，并把每个字段锁定到资产选择、
// 平台证据或带理由的派生字段；新增、漏登和陈旧登记都必须失败。
func TestWindowsHostPlatformFieldCoverageGuard(t *testing.T) {
	type fieldUse struct {
		consumer string
		reason   string
	}
	registry := map[string]fieldUse{
		"OS":             {consumer: "SelectWindowsLockedAsset Windows host gate"},
		"Arch":           {reason: "derived compatibility view; detectWindowsHostPlatform always copies NativeArch and production selection never reads Arch"},
		"NativeArch":     {consumer: "SelectWindowsLockedAsset exact native asset key"},
		"ProcessArch":    {consumer: "platform-visible catalog and runtime receipt evidence; never an asset selection key"},
		"WindowsVersion": {consumer: "SelectWindowsLockedAsset minimum Windows version gate"},
		"WindowsBuild":   {consumer: "SelectWindowsLockedAsset minimum Windows build gate"},
	}

	typeOfPlatform := reflect.TypeOf(WindowsHostPlatform{})
	producerFields := make(map[string]struct{}, typeOfPlatform.NumField())
	for index := 0; index < typeOfPlatform.NumField(); index++ {
		field := typeOfPlatform.Field(index)
		if field.IsExported() {
			producerFields[field.Name] = struct{}{}
		}
	}
	missing := make([]string, 0)
	for field := range producerFields {
		if _, ok := registry[field]; !ok {
			missing = append(missing, field)
		}
	}
	stale := make([]string, 0)
	for field, use := range registry {
		if _, ok := producerFields[field]; !ok {
			stale = append(stale, field)
		}
		if strings.TrimSpace(use.consumer) == "" && strings.TrimSpace(use.reason) == "" {
			t.Errorf("WindowsHostPlatform field %s has neither a consumer nor a reasoned exemption", field)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("WindowsHostPlatform field coverage drift: missing=%v stale=%v", missing, stale)
	}
}
