package archtest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDAGDesignerPromptAssetsUseCreateOnlyContract(t *testing.T) {
	root := repoRoot(t)
	for _, tc := range []struct {
		name     string
		section  string
		template string
		must     []string
		forbid   []string
	}{
		{
			name:     "zh",
			section:  filepath.Join(root, "internal/platform/shared/builtinprompts/assets/sections/main-dag-designer-zh/00-runtime-tools.md"),
			template: filepath.Join(root, "internal/platform/shared/builtinprompts/assets/templates/main-dag-designer-zh.json"),
			must: []string{
				"task_create_dag(dag_key, title, description?, schedule?, final_node_key?, nodes?)",
				"可信 ToolScope `_agentId`",
				"不要编造或传入 `agent_id`",
				"拒绝 `schedule.trigger=\"scheduled\"`",
				"CRON_TZ=Asia/Shanghai",
				"assigned_to",
				"task_dispatch_node",
				"video_with_audio",
				"`prompt`、`negative_prompt`、`voice_text`",
				"`outputs.to_artifact`",
				"`\"output_path\":\"<path>\"`",
			},
			forbid: []string{"新节点的 `depends_on` 必须全部指向已经 `done` 的节点"},
		},
		{
			name:     "en",
			section:  filepath.Join(root, "internal/platform/shared/builtinprompts/assets/sections/main-dag-designer-en/00-runtime-tools.md"),
			template: filepath.Join(root, "internal/platform/shared/builtinprompts/assets/templates/main-dag-designer-en.json"),
			must: []string{
				"task_create_dag(dag_key, title, description?, schedule?, final_node_key?, nodes?)",
				"trusted ToolScope `_agentId`",
				"do not invent or pass `agent_id`",
				"rejects `schedule.trigger=\"scheduled\"`",
				"CRON_TZ=Asia/Shanghai",
				"assigned_to",
				"task_dispatch_node",
				"video_with_audio",
				"`prompt`, `negative_prompt`, and `voice_text`",
				"`outputs.to_artifact`",
				"`\"output_path\":\"<path>\"`",
			},
			forbid: []string{"every new node's `depends_on` must point only to nodes that are already `done`"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := readTextFile(t, tc.section)
			for _, want := range tc.must {
				if !strings.Contains(body, want) {
					t.Fatalf("%s missing prompt contract %q", tc.section, want)
				}
			}
			for _, forbidden := range tc.forbid {
				if strings.Contains(body, forbidden) {
					t.Fatalf("%s keeps obsolete prompt contract %q", tc.section, forbidden)
				}
			}
			assertDAGDesignerTemplateEnablesDispatch(t, tc.template)
		})
	}
}

func assertDAGDesignerTemplateEnablesDispatch(t *testing.T, path string) {
	t.Helper()
	var tmpl struct {
		Sections []struct {
			EnableWhen struct {
				EnabledToolsAll []string `json:"enabled_tools_all"`
			} `json:"enable_when"`
		} `json:"sections"`
	}
	if err := json.Unmarshal([]byte(readTextFile(t, path)), &tmpl); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(tmpl.Sections) != 1 {
		t.Fatalf("%s sections = %d, want 1", path, len(tmpl.Sections))
	}
	for _, want := range []string{"task_create_dag", "task_get_dag", "task_dag_apply_ops", "task_start_dag", "task_dispatch_node"} {
		if !stringSliceContains(tmpl.Sections[0].EnableWhen.EnabledToolsAll, want) {
			t.Fatalf("%s enabled_tools_all missing %q: %#v", path, want, tmpl.Sections[0].EnableWhen.EnabledToolsAll)
		}
	}
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
