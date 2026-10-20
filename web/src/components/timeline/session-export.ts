import type { MessageInfo, MessagePart } from "../../services/sse-types";
import { hoxiAPI } from "../../services/api";
import { sessionDirectory, workspace } from "../../store/workspace";

function formatTime(value?: number): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toISOString();
}

function truncate(value: string, max: number): string {
  if (value.length <= max) return value;
  return `${value.slice(0, max)}\n… (${value.length - max} more characters)`;
}

function safeJson(value: unknown, max = 4000): string {
  try {
    return truncate(JSON.stringify(value ?? {}, null, 2), max);
  } catch {
    return String(value);
  }
}

function fenced(value: string, language = ""): string {
  const body = value.replace(/```/g, "`\u200b``");
  return `\`\`\`${language}\n${body}\n\`\`\``;
}

function renderPart(part: MessagePart): string {
  switch (part.type) {
    case "text":
      return part.text.trim();
    case "reasoning": {
      const text = part.text.trim();
      if (!text) return "";
      return `<details>\n<summary>Reasoning</summary>\n\n${text}\n\n</details>`;
    }
    case "file":
      return `[${part.filename || part.mime}](${part.url})`;
    case "tool": {
      const state = part.state;
      const input = safeJson(state.input);
      const lines = [`**Tool · ${part.tool}**`, "", fenced(input, "json")];
      if (state.status === "completed" && state.output) {
        lines.push("", "Output:", "", fenced(truncate(state.output, 8000)));
      } else if (state.status === "error" && state.error) {
        lines.push("", "Error:", "", fenced(truncate(state.error, 4000)));
      }
      return lines.join("\n");
    }
    case "patch":
      return `**Patch · ${part.files.length} file(s)**\n\n${part.files.map((file) => `- ${file}`).join("\n")}`;
    default:
      return "";
  }
}

function turnHeading(info: MessageInfo): string {
  const meta: string[] = [];
  if (info.agent) meta.push(info.agent);
  if (info.modelID) meta.push(info.modelID);
  if (info.time?.created) meta.push(formatTime(info.time.created));
  const suffix = meta.length > 0 ? ` (${meta.join(" · ")})` : "";
  return info.role === "user" ? `## User${suffix}` : `## Assistant${suffix}`;
}

export function buildSessionMarkdown(
  sessionID: string,
  turnsOverride?: Array<{ info: MessageInfo; parts: MessagePart[] }>
): string {
  const session = workspace.sessions.find((item) => item.id === sessionID);
  const turns = turnsOverride ?? workspace.messagesBySession[sessionID] ?? [];
  const header = [
    `# ${session?.title || sessionID}`,
    "",
    `- Session: \`${sessionID}\``,
    `- Directory: \`${session?.directory || workspace.rootDir || ""}\``,
    `- Runtime: ${session?.runtime || "opencode"}`,
    ...(session?.modelID ? [`- Model: \`${session.modelID}\``] : []),
    ...(session?.time?.created ? [`- Created: ${formatTime(session.time.created)}`] : []),
    ...(session?.time?.updated ? [`- Updated: ${formatTime(session.time.updated)}`] : []),
    "",
    "---",
    "",
  ];

  const body = turns.map((turn) => {
    const parts = turn.parts.map(renderPart).filter(Boolean).join("\n\n");
    return [turnHeading(turn.info), "", parts].filter(Boolean).join("\n");
  });

  return `${[...header, ...body].join("\n")}\n`;
}

export function buildSessionJSON(
  sessionID: string,
  turnsOverride?: Array<{ info: MessageInfo; parts: MessagePart[] }>
): string {
  const session = workspace.sessions.find((item) => item.id === sessionID);
  return JSON.stringify(
    {
      session,
      turns: turnsOverride ?? workspace.messagesBySession[sessionID] ?? [],
    },
    null,
    2
  );
}

function download(content: string, filename: string, mime: string) {
  const blob = new Blob([content], { type: `${mime};charset=utf-8` });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 60_000);
}

function safeFilename(value: string): string {
  const cleaned = value.trim().replace(/[\\/:*?"<>|]+/g, "-").replace(/\s+/g, "-");
  return cleaned.slice(0, 80) || "hoxi-session";
}

export async function downloadSessionExport(sessionID: string, format: "markdown" | "json" = "markdown") {
  let turns = workspace.messagesBySession[sessionID];
  if (!turns || turns.length === 0) {
    const directory = workspace.sessions.find((item) => item.id === sessionID)?.directory
      || sessionDirectory(sessionID);
    turns = await hoxiAPI.getMessages(sessionID, directory, 1000).catch(() => []);
  }
  const session = workspace.sessions.find((item) => item.id === sessionID);
  const base = safeFilename(session?.title || sessionID);
  if (format === "json") {
    download(buildSessionJSON(sessionID, turns), `${base}.json`, "application/json");
    return;
  }
  download(buildSessionMarkdown(sessionID, turns), `${base}.md`, "text/markdown");
}

export function buildMessageLink(sessionID: string, messageID?: string): string {
  const params = new URLSearchParams({ session: sessionID });
  if (messageID) params.set("message", messageID);
  const hash = `#${params.toString()}`;
  if (typeof window !== "undefined" && window.location?.href) {
    return `${window.location.origin}${window.location.pathname}${hash}`;
  }
  return `hoxi://session/${encodeURIComponent(sessionID)}${messageID ? `?message=${encodeURIComponent(messageID)}` : ""}`;
}

export async function copyMessageLink(sessionID: string, messageID?: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(buildMessageLink(sessionID, messageID));
    return true;
  } catch {
    return false;
  }
}
