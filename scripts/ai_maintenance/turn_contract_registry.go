package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const turnContractRegistryPath = "internal/dto/turn/schema/field_consumers.json"

type turnContractRegistry struct {
	Version     int                           `json:"version"`
	Schemas     map[string]turnContractSchema `json:"schemas"`
	GoChains    []turnContractLocator         `json:"goChains"`
	GoConstants []turnContractLocator         `json:"goConstants"`
	JSMappers   []turnContractLocator         `json:"jsMappers"`
}

type turnContractSchema struct {
	GoType      turnContractLocator   `json:"goType"`
	GoValidator turnContractLocator   `json:"goValidator"`
	GoConsumers []turnContractLocator `json:"goConsumers"`
	JSValidator turnContractLocator   `json:"jsValidator"`
	JSConsumers []turnContractLocator `json:"jsConsumers"`
}

type turnContractLocator struct {
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
}

// loadTurnContractPaths 从字段消费 registry 派生 turncontract gate 的生产路径集合。
func loadTurnContractPaths(repoRoot string) (map[string]bool, error) {
	registryFile := filepath.Join(repoRoot, filepath.FromSlash(turnContractRegistryPath))
	raw, err := os.ReadFile(registryFile)
	if err != nil {
		return nil, fmt.Errorf("read turn contract consumer registry %s: %w", turnContractRegistryPath, err)
	}
	var registry turnContractRegistry
	if err := json.Unmarshal(raw, &registry); err != nil {
		return nil, fmt.Errorf("parse turn contract consumer registry %s: %w", turnContractRegistryPath, err)
	}
	if registry.Version != 2 {
		return nil, fmt.Errorf("turn contract consumer registry must use version 2, got %d", registry.Version)
	}
	if len(registry.Schemas) == 0 {
		return nil, fmt.Errorf("turn contract consumer registry schemas must be a non-empty object")
	}

	paths := make(map[string]bool, len(turnContractInfrastructureFiles))
	for file := range turnContractInfrastructureFiles {
		paths[file] = true
	}
	for schemaName, schema := range registry.Schemas {
		if err := addTurnContractSchemaPaths(paths, schemaName, schema); err != nil {
			return nil, err
		}
	}
	collections := []struct {
		label    string
		locators []turnContractLocator
	}{
		{label: "goChains", locators: registry.GoChains},
		{label: "goConstants", locators: registry.GoConstants},
		{label: "jsMappers", locators: registry.JSMappers},
	}
	for _, collection := range collections {
		if err := addTurnContractLocators(paths, collection.locators, collection.label); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

// addTurnContractSchemaPaths 登记单个 schema 的类型、校验器及全部生产消费 locator。
func addTurnContractSchemaPaths(paths map[string]bool, schemaName string, schema turnContractSchema) error {
	if strings.TrimSpace(schemaName) == "" {
		return fmt.Errorf("turn contract consumer registry contains a blank schema name")
	}
	locators := []struct {
		label   string
		locator turnContractLocator
	}{
		{label: "goType", locator: schema.GoType},
		{label: "goValidator", locator: schema.GoValidator},
		{label: "jsValidator", locator: schema.JSValidator},
	}
	for _, item := range locators {
		if err := addTurnContractLocator(paths, item.locator, schemaName+" "+item.label); err != nil {
			return err
		}
	}
	collections := []struct {
		label    string
		locators []turnContractLocator
	}{
		{label: "goConsumers", locators: schema.GoConsumers},
		{label: "jsConsumers", locators: schema.JSConsumers},
	}
	for _, collection := range collections {
		if err := addTurnContractLocators(paths, collection.locators, schemaName+" "+collection.label); err != nil {
			return err
		}
	}
	return nil
}

func addTurnContractLocators(paths map[string]bool, locators []turnContractLocator, label string) error {
	if len(locators) == 0 {
		return fmt.Errorf("turn contract consumer registry %s must be a non-empty array", label)
	}
	for index, locator := range locators {
		if err := addTurnContractLocator(paths, locator, fmt.Sprintf("%s[%d]", label, index)); err != nil {
			return err
		}
	}
	return nil
}

// addTurnContractLocator 拒绝缺失符号或可逃逸仓库的路径，避免路由集合静默漏项。
func addTurnContractLocator(paths map[string]bool, locator turnContractLocator, label string) error {
	if strings.TrimSpace(locator.Symbol) == "" {
		return fmt.Errorf("turn contract consumer registry %s symbol must be non-blank", label)
	}
	if locator.Path == "" || strings.TrimSpace(locator.Path) != locator.Path || strings.Contains(locator.Path, `\`) {
		return fmt.Errorf("turn contract consumer registry %s path must be a normalized repository-relative path", label)
	}
	cleaned := path.Clean(locator.Path)
	if cleaned != locator.Path || cleaned == "." || path.IsAbs(locator.Path) || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("turn contract consumer registry %s path %q must be normalized and repository-confined", label, locator.Path)
	}
	paths[locator.Path] = true
	return nil
}
