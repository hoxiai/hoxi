import { fileLanguage, getCachedHighlight, highlightCode } from "../components/timeline/syntax-highlight";
import {
  createIncrementalMarkdownParser,
  isLikelyFilePath,
  parseInline,
  parseMarkdownBlocks,
} from "../components/timeline/markdown-parser";
import {
  extractFilePathFromHref,
  extractFileSuffix,
  looksLikeFilePath,
  normalizeCandidatePath,
} from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

describe("Chat code syntax highlighting", () => {
  it("detects file preview languages from paths and special filenames", async () => {
    for (const [path, language] of [
      ["src/App.tsx", "tsx"], ["src/main.go", "go"], ["config.yml", "yaml"],
      ["Dockerfile", "dockerfile"], ["Dockerfile.dev", "dockerfile"],
      ["Makefile", "make"], [".env", "dotenv"], ["script.sh", "shellscript"],
      ["C:\\src\\main.py", "python"], ["README.md", "markdown"],
    ]) assertEqual(fileLanguage(path), language);
    const code = 'export const value: string = "hello";\n';
    const result = await highlightCode(code, fileLanguage("src/main.ts"), "dark");
    assertTrue(!!result);
    assertEqual(result!.tokens.map((line) => line.map((token) => token.content).join("")).join("\n"), code);
  });
  it("highlights common languages and aliases without changing source text", async () => {
    for (const [lang, code] of [
      ["TS", 'const message: string = "hello"; // comment\n'],
      ["go", 'package main\nfunc main() { println("hello") }'],
      ["py", 'def hello():\n    return "hello"'],
      ["sh", 'echo "hello"'],
      ["json", '{"hello": true}'],
      ["html", '<script>alert("hello")</script>'],
      ["c++", 'int main() { return 0; }'],
    ]) {
      const result = await highlightCode(code, lang, "dark");
      assertTrue(!!result, `Expected highlighting for ${lang}`);
      assertEqual(result!.tokens.map((line) => line.map((token) => token.content).join("")).join("\n"), code);
      assertTrue(new Set(result!.tokens.flat().map((token) => token.color)).size > 1);
    }
  });

  it("uses different token colors for light and dark themes", async () => {
    const code = 'const value = "hello";';
    const dark = await highlightCode(code, "js", "dark");
    const light = await highlightCode(code, "js", "light");
    assertTrue(JSON.stringify(dark!.tokens) !== JSON.stringify(light!.tokens));
  });

  it("supports unfinished streamed code and treats HTML as text tokens", async () => {
    for (const code of ['const value = "unfinished', '<img src=x onerror="alert(1)">\n\n']) {
      const result = await highlightCode(code, "tsx", "light");
      assertEqual(result!.tokens.map((line) => line.map((token) => token.content).join("")).join("\n"), code);
    }
  });

  it("falls back for plain text, unknown languages, empty and oversized code", async () => {
    for (const lang of ["text", "", "unknown-language", "__proto__"]) {
      assertEqual(await highlightCode("hello", lang, "dark"), undefined);
    }
    assertEqual(await highlightCode("", "js", "dark"), undefined);
    assertEqual(await highlightCode("x".repeat(50_001), "js", "dark"), undefined);
    assertEqual(await highlightCode("\n".repeat(1_000), "js", "dark"), undefined);
  });

  it("returns synchronous cached highlight tokens on subsequent lookups", async () => {
    const snippet = 'const cached = "kilo-lru-cache-test";\n';
    assertEqual(getCachedHighlight(snippet, "ts", "dark"), undefined);
    const result = await highlightCode(snippet, "ts", "dark");
    assertTrue(!!result);
    const syncHit = getCachedHighlight(snippet, "typescript", "dark");
    assertEqual(syncHit, result);
  });

  it("recognizes dotfiles, extensionless build files, and paths while rejecting slash commands", async () => {
    for (const valid of [
      "Dockerfile",
      "Dockerfile.prod",
      "docker/Dockerfile",
      "Makefile",
      "Justfile",
      "Caddyfile",
      "CMakeLists.txt",
      ".env",
      ".env.local",
      "config/.env.production",
      ".gitignore",
      ".eslintrc.cjs",
      "go.mod",
      "go.work",
      "Cargo.lock",
      "web/src/App.tsx:42",
    ]) {
      assertTrue(isLikelyFilePath(valid), `Expected file path match for ${valid}`);
    }

    for (const invalid of [
      "/plan",
      "/compact",
      "/goal",
      "https://hoxi.ai/file.ts",
      "just some words",
      "",
    ]) {
      assertTrue(!isLikelyFilePath(invalid), `Expected non-match for ${invalid}`);
    }

    const spans = parseInline("Check docker/Dockerfile and `Makefile` and ~/.gemini/antigravity/brain/task.md#L12 but not README in prose.");
    assertTrue(spans.some((s) => s.type === "code" && s.text === "docker/Dockerfile"));
    assertTrue(spans.some((s) => s.type === "code" && s.text === "Makefile"));
    assertTrue(spans.some((s) => s.type === "code" && s.text === "~/.gemini/antigravity/brain/task.md#L12"));
    assertTrue(!spans.some((s) => s.type === "code" && s.text === "README"));

    assertEqual(
      extractFilePathFromHref("file:///Users/test/%E6%96%87%E6%A1%A3/my%20plan.md#L10-L20"),
      "/Users/test/\u6587\u6863/my plan.md#L10-L20"
    );
    assertEqual(
      normalizeCandidatePath("file:///Users/test/%E6%96%87%E6%A1%A3/my%20plan.md#L10-L20"),
      "/Users/test/\u6587\u6863/my plan.md"
    );
    assertDeepEqual(
      extractFileSuffix("/Users/test/\u6587\u6863/my plan.md#L15-L25"),
      { path: "/Users/test/\u6587\u6863/my plan.md", line: 15, column: undefined }
    );
    assertTrue(looksLikeFilePath("~/.gemini/antigravity/brain/plan.md"));
    assertTrue(looksLikeFilePath("/Users/test/\u6587\u6863/\u8bbe\u8ba1\u8bf4\u660e.md"));
    assertTrue(!looksLikeFilePath("application/json"));
    assertTrue(!looksLikeFilePath("true/false"));
  });

  it("incrementally parses long streaming markdown while preserving prefix block references", () => {
    const parse = createIncrementalMarkdownParser();
    const sections = Array.from({ length: 8 }, (_, idx) =>
      `## Section ${idx + 1}\n\nParagraph ${idx + 1} with detailed explanation ${"x".repeat(220)}.\n\n\`\`\`ts\nexport const v${idx} = ${idx};\n\`\`\``
    );
    const doc1 = sections.slice(0, 6).join("\n\n");
    const blocks1 = parse(doc1);
    assertDeepEqual(blocks1, parseMarkdownBlocks(doc1));

    const doc2 = `${doc1}\n\n${sections[6]}`;
    const blocks2 = parse(doc2);
    assertDeepEqual(blocks2, parseMarkdownBlocks(doc2));
    assertTrue(blocks2[0] === blocks1[0]);
    assertTrue(blocks2[1] === blocks1[1]);

    const doc3 = `${doc2}\n\n${sections[7]}`;
    const blocks3 = parse(doc3);
    assertDeepEqual(blocks3, parseMarkdownBlocks(doc3));
    assertTrue(blocks3[0] === blocks1[0]);
  });
});
