import { browserWorkflowAPI } from "../services/browser-workflows";
import { browserViewportPresets } from "../store/browser-developer";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

describe("Browser file and developer workflows", () => {
  it("scopes file requests and dialog answers to their originating session, worktree and tab", async () => {
    const previous = globalThis.fetch;
    const calls: Array<{ url: URL; init: RequestInit }> = [];
    globalThis.fetch = (async (url, init) => {
      calls.push({ url: new URL(String(url), "http://127.0.0.1"), init: init || {} });
      return new Response(JSON.stringify({ downloads: [], diagnostics: [] }));
    }) as typeof fetch;
    const scope = { sessionID: "ses_files", directory: "/repo/.hoxi/worktrees/files" };
    const signal = new AbortController().signal;
    try {
      await browserWorkflowAPI.state(scope, "tab-one", signal);
      await browserWorkflowAPI.answer(scope, "tab-one", "dialog-one", true, "explicit reply", signal);
      await browserWorkflowAPI.download(scope, "save", "download-one", "Save as", signal);
      await browserWorkflowAPI.clearDiagnostics(scope, signal);
      for (const call of calls) {
        assertEqual(call.url.searchParams.get("sessionID"), scope.sessionID);
        assertEqual(call.url.searchParams.get("directory"), scope.directory);
        assertEqual((call.init.headers as Record<string, string>)["X-Hoxi-Browser"], "true");
        assertEqual(call.init.signal, signal);
      }
      assertEqual(calls[0].url.searchParams.get("tabID"), "tab-one");
      assertEqual(calls[1].url.searchParams.get("tabID"), "tab-one");
      assertDeepEqual(JSON.parse(calls[1].init.body as string), { id: "dialog-one", accept: true, text: "explicit reply" });
      assertEqual(calls[2].url.pathname, "/api/hoxi/browser/download-save");
      assertEqual(calls[3].init.method, "POST");
    } finally { globalThis.fetch = previous; }
  });

  it("uploads only selected file bytes and an opaque request id, without local filesystem paths", async () => {
    const previous = globalThis.fetch;
    let sent: FormData | undefined;
    globalThis.fetch = (async (_url, init) => {
      sent = init?.body as FormData;
      assertEqual((init?.headers as Record<string, string>)["Content-Type"], undefined);
      return new Response('{"downloads":[]}');
    }) as typeof fetch;
    try {
      await browserWorkflowAPI.upload({ sessionID: "ses_file", directory: "/repo" }, "opaque-request",
        [new File(["selected bytes"], "selected.txt", { type: "text/plain" })], new AbortController().signal);
      assertEqual(sent?.get("id"), "opaque-request");
      const files = sent?.getAll("files") as File[];
      assertEqual(files.length, 1); assertEqual(files[0].name, "selected.txt");
      assertEqual(await files[0].text(), "selected bytes");
      assertEqual(sent?.get("path"), null);
    } finally { globalThis.fetch = previous; }
  });

  it("keeps device presets within viewport and pixel budgets", () => {
    assertDeepEqual(browserViewportPresets.mobile, { width: 390, height: 844, deviceScaleFactor: 1 });
    for (const preset of Object.values(browserViewportPresets)) {
      assertTrue(preset.width <= 4096 && preset.height <= 4096);
      assertTrue(preset.width * preset.height * preset.deviceScaleFactor ** 2 <= 8388608);
    }
  });
});
