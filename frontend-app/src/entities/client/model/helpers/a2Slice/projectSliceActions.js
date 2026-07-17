function projectVisiblePaths(runtime, normalizePath) {
  const projects = runtime.get().projects;
  if (!Array.isArray(projects)) return [];
  return projects.map(normalizePath).filter(Boolean);
}

function shouldRegisterProject(target, previousActiveProject, visibleProjects) {
  return target !== '.' && (previousActiveProject === '.' || !visibleProjects.includes(target));
}

function optimisticProjectList(target, visibleProjects, previousProjects) {
  if (target === '.' || visibleProjects.includes(target)) return previousProjects;
  return [...new Set([...previousProjects, target])];
}

function projectCwdForSelection(project, cwd) {
  return project && project !== '.' ? project : cwd;
}

function restoreActiveProject(runtime, previousActiveProject, previousProjects) {
  runtime.set({
    activeProject: previousActiveProject,
    projects: previousProjects,
    chatSurfaceLoadingCwd: '',
  });
}

async function registerProjectIfNeeded(runtime, deps, context) {
  if (!shouldRegisterProject(context.target, context.previousActiveProject, context.visibleProjects)) return;
  const addedProjects = await deps.addProject({ cwd: context.cwd, path: context.target });
  runtime.applyProjects(addedProjects, context.cwd);
}

function refreshSelectedProjectIfChanged(runtime, deps, context) {
  const selectedProject = deps.normalizePath(runtime.get().activeProject);
  const selectedCwd = projectCwdForSelection(selectedProject, context.cwd);
  if (selectedCwd === context.optimisticCwd) return;
  runtime.refreshChatSurfaceForCwdInBackground(selectedCwd, {
    preserveActiveThreadId: context.preserveActiveThreadId,
  });
}

async function addAndActivateSelectedProject(runtime, deps, scopeCwd, selected) {
  const projects = await deps.addProject({ cwd: scopeCwd, path: selected });
  runtime.applyProjects(projects, scopeCwd);
  if (deps.normalizePath(runtime.get().activeProject) === selected) return;
  const activatedProjects = await deps.setActiveProject({ cwd: scopeCwd, path: selected });
  runtime.applyProjects(activatedProjects, scopeCwd);
}

function pickerSeedProject(runtime, normalizePath, scopeCwd) {
  const activeProject = normalizePath(runtime.get().activeProject);
  return activeProject && activeProject !== '.' ? activeProject : scopeCwd;
}

function createSetActiveProjectPathAction(runtime, deps) {
  const { normalizePath, projectShortLabel, setActiveProject } = deps;
  return async (path, options = {}) => {
    const target = normalizePath(path);
    if (!target) return false;
    const cwd = runtime.requireProjectScopeCwd('project.setActive');
    const preserveActiveThreadId = options.preserveActiveThreadId === true;
    const previousActiveProject = normalizePath(runtime.get().activeProject);
    const previousProjects = Array.isArray(runtime.get().projects) ? [...runtime.get().projects] : [];
    try {
      void runtime.saveActiveComposerDraft();
      const visibleProjects = projectVisiblePaths(runtime, normalizePath);
      await registerProjectIfNeeded(runtime, deps, { cwd, target, previousActiveProject, visibleProjects });
      const optimisticProjects = optimisticProjectList(target, visibleProjects, previousProjects);
      runtime.set({ projects: optimisticProjects, activeProject: target });
      const optimisticCwd = projectCwdForSelection(target, cwd);
      runtime.refreshChatSurfaceForCwdInBackground(optimisticCwd, { preserveActiveThreadId });
      const projects = await setActiveProject({ cwd, path: target });
      runtime.applyProjects(projects, cwd);
      refreshSelectedProjectIfChanged(runtime, { normalizePath }, { cwd, optimisticCwd, preserveActiveThreadId });
      runtime.notifyAction(`已切换项目：${projectShortLabel(target)}`, 'success');
      return true;
    }
    catch (error) {
      restoreActiveProject(runtime, previousActiveProject, previousProjects);
      runtime.notifyAction('切换项目失败，请重试。', 'error');
      runtime.addWarning('error', 'project.set_active.failed', { path: target, error: 'action failure; see Health diagnostic ID' });
      throw error;
    }
  };
}

function createAddProjectFromPickerAction(runtime, deps) {
  const { normalizePath, projectShortLabel, selectProjectDir } = deps;
  return async () => {
    const scopeCwd = runtime.requireProjectScopeCwd('project.add');
    const seed = pickerSeedProject(runtime, normalizePath, scopeCwd);
    let selected = '';
    try {
      selected = normalizePath(await selectProjectDir(seed));
      if (!selected) {
        runtime.notifyAction('未选择项目', 'info');
        return false;
      }
      await addAndActivateSelectedProject(runtime, deps, scopeCwd, selected);
      runtime.refreshChatSurfaceForCwdInBackground(selected);
      runtime.notifyAction(`已添加项目：${projectShortLabel(selected)}`, 'success');
      return true;
    }
    catch (error) {
      runtime.notifyAction('添加项目失败，请重试。', 'error');
      runtime.addWarning('error', 'project.add.failed', { path: selected, error: 'action failure; see Health diagnostic ID' });
      throw error;
    }
  };
}

function createOpenNewWindowAction(runtime, deps) {
  const { normalizePath, openNewWindow, projectShortLabel, selectProjectDir } = deps;
  return async () => {
    const scopeCwd = runtime.requireProjectScopeCwd('ui.open_new_window');
    const seed = pickerSeedProject(runtime, normalizePath, scopeCwd);
    let selected = '';
    try {
      selected = normalizePath(await selectProjectDir(seed));
      if (!selected) {
        runtime.notifyAction('未选择新窗口目录', 'info');
        return false;
      }
      await openNewWindow({ cwd: selected });
      runtime.notifyAction(`已打开新窗口：${projectShortLabel(selected)}`, 'success');
      return true;
    }
    catch (error) {
      runtime.notifyAction('打开新窗口失败，请重试。', 'error');
      runtime.addWarning('error', 'ui.open_new_window.failed', { path: selected, error: 'action failure; see Health diagnostic ID' });
      throw error;
    }
  };
}

function createRemoveProjectPathAction(runtime, deps) {
  const { normalizePath, projectShortLabel, removeProject } = deps;
  return async (path) => {
    const target = normalizePath(path);
    if (!target) return false;
    const cwd = runtime.requireProjectScopeCwd('project.remove');
    try {
      const projects = await removeProject({ cwd, path: target });
      runtime.applyProjects(projects, cwd);
      runtime.notifyAction(`已移除项目：${projectShortLabel(target)}`, 'success');
      return true;
    }
    catch (error) {
      runtime.notifyAction('移除项目失败，请重试。', 'error');
      runtime.addWarning('error', 'project.remove.failed', { path: target, error: 'action failure; see Health diagnostic ID' });
      throw error;
    }
  };
}

function createProjectActionSet(runtime, deps) {
  return {
    setActiveProjectPath: createSetActiveProjectPathAction(runtime, deps),
    addProjectFromPicker: createAddProjectFromPickerAction(runtime, deps),
    openNewWindow: createOpenNewWindowAction(runtime, deps),
    removeProjectPath: createRemoveProjectPathAction(runtime, deps),
  };
}

export { createProjectActionSet };
