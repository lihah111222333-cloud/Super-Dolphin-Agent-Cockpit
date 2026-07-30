package main

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
)

const productionPathStructTag = "production_path"

type productionPathTransform func(string) (string, error)

type storedProductionCoordinatorConfig productionCoordinatorConfig

// Validate 校验磁盘格式路径元数据和词法规范，完整语义在解析为运行时路径后校验。
func (config storedProductionCoordinatorConfig) Validate() error {
	runtimeConfig := productionCoordinatorConfig(config)
	return transformProductionCoordinatorConfigPaths(&runtimeConfig, func(path string) (string, error) {
		if path == "" {
			return "", nil
		}
		if filepath.IsAbs(path) {
			return "", fmt.Errorf("path %q must be relative to production.json", path)
		}
		native := filepath.FromSlash(path)
		if filepath.Clean(native) != native || filepath.ToSlash(native) != path {
			return "", fmt.Errorf("relative path %q is not canonical", path)
		}
		return path, nil
	})
}

// resolveProductionCoordinatorConfigPaths 把持久化相对路径解析为仅供运行时校验和使用的绝对路径。
func resolveProductionCoordinatorConfigPaths(base string, config *productionCoordinatorConfig) error {
	return transformProductionCoordinatorConfigPaths(config, func(path string) (string, error) {
		if path == "" {
			return path, nil
		}
		if filepath.IsAbs(path) {
			return "", fmt.Errorf("path %q must be relative to production.json", path)
		}
		native := filepath.FromSlash(path)
		if filepath.Clean(native) != native || filepath.ToSlash(native) != path {
			return "", fmt.Errorf("path %q is not canonical", path)
		}
		return filepath.Join(base, native), nil
	})
}

// portableProductionCoordinatorConfig 把运行时绝对路径转为相对 production.json 的可迁移表示。
func portableProductionCoordinatorConfig(
	base string,
	config productionCoordinatorConfig,
) (productionCoordinatorConfig, error) {
	err := transformProductionCoordinatorConfigPaths(&config, func(path string) (string, error) {
		if path == "" {
			return "", nil
		}
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return "", fmt.Errorf("runtime path %q is not canonical and absolute", path)
		}
		relative, err := filepath.Rel(base, path)
		if err != nil {
			return "", fmt.Errorf("make path %q relative to production config: %w", path, err)
		}
		if filepath.IsAbs(relative) {
			return "", fmt.Errorf("path %q cannot be made portable", path)
		}
		return filepath.ToSlash(relative), nil
	})
	return config, err
}

func transformProductionCoordinatorConfigPaths(
	config *productionCoordinatorConfig,
	transform productionPathTransform,
) error {
	return transformProductionPathValue(reflect.ValueOf(config).Elem(), transform)
}

func transformProductionPathValue(value reflect.Value, transform productionPathTransform) error {
	valueType := value.Type()
	for index := 0; index < value.NumField(); index++ {
		fieldType := valueType.Field(index)
		fieldValue := value.Field(index)
		jsonName := strings.Split(fieldType.Tag.Get("json"), ",")[0]
		isPath := fieldType.Tag.Get(productionPathStructTag) == "true"
		if productionJSONFieldLooksLikePath(jsonName) != isPath {
			return fmt.Errorf("production config field %s has inconsistent path metadata", fieldType.Name)
		}
		if isPath {
			if fieldValue.Kind() != reflect.String || !fieldValue.CanSet() {
				return fmt.Errorf("production config path field %s is not a settable string", fieldType.Name)
			}
			resolved, err := transform(fieldValue.String())
			if err != nil {
				return fmt.Errorf("%s: %w", jsonName, err)
			}
			fieldValue.SetString(resolved)
			continue
		}
		if fieldValue.Kind() == reflect.Struct {
			if err := transformProductionPathValue(fieldValue, transform); err != nil {
				return err
			}
		}
	}
	return nil
}

func productionJSONFieldLooksLikePath(name string) bool {
	for _, suffix := range []string{"_file", "_profile", "_repository", "_root"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
