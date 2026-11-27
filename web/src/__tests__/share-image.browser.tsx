import { render } from "solid-js/web";
import ShareImageModal from "../components/share/ShareImageModal";
import { setLocale } from "../i18n";
import { hoxiAPI } from "../services/api";
import { serializeContextEnvelopes } from "../store/context-protocol";
import { closeShareImageModal, openShareImageModal, saveShareImagePages } from "../store/share-image";
import { setWorkspace, type MessageTurn } from "../store/workspace-state";
import { assertDeepEqual, assertEqual, assertTrue } from "./test-harness";

async function waitFor(check: () => boolean, label: string) {
  for (let i = 0; i < 160; i++) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 30));
  }
  throw new Error("Timed out waiting for " + label);
}

export async function runBrowserTest() {
  const id = "ses_share";
  const quotedFirstUser = serializeContextEnvelopes(
    [
      {
        version: 1,
        items: [{ kind: "quote", title: "Quoted from conversation", content: "- Item one\n- Item two" }],
      },
    ],
    "early user"
  );
  const turns: MessageTurn[] = ["early", "middle", "recent"].flatMap((name, i) =>
    (["user", "assistant"] as const).map((role, j) => {
      const messageID = "msg_" + name + role;
      return {
        info: { sessionID: id, id: messageID, role, time: { created: 1 + i * 2 + j } },
        parts: [
          {
            sessionID: id,
            id: "prt_" + messageID,
            messageID,
            type: "text",
            text: i === 0 && role === "user" ? quotedFirstUser : name + " " + role,
          },
        ],
      };
    })
  );
  const checkbox = (text: string) =>
    Array.from(document.querySelectorAll("label"))
      .find((label) => label.textContent?.includes(text))
      ?.querySelector("input");
  const save = () =>
    Array.from(document.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Save PNG"
    );
  const revealBtn = () =>
    Array.from(document.querySelectorAll("button")).find((button) => {
      const txt = button.textContent?.trim() || "";
      return txt === "Show in Finder" || txt === "Show in Folder";
    });
  const root = document.createElement("div");
  document.body.append(root);
  setLocale("en-US");
  setWorkspace({
    rootDir: "/share",
    sessions: [{ id, title: "Test", directory: "/share", time: { created: 1, updated: 7 } }],
  });
  setWorkspace("messagesBySession", id, turns.slice(2));
  const originalGetMessages = hoxiAPI.getMessages;
  const originalSaveImages = hoxiAPI.saveImages;
  const originalOpenExternalFile = hoxiAPI.openExternalFile;
  const originalToBlob = HTMLCanvasElement.prototype.toBlob;
  const originalClick = HTMLAnchorElement.prototype.click;
  let finish!: (data: MessageTurn[]) => void;
  hoxiAPI.getMessages = () =>
    new Promise((resolve) => {
      finish = resolve;
    });
  const dispose = render(ShareImageModal, root);
  try {
    openShareImageModal({ sessionID: id });
    await waitFor(() => Boolean(save()), "modal");
    assertEqual(save()?.disabled, true);
    assertTrue(!checkbox("middle user"), "partial history offered a provisional selection");
    finish(turns);
    await waitFor(() => save()?.disabled === false, "first PNG");
    assertEqual(checkbox("early user")?.checked, true);
    assertTrue(!document.body.innerText.includes("<details>"), "quoted context leaked raw <details> tag");

    // 1. Native desktop save shows exact saved path and wires Show in Finder/Folder reveal.
    const revealed: Array<{ path: string; action?: string }> = [];
    hoxiAPI.saveImages = async () => ({
      supported: true,
      saved: true,
      canceled: false,
      paths: ["/Users/alice/Downloads/Test.png"],
      directory: "/Users/alice/Downloads",
    });
    hoxiAPI.openExternalFile = async (filePath, opts) => {
      revealed.push({ path: filePath, action: opts?.action });
      return { ok: true, path: filePath, action: opts?.action || "reveal" };
    };
    save()!.click();
    await waitFor(
      () => document.body.innerText.includes("Saved to ~/Downloads/Test.png") && Boolean(revealBtn()),
      "native saved location"
    );
    revealBtn()!.click();
    await waitFor(() => revealed.length === 1, "reveal action");
    assertEqual(revealed[0].path, "/Users/alice/Downloads/Test.png");
    assertEqual(revealed[0].action, "reveal");

    // 2. Headless / browser fallback triggers <a download> and clarifies Downloads folder.
    let downloads = 0;
    HTMLAnchorElement.prototype.click = () => {
      downloads++;
    };
    hoxiAPI.saveImages = async () => ({ supported: false });
    save()!.click();
    await waitFor(
      () => downloads === 1 && document.body.innerText.includes("Downloaded Test.png"),
      "browser download fallback"
    );

    // 3. Render error disables save button and blocks exporting stale image.
    HTMLCanvasElement.prototype.toBlob = (callback) => callback(null);
    checkbox("middle user")!.click();
    await waitFor(() => document.body.innerText.includes("Could not render image"), "render error");
    assertEqual(checkbox("middle user")?.checked, false);
    assertEqual(save()?.disabled, true);
    downloads = 0;
    save()!.click();
    assertEqual(downloads, 0);

    // 4. Failed history load never exposes partial cache.
    closeShareImageModal();
    hoxiAPI.getMessages = async () => {
      throw new Error("share history offline");
    };
    openShareImageModal({ sessionID: id });
    await waitFor(() => document.body.innerText.includes("share history offline"), "history error");
    assertEqual(save()?.disabled, true);
    assertTrue(!checkbox("middle user"), "failed load exposed a partial history");

    const blob = new Blob(["test"], { type: "image/png" });
    const pages = Array.from({ length: 33 }, () => ({ blob }));
    const batches: Array<Parameters<typeof hoxiAPI.saveImages>[0]> = [];
    let cancelLastBatch = false;
    hoxiAPI.saveImages = async (request) => {
      batches.push(request);
      assertTrue(request.images.length <= 32, "desktop rejects more than 32 images per request");
      if (cancelLastBatch && request.images.length === 1) return { supported: true, canceled: true };
      const directory = request.images.length === 1 ? "/second-folder" : "/first-folder";
      return { supported: true, saved: true, paths: request.images.map(({ filename }) => `${directory}/${filename}`), directory };
    };
    const result = await saveShareImagePages(pages, "Long session", {
      batchDialogTitle: (first, last) => `${first}–${last}`,
    });
    assertDeepEqual(batches.map((request) => [request.images.length, request.title]), [[32, "1–32"], [1, "33–33"]]);
    assertEqual(batches[1].defaultDir, "/first-folder");
    assertEqual(batches[1].images[0].filename, "Long-session-33.png");
    if (result.status !== "saved" || result.mode !== "native") throw new Error("33-page native save did not finish");
    assertEqual(result.paths.length, 33);
    assertEqual(result.paths[32], "/second-folder/Long-session-33.png");
    assertEqual(result.directory, "");
    cancelLastBatch = true;
    assertDeepEqual(await saveShareImagePages(pages, "Long session"), {
      status: "partial", savedCount: 32, total: 33, error: undefined,
    });
    assertEqual(downloads, 0, "canceling a later batch must not restart the export as browser downloads");
    return [
      "Share selection uses full history and structures quoted context",
      "Native save shows path with reveal action and falls back cleanly in browser mode",
      "Export follows the latest successful render",
      "33-page exports save in bounded batches and report a later cancellation accurately",
    ];
  } finally {
    closeShareImageModal();
    dispose();
    root.remove();
    hoxiAPI.getMessages = originalGetMessages;
    hoxiAPI.saveImages = originalSaveImages;
    hoxiAPI.openExternalFile = originalOpenExternalFile;
    HTMLCanvasElement.prototype.toBlob = originalToBlob;
    HTMLAnchorElement.prototype.click = originalClick;
  }
}
