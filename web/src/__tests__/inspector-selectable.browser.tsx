import { render } from "solid-js/web";
import { FileDiffBlock } from "../components/inspector/pierre-diff-parts";
import "../styles.css";

function check(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}

const settle = () => new Promise<void>((resolve) => setTimeout(resolve, 320));

/**
 * Real DOM regression; run with `npm run test:inspector-selectable`
 * (Chrome required). The diff body renders inside an open shadow root, so the
 * app-wide `user-select: none` must be re-enabled there or the inspector cannot
 * be copied from.
 */
export async function runBrowserTest(): Promise<string[]> {
  window.fetch = async () => new Response("{}", { headers: { "Content-Type": "application/json" } });
  const results: string[] = [];

  // No `.selectable-text` ancestor here: the diff host must opt in on its own,
  // because the shadow root does not inherit the app's selection opt-out.
  const root = document.createElement("div");
  root.style.width = "640px";
  document.body.append(root);

  const patch = [
    "--- a/src/task.ts",
    "+++ b/src/task.ts",
    "@@ -1,3 +1,4 @@",
    " const alpha = 1;",
    "-const beta = 2;",
    "+const beta = 3;",
    "+const gamma = 4;",
  ].join("\n");

  const dispose = render(
    () => (
      <FileDiffBlock patch={patch} diffStyle="unified" />
    ),
    root,
  );

  try {
    await settle();

    const host = root.querySelector<HTMLElement>("diffs-container");
    check(host, "the diff shadow host did not render");
    const shadow = host.shadowRoot;
    check(shadow, "the diff did not render into an open shadow root");

    const bodySelect = getComputedStyle(document.body).userSelect;
    check(bodySelect === "none", `body user-select should stay none, got ${bodySelect}`);

    const hostSelect = getComputedStyle(host).userSelect;
    check(
      hostSelect === "text",
      `diff host should opt into text selection, got ${hostSelect}`,
    );

    const line = shadow.querySelector<HTMLElement>("[data-line]");
    check(line, "no diff code line rendered");
    const lineSelect = getComputedStyle(line).userSelect;
    check(
      lineSelect === "text",
      `diff code line should inherit text selection, got ${lineSelect}`,
    );

    const range = document.createRange();
    range.selectNodeContents(line);
    const selection = window.getSelection();
    check(selection, "no window selection available");
    selection!.removeAllRanges();
    selection!.addRange(range);
    const copied = selection!.toString();
    check(
      copied.trim().length > 0,
      `selecting a diff line should yield text, got ${JSON.stringify(copied)}`,
    );
    selection!.removeAllRanges();

    results.push("diff shadow root and code lines opt into text selection");
    results.push("selecting a diff line yields copyable text");

    // `.selectable-text` on an ancestor must also reach open shadow content,
    // otherwise the inspector's other tabs would stay non-selectable.
    const ancestorHost = document.createElement("div");
    ancestorHost.className = "selectable-text";
    const ancestorShadowHost = document.createElement("div");
    ancestorHost.append(ancestorShadowHost);
    document.body.append(ancestorHost);
    const ancestorShadow = ancestorShadowHost.attachShadow({ mode: "open" });
    const ancestorLine = document.createElement("span");
    ancestorLine.textContent = "const alpha = 1;";
    ancestorShadow.append(ancestorLine);
    await settle();
    const ancestorSelect = getComputedStyle(ancestorLine).userSelect;
    check(
      ancestorSelect === "text",
      `ancestor .selectable-text should reach shadow content, got ${ancestorSelect}`,
    );
    ancestorHost.remove();
    results.push("ancestor .selectable-text reaches open shadow content");

    // Negative control: without either opt-in, shadow content inherits the
    // body's `user-select: none`, which is the bug this fix targets.
    const controlHost = document.createElement("div");
    document.body.append(controlHost);
    const controlShadow = controlHost.attachShadow({ mode: "open" });
    const controlLine = document.createElement("span");
    controlLine.textContent = "const alpha = 1;";
    controlShadow.append(controlLine);
    await settle();
    const controlSelect = getComputedStyle(controlLine).userSelect;
    check(
      controlSelect === "none",
      `an unconfigured shadow root should stay non-selectable, got ${controlSelect}`,
    );
    controlHost.remove();
    results.push("an unconfigured shadow root inherits the non-selectable body style");
  } finally {
    dispose();
    root.remove();
  }

  return results;
}
