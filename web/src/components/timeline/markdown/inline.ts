// web/src/components/timeline/markdown/inline.ts
import type { InlineSpan } from "./types";

const EXTENSIONS_SOURCE =
  "ts|tsx|js|jsx|mjs|cjs|mts|cts|go|py|pyi|rs|md|mdx|markdown|json|jsonc|json5|yaml|yml|toml|xml|plist|svg|png|jpg|jpeg|gif|webp|ico|bmp|avif|pdf|css|scss|sass|less|html|htm|sh|zsh|bash|fish|sql|vue|svelte|astro|c|cpp|cc|cxx|h|hpp|m|mm|java|kt|kts|swift|rb|php|proto|graphql|gql|ini|cfg|conf|env|lock|mod|sum|work|gradle|properties|cmake|mk|dockerfile|tf|tfvars|hcl|prisma|sol|zig|lua|ex|exs|erl|hrl|hs|ml|mli|fs|fsx|cs|vb|dart|groovy|scala|clj|cljs|edn|nim|cr|v|sv|vhd|vhdl|glsl|wgsl|hlsl|metal|wasm|wat|csv|tsv|txt|log";

const KNOWN_FILENAMES_SOURCE =
  "(?:Dockerfile|Containerfile|Makefile|GNUmakefile|Justfile|Caddyfile|Procfile|Gemfile|Rakefile|Guardfile|Brewfile|Vagrantfile|Tiltfile|Earthfile|BUILD|WORKSPACE|CMakeLists\\.txt|CODEOWNERS|LICENSE|README|CHANGELOG|CONTRIBUTING|AUTHORS|NOTICE)(?:\\.[\\w.-]+)?";

const KNOWN_DOTFILES_SOURCE =
  "\\.(?:env(?:\\.[\\w.-]+)?|gitignore|gitattributes|gitmodules|dockerignore|npmignore|eslintignore|prettierignore|editorconfig|nvmrc|node-version|python-version|ruby-version|tool-versions|babelrc(?:\\.[\\w.-]+)?|eslintrc(?:\\.[\\w.-]+)?|prettierrc(?:\\.[\\w.-]+)?|stylelintrc(?:\\.[\\w.-]+)?|npmrc|yarnrc(?:\\.[\\w.-]+)?|bashrc|zshrc|profile|clang-format|clang-tidy)";

const FILE_LEAF_SOURCE = `(?:[\\w.+\\-\\u4e00-\\u9fa5]+\\.(?:${EXTENSIONS_SOURCE})|${KNOWN_FILENAMES_SOURCE}|${KNOWN_DOTFILES_SOURCE})`;

const LINE_SUFFIX_SOURCE = "(?::\\d+(?:[:\\-–—]\\d+)?|#L\\d+(?:[-–—]L?\\d+)?)?";

const BARE_FILE_PATH_SOURCE = `(?:~\\/|\\.\\.?\\/|\\/)?(?:[\\w@.+\\-\\u4e00-\\u9fa5]+\\/)+${FILE_LEAF_SOURCE}${LINE_SUFFIX_SOURCE}`;

const PAREN_URL_BODY_SOURCE = "(?:[^()\\s]|\\([^()\\s]*\\))+";

const IMAGE_SOURCE = `!\\[[^\\]\\n]*\\]\\(${PAREN_URL_BODY_SOURCE}(?:\\s+(?:"[^"\\n]*"|'[^'\\n]*'))?\\)`;

const INLINE_CODE_SPAN_SOURCE = "`[^`\\n]+`";
const UNCLOSED_CODE_SPAN_SOURCE = "`[^`\\n]*$";
const LONE_BACKTICK_SOURCE = "`(?![^`\\n]+`)";

const CLOSED_BOLD_ASTERISK_SOURCE = `\\*\\*(?:${INLINE_CODE_SPAN_SOURCE}|[^*\`]|${LONE_BACKTICK_SOURCE}|\\*(?!\\*))+\\*\\*`;
const UNCLOSED_BOLD_ASTERISK_SOURCE = `\\*\\*(?:${INLINE_CODE_SPAN_SOURCE}|[^*\`\\n]|${LONE_BACKTICK_SOURCE}|\\*(?!\\*))*$`;
const CLOSED_BOLD_UNDERSCORE_SOURCE = `__(?:${INLINE_CODE_SPAN_SOURCE}|[^_\`]|${LONE_BACKTICK_SOURCE}|_(?!_))+__`;

