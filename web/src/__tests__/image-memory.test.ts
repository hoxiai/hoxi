import { hoxiAPI, type FileContentResponse } from "../services/api";
import { decodeBrowserFrame } from "../services/browser";
import {
  forgetSessionMarkdownImages, getCachedMarkdownImage, loadMarkdownImage,
  markdownImageCacheUsage, MAX_MARKDOWN_IMAGE_CACHE_BYTES,
} from "../store/markdown-images";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

const imageResponse = (content: string): FileContentResponse => ({
  type: "binary", encoding: "base64", content, mimeType: "image/png", size: 1024,
});

describe("image memory lifecycle", () => {
  it("bounds retained image bytes, refreshes LRU and preserves uncached oversized display", async () => {
    const original = hoxiAPI.readFileContent;
    const sid = "ses_image_budget";
    const payload = "A".repeat(1024 * 1024);
    hoxiAPI.readFileContent = async () => imageResponse(payload);
    try {
      for (let i = 0; i < 7; i++) await loadMarkdownImage(`${i}.png`, "/repo", sid);
      assertTrue(Boolean(getCachedMarkdownImage("0.png", "/repo", sid)));
      await loadMarkdownImage("7.png", "/repo", sid);
      assertEqual(getCachedMarkdownImage("1.png", "/repo", sid), undefined);
      assertTrue(Boolean(getCachedMarkdownImage("0.png", "/repo", sid)));
      assertTrue(markdownImageCacheUsage().bytes <= MAX_MARKDOWN_IMAGE_CACHE_BYTES);
      hoxiAPI.readFileContent = async () => imageResponse("A".repeat(MAX_MARKDOWN_IMAGE_CACHE_BYTES / 2));
      assertTrue(Boolean(await loadMarkdownImage("oversized.png", "/repo", sid)));
      assertEqual(getCachedMarkdownImage("oversized.png", "/repo", sid), undefined);
    } finally {
      hoxiAPI.readFileContent = original;
      forgetSessionMarkdownImages([sid]);
    }
  });

  it("coalesces scoped reads and does not repopulate an evicted session from a late response", async () => {
    const original = hoxiAPI.readFileContent;
    const sid = "ses_image_late";
    let release!: (response: FileContentResponse) => void;
    let calls = 0;
    hoxiAPI.readFileContent = () => { calls++; return new Promise((resolve) => { release = resolve; }); };
    try {
      const first = loadMarkdownImage("same.png", "/repo", sid);
      const second = loadMarkdownImage("same.png", "/repo", sid);
      assertEqual(first, second);
      await Promise.resolve();
      assertEqual(calls, 1);
      forgetSessionMarkdownImages([sid]);
      release(imageResponse("AAAA"));
      await first;
      assertEqual(getCachedMarkdownImage("same.png", "/repo", sid), undefined);
    } finally {
      release?.(imageResponse("AAAA"));
      hoxiAPI.readFileContent = original;
      forgetSessionMarkdownImages([sid]);
    }
  });

  it("returns revocable browser frame URLs and releases failed decodes", async () => {
    const originalImage = globalThis.Image;
    const originalCreate = URL.createObjectURL;
    const originalRevoke = URL.revokeObjectURL;
    const revoked: string[] = [];
    let fails = false;
    globalThis.Image = class {
      src = "";
      async decode() { if (fails) throw new Error("invalid image"); }
    } as unknown as typeof Image;
    URL.createObjectURL = () => "blob:frame";
    URL.revokeObjectURL = (url) => { revoked.push(url); };
    try {
      const frame = { image: "AAAA", width: 1, height: 1, tabID: "tab" };
      assertEqual(await decodeBrowserFrame(frame), "blob:frame");
      assertEqual(revoked.length, 0);
      fails = true;
      let rejected = false;
      try { await decodeBrowserFrame(frame); } catch { rejected = true; }
      assertTrue(rejected);
      assertEqual(revoked[0], "blob:frame");
    } finally {
      globalThis.Image = originalImage;
      URL.createObjectURL = originalCreate;
      URL.revokeObjectURL = originalRevoke;
    }
  });
});
