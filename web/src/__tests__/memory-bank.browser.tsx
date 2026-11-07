import { render } from "solid-js/web";
import MemoryBankView from "../components/inspector/MemoryBankView";
import { t } from "../i18n";
import { hoxiAPI } from "../services/api";
import { resetMemoryBankDrafts, setWorkspace } from "../store/workspace";
import "../styles.css";

function check(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}

const settle = () => new Promise<void>((resolve) => setTimeout(resolve, 100));

export async function runBrowserTest(): Promise<string[]> {
  const results: string[] = [];
  const project = "/audit-project";
  resetMemoryBankDrafts();

  setWorkspace({
    rootDir: project,
    activeSessionID: "ses_audit_a",
    inspectorOpen: true,
    inspectorTab: "memory",
    sessions: [
      { id: "ses_audit_a", title: "A", directory: project, time: { created: 1, updated: 1 } },
      { id: "ses_audit_b", title: "B", directory: project, time: { created: 1, updated: 1 } },
    ],
  });

  let reads = 0;
  let saves = 0;
  let delayReads = false;
  const content = "# Memory A\n- PRIVATE_A_DRAFT\n";

  hoxiAPI.readFileContent = async () => {
    reads++;
    if (delayReads) {
      return await new Promise(() => {});
    }
    return { content } as any;
  };
  hoxiAPI.saveMemory = async (input) => {
    saves++;
    return { ok: true, scope: input.scope, path: "mock", content: input.content };
  };

  const host = document.createElement("div");
  document.body.append(host);
  const dispose = render(() => <MemoryBankView />, host);
  const findEditBtn = () =>
    [...host.querySelectorAll<HTMLButtonElement>("button")].find(
      (b) => b.title === t("inspector.memory.editMode")
    );

  try {
    await settle();
    const initialReads = reads;
    check(initialReads === 1, `expected 1 initial read on mount, got ${initialReads}`);
    results.push("mounting MemoryBankView reads memory content once");

    const editBtn = findEditBtn();
    check(editBtn, "edit button missing");
    editBtn!.click();
    await settle();

    const textarea = host.querySelector<HTMLTextAreaElement>("textarea");
    check(textarea, "expected textarea to remain open after clicking edit");
    check(
      reads === initialReads,
      `expected clicking edit not to reload content (reads before=${initialReads}, after=${reads})`
    );
    results.push("clicking Edit keeps textarea open without triggering extra reads");

    // Typing a draft and receiving a background sessionFinishedAt refresh must not close the editor.
    textarea!.value = "# Memory A\n- USER_IN_PROGRESS_DRAFT\n";
    textarea!.dispatchEvent(new Event("input", { bubbles: true }));
    setWorkspace("sessionFinishedAt", "ses_audit_a", Date.now());
    await settle();
    const textareaAfterBg = host.querySelector<HTMLTextAreaElement>("textarea");
    check(textareaAfterBg, "background sessionFinishedAt refresh closed active editor");
    check(
      textareaAfterBg!.value.includes("USER_IN_PROGRESS_DRAFT"),
      "background refresh overwrote active draft"
    );
    results.push("background turn completion preserves active editor and draft");

    // Switching from A to B while B's load is delayed must never allow A's draft to save to B.
    delayReads = true;
    setWorkspace("activeSessionID", "ses_audit_b");
    await settle();
    const saveBtn = [...host.querySelectorAll<HTMLButtonElement>("button")].find(
      (b) => b.textContent?.trim() === t("inspector.memory.save")
    );
    if (saveBtn) saveBtn.click();
    await settle();
    check(saves === 0, `expected 0 saves after switching A -> B during delayed load, got ${saves}`);
    results.push("switching sessions during delayed load prevents saving stale draft to new session");
  } finally {
    dispose();
    host.remove();
    resetMemoryBankDrafts();
  }

  return results;
}
