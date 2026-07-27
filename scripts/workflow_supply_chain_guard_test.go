package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var immutableActionRef = regexp.MustCompile(`^[0-9a-f]{40}$`)

func TestWorkflowThirdPartyActionsUseImmutableCommitRefs(t *testing.T) {
	root := scriptsRepoRoot(t)
	workflowRoot := filepath.Join(root, ".github", "workflows")
	err := filepath.WalkDir(workflowRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		return inspectWorkflowSupplyChainEntry(t, path, entry, walkErr)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func inspectWorkflowSupplyChainEntry(t *testing.T, path string, entry fs.DirEntry, walkErr error) error {
	t.Helper()
	if walkErr != nil {
		return walkErr
	}
	if !workflowYAMLFile(path, entry) {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	assertWorkflowSupplyChainDocument(t, path, data)
	return nil
}

func workflowYAMLFile(path string, entry fs.DirEntry) bool {
	if entry.IsDir() {
		return false
	}
	return strings.HasSuffix(path, ".yml") || strings.HasSuffix(path, ".yaml")
}

func assertWorkflowSupplyChainDocument(t *testing.T, path string, data []byte) {
	t.Helper()
	if strings.Contains(string(data), "@latest") {
		t.Errorf("%s contains mutable @latest tool input", path)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Errorf("parse %s: %v", path, err)
		return
	}
	for _, uses := range workflowUsesValues(&document) {
		assertImmutableWorkflowActionRef(t, path, uses)
	}
}

func assertImmutableWorkflowActionRef(t *testing.T, path, uses string) {
	t.Helper()
	if strings.HasPrefix(uses, "./") {
		return
	}
	action, ref, ok := strings.Cut(uses, "@")
	if !ok || action == "" || !immutableActionRef.MatchString(ref) {
		t.Errorf("%s uses mutable action reference %q; require a full commit SHA", path, uses)
	}
}

func workflowUsesValues(node *yaml.Node) []string {
	var values []string
	if node.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(node.Content); index += 2 {
			key := node.Content[index]
			value := node.Content[index+1]
			if key.Value == "uses" && value.Kind == yaml.ScalarNode {
				values = append(values, value.Value)
			}
			values = append(values, workflowUsesValues(value)...)
		}
		return values
	}
	for _, child := range node.Content {
		values = append(values, workflowUsesValues(child)...)
	}
	return values
}
