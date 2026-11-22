import { render } from "solid-js/web";
import TaskOverviewView from "../components/tasks/TaskOverviewView";
import { t } from "../i18n";
import { loadTaskOverviewPlans, setWorkspace, workspace } from "../store/workspace";
import { assertEqual, assertTrue } from "./test-harness";

export async function runBrowserTest(): Promise<string[]> {
  const root = "ses_tasks_browser";
  const done = "ses_tasks_browser_done";
  const busy = "ses_tasks_browser_busy";
  const directory = "/task-overview-test";
  const storageKey = "hoxi.task-overview.dismissed:v1";
  const originalFetch = window.fetch;
  const originalStorage = localStorage.getItem(storageKey);
  const requests: string[] = [];
  window.fetch = async (_input, init) => {
    requests.push(init?.method ?? "GET");
    return Response.json([]);
  };
  setWorkspace({
    activeSessionID: root,
    rootDir: directory,
    inspectorOpen: true,
    inspectorTab: "tasks",
    sessions: [
      { id: root, title: "Parent", directory, time: { created: 1, updated: 1 } },
      { id: done, title: "Finished child", parentID: root, time: { created: 2, updated: 2 } },
      { id: busy, title: "Running child", parentID: root, time: { created: 3, updated: 3 } },
    ],
    statusBySession: { [busy]: { type: "busy" } },
  });
  const host = document.createElement("div");
  document.body.append(host);
  const dispose = render(() => <TaskOverviewView />, host);
  const settleMenu = () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
  const button = (scope: ParentNode, label: string) => Array.from(scope.querySelectorAll("button")).find(
    (element) => element.textContent?.trim().startsWith(label)
  );
  const actionSelector = `button[aria-label="${t("tasks.rowActions")}"]`;
  try {
    assertEqual(host.querySelectorAll(actionSelector).length, 1, "only finished subtask has row actions");
    const trigger = host.querySelector<HTMLButtonElement>(actionSelector)!;
    trigger.click();
    await settleMenu();
    assertEqual(trigger.getAttribute("aria-expanded"), "true", "menu opens");
    assertEqual(document.activeElement, button(document, t("tasks.removeFromList")), "menu focuses remove button");
    // A background plan poll while the menu is open must keep the row and menu intact.
    await loadTaskOverviewPlans(directory, [root]);
    await settleMenu();
    assertEqual(trigger.getAttribute("aria-expanded"), "true", "menu stays open across plan reload");
    assertEqual(document.activeElement, button(document, t("tasks.removeFromList")), "menu retains focus across plan reload");
    document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    assertEqual(trigger.getAttribute("aria-expanded"), "false", "Escape closes menu");
    assertEqual(document.activeElement, trigger, "Escape restores focus to trigger");

    trigger.click();
    await settleMenu();
    button(document, t("tasks.removeFromList"))!.click();
    assertEqual(button(host, "Finished child"), undefined, "finished child row is removed from list");
    assertTrue(Boolean(button(host, "Running child")));
    assertTrue(workspace.sessions.some((session) => session.id === done));
    assertTrue(localStorage.getItem(storageKey)?.includes(`subagent:${done}`) ?? false);
    button(host, t("tasks.undoRemoval"))!.click();
    assertTrue(Boolean(button(host, "Finished child")));
    assertTrue(requests.every((method) => method === "GET"), "removal must not call a mutation API");
    return ["A finished subtask can be removed and restored without mutating its session; running tasks cannot be removed and plan reloads keep menus open"];
  } finally {
    dispose();
    host.remove();
    window.fetch = originalFetch;
    if (originalStorage === null) localStorage.removeItem(storageKey);
    else localStorage.setItem(storageKey, originalStorage);
  }
}