const LINK_LABEL_SOURCE = `(?:${INLINE_CODE_SPAN_SOURCE}|[^\\]\`]|${LONE_BACKTICK_SOURCE})+`;
const UNCLOSED_LINK_LABEL_SOURCE = `(?:${INLINE_CODE_SPAN_SOURCE}|[^\\]\`\\n]|${LONE_BACKTICK_SOURCE})+`;
const CLOSED_LINK_SOURCE = `\\[${LINK_LABEL_SOURCE}\\]\\(${PAREN_URL_BODY_SOURCE}\\)`;
const UNCLOSED_LINK_SOURCE = `\\[${UNCLOSED_LINK_LABEL_SOURCE}\\]\\([^()\\s]*$`;

const CLOSED_LINK_MATCH_REGEX = new RegExp(
  `^\\[(${LINK_LABEL_SOURCE})\\]\\((${PAREN_URL_BODY_SOURCE})\\)$`
);
const UNCLOSED_LINK_MATCH_REGEX = new RegExp(
  `^\\[(${UNCLOSED_LINK_LABEL_SOURCE})\\]\\(([^()\\s]*)$`
);

const INLINE_TOKEN_REGEX = new RegExp(
  `(${IMAGE_SOURCE}|${INLINE_CODE_SPAN_SOURCE}|${UNCLOSED_CODE_SPAN_SOURCE}|<br\\s*\\/?>|${CLOSED_BOLD_ASTERISK_SOURCE}|${UNCLOSED_BOLD_ASTERISK_SOURCE}|${CLOSED_BOLD_UNDERSCORE_SOURCE}|~~[^~\\n]+~~|${CLOSED_LINK_SOURCE}|${UNCLOSED_LINK_SOURCE}|\\*[^*\\n]+\\*|https?:\\/\\/[^\\s<>\\])"]+|${BARE_FILE_PATH_SOURCE})`,
  "gi"
);

function appendPlainTextSpan(spans: InlineSpan[], text: string) {
  if (!text) return;
  const last = spans[spans.length - 1];
  if (last && last.type === "text") {
    last.text += text;
  } else {
    spans.push({ type: "text", text });
  }
}

