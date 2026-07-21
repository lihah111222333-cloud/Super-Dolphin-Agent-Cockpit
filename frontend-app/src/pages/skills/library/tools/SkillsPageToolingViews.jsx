import React, { useCallback, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, RefreshCw } from "lucide-react";
import { skillsPageService } from "../../services/skillsPageService.js";
import { normalizeSettingsCwd } from "../SkillsPageMarkdownModel.js";
import { errorMessage, optionalSettingsCwd } from "../../../shared/pageShared.js";
import { SkillToolsState } from "./SkillToolsTable.jsx";
import { MCPToolCard } from "./MCPToolCard.jsx";
import { AddSkillToolDialog } from "./AddSkillToolDialog.jsx";
import { useSkillToolRegistration } from "./skillToolRegistrationModel.js";
import {
  runBackgroundAction,
  runUIAction,
} from "../../../../shared/ui/runUIAction.js";
import {
  normalizeSkillToolsResponse,
  skillToolsQueryKey,
} from "./SkillsPageToolsModel.js";
import {
  MCP_TOOL_DEFINITIONS,
  mcpServerStatus,
  mcpServersListQueryKey,
  mergeMCPServerEnabled,
  mcpToolState,
} from "./SkillsPageMCPModel.js";

const { createSkillTool, listMCPServers, listSkillTools } = skillsPageService;
const SKILL_TOOLS_LIST_LIMIT = 200;
const SKILL_TOOLS_UI = Object.freeze({
  create: "新增工具",
  loading: "读取中...",
  refresh: "刷新",
  sectionTitle: "插件与技能",
  title: "Skill工具",
  waitingProject: "正在连接本地项目...",
});

export function SkillToolsView({ projectPath }) {
  const queryClient = useQueryClient();
  const cwd = useMemo(() => {
    try {
      return normalizeSettingsCwd(projectPath);
    } catch {
      return "";
    }
  }, [projectPath]);
  const {
    data: tools = [],
    error,
    isError,
    isFetching,
    isLoading,
  } = useQuery({
    queryKey: skillToolsQueryKey(cwd),
    enabled: Boolean(cwd),
    queryFn: () =>
      runBackgroundAction("skill.tools.load", async () =>
        normalizeSkillToolsResponse(
          await listSkillTools({
            cwd,
            keyword: "",
            limit: SKILL_TOOLS_LIST_LIMIT,
          }),
        ),
      ),
  });
  const refreshTools = () => {
    if (cwd)
      void queryClient.invalidateQueries({ queryKey: skillToolsQueryKey(cwd) });
  };
  return (
    <section className="skill-tools-panel" aria-label={SKILL_TOOLS_UI.title}>
      <div className="skill-tools-header">
        <div>
          <span className="skill-tools-kicker">
            {SKILL_TOOLS_UI.sectionTitle}
          </span>
          <h2>{SKILL_TOOLS_UI.title}</h2>
        </div>
        <div className="skill-tools-actions">
          <button type="button">{SKILL_TOOLS_UI.create}</button>
          <button
            type="button"
            className="ghost"
            onClick={refreshTools}
            disabled={!cwd || isFetching}
          >
            <RefreshCw size={16} aria-hidden="true" />
            <span>{SKILL_TOOLS_UI.refresh}</span>
          </button>
        </div>
      </div>
      {!cwd ? (
        <p className="skill-tools-notice">{SKILL_TOOLS_UI.waitingProject}</p>
      ) : null}
      {cwd && isLoading ? (
        <p className="skill-tools-notice">{SKILL_TOOLS_UI.loading}</p>
      ) : null}
      <SkillToolsState
        cwd={cwd}
        error={error}
        errorMessage={errorMessage}
        isError={isError}
        isLoading={isLoading}
        tools={tools}
      />
    </section>
  );
}

