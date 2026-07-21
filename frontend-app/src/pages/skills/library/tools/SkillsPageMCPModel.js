import { Database, MousePointer2 } from "lucide-react";
import { optionalSettingsCwd } from "../../../shared/pageShared.js";
import { skillsPageService } from "../../services/skillsPageService.js";
import { optionalObject, trimmedText } from "../dashboard/skillsDashboardModel.js";

const {
  startPlaywrightMCPServer,
  startSQLiteMCPServer,
  stopPlaywrightMCPServer,
  stopSQLiteMCPServer,
} = skillsPageService;

function mcpServersListQueryKey(projectPath) {
  return ["mcpServer", "list", optionalSettingsCwd(projectPath) || "pending"];
}
const MCP_TOOL_DEFINITIONS = [
  {
    id: "sqlite",
    title: "SQLite MCP",
    description: "使用 @bytebase/dbhub 暴露本地 Super-Dolphin SQLite 数据库。",
    Icon: Database,
    testId: "sqlite-mcp-status",
    start: startSQLiteMCPServer,
    stop: stopSQLiteMCPServer,
  },
  {
    id: "playwright",
    title: "Playwright MCP",
    description: "使用 @playwright/mcp@latest 提供浏览器自动化 MCP 工具。",
    Icon: MousePointer2,
    testId: "playwright-mcp-status",
    start: startPlaywrightMCPServer,
    stop: stopPlaywrightMCPServer,
  },
];
function mcpServerMap(response) {
  if (!response || typeof response !== "object" || Array.isArray(response)) {
    throw new Error("mcp/list response must be an object");
  }
  if (
    !response.mcpServers ||
    typeof response.mcpServers !== "object" ||
    Array.isArray(response.mcpServers)
  ) {
    throw new Error("mcp/list response.mcpServers must be an object");
  }
  return response.mcpServers;
}
function mcpServerConfig(response, serverName) {
  const servers = mcpServerMap(response);
  const config = servers[serverName];
  return config && typeof config === "object" && !Array.isArray(config)
    ? config
    : null;
}
function mcpServerStatus(projectReady, query, serverName) {
  if (!projectReady) return { label: "未选择项目", tone: "missing" };
  if (query.isLoading || (query.isFetching && !query.data))
    return { label: "读取中", tone: "loading" };
  if (query.isError) return { label: "读取失败", tone: "error" };
  const config = mcpServerConfig(query.data, serverName);
  if (!config) return { label: "未配置", tone: "missing" };
  return config.enabled === false
    ? { label: "已关闭", tone: "disabled" }
    : { label: "已开启", tone: "enabled" };
}
function mergeMCPServerEnabled(response, result, serverName, enabled) {
  const current = optionalObject(response);
  if (!current) throw new Error("mcp/list cached response must be an object");
  const servers = mcpServerMap(current);
  const resultName = trimmedText(result?.serverName || serverName);
  if (!resultName) return current;
  const existingConfig = servers[resultName];
  const existing =
    existingConfig &&
    typeof existingConfig === "object" &&
    !Array.isArray(existingConfig)
      ? existingConfig
      : {};
  const nextConfig = { ...existing, enabled };
  return { ...current, mcpServers: { ...servers, [resultName]: nextConfig } };
}
function mcpToolState(state) {
  return state;
}

export {
  MCP_TOOL_DEFINITIONS,
  mcpServerMap,
  mcpServerConfig,
  mcpServerStatus,
  mcpServersListQueryKey,
  mergeMCPServerEnabled,
  mcpToolState,
};
