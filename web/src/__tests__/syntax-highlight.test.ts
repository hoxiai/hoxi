import { fileLanguage, highlightCode } from "../services/syntax-highlight";
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
});
