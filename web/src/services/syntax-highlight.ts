import type { ThemedToken } from "shiki";

export type HighlightedCode = { code: string; tokens: ThemedToken[][] };

let shikiPromise: Promise<typeof import("shiki")> | undefined;

export function fileLanguage(path: string): string {
  const name = path.split(/[\\/]/).pop()?.toLowerCase() ?? "";
  if (/^dockerfile(?:\.|$)/.test(name)) return "dockerfile";
  if (name === "makefile") return "make";
  const extension = name.split(".").pop() ?? "";
  const aliases: Record<string, string> = {
    js: "javascript", jsx: "jsx", ts: "typescript", tsx: "tsx",
    mjs: "javascript", cjs: "javascript", py: "python", rb: "ruby",
    rs: "rust", sh: "shellscript", bash: "shellscript", zsh: "shellscript",
    yml: "yaml", md: "markdown", h: "c", m: "objective-c", mm: "objective-cpp",
    go: "go", mod: "gomod", sum: "text", env: "dotenv", conf: "ini",
  };
  return aliases[extension] ?? extension;
}

export async function highlightCode(
  code: string,
  language: string,
  theme: "dark" | "light",
): Promise<HighlightedCode | undefined> {
  const lang = language.trim().toLowerCase();
  // Keep large blocks and unlabelled output cheap while preserving their full text.
  if (!code || code.length > 50_000 || code.split("\n").length > 1_000 ||
      ["", "text", "txt", "plaintext", "plain"].includes(lang)) return;

  try {
    const shiki = await (shikiPromise ??= import("shiki"));
    if (!Object.hasOwn(shiki.bundledLanguages, lang)) return;
    const result = await shiki.codeToTokens(code, {
      lang: lang as keyof typeof shiki.bundledLanguages,
      theme: theme === "dark" ? "github-dark" : "github-light",
    });
    return { code, tokens: result.tokens };
  } catch {
    // Loading or tokenization failures must never hide the original code.
    shikiPromise = undefined;
    return;
  }
}