function stripPendingTrailingDelimiter(tail: string): string {
  // Hold back a lone trailing `*` after start-of-line, whitespace, or open punctuation
  // so the first `*` of an upcoming `**bold**` doesn't flash for one frame.
  if (tail === "*" || /[\s:：,，(（]\*$/.test(tail)) {
    return tail.slice(0, -1);
  }
  return tail;
}

function isAllowedImageSrc(src: string): boolean {
  if (/^https?:\/\//i.test(src)) return true;
  if (/^data:image\/(?:png|jpe?g|gif|webp|avif);base64,/i.test(src)) return true;
  // Windows drive letters look like a URI scheme but are local paths.
  if (/^[a-zA-Z]:[\\/]/.test(src)) return true;
  if (/^[a-z][a-z0-9+.-]*:/i.test(src)) return false;
  return true;
}

function parseImageSpan(token: string): InlineSpan | null {
  const match = token.match(/^!\[([^\]]*)\]\((.*)\)$/s);
  if (!match) return null;
  let src = match[2].trim();
  let title: string | undefined;
  const titled = src.match(/^(.*?)\s+(?:"([^"]*)"|'([^']*)')$/s);
  if (titled) {
    src = titled[1].trim();
    title = titled[2] ?? titled[3];
  }
  if (!src || !isAllowedImageSrc(src)) return null;
  return title
    ? { type: "image", alt: match[1], src, title }
    : { type: "image", alt: match[1], src };
}

export function parseInline(text: string): InlineSpan[] {
  const spans: InlineSpan[] = [];
  const regex = new RegExp(INLINE_TOKEN_REGEX.source, "gi");
  let lastIdx = 0;
  let match: RegExpExecArray | null;

  while ((match = regex.exec(text)) !== null) {
    const token = match[0];

    if (token.startsWith("![")) {
      if (match.index > lastIdx) {
        appendPlainTextSpan(spans, text.slice(lastIdx, match.index));
      }
      const image = parseImageSpan(token);
      if (image) {
        spans.push(image);
      } else {
        appendPlainTextSpan(spans, token);
      }
      lastIdx = regex.lastIndex;
      continue;
    }

    // Guard bare file path matches so they aren't mid-word or part of a URL
    if (
      !token.startsWith("`") &&
      !token.startsWith("<") &&
      !token.startsWith("*") &&
      !token.startsWith("_") &&
      !token.startsWith("~~") &&
      !token.startsWith("[") &&
      !/^https?:\/\//i.test(token)
    ) {
      const prevChar = match.index > 0 ? text[match.index - 1] : "";
      if (prevChar && /[A-Za-z0-9_/:.~@\-`]/.test(prevChar)) {
        continue;
      }
      if (match.index > lastIdx) {
        appendPlainTextSpan(spans, text.slice(lastIdx, match.index));
      }
      spans.push({ type: "code", text: token });
      lastIdx = regex.lastIndex;
      continue;
    }

    if (match.index > lastIdx) {
      appendPlainTextSpan(spans, text.slice(lastIdx, match.index));
    }

    if (/^<br\s*\/?>$/i.test(token)) {
      spans.push({ type: "br" });
    } else if (token.startsWith("`")) {
      if (token.length >= 2 && token.endsWith("`")) {
        spans.push({ type: "code", text: token.slice(1, -1) });
      } else {
        const codeBody = token.slice(1);
        if (codeBody.length > 0) {
          spans.push({ type: "code", text: codeBody, unclosed: true });
        }
      }
    } else if (
      (token.startsWith("**") && token.length >= 4 && token.endsWith("**")) ||
      (token.startsWith("__") && token.length >= 4 && token.endsWith("__"))
    ) {
      spans.push({ type: "bold", text: token.slice(2, -2) });
    } else if (token.startsWith("**")) {
      const rawBody = token.slice(2);
      const boldBody = rawBody.endsWith("*") ? rawBody.slice(0, -1) : rawBody;
      if (boldBody.length > 0) {
        spans.push({ type: "bold", text: boldBody });
      }
    } else if (token.startsWith("~~") && token.endsWith("~~")) {
      spans.push({ type: "strike", text: token.slice(2, -2) });
    } else if (token.startsWith("[")) {
      // A failed image token leaves a stray `!`; keep the whole thing literal
      // instead of rendering a broken image plus a separate link.
      if (match.index > 0 && text[match.index - 1] === "!") {
        appendPlainTextSpan(spans, token);
        lastIdx = regex.lastIndex;
        continue;
      }
      const closedMatch = token.match(CLOSED_LINK_MATCH_REGEX);
      if (closedMatch) {
        spans.push({ type: "link", text: closedMatch[1], href: closedMatch[2] });
      } else {
        const unclosedMatch = token.match(UNCLOSED_LINK_MATCH_REGEX);
        if (unclosedMatch) {
          spans.push({
            type: "link",
            text: unclosedMatch[1],
            href: unclosedMatch[2] || "#",
          });
        } else {
          appendPlainTextSpan(spans, token);
        }
      }
    } else if (token.startsWith("*") && token.endsWith("*")) {
      spans.push({ type: "italic", text: token.slice(1, -1) });
    } else if (/^https?:\/\//i.test(token)) {
      const cleanUrl = token.replace(/[.,;:!?]+$/, "");
      const trailing = token.slice(cleanUrl.length);
      spans.push({ type: "link", text: cleanUrl, href: cleanUrl });
      if (trailing) {
        appendPlainTextSpan(spans, trailing);
      }
    } else {
      appendPlainTextSpan(spans, token);
    }
    lastIdx = regex.lastIndex;
  }

  if (lastIdx < text.length) {
    appendPlainTextSpan(spans, stripPendingTrailingDelimiter(text.slice(lastIdx)));
  }
  return spans;
}

export function isSameInlineSpan(a: InlineSpan | undefined, b: InlineSpan): boolean {
  if (!a || a.type !== b.type) return false;
  switch (b.type) {
    case "br":
      return true;
    case "image":
      return (
        a.type === "image" && a.alt === b.alt && a.src === b.src && a.title === b.title
      );
    case "code":
      return (
        a.type === "code" && a.text === b.text && Boolean(a.unclosed) === Boolean(b.unclosed)
      );
    case "link":
      return a.type === "link" && a.text === b.text && a.href === b.href;
    default:
      return "text" in a && "text" in b ? a.text === b.text : true;
  }
}
