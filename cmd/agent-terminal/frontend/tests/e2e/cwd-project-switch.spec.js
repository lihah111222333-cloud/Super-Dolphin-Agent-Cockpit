// @ts-nocheck
import { test, expect } from "@playwright/test";

const PROJECT_A = "/home/testuser/projects/go-agent-v2";
const PROJECT_B = "/home/testuser/projects/go-agent-v2/.worktrees/tag-v1.02";

test("project selector can switch cwd options and propagate active project selection", async ({ page }) => {
  await page.addInitScript((payload) => {
    const calls = [];
    const state = {
      projects: [payload.projectA, payload.projectB],
      active: payload.projectA,
      calls,
    };

    const win = globalThis;
    win.__cwdPlaywrightState = state;
    win.__AO_PROJECTS_CALL_API__ = async (method, params = {}) => {
      state.calls.push({ method, params });

      if (method === "ui/projects/get") {
        return { projects: state.projects, active: state.active };
      }

      if (method === "ui/projects/setActive") {
        state.active = (params?.path || ".").toString();
        return { projects: state.projects, active: state.active };
      }

      return { projects: state.projects, active: state.active };
    };
  }, { projectA: PROJECT_A, projectB: PROJECT_B });

  await page.goto("/");
  await expect(page.getByTestId("app-shell")).toBeVisible();

  const selector = page.locator(".project-selector");
  await expect(selector).toBeVisible();
  await expect(selector).toHaveAttribute('title', PROJECT_A);

  await selector.click();
  await expect(page.locator(`.project-dropdown-item[title="${PROJECT_B}"]`)).toBeVisible();
  await page.locator(`.project-dropdown-item[title="${PROJECT_B}"]`).click();
  await expect(selector).toHaveAttribute('title', PROJECT_B);

  await selector.click();
  await expect(page.locator(`.project-dropdown-item[title="${PROJECT_A}"]`)).toBeVisible();
  await page.locator(`.project-dropdown-item[title="${PROJECT_A}"]`).click();
  await expect(selector).toHaveAttribute('title', PROJECT_A);

  const calls = await page.evaluate(() => globalThis.__cwdPlaywrightState?.calls || []);
  const setActiveCalls = calls.filter((item) => item?.method === "ui/projects/setActive");
  expect(setActiveCalls.length).toBeGreaterThanOrEqual(2);
  expect(setActiveCalls[setActiveCalls.length - 2]?.params?.path).toBe(PROJECT_B);
  expect(setActiveCalls[setActiveCalls.length - 1]?.params?.path).toBe(PROJECT_A);
});
