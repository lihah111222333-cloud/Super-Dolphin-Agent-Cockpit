/*
 * project slice 管当前窗口选中的项目。
 * 切换项目会保存草稿、刷新聊天列表；失败要回到原来的项目。
 */

function createActiveProjectActions(runtime, deps) {
  const {
    addProject,
    normalizePath,
    projectShortLabel,
    setActiveProject,
  } = deps;

  return {
    setActiveProjectPath: async (path) => {
      const target = normalizePath(path);
      if (!target) return false;
      const cwd = runtime.requireProjectScopeCwd('project.setActive');
      const previousActiveProject = normalizePath(runtime.get().activeProject);
      const previousProjects = Array.isArray(runtime.get().projects) ? [...runtime.get().projects] : [];
      try {
        void runtime.saveActiveComposerDraft();
        const visibleProjects = Array.isArray(runtime.get().projects) ? runtime.get().projects.map(normalizePath).filter(Boolean) : [];
        if (target !== '.' && (previousActiveProject === '.' || !visibleProjects.includes(target))) {
          const addedProjects = await addProject({ cwd, path: target });
          runtime.applyProjects(addedProjects, cwd);
        }
        const optimisticProjects = target === '.' || visibleProjects.includes(target)
          ? previousProjects
          : [...new Set([...previousProjects, target])];
        runtime.set({
          projects: optimisticProjects,
          activeProject: target,
        });
        const optimisticCwd = target && target !== '.' ? target : cwd;
        runtime.refreshChatSurfaceForCwdInBackground(optimisticCwd);
        const projects = await setActiveProject({ cwd, path: target });
        runtime.applyProjects(projects, cwd);
        const selectedProject = normalizePath(runtime.get().activeProject);
        const selectedCwd = selectedProject && selectedProject !== '.' ? selectedProject : cwd;
        if (selectedCwd !== optimisticCwd) {
          runtime.refreshChatSurfaceForCwdInBackground(selectedCwd);
        }
        runtime.notifyAction(`已切换项目：${projectShortLabel(target)}`, 'success');
        return true;
      }
      catch (error) {
        runtime.set({
          activeProject: previousActiveProject,
          projects: previousProjects,
          chatSurfaceLoadingCwd: '',
        });
        runtime.notifyAction(`切换项目失败：${error.message}`, 'error');
        runtime.addWarning('error', 'project.set_active.failed', { path: target, error: error.message });
        return false;
      }
    },


  };
}

function createProjectPickerActions(runtime, deps) {
  const {
    addProject,
    normalizePath,
    openNewWindow,
    projectShortLabel,
    removeProject,
    selectProjectDir,
    setActiveProject,
  } = deps;

  return {
    addProjectFromPicker: async () => {
      /*
       * 选择器只给路径。
       * 真正注册项目和修正 activeProject 由后端 addProject 完成。
       */
      const scopeCwd = runtime.requireProjectScopeCwd('project.add');
      const activeProject = normalizePath(runtime.get().activeProject);
      const seed = activeProject && activeProject !== '.' ? activeProject : scopeCwd;
      let selected = '';
      try {
        selected = normalizePath(await selectProjectDir(seed));
        if (!selected) {
          runtime.notifyAction('未选择项目', 'info');
          return false;
        }
        const projects = await addProject({ cwd: scopeCwd, path: selected });
        runtime.applyProjects(projects, scopeCwd);
        if (normalizePath(runtime.get().activeProject) !== selected) {
          const activatedProjects = await setActiveProject({ cwd: scopeCwd, path: selected });
          runtime.applyProjects(activatedProjects, scopeCwd);
        }
        runtime.refreshChatSurfaceForCwdInBackground(selected);
        runtime.notifyAction(`已添加项目：${projectShortLabel(selected)}`, 'success');
        return true;
      }
      catch (error) {
        runtime.notifyAction(`添加项目失败：${error.message}`, 'error');
        runtime.addWarning('error', 'project.add.failed', { path: selected, error: error.message });
        return false;
      }
    },

    openNewWindow: async () => {
      const scopeCwd = runtime.requireProjectScopeCwd('ui.open_new_window');
      const activeProject = normalizePath(runtime.get().activeProject);
      const seed = activeProject && activeProject !== '.' ? activeProject : scopeCwd;
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
        runtime.notifyAction(`打开新窗口失败：${error.message}`, 'error');
        runtime.addWarning('error', 'ui.open_new_window.failed', { path: selected, error: error.message });
        return false;
      }
    },

    removeProjectPath: async (path) => {
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
        runtime.notifyAction(`移除项目失败：${error.message}`, 'error');
        runtime.addWarning('error', 'project.remove.failed', { path: target, error: error.message });
        return false;
      }
    },


  };
}

export function createProjectSlice(runtime, deps) {
  return {
    ...createActiveProjectActions(runtime, deps),
    ...createProjectPickerActions(runtime, deps),
  };
}
