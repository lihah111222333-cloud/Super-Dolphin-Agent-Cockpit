function projectVisiblePaths(runtime, normalizePath) {
  const projects = runtime.get().projects;
  if (!Array.isArray(projects)) return [];
  return projects.map(normalizePath).filter(Boolean);
}

function shouldRegisterProject(target, previousActiveProject, visibleProjects) {
  return target !== '.' && (previousActiveProject === '.' || !visibleProjects.includes(target));
}

function projectCwdForSelection(project, cwd) {
  return project && project !== '.' ? project : cwd;
}

async function rebindConfirmedProjectScope(runtime, normalizePath, projects, cwd, requestedCwd) {
  const confirmedCwd = projectCwdForSelection(normalizePath(projects?.active), cwd);
  if (confirmedCwd !== requestedCwd) await runtime.rebindBridgeEventScope(confirmedCwd);
  return confirmedCwd;
}

async function restoreProjectScopeAfterBackendFailure(runtime, preparedScope, target) {
  try {
    await runtime.restorePreparedBridgeEventScope(preparedScope);
  }
  catch {
    runtime.set({ bootstrapStatus: 'failed', error: '连接后端失败，请重试。' });
    runtime.addWarning('error', 'project.scope.restore_failed', {
      path: target,
      error: 'scope compensation failure; see Health diagnostic ID',
    });
  }
}

async function setActiveProjectWithScopeCompensation(runtime, setActiveProject, context) {
  try {
    return await setActiveProject({ cwd: context.cwd, path: context.target });
  }
  catch (error) {
    await restoreProjectScopeAfterBackendFailure(runtime, context.preparedScope, context.target);
    throw error;
  }
}

async function registerProjectIfNeeded(deps, context, isCurrentIntent) {
  if (!shouldRegisterProject(context.target, context.previousActiveProject, context.visibleProjects)) return true;
  await deps.addProject({ cwd: context.cwd, path: context.target });
  if (!isCurrentIntent()) return false;
  return true;
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

async function runActiveProjectSwitch(runtime, deps, context) {
  const { cwd, isCurrentIntent, preserveActiveThreadId, target } = context;
  const { normalizePath, projectShortLabel, setActiveProject } = deps;
  if (!isCurrentIntent()) return false;
  const previousActiveProject = normalizePath(runtime.get().activeProject);
  try {
    void runtime.saveActiveComposerDraft();
    const visibleProjects = projectVisiblePaths(runtime, normalizePath);
    const registered = await registerProjectIfNeeded(
      deps,
      { cwd, target, previousActiveProject, visibleProjects },
      isCurrentIntent,
    );
    if (!registered || !isCurrentIntent()) return false;
    const requestedCwd = projectCwdForSelection(target, cwd);
    const preparedScope = await runtime.prepareBridgeEventScope(requestedCwd);
    const projects = await setActiveProjectWithScopeCompensation(runtime, setActiveProject, {
      cwd,
      preparedScope,
      target,
    });
    if (!isCurrentIntent()) return false;
    const confirmedCwd = await rebindConfirmedProjectScope(runtime, normalizePath, projects, cwd, requestedCwd);
    runtime.applyProjects(projects, cwd);
    runtime.refreshChatSurfaceForCwdInBackground(confirmedCwd, { preserveActiveThreadId });
    runtime.notifyAction(`已切换项目：${projectShortLabel(target)}`, 'success');
    return true;
  }
  catch (error) {
    if (!isCurrentIntent()) return false;
    runtime.notifyAction('切换项目失败，请重试。', 'error');
    runtime.addWarning('error', 'project.set_active.failed', { path: target, error: 'action failure; see Health diagnostic ID' });
    throw error;
  }
}

function createSetActiveProjectPathAction(runtime, deps) {
  const { normalizePath } = deps;
  let switchGeneration = 0;
  let switchTail = Promise.resolve();
  return async (path, options = {}) => {
    const target = normalizePath(path);
    if (!target) return false;
    const cwd = runtime.requireProjectScopeCwd('project.setActive');
    const generation = ++switchGeneration;
    const isCurrentIntent = () => generation === switchGeneration;
    const preserveActiveThreadId = options.preserveActiveThreadId === true;
    const runSwitch = () => runActiveProjectSwitch(runtime, deps, {
      cwd,
      isCurrentIntent,
      preserveActiveThreadId,
      target,
    });
    const pending = switchTail.then(runSwitch, runSwitch);
    switchTail = pending.catch(() => undefined);
    return pending;
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
      await runtime.rebindBridgeEventScope(selected);
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
