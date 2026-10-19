import { fileLanguage, getCachedHighlight, highlightCode } from "../components/timeline/syntax-highlight";
import { isLikelyFilePath, parseInline } from "../components/timeline/markdown-parser";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

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
      "https://example.com/file.ts",
      "just some words",
      "",
    ]) {
      assertTrue(!isLikelyFilePath(invalid), `Expected non-match for ${invalid}`);
    }

    const spans = parseInline("Check docker/Dockerfile and `Makefile` but not README in prose.");
    assertTrue(spans.some((s) => s.type === "code" && s.text === "docker/Dockerfile"));
    assertTrue(spans.some((s) => s.type === "code" && s.text === "Makefile"));
    assertTrue(!spans.some((s) => s.type === "code" && s.text === "README"));
  });
});
