import { render } from "solid-js/web";
import ShareImageModal from "../components/share/ShareImageModal";
import { setLocale } from "../i18n";
import { hoxiAPI } from "../services/api";
import { closeShareImageModal, openShareImageModal } from "../store/share-image";
import { setWorkspace, type MessageTurn } from "../store/workspace-state";
import { assertEqual, assertTrue } from "./test-harness";

async function waitFor(check: () => boolean, label: string) {
  for (let i = 0; i < 160; i++) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 30));
  }
  throw new Error("Timed out waiting for " + label);
}

export async function runBrowserTest() {
  const id = "ses_share";
  const turns: MessageTurn[] = ["early", "middle", "recent"].flatMap((name, i) =>
    (["user", "assistant"] as const).map((role, j) => {
      const messageID = "msg_" + name + role;
      return {
        info: { sessionID: id, id: messageID, role, time: { created: 1 + i * 2 + j } },
        parts: [{ sessionID: id, id: "prt_" + messageID, messageID, type: "text", text: name + " " + role }],
      };
    }));
  const checkbox = (text: string) => Array.from(document.querySelectorAll("label"))
    .find((label) => label.textContent?.includes(text))?.querySelector("input");
  const save = () => Array.from(document.querySelectorAll("button"))
    .find((button) => button.textContent?.trim() === "Save PNG");
  const root = document.createElement("div");
  document.body.append(root);
  setLocale("en-US");
  setWorkspace({ rootDir: "/share", sessions: [{ id, title: "Test", directory: "/share", time: { created: 1, updated: 7 } }] });
  setWorkspace("messagesBySession", id, turns.slice(2));
  const originalGetMessages = hoxiAPI.getMessages;
  const originalToBlob = HTMLCanvasElement.prototype.toBlob;
  const originalClick = HTMLAnchorElement.prototype.click;
  let finish!: (data: MessageTurn[]) => void;
  hoxiAPI.getMessages = () => new Promise((resolve) => { finish = resolve; });
  const dispose = render(ShareImageModal, root);
  try {
    openShareImageModal({ sessionID: id });
    await waitFor(() => Boolean(save()), "modal");
    assertEqual(save()?.disabled, true);
    assertTrue(!checkbox("middle user"), "partial history offered a provisional selection");
    finish(turns);
    await waitFor(() => save()?.disabled === false, "first PNG");
    assertEqual(checkbox("early user")?.checked, true);
    HTMLCanvasElement.prototype.toBlob = (callback) => callback(null);
    checkbox("middle user")!.click();
    await waitFor(() => document.body.innerText.includes("Could not render image"), "render error");
    assertEqual(checkbox("middle user")?.checked, false);
    assertEqual(save()?.disabled, true);
    let downloads = 0;
    HTMLAnchorElement.prototype.click = () => { downloads++; };
    save()!.click();
    assertEqual(downloads, 0);

    closeShareImageModal();
    hoxiAPI.getMessages = async () => { throw new Error("share history offline"); };
    openShareImageModal({ sessionID: id });
    await waitFor(() => document.body.innerText.includes("share history offline"), "history error");
    assertEqual(save()?.disabled, true);
    assertTrue(!checkbox("middle user"), "failed load exposed a partial history");
    return ["Share selection uses full history", "Export follows the latest successful render"];
  } finally {
    closeShareImageModal();
    dispose();
    root.remove();
    hoxiAPI.getMessages = originalGetMessages;
    HTMLCanvasElement.prototype.toBlob = originalToBlob;
    HTMLAnchorElement.prototype.click = originalClick;
  }
}