function usePluginsSquareState(copy, projectPath) {
  const projectReady = Boolean(optionalSettingsCwd(projectPath));
  const queryClient = useQueryClient();
  const [mcpActions, setMCPActions] = useState({});
  const [mcpNotices, setMCPNotices] = useState({});
  const [mcpErrors, setMCPErrors] = useState({});
  const [panelNotice, setPanelNotice] = useState("");
  const toolRegistration = useSkillToolRegistration({
    createTool: createSkillTool,
    listTools: listSkillTools,
    projectPath,
    queryClient,
    setPanelNotice,
  });
  const {
    data: mcpServersData,
    error: mcpServersError,
    isError: mcpServersIsError,
    isFetching: mcpServersIsFetching,
    isLoading: mcpServersIsLoading,
  } = useQuery({
    queryKey: mcpServersListQueryKey(projectPath),
    queryFn: () =>
      runBackgroundAction("mcp.servers.load", () => listMCPServers()),
    enabled: projectReady,
  });
  const mcpStatusQuery = useMemo(
    () => ({
      data: mcpServersData,
      isError: mcpServersIsError,
      isFetching: mcpServersIsFetching,
      isLoading: mcpServersIsLoading,
    }),
    [
      mcpServersData,
      mcpServersIsError,
      mcpServersIsFetching,
      mcpServersIsLoading,
    ],
  );
  const executeMCPAction = useCallback(
    async (tool, action) => {
      const label = action === "start" ? "开启" : "关闭";
      const enabled = action === "start";
      setMCPActions((current) => ({ ...current, [tool.id]: action }));
      setMCPNotices((current) => ({ ...current, [tool.id]: "" }));
      setMCPErrors((current) => ({ ...current, [tool.id]: "" }));
      try {
        normalizeSettingsCwd(projectPath);
        const result =
          action === "start" ? await tool.start() : await tool.stop();
        queryClient.setQueryData(
          mcpServersListQueryKey(projectPath),
          (current) => mergeMCPServerEnabled(current, result, tool.id, enabled),
        );
        setMCPNotices((current) => ({
          ...current,
          [tool.id]: `${tool.title} 已${label}`,
        }));
      } catch (error) {
        setMCPErrors((current) => ({
          ...current,
          [tool.id]: `${tool.title} ${label}失败，请重试。`,
        }));
        throw error;
      } finally {
        setMCPActions((current) => ({ ...current, [tool.id]: "" }));
      }
    },
    [projectPath, queryClient],
  );
  const runMCPAction = useCallback(
    (tool, action) => {
      if (tool.id === "sqlite" && action === "start")
        return runUIAction("mcp.sqlite.start", () =>
          executeMCPAction(tool, action),
        );
      if (tool.id === "sqlite" && action === "stop")
        return runUIAction("mcp.sqlite.stop", () =>
          executeMCPAction(tool, action),
        );
      if (tool.id === "playwright" && action === "start")
        return runUIAction("mcp.playwright.start", () =>
          executeMCPAction(tool, action),
        );
      if (tool.id === "playwright" && action === "stop")
        return runUIAction("mcp.playwright.stop", () =>
          executeMCPAction(tool, action),
        );
      throw new Error(`unsupported MCP action: ${tool.id}.${action}`);
    },
    [executeMCPAction],
  );
  const requireProjectNotice = useCallback(
    (actionLabel) => setPanelNotice(`请先在聊天页选择项目，再${actionLabel}。`),
    [],
  );
  const handleAddNewSkill = useCallback(() => {
    if (!projectReady) {
      setPanelNotice(copy.registerTool.projectRequired);
      return;
    }
    setPanelNotice("");
    toolRegistration.openEditor();
  }, [copy.registerTool.projectRequired, projectReady, toolRegistration]);
  return {
    handleAddNewSkill,
    mcpActions,
    mcpErrors,
    mcpNotices,
    mcpServersError,
    mcpServersIsError,
    mcpStatusQuery,
    panelNotice,
    projectReady,
    requireProjectNotice,
    runMCPAction,
    toolRegistration,
  };
}

export function PluginsSquareView({ copy, projectPath }) {
  const state = usePluginsSquareState(copy, projectPath);
  const {
    handleAddNewSkill,
    mcpActions,
    mcpErrors,
    mcpNotices,
    mcpServersError,
    mcpServersIsError,
    mcpStatusQuery,
    panelNotice,
    projectReady,
    requireProjectNotice,
    runMCPAction,
    toolRegistration,
  } = state;
  return (
    <div className="plugins-square-container">
      <div className="plugins-square-header">
        <h1>{copy.pluginsTitle}</h1>
        <p className="plugins-square-subtitle">{copy.pluginsSubtitle}</p>
      </div>
      {panelNotice ? (
        <p className="plugins-square-panel-notice" role="status">
          {panelNotice}
        </p>
      ) : null}
      <div className="mcp-tool-panel">
        {MCP_TOOL_DEFINITIONS.map((tool) => (
          <MCPToolCard
            errorMessage={errorMessage}
            key={tool.id}
            mcpServerStatus={mcpServerStatus}
            tool={tool}
            state={mcpToolState({
              mcpActions,
              mcpErrors,
              mcpNotices,
              mcpServersError,
              mcpServersIsError,
              mcpStatusQuery,
              onProjectRequired: () =>
                requireProjectNotice(`管理 ${tool.title}`),
              projectReady,
              runMCPAction,
            })}
          />
        ))}
        <button
          type="button"
          className="mcp-tool-card add-new-card"
          aria-label={copy.registerTool.card}
          onClick={handleAddNewSkill}
        >
          <span className="mcp-tool-icon add-new" aria-hidden="true">
            <Plus size={20} />
          </span>
          <span className="mcp-tool-main">
            <span className="mcp-tool-title-line">
              <span className="add-new-title">{copy.registerTool.card}</span>
            </span>
            <span className="mcp-tool-notice">
              {copy.registerTool.cardHint}
            </span>
          </span>
        </button>
      </div>
      {toolRegistration.toolEditorOpen ? (
        <AddSkillToolDialog
          availableSkills={toolRegistration.availableSkills}
          copy={copy.registerTool}
          description={toolRegistration.description}
          enabled={toolRegistration.enabled}
          loadState={toolRegistration.loadState}
          onChangeDescription={toolRegistration.setDescription}
          onChangeEnabled={toolRegistration.setEnabled}
          onClose={toolRegistration.closeEditor}
          onSave={() =>
            runUIAction("skill.tool.register", toolRegistration.saveTool)
          }
          onSelectSkill={toolRegistration.selectSkill}
          registeredCount={toolRegistration.registeredCount}
          saveError={toolRegistration.saveError}
          saving={toolRegistration.toolSaving}
          selection={toolRegistration.selection}
        />
      ) : null}
    </div>
  );
}
