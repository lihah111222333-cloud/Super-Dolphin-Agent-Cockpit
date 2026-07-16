package turn

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

type consumerRegistryEntry struct {
	GoValidator string                       `json:"goValidator"`
	JSValidator string                       `json:"jsValidator"`
	Fields      map[string]map[string]string `json:"fields"`
}

// TestTurnContractFieldGuard derives producer fields from canonical schemas and rejects registry drift.
func TestTurnContractFieldGuard(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	registry := loadConsumerRegistry(t, root)
	if err := validateConsumerRegistry(root, registry); err != nil {
		t.Fatal(err)
	}
}

// TestTurnContractFieldGuardFailsFirst locks the missing-field path before green registry coverage.
func TestTurnContractFieldGuardFailsFirst(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	registry := loadConsumerRegistry(t, root)
	delete(registry["TurnRefV1"].Fields, "turnId")
	if err := validateConsumerRegistry(root, registry); err == nil || !strings.Contains(err.Error(), "missing fields") {
		t.Fatalf("missing registry field error = %v, want missing fields", err)
	}
}

func validateConsumerRegistry(root string, registry map[string]consumerRegistryEntry) error {
	producers, err := canonicalSchemaFields(filepath.Join(root, "internal", "dto", "turn", "schema"))
	if err != nil {
		return err
	}
	goValidatorSource, err := os.ReadFile(filepath.Join(root, "internal", "dto", "turn", "contract_validator.go"))
	if err != nil {
		return fmt.Errorf("read Go validator: %w", err)
	}
	for name, fields := range producers {
		if err := validateRegistryEntry(name, fields, registry, string(goValidatorSource)); err != nil {
			return err
		}
	}
	return rejectStaleRegistrySchemas(producers, registry)
}

func validateRegistryEntry(name string, fields map[string]bool, registry map[string]consumerRegistryEntry, validatorSource string) error {
	entry, ok := registry[name]
	if !ok {
		return fmt.Errorf("consumer registry missing schema %s", name)
	}
	if entry.GoValidator == "" || entry.JSValidator == "" {
		return fmt.Errorf("consumer registry %s has blank validator", name)
	}
	if !strings.Contains(validatorSource, "func "+entry.GoValidator+"(") {
		return fmt.Errorf("consumer registry %s references missing Go validator %s", name, entry.GoValidator)
	}
	return assertExactFieldCoverage(name, fields, entry.Fields)
}

func rejectStaleRegistrySchemas(producers map[string]map[string]bool, registry map[string]consumerRegistryEntry) error {
	for name := range registry {
		if _, ok := producers[name]; !ok {
			return fmt.Errorf("consumer registry has stale schema %s", name)
		}
	}
	return nil
}

func canonicalSchemaFields(dir string) (map[string]map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read canonical schema directory: %w", err)
	}
	result := map[string]map[string]bool{}
	for _, entry := range entries {
		if isCanonicalSchema(entry) {
			title, fields, err := readCanonicalSchema(filepath.Join(dir, entry.Name()), entry.Name())
			if err != nil {
				return nil, err
			}
			result[title] = fields
		}
	}
	return result, nil
}

func isCanonicalSchema(entry os.DirEntry) bool {
	return !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") && entry.Name() != "field_consumers.json"
}

func readCanonicalSchema(path, name string) (string, map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("read canonical schema %s: %w", name, err)
	}
	var document struct {
		Title      string         `json:"title"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return "", nil, fmt.Errorf("parse canonical schema %s: %w", name, err)
	}
	if document.Title == "" || len(document.Properties) == 0 {
		return "", nil, fmt.Errorf("canonical schema %s has no title or properties", name)
	}
	fields := map[string]bool{}
	for field := range document.Properties {
		fields[field] = true
	}
	return document.Title, fields, nil
}

func assertExactFieldCoverage(name string, fields map[string]bool, registered map[string]map[string]string) error {
	missing := make([]string, 0)
	for field := range fields {
		if _, ok := registered[field]; !ok {
			missing = append(missing, field)
		}
	}
	stale := make([]string, 0)
	for field, consumers := range registered {
		if !fields[field] {
			stale = append(stale, field)
		}
		if strings.TrimSpace(consumers["go"]) == "" || strings.TrimSpace(consumers["js"]) == "" {
			return fmt.Errorf("%s field %s has blank consumer coverage", name, field)
		}
	}
	if len(missing) == 0 && len(stale) == 0 {
		return nil
	}
	sort.Strings(missing)
	sort.Strings(stale)
	return fmt.Errorf("%s consumer registry missing fields %v; stale fields %v", name, missing, stale)
}

func loadConsumerRegistry(t *testing.T, root string) map[string]consumerRegistryEntry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "internal", "dto", "turn", "schema", "field_consumers.json"))
	if err != nil {
		t.Fatalf("read consumer registry: %v", err)
	}
	registry := map[string]consumerRegistryEntry{}
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatalf("parse consumer registry: %v", err)
	}
	if len(registry) == 0 {
		t.Fatal("consumer registry must not be empty")
	}
	return registry
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve field guard path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", ".."))
}
