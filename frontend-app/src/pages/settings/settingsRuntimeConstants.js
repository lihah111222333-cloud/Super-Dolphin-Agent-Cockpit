export const SETTINGS_KEYS = Object.freeze({
  stallThreshold: "stallThresholdSec",
  contextThresholds: "contextUsageAlerts.thresholds",
  activeProvider: "settings.provider.active",
});

export const SETTINGS_PROJECT_CWD_REQUIRED = "当前项目路径为空，无法保存设置";

export const SETTINGS_DEFAULTS = Object.freeze({
  stallThresholdSec: 30,
  contextThresholds: [70, 85, 95],
  activeProvider: "codex",
  codexHome: "~/.codex",
  codexInstanceKey: "default",
  providerModel: "gpt-5.5",
  providerEffort: "xhigh",
  personality: "pragmatic",
  sandboxPolicy: "workspaceWrite",
  readOnlyMode: "fullAccess",
  readableRoots: "",
  writableRoots: "",
  networkAccess: false,
});
